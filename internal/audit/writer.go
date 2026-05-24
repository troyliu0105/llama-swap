package audit

import (
	"database/sql"
	"fmt"
	"time"
)

type auditEvent struct {
	apiKey          string
	userName        string
	metricID        int
	model           string
	reqPath         string
	statusCode      int
	inputTokens     int
	outputTokens    int
	cachedTokens    int
	durationMs      int
	tokensPerSecond float64
	promptPerSecond float64
	captureData     []byte
	fingerprint     string
	codexAccount    string
}

type captureBatchItem struct {
	userID       int64
	requestLogID int64
	model        string
	reqPath      string
	fingerprint  string
	captureData  []byte
}

func (s *AuditStore) writer() {
	ticker := time.NewTicker(s.captureFlushInterval)
	defer ticker.Stop()

	eventBuf := make([]auditEvent, 0, s.captureFlushSize)
	captureBuf := make([]captureBatchItem, 0, s.captureFlushSize)

	flush := func() {
		if len(eventBuf) == 0 && len(captureBuf) == 0 {
			return
		}
		if len(eventBuf) > 0 {
			newCaptures := s.flushRequestLogs(eventBuf)
			captureBuf = append(captureBuf, newCaptures...)
			eventBuf = eventBuf[:0]
		}
		if len(captureBuf) > 0 {
			if err := s.flushCaptures(captureBuf); err != nil && s.logger != nil {
				s.logger.Errorf("flush audit captures: %v", err)
			}
			captureBuf = captureBuf[:0]
		}
	}

	for {
		select {
		case event := <-s.auditCh:
			eventBuf = append(eventBuf, event)
			if len(eventBuf) >= s.captureFlushSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-s.doneCh:
			for {
				select {
				case event := <-s.auditCh:
					eventBuf = append(eventBuf, event)
				default:
					flush()
					return
				}
			}
		}
	}
}

type requestLogResult struct {
	userID       int64
	requestLogID int64
	model        string
	reqPath      string
	fingerprint  string
	captureData  []byte
}

func (s *AuditStore) flushRequestLogs(events []auditEvent) []captureBatchItem {
	s.writeMu.Lock()
	tx, err := s.db.Begin()
	if err != nil {
		s.writeMu.Unlock()
		if s.logger != nil {
			s.logger.Errorf("begin audit request log transaction: %v", err)
		}
		return nil
	}

	userCache := make(map[string]int64)
	results := make([]requestLogResult, 0, len(events))

	for i, event := range events {
		userID, ok := userCache[event.apiKey]
		if !ok {
			var err error
			userID, err = s.ensureUserTx(tx, event.apiKey, event.userName)
			if err != nil {
				_ = tx.Rollback()
				s.writeMu.Unlock()
				if s.logger != nil {
					s.logger.Errorf("ensure audit user (event %d/%d): %v", i, len(events), err)
				}
				return nil
			}
			userCache[event.apiKey] = userID
		}

		requestLogID, err := s.insertRequestLogTx(tx, userID, event)
		if err != nil {
			_ = tx.Rollback()
			s.writeMu.Unlock()
			if s.logger != nil {
				s.logger.Errorf("insert audit request log (event %d/%d): %v", i, len(events), err)
			}
			return nil
		}

		results = append(results, requestLogResult{
			userID:       userID,
			requestLogID: requestLogID,
			model:        event.model,
			reqPath:      event.reqPath,
			fingerprint:  event.fingerprint,
			captureData:  event.captureData,
		})
	}

	if err := tx.Commit(); err != nil {
		s.writeMu.Unlock()
		if s.logger != nil {
			s.logger.Errorf("commit audit request log transaction: %v", err)
		}
		return nil
	}
	s.writeMu.Unlock()

	var captures []captureBatchItem
	for _, r := range results {
		if r.captureData == nil {
			continue
		}
		fp := r.fingerprint
		if fp == "" {
			fp = randomFingerprint()
		}
		captures = append(captures, captureBatchItem{
			userID:       r.userID,
			requestLogID: r.requestLogID,
			model:        r.model,
			reqPath:      r.reqPath,
			fingerprint:  fp,
			captureData:  r.captureData,
		})
	}
	return captures
}

func (s *AuditStore) ensureUserTx(tx *sql.Tx, apiKey, userName string) (int64, error) {
	if _, err := tx.Exec(`
		INSERT INTO users (api_key, name) VALUES (?, ?)
		ON CONFLICT(api_key) DO UPDATE SET name = CASE WHEN ? != '' THEN ? ELSE users.name END
	`, apiKey, userName, userName, userName); err != nil {
		return 0, fmt.Errorf("upsert user: %w", err)
	}

	var userID int64
	if err := tx.QueryRow(`SELECT id FROM users WHERE api_key = ?`, apiKey).Scan(&userID); err != nil {
		return 0, fmt.Errorf("select user: %w", err)
	}

	return userID, nil
}

func (s *AuditStore) insertRequestLogTx(tx *sql.Tx, userID int64, event auditEvent) (int64, error) {
	result, err := tx.Exec(`
		INSERT INTO request_log (
			metric_id, user_id, model, req_path, status_code, input_tokens, output_tokens,
			cached_tokens, duration_ms, tokens_per_second, prompt_per_second, codex_account
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, event.metricID, userID, event.model, event.reqPath, event.statusCode, event.inputTokens, event.outputTokens, event.cachedTokens, event.durationMs, event.tokensPerSecond, event.promptPerSecond, sql.NullString{String: event.codexAccount, Valid: event.codexAccount != ""})
	if err != nil {
		return 0, fmt.Errorf("insert request_log: %w", err)
	}

	requestLogID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get request_log id: %w", err)
	}

	return requestLogID, nil
}

func (s *AuditStore) flushCaptures(items []captureBatchItem) error {
	s.writeMu.Lock()
	tx, err := s.db.Begin()
	if err != nil {
		s.writeMu.Unlock()
		return fmt.Errorf("begin capture flush: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	for _, item := range items {
		sessionID, flushErr := upsertSession(tx, item)
		if flushErr != nil {
			err = flushErr
			s.writeMu.Unlock()
			return err
		}

		if _, flushErr = tx.Exec(`
			INSERT INTO captures (session_id, request_log_id, seq_num, data)
			VALUES (?, ?, (SELECT COALESCE(MAX(seq_num), 0) + 1 FROM captures WHERE session_id = ?), ?)
		`, sessionID, item.requestLogID, sessionID, item.captureData); flushErr != nil {
			err = fmt.Errorf("insert capture: %w", flushErr)
			s.writeMu.Unlock()
			return err
		}
	}

	if err = tx.Commit(); err != nil {
		s.writeMu.Unlock()
		return fmt.Errorf("commit capture flush: %w", err)
	}
	s.writeMu.Unlock()

	return nil
}

func upsertSession(tx *sql.Tx, item captureBatchItem) (int64, error) {
	result, err := tx.Exec(`
		INSERT INTO sessions (user_id, model, fingerprint, req_path)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, fingerprint) DO UPDATE SET
			model = excluded.model,
			req_path = excluded.req_path,
			updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
	`, item.userID, item.model, item.fingerprint, item.reqPath)
	if err != nil {
		return 0, fmt.Errorf("upsert session: %w", err)
	}

	sessionID, err := result.LastInsertId()
	if err == nil && sessionID != 0 {
		return sessionID, nil
	}

	if err := tx.QueryRow(`SELECT id FROM sessions WHERE user_id = ? AND fingerprint = ?`, item.userID, item.fingerprint).Scan(&sessionID); err != nil {
		return 0, fmt.Errorf("select session: %w", err)
	}

	return sessionID, nil
}
