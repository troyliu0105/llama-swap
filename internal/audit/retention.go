package audit

import (
	"fmt"
	"time"
)

func (s *AuditStore) startRetention() {
	if s.retentionDays <= 0 {
		return
	}

	s.wg.Go(func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()

		if err := s.cleanExpired(); err != nil && s.logger != nil {
			s.logger.Errorf("clean expired audit records: %v", err)
		}

		for {
			select {
			case <-ticker.C:
				if err := s.cleanExpired(); err != nil && s.logger != nil {
					s.logger.Errorf("clean expired audit records: %v", err)
				}
			case <-s.doneCh:
				return
			}
		}
	})
}

func (s *AuditStore) cleanExpired() error {
	cutoff := time.Now().UTC().AddDate(0, 0, -s.retentionDays).Format("2006-01-02T15:04:05.000Z")

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin audit retention cleanup: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.Exec(`DELETE FROM request_log WHERE created_at < ?`, cutoff); err != nil {
		return fmt.Errorf("delete expired request logs: %w", err)
	}
	if _, err = tx.Exec(`DELETE FROM sessions WHERE id NOT IN (SELECT session_id FROM captures)`); err != nil {
		return fmt.Errorf("delete orphaned sessions: %w", err)
	}
	if _, err = tx.Exec(`DELETE FROM users WHERE id NOT IN (SELECT user_id FROM request_log)`); err != nil {
		return fmt.Errorf("delete orphaned users: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit audit retention cleanup: %w", err)
	}

	return nil
}

func (s *AuditStore) startCapturePurge() {
	if s.capturePurgeDays <= 0 {
		return
	}

	s.wg.Go(func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()

		if err := s.purgeOldCaptures(); err != nil && s.logger != nil {
			s.logger.Errorf("purge old captures: %v", err)
		}

		for {
			select {
			case <-ticker.C:
				if err := s.purgeOldCaptures(); err != nil && s.logger != nil {
					s.logger.Errorf("purge old captures: %v", err)
				}
			case <-s.doneCh:
				return
			}
		}
	})
}

func (s *AuditStore) purgeOldCaptures() error {
	cutoff := time.Now().UTC().AddDate(0, 0, -s.capturePurgeDays).Format("2006-01-02T15:04:05.000Z")

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin capture purge: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	result, err := tx.Exec(`DELETE FROM captures WHERE created_at < ?`, cutoff)
	if err != nil {
		return fmt.Errorf("delete old captures: %w", err)
	}

	// Clean up orphaned sessions (no captures left)
	if _, err = tx.Exec(`DELETE FROM sessions WHERE id NOT IN (SELECT session_id FROM captures)`); err != nil {
		return fmt.Errorf("delete orphaned sessions after capture purge: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit capture purge: %w", err)
	}

	if n, _ := result.RowsAffected(); n > 0 && s.logger != nil {
		s.logger.Infof("audit db: purged %d old capture blobs older than %d days", n, s.capturePurgeDays)
	}

	return nil
}
