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

type CodexUsageEntry struct {
	UserID       int64  `json:"user_id"`
	Name         string `json:"name"`
	CodexAccount string `json:"codex_account"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	CachedTokens int64  `json:"cached_tokens"`
	RequestCount int64  `json:"request_count"`
}

type CodexAccountStatsEntry struct {
	CodexAccount  string  `json:"codex_account"`
	TotalRequests int64   `json:"totalRequests"`
	CachedTokens  int64   `json:"cachedTokens"`
	InputTokens   int64   `json:"inputTokens"`
	CacheHitRate  float64 `json:"cacheHitRate"`
}

type CodexUserUsageEntry struct {
	CodexAccount string `json:"codex_account"`
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

func (s *AuditStore) GetCodexAccountStats(period string) ([]CodexAccountStatsEntry, error) {
	since := periodSince(period)
	rows, err := s.db.Query(`
		SELECT codex_account,
			COUNT(*),
			COALESCE(SUM(cached_tokens), 0),
			COALESCE(SUM(input_tokens), 0)
		FROM request_log
		WHERE codex_account IS NOT NULL AND created_at >= ?
		GROUP BY codex_account
		ORDER BY codex_account
	`, since)
	if err != nil {
		return nil, fmt.Errorf("get codex account stats: %w", err)
	}
	defer rows.Close()

	entries := make([]CodexAccountStatsEntry, 0)
	for rows.Next() {
		var entry CodexAccountStatsEntry
		if err := rows.Scan(&entry.CodexAccount, &entry.TotalRequests, &entry.CachedTokens, &entry.InputTokens); err != nil {
			return nil, fmt.Errorf("scan codex account stats: %w", err)
		}
		if entry.InputTokens > 0 {
			entry.CacheHitRate = float64(entry.CachedTokens) / float64(entry.InputTokens)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate codex account stats: %w", err)
	}

	return entries, nil
}

func (s *AuditStore) GetCodexUsage(period string) ([]CodexUsageEntry, error) {
	since := periodSince(period)
	rows, err := s.db.Query(`
		SELECT u.id, u.name, rl.codex_account,
			COALESCE(SUM(rl.input_tokens), 0),
			COALESCE(SUM(rl.output_tokens), 0),
			COALESCE(SUM(CASE WHEN rl.cached_tokens > 0 THEN rl.cached_tokens ELSE 0 END), 0),
			COUNT(*)
		FROM request_log rl
		JOIN users u ON u.id = rl.user_id
		WHERE rl.created_at >= ? AND rl.codex_account IS NOT NULL
		GROUP BY u.id, u.name, rl.codex_account
		ORDER BY u.id, rl.codex_account
	`, since)
	if err != nil {
		return nil, fmt.Errorf("get codex usage: %w", err)
	}
	defer rows.Close()

	entries := make([]CodexUsageEntry, 0)
	for rows.Next() {
		var entry CodexUsageEntry
		if err := rows.Scan(&entry.UserID, &entry.Name, &entry.CodexAccount, &entry.InputTokens, &entry.OutputTokens, &entry.CachedTokens, &entry.RequestCount); err != nil {
			return nil, fmt.Errorf("scan codex usage: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate codex usage: %w", err)
	}

	return entries, nil
}

func (s *AuditStore) GetCodexUserUsage(userID int64, period string) ([]CodexUserUsageEntry, error) {
	since := periodSince(period)
	rows, err := s.db.Query(`
		SELECT codex_account, model,
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(CASE WHEN cached_tokens > 0 THEN cached_tokens ELSE 0 END), 0),
			COUNT(*)
		FROM request_log
		WHERE user_id = ? AND created_at >= ? AND codex_account IS NOT NULL
		GROUP BY codex_account, model
		ORDER BY codex_account, model
	`, userID, since)
	if err != nil {
		return nil, fmt.Errorf("get codex user usage: %w", err)
	}
	defer rows.Close()

	entries := make([]CodexUserUsageEntry, 0)
	for rows.Next() {
		var entry CodexUserUsageEntry
		if err := rows.Scan(&entry.CodexAccount, &entry.Model, &entry.InputTokens, &entry.OutputTokens, &entry.CachedTokens, &entry.RequestCount); err != nil {
			return nil, fmt.Errorf("scan codex user usage: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate codex user usage: %w", err)
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

func (s *AuditStore) SaveCodexQuotaSnapshot(accountName string, snapshotJSON []byte) error {
	_, err := s.db.Exec(`
		INSERT INTO codex_quota_snapshots (account_name, snapshot, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(account_name) DO UPDATE SET
			snapshot = excluded.snapshot,
			updated_at = CURRENT_TIMESTAMP
	`, accountName, string(snapshotJSON))
	if err != nil {
		return fmt.Errorf("save codex quota snapshot for %q: %w", accountName, err)
	}
	return nil
}

type CodexQuotaSnapshotRow struct {
	AccountName string
	Snapshot    string
}

func (s *AuditStore) LoadCodexQuotaSnapshots() ([]CodexQuotaSnapshotRow, error) {
	rows, err := s.db.Query(`SELECT account_name, snapshot FROM codex_quota_snapshots`)
	if err != nil {
		return nil, fmt.Errorf("load codex quota snapshots: %w", err)
	}
	defer rows.Close()

	var result []CodexQuotaSnapshotRow
	for rows.Next() {
		var row CodexQuotaSnapshotRow
		if err := rows.Scan(&row.AccountName, &row.Snapshot); err != nil {
			return nil, fmt.Errorf("scan codex quota snapshot: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate codex quota snapshots: %w", err)
	}
	return result, nil
}

type ActivityEntry struct {
	ID              int    `json:"id"`
	Timestamp       string `json:"timestamp"`
	Model           string `json:"model"`
	ReqPath         string `json:"req_path"`
	RespContentType string `json:"resp_content_type"`
	RespStatusCode  int    `json:"resp_status_code"`
	DurationMs      int    `json:"duration_ms"`
	HasCapture      bool   `json:"has_capture"`
	Tokens          struct {
		CachedTokens    int     `json:"cache_tokens"`
		InputTokens     int     `json:"input_tokens"`
		OutputTokens    int     `json:"output_tokens"`
		PromptPerSecond float64 `json:"prompt_per_second"`
		TokensPerSecond float64 `json:"tokens_per_second"`
	} `json:"tokens"`
}

func (s *AuditStore) GetRecentActivity(limit int) ([]ActivityEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`
		SELECT rl.metric_id, rl.created_at, rl.model, rl.req_path, rl.status_code,
			rl.input_tokens, rl.output_tokens,
			CASE WHEN rl.cached_tokens > 0 THEN rl.cached_tokens ELSE 0 END,
			rl.duration_ms,
			CASE WHEN rl.tokens_per_second >= 0 THEN rl.tokens_per_second ELSE -1 END,
			CASE WHEN rl.prompt_per_second >= 0 THEN rl.prompt_per_second ELSE -1 END,
			EXISTS(SELECT 1 FROM captures c WHERE c.request_log_id = rl.id)
		FROM request_log rl
		ORDER BY rl.id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("get recent activity: %w", err)
	}
	defer rows.Close()

	entries := make([]ActivityEntry, 0, limit)
	for rows.Next() {
		var e ActivityEntry
		if err := rows.Scan(&e.ID, &e.Timestamp, &e.Model, &e.ReqPath, &e.RespStatusCode,
			&e.Tokens.InputTokens, &e.Tokens.OutputTokens, &e.Tokens.CachedTokens,
			&e.DurationMs, &e.Tokens.TokensPerSecond, &e.Tokens.PromptPerSecond, &e.HasCapture); err != nil {
			return nil, fmt.Errorf("scan activity entry: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate activity entries: %w", err)
	}
	return entries, nil
}
