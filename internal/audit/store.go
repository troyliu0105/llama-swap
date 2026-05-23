package audit

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	_ "modernc.org/sqlite"
)

const (
	auditChannelSize            = 1024
	defaultCaptureFlushSize     = 50
	defaultCaptureFlushInterval = 30 * time.Second
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type AuditStore struct {
	db                   *sql.DB
	auditCh              chan auditEvent
	logger               *logmon.Monitor
	captureFlushSize     int
	captureFlushInterval time.Duration
	doneCh               chan struct{}
	wg                   sync.WaitGroup
	retentionDays        int
	capturePurgeDays     int
}

func NewAuditStore(dbPath string, retentionDays int, capturePurgeDays int, captureFlushSize int, captureFlushIntervalSec int, logger *logmon.Monitor) (*AuditStore, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("create audit db directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open audit db: %w", err)
	}

	store := &AuditStore{
		db:                   db,
		auditCh:              make(chan auditEvent, auditChannelSize),
		logger:               logger,
		captureFlushSize:     captureFlushSize,
		captureFlushInterval: time.Duration(captureFlushIntervalSec) * time.Second,
		doneCh:               make(chan struct{}),
		retentionDays:        retentionDays,
		capturePurgeDays:     capturePurgeDays,
	}

	if store.captureFlushSize <= 0 {
		store.captureFlushSize = defaultCaptureFlushSize
	}
	if store.captureFlushInterval <= 0 {
		store.captureFlushInterval = defaultCaptureFlushInterval
	}

	if logger != nil {
		logger.Infof("audit db: opened %s", dbPath)
	}

	if err := store.configureDB(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := runMigrations(dbPath, store.logger); err != nil {
		_ = db.Close()
		return nil, err
	}

	store.wg.Go(store.writer)
	store.startRetention()
	store.startCapturePurge()

	return store, nil
}

func (s *AuditStore) Close() error {
	close(s.doneCh)
	s.wg.Wait()
	return s.db.Close()
}

func (s *AuditStore) RecordRequest(metricID int, apiKey, userName, model, reqPath string, statusCode int, inputTokens, outputTokens, cachedTokens int, durationMs int, tokensPerSecond, promptPerSecond float64, captureData []byte, fingerprint string, codexAccount string) {
	select {
	case <-s.doneCh:
		return
	default:
	}

	event := auditEvent{
		metricID:        metricID,
		apiKey:          apiKey,
		userName:        userName,
		model:           model,
		reqPath:         reqPath,
		statusCode:      statusCode,
		inputTokens:     inputTokens,
		outputTokens:    outputTokens,
		cachedTokens:    cachedTokens,
		durationMs:      durationMs,
		tokensPerSecond: tokensPerSecond,
		promptPerSecond: promptPerSecond,
		fingerprint:     fingerprint,
		codexAccount:    codexAccount,
	}
	if captureData != nil {
		event.captureData = append([]byte(nil), captureData...)
	}

	select {
	case s.auditCh <- event:
	default:
		if s.logger != nil {
			s.logger.Warnf("audit queue full, dropping request log for model %q", model)
		}
	}
}

func (s *AuditStore) configureDB() error {
	// Allow 2 open connections so WAL-mode concurrent readers don't block
	// on the writer goroutine. SQLite's WAL journal handles safety; the Go
	// pool only needs to ensure at most one writer at a time (the writer
	// goroutine serialises all writes).
	s.db.SetMaxOpenConns(2)

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=10000",
		"PRAGMA foreign_keys=ON",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA cache_size=-16384",
		"PRAGMA temp_store=MEMORY",
		"PRAGMA mmap_size=268435456",
	}

	for _, pragma := range pragmas {
		if _, err := s.db.Exec(pragma); err != nil {
			return fmt.Errorf("configure audit db %q: %w", pragma, err)
		}
	}

	if s.logger != nil {
		s.logger.Infof("audit db: configured PRAGMAs: %s", strings.Join(pragmas, ", "))
	}

	return nil
}

func runMigrations(dbPath string, logger *logmon.Monitor) error {
	sourceDriver, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("create audit migration source: %w", err)
	}

	migrator, err := migrate.NewWithSourceInstance("iofs", sourceDriver, "sqlite://"+filepath.ToSlash(dbPath)+"?x-migrations-table=audit_migration_versions")
	if err != nil {
		return fmt.Errorf("create audit migrator: %w", err)
	}

	err = migrator.Up()

	if errors.Is(err, migrate.ErrNoChange) {
		version, _, _ := migrator.Version()
		_, _ = migrator.Close()
		if logger != nil {
			logger.Infof("audit db migrations: schema is up to date (version %d)", version)
		}
		return nil
	}

	if err != nil {
		_, _ = migrator.Close()
		return fmt.Errorf("run audit migrations: %w", err)
	}

	version, _, _ := migrator.Version()
	_, _ = migrator.Close()
	if logger != nil {
		logger.Infof("audit db migrations: applied successfully (now at version %d)", version)
	}

	return nil
}
