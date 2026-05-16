package audit

import (
	"database/sql"
	"fmt"
	"time"
)

type UserInfo struct {
	ID            int64  `json:"id"`
	APIKey        string `json:"api_key"`
	Name          string `json:"name"`
	CreatedAt     string `json:"created_at"`
	TotalRequests int    `json:"total_requests"`
	LastRequestAt string `json:"last_request_at,omitempty"`
}

type UsageEntry struct {
	UserID       int64  `json:"user_id"`
	Name         string `json:"name"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	CachedTokens int64  `json:"cached_tokens"`
}

type ModelUsageEntry struct {
	Model        string `json:"model"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	CachedTokens int64  `json:"cached_tokens"`
	RequestCount int64  `json:"request_count"`
}

type CaptureInfo struct {
	ID        int64  `json:"id"`
	SessionID int64  `json:"session_id"`
	Model     string `json:"model"`
	ReqPath   string `json:"req_path"`
	SeqNum    int    `json:"seq_num"`
	CreatedAt string `json:"created_at"`
}

func (s *AuditStore) ListUsers() ([]UserInfo, error) {
	rows, err := s.db.Query(`
		SELECT u.id, u.api_key, u.name, u.created_at, COUNT(rl.id), COALESCE(MAX(rl.created_at), '')
		FROM users u
		LEFT JOIN request_log rl ON rl.user_id = u.id
		GROUP BY u.id, u.api_key, u.name, u.created_at
		ORDER BY u.created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	users := make([]UserInfo, 0)
	for rows.Next() {
		var user UserInfo
		if err := rows.Scan(&user.ID, &user.APIKey, &user.Name, &user.CreatedAt, &user.TotalRequests, &user.LastRequestAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		user.APIKey = maskAPIKey(user.APIKey)
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}

	return users, nil
}

func (s *AuditStore) GetUsageOverview(period string) ([]UsageEntry, error) {
	since := periodSince(period)
	rows, err := s.db.Query(`
		SELECT u.id, u.name,
			COALESCE(SUM(rl.input_tokens), 0),
			COALESCE(SUM(rl.output_tokens), 0),
			COALESCE(SUM(CASE WHEN rl.cached_tokens > 0 THEN rl.cached_tokens ELSE 0 END), 0)
		FROM users u
		JOIN request_log rl ON rl.user_id = u.id
		WHERE rl.created_at >= ?
		GROUP BY u.id, u.name
		ORDER BY u.id
	`, since)
	if err != nil {
		return nil, fmt.Errorf("get usage overview: %w", err)
	}
	defer rows.Close()

	entries := make([]UsageEntry, 0)
	for rows.Next() {
		var entry UsageEntry
		if err := rows.Scan(&entry.UserID, &entry.Name, &entry.InputTokens, &entry.OutputTokens, &entry.CachedTokens); err != nil {
			return nil, fmt.Errorf("scan usage overview: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate usage overview: %w", err)
	}

	return entries, nil
}

func (s *AuditStore) GetUserUsage(userID int64, period string) ([]ModelUsageEntry, error) {
	since := periodSince(period)
	rows, err := s.db.Query(`
		SELECT model,
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(CASE WHEN cached_tokens > 0 THEN cached_tokens ELSE 0 END), 0),
			COUNT(*)
		FROM request_log
		WHERE user_id = ? AND created_at >= ?
		GROUP BY model
		ORDER BY model
	`, userID, since)
	if err != nil {
		return nil, fmt.Errorf("get user usage: %w", err)
	}
	defer rows.Close()

	entries := make([]ModelUsageEntry, 0)
	for rows.Next() {
		var entry ModelUsageEntry
		if err := rows.Scan(&entry.Model, &entry.InputTokens, &entry.OutputTokens, &entry.CachedTokens, &entry.RequestCount); err != nil {
			return nil, fmt.Errorf("scan user usage: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user usage: %w", err)
	}

	return entries, nil
}

func (s *AuditStore) ListCaptures(userID int64, limit, offset int) ([]CaptureInfo, error) {
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := s.db.Query(`
		SELECT c.id, c.session_id, s.model, s.req_path, c.seq_num, c.created_at
		FROM captures c
		JOIN sessions s ON s.id = c.session_id
		WHERE s.user_id = ?
		ORDER BY c.created_at DESC
		LIMIT ? OFFSET ?
	`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list captures: %w", err)
	}
	defer rows.Close()

	captures := make([]CaptureInfo, 0)
	for rows.Next() {
		var capture CaptureInfo
		if err := rows.Scan(&capture.ID, &capture.SessionID, &capture.Model, &capture.ReqPath, &capture.SeqNum, &capture.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan capture: %w", err)
		}
		captures = append(captures, capture)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate captures: %w", err)
	}

	return captures, nil
}

func (s *AuditStore) GetCaptureData(id int64) ([]byte, error) {
	var data []byte
	if err := s.db.QueryRow(`SELECT data FROM captures WHERE id = ?`, id).Scan(&data); err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, fmt.Errorf("get capture data: %w", err)
	}
	return data, nil
}

func (s *AuditStore) GetCaptureByMetricID(metricID int64) ([]byte, error) {
	var data []byte
	err := s.db.QueryRow(`
		SELECT c.data FROM captures c
		JOIN request_log rl ON rl.id = c.request_log_id
		WHERE rl.metric_id = ?
		ORDER BY rl.created_at DESC, rl.id DESC
		LIMIT 1
	`, metricID).Scan(&data)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get capture by metric id: %w", err)
	}
	return data, nil
}

func (s *AuditStore) GetMaxMetricID() (int, error) {
	var maxID sql.NullInt64
	err := s.db.QueryRow(`SELECT MAX(metric_id) FROM request_log`).Scan(&maxID)
	if err != nil {
		return 0, fmt.Errorf("get max metric id: %w", err)
	}
	if !maxID.Valid {
		return 0, nil
	}
	return int(maxID.Int64), nil
}

func periodSince(period string) string {
	var duration time.Duration
	switch period {
	case "24h":
		duration = 24 * time.Hour
	case "7d":
		duration = 168 * time.Hour
	case "30d":
		duration = 720 * time.Hour
	default:
		duration = 24 * time.Hour
	}
	return time.Now().UTC().Add(-duration).Format("2006-01-02T15:04:05.000Z")
}

func maskAPIKey(apiKey string) string {
	if len(apiKey) <= 8 {
		return apiKey
	}
	return apiKey[:8] + "..."
}
