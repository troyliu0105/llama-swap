package audit

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestAuditStore(t *testing.T, retentionDays int, captureFlushSize int) (*AuditStore, string) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "audit.db")
	store, err := NewAuditStore(dbPath, retentionDays, captureFlushSize, 1, nil)
	require.NoError(t, err)
	return store, dbPath
}

func openTestDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
	})

	return db
}

func eventuallyCount(t *testing.T, db *sql.DB, query string, want int) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for {
		var got int
		err := db.QueryRow(query).Scan(&got)
		if err == nil && got == want {
			return
		}
		if time.Now().After(deadline) {
			require.NoError(t, err)
			assert.Equal(t, want, got)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func queryInt(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()

	var got int
	require.NoError(t, db.QueryRow(query, args...).Scan(&got))
	return got
}

func recordRequest(store *AuditStore, apiKey, model, reqPath string, inputTokens, outputTokens, cachedTokens int, captureData []byte, fingerprint string) {
	store.RecordRequest(0, apiKey, "TestUser", model, reqPath, 200, inputTokens, outputTokens, cachedTokens, 123, 45.6, 7.8, captureData, fingerprint)
}

func closeStore(t *testing.T, store *AuditStore) {
	t.Helper()
	require.NoError(t, store.Close())
}

func TestAuditStore_NewStore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "audit.db")

	store, err := NewAuditStore(dbPath, 0, 10, 1, nil)
	require.NoError(t, err)
	defer store.Close()

	assert.FileExists(t, dbPath)
	assert.Equal(t, "wal", queryJournalMode(t, store.db))
	assert.Equal(t, 1, queryInt(t, store.db, `SELECT COUNT(*) FROM audit_migration_versions`))
	assert.Equal(t, 0, queryInt(t, store.db, `SELECT COUNT(*) FROM request_log`))
}

func TestAuditStore_NewStore_CreatesDirectory(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "nested", "audit", "audit.db")

	store, err := NewAuditStore(dbPath, 0, 10, 1, nil)
	require.NoError(t, err)
	defer store.Close()

	assert.FileExists(t, dbPath)
}

func TestAuditStore_DoubleOpen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "audit.db")

	store1, err := NewAuditStore(dbPath, 0, 10, 1, nil)
	require.NoError(t, err)
	defer store1.Close()

	store2, err := NewAuditStore(dbPath, 0, 10, 1, nil)
	require.NoError(t, err)
	defer store2.Close()

	assert.Equal(t, 1, queryInt(t, store2.db, `SELECT COUNT(*) FROM audit_migration_versions`))
}

func queryJournalMode(t *testing.T, db *sql.DB) string {
	t.Helper()

	var mode string
	require.NoError(t, db.QueryRow(`PRAGMA journal_mode`).Scan(&mode))
	return mode
}

func TestAuditStore_RecordRequest(t *testing.T) {
	store, dbPath := newTestAuditStore(t, 0, 1)

	recordRequest(store, "key-one", "model-a", "/v1/chat/completions", 11, 22, 3, nil, "")
	closeStore(t, store)
	db := openTestDB(t, dbPath)

	assert.Equal(t, 1, queryInt(t, db, `SELECT COUNT(*) FROM request_log`))

	var model, reqPath string
	var statusCode, inputTokens, outputTokens, cachedTokens, durationMs int
	var tokensPerSecond, promptPerSecond float64
	require.NoError(t, db.QueryRow(`
		SELECT model, req_path, status_code, input_tokens, output_tokens,
			cached_tokens, duration_ms, tokens_per_second, prompt_per_second
		FROM request_log
	`).Scan(&model, &reqPath, &statusCode, &inputTokens, &outputTokens, &cachedTokens, &durationMs, &tokensPerSecond, &promptPerSecond))

	assert.Equal(t, "model-a", model)
	assert.Equal(t, "/v1/chat/completions", reqPath)
	assert.Equal(t, 200, statusCode)
	assert.Equal(t, 11, inputTokens)
	assert.Equal(t, 22, outputTokens)
	assert.Equal(t, 3, cachedTokens)
	assert.Equal(t, 123, durationMs)
	assert.Equal(t, 45.6, tokensPerSecond)
	assert.Equal(t, 7.8, promptPerSecond)
}

func TestAuditStore_RecordRequest_DropsOnFullChannel(t *testing.T) {
	store := &AuditStore{
		auditCh: make(chan auditEvent, 1),
		doneCh:  make(chan struct{}),
	}
	store.auditCh <- auditEvent{apiKey: "queued"}

	done := make(chan struct{})
	go func() {
		store.RecordRequest(0, "dropped", "", "model", "/v1/chat/completions", 200, 0, 0, 0, 0, 0, 0, nil, "")
		close(done)
	}()

	select {
	case <-done:
		assert.Len(t, store.auditCh, 1)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("RecordRequest blocked when the audit channel was full")
	}
}

func TestAuditStore_Close_FlushesPending(t *testing.T) {
	store, dbPath := newTestAuditStore(t, 0, 100)

	recordRequest(store, "flush-key", "model-a", "/v1/chat/completions", 1, 2, 0, []byte(`{"request":"one"}`), "flush-fp")
	closeStore(t, store)

	db := openTestDB(t, dbPath)
	assert.Equal(t, 1, queryInt(t, db, `SELECT COUNT(*) FROM request_log`))
	assert.Equal(t, 1, queryInt(t, db, `SELECT COUNT(*) FROM sessions`))
	assert.Equal(t, 1, queryInt(t, db, `SELECT COUNT(*) FROM captures`))
}

func TestAuditStore_UserEnsure(t *testing.T) {
	store, _ := newTestAuditStore(t, 0, 1)
	defer closeStore(t, store)

	recordRequest(store, "same-key", "model-a", "/v1/chat/completions", 1, 1, 0, nil, "")
	recordRequest(store, "same-key", "model-b", "/v1/chat/completions", 1, 1, 0, nil, "")
	recordRequest(store, "other-key", "model-c", "/v1/chat/completions", 1, 1, 0, nil, "")
	eventuallyCount(t, store.db, `SELECT COUNT(*) FROM request_log`, 3)

	assert.Equal(t, 2, queryInt(t, store.db, `SELECT COUNT(*) FROM users`))
	assert.Equal(t, 2, queryInt(t, store.db, `SELECT COUNT(*) FROM request_log WHERE user_id = (SELECT id FROM users WHERE api_key = 'same-key')`))
}

func TestComputeFingerprint_ChatCompletions(t *testing.T) {
	body := []byte(`{"messages":[{"role":"system","content":"You are helpful"},{"role":"user","content":"Hello"}]}`)

	fp := ComputeFingerprint("/v1/chat/completions", body)
	fp2 := ComputeFingerprint(" /v1/chat/completions?stream=true ", body)

	assert.NotEmpty(t, fp)
	assert.Equal(t, fp, fp2)
	assert.NotEqual(t, fp, ComputeFingerprint("/v1/chat/completions", []byte(`{"messages":[{"role":"user","content":"Different"}]}`)))
}

func TestComputeFingerprint_Responses_WithPreviousResponseID(t *testing.T) {
	body := []byte(`{"previous_response_id":"resp_123","instructions":"ignored","input":"ignored"}`)

	fp := ComputeFingerprint("/v1/responses", body)
	fp2 := ComputeFingerprint("/v1/responses", []byte(`{"previous_response_id":"resp_123","instructions":"different","input":"different"}`))

	assert.NotEmpty(t, fp)
	assert.Equal(t, fp, fp2)
	assert.NotEqual(t, fp, ComputeFingerprint("/v1/responses", []byte(`{"previous_response_id":"resp_456"}`)))
}

func TestComputeFingerprint_Responses_NoPreviousResponseID(t *testing.T) {
	body := []byte(`{"instructions":"Be concise","input":[{"role":"system","content":"ignored"},{"role":"user","content":"Summarize this"}]}`)

	fp := ComputeFingerprint("/v1/responses", body)
	fp2 := ComputeFingerprint("v1/responses/", body)

	assert.NotEmpty(t, fp)
	assert.Equal(t, fp, fp2)
	assert.NotEqual(t, fp, ComputeFingerprint("/v1/responses", []byte(`{"instructions":"Be verbose","input":[{"role":"user","content":"Summarize this"}]}`)))
}

func TestComputeFingerprint_Messages(t *testing.T) {
	body := []byte(`{"system":"You are Claude","messages":[{"role":"assistant","content":"Hi"},{"role":"user","content":"Hello"}]}`)

	fp := ComputeFingerprint("/v1/messages", body)
	fp2 := ComputeFingerprint("/v1/messages", body)

	assert.NotEmpty(t, fp)
	assert.Equal(t, fp, fp2)
}

func TestComputeFingerprint_Messages_SystemArray(t *testing.T) {
	body := []byte(`{"system":[{"type":"text","text":"Part one"},{"type":"text","text":"Part two"},{"type":"image","source":"ignored"}],"messages":[{"role":"user","content":[{"type":"text","text":"Hello"}]}]}`)

	fp := ComputeFingerprint("/v1/messages", body)
	fp2 := ComputeFingerprint("/v1/messages", []byte(`{"system":"Part onePart two","messages":[{"role":"user","content":"Hello"}]}`))

	assert.NotEmpty(t, fp)
	assert.Equal(t, fp, fp2)
}

func TestComputeFingerprint_Completions(t *testing.T) {
	body := []byte(`{"prompt":"Complete this sentence"}`)

	fp := ComputeFingerprint("/v1/completions", body)
	fp2 := ComputeFingerprint("/v1/completions", body)

	assert.NotEmpty(t, fp)
	assert.Equal(t, fp, fp2)
	assert.NotEqual(t, fp, ComputeFingerprint("/v1/completions", []byte(`{"prompt":"Different"}`)))
}

func TestComputeFingerprint_EmptyBody(t *testing.T) {
	fp := ComputeFingerprint("/v1/chat/completions", nil)
	fp2 := ComputeFingerprint("/v1/chat/completions", nil)

	assert.NotEmpty(t, fp)
	assert.NotEmpty(t, fp2)
	assert.NotEqual(t, fp, fp2)
}

func TestIsChatEndpoint(t *testing.T) {
	assert.True(t, IsChatEndpoint("/v1/chat/completions"))
	assert.True(t, IsChatEndpoint("/v1/responses"))
	assert.True(t, IsChatEndpoint("/v1/messages"))
	assert.True(t, IsChatEndpoint("v1/messages/?beta=true"))
	assert.False(t, IsChatEndpoint("/v1/completions"))
	assert.False(t, IsChatEndpoint("/v1/embeddings"))
	assert.False(t, IsChatEndpoint("/v1/audio/speech"))
}

func TestAuditStore_CaptureSessionDedup(t *testing.T) {
	store, _ := newTestAuditStore(t, 0, 10)
	defer closeStore(t, store)

	tx, err := store.db.Begin()
	require.NoError(t, err)
	userID, err := store.ensureUserTx(tx, "capture-key", "")
	require.NoError(t, err)
	requestLogID, err := store.insertRequestLogTx(tx, userID, auditEvent{
		model:      "model-a",
		reqPath:    "/v1/chat/completions",
		statusCode: 200,
	})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	first := captureBatchItem{
		userID:       userID,
		requestLogID: requestLogID,
		model:        "model-a",
		reqPath:      "/v1/chat/completions",
		fingerprint:  "same-fp",
		captureData:  []byte(`first`),
	}
	second := first
	second.model = "model-b"
	second.reqPath = "/v1/responses"
	second.captureData = []byte(`second`)

	require.NoError(t, store.flushCaptures([]captureBatchItem{first}))
	require.NoError(t, store.flushCaptures([]captureBatchItem{second}))

	assert.Equal(t, 1, queryInt(t, store.db, `SELECT COUNT(*) FROM sessions`))
	assert.Equal(t, 1, queryInt(t, store.db, `SELECT COUNT(*) FROM captures`))

	var model, reqPath string
	var seqNum int
	var data []byte
	require.NoError(t, store.db.QueryRow(`
		SELECT s.model, s.req_path, c.seq_num, c.data
		FROM captures c JOIN sessions s ON s.id = c.session_id
	`).Scan(&model, &reqPath, &seqNum, &data))
	assert.Equal(t, "model-b", model)
	assert.Equal(t, "/v1/responses", reqPath)
	assert.Equal(t, 2, seqNum)
	assert.Equal(t, []byte(`second`), data)
}

func TestAuditStore_CaptureOnlyForChatEndpoints(t *testing.T) {
	store, _ := newTestAuditStore(t, 0, 1)
	defer closeStore(t, store)

	recordRequest(store, "embed-key", "embed-model", "/v1/embeddings", 5, 0, 0, nil, ComputeFingerprint("/v1/embeddings", []byte(`{"input":"hello"}`)))
	eventuallyCount(t, store.db, `SELECT COUNT(*) FROM request_log`, 1)

	assert.Equal(t, 0, queryInt(t, store.db, `SELECT COUNT(*) FROM sessions`))
	assert.Equal(t, 0, queryInt(t, store.db, `SELECT COUNT(*) FROM captures`))
}

func TestAuditStore_RetentionCleanup(t *testing.T) {
	disabledStore, disabledPath := newTestAuditStore(t, 0, 1)
	insertOldAuditRows(t, disabledStore.db)
	closeStore(t, disabledStore)

	disabledDB := openTestDB(t, disabledPath)
	assert.Equal(t, 1, queryInt(t, disabledDB, `SELECT COUNT(*) FROM request_log`))
	assert.Equal(t, 1, queryInt(t, disabledDB, `SELECT COUNT(*) FROM users`))

	enabledStore, _ := newTestAuditStore(t, 0, 1)
	defer closeStore(t, enabledStore)
	insertOldAuditRows(t, enabledStore.db)

	assert.Equal(t, 1, queryInt(t, enabledStore.db, `SELECT COUNT(*) FROM request_log`))
	assert.Equal(t, 1, queryInt(t, enabledStore.db, `SELECT COUNT(*) FROM sessions`))

	enabledStore.retentionDays = 1
	require.NoError(t, enabledStore.cleanExpired())

	assert.Equal(t, 0, queryInt(t, enabledStore.db, `SELECT COUNT(*) FROM request_log`))
	assert.Equal(t, 0, queryInt(t, enabledStore.db, `SELECT COUNT(*) FROM sessions`))
	assert.Equal(t, 0, queryInt(t, enabledStore.db, `SELECT COUNT(*) FROM users`))
}

func insertOldAuditRows(t *testing.T, db *sql.DB) {
	t.Helper()

	oldTime := time.Now().UTC().AddDate(0, 0, -2).Format("2006-01-02T15:04:05.000Z")
	result, err := db.Exec(`INSERT INTO users (api_key, created_at) VALUES (?, ?)`, "old-key", oldTime)
	require.NoError(t, err)
	userID, err := result.LastInsertId()
	require.NoError(t, err)
	result, err = db.Exec(`
		INSERT INTO request_log (user_id, model, req_path, status_code, input_tokens, output_tokens, cached_tokens, duration_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, userID, "old-model", "/v1/chat/completions", 200, 1, 2, 0, 10, oldTime)
	require.NoError(t, err)
	requestLogID, err := result.LastInsertId()
	require.NoError(t, err)
	result, err = db.Exec(`
		INSERT INTO sessions (user_id, model, fingerprint, req_path, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, userID, "old-model", "old-fp", "/v1/chat/completions", oldTime, oldTime)
	require.NoError(t, err)
	sessionID, err := result.LastInsertId()
	require.NoError(t, err)
	_, err = db.Exec(`
		INSERT INTO captures (session_id, request_log_id, seq_num, data, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, sessionID, requestLogID, 1, []byte(`old`), oldTime)
	require.NoError(t, err)
}

func TestAuditStore_ListUsers(t *testing.T) {
	store, _ := newTestAuditStore(t, 0, 1)
	defer closeStore(t, store)
	recordRequest(store, "user-one-key", "model-a", "/v1/chat/completions", 1, 2, 0, nil, "")
	recordRequest(store, "user-one-key", "model-b", "/v1/chat/completions", 3, 4, 0, nil, "")
	recordRequest(store, "user-two-key", "model-c", "/v1/chat/completions", 5, 6, 0, nil, "")
	eventuallyCount(t, store.db, `SELECT COUNT(*) FROM request_log`, 3)

	users, err := store.ListUsers()
	require.NoError(t, err)
	require.Len(t, users, 2)

	byKey := map[string]UserInfo{}
	for _, user := range users {
		byKey[user.APIKey] = user
	}
	assert.Equal(t, 2, byKey["user-one..."].TotalRequests)
	assert.Equal(t, 1, byKey["user-two..."].TotalRequests)
	assert.NotEmpty(t, byKey["user-one..."].CreatedAt)
	assert.NotEmpty(t, byKey["user-one..."].LastRequestAt)
}

func TestAuditStore_GetUsageOverview(t *testing.T) {
	store, _ := newTestAuditStore(t, 0, 1)
	defer closeStore(t, store)
	recordRequest(store, "usage-one", "model-a", "/v1/chat/completions", 10, 20, 5, nil, "")
	recordRequest(store, "usage-one", "model-b", "/v1/chat/completions", 1, 2, -1, nil, "")
	recordRequest(store, "usage-two", "model-c", "/v1/chat/completions", 3, 4, 7, nil, "")
	eventuallyCount(t, store.db, `SELECT COUNT(*) FROM request_log`, 3)

	entries, err := store.GetUsageOverview("invalid")
	require.NoError(t, err)
	require.Len(t, entries, 2)

	byID := map[int64]UsageEntry{}
	for _, entry := range entries {
		byID[entry.UserID] = entry
	}

	var userOneID, userTwoID int64
	require.NoError(t, store.db.QueryRow(`SELECT id FROM users WHERE api_key = ?`, "usage-one").Scan(&userOneID))
	require.NoError(t, store.db.QueryRow(`SELECT id FROM users WHERE api_key = ?`, "usage-two").Scan(&userTwoID))
	assert.Equal(t, int64(11), byID[userOneID].InputTokens)
	assert.Equal(t, int64(22), byID[userOneID].OutputTokens)
	assert.Equal(t, int64(5), byID[userOneID].CachedTokens)
	assert.Equal(t, int64(3), byID[userTwoID].InputTokens)
	assert.Equal(t, int64(4), byID[userTwoID].OutputTokens)
	assert.Equal(t, int64(7), byID[userTwoID].CachedTokens)
}

func TestAuditStore_GetUserUsage(t *testing.T) {
	store, _ := newTestAuditStore(t, 0, 1)
	defer closeStore(t, store)
	recordRequest(store, "model-usage", "model-b", "/v1/chat/completions", 1, 2, 0, nil, "")
	recordRequest(store, "model-usage", "model-a", "/v1/chat/completions", 10, 20, 5, nil, "")
	recordRequest(store, "model-usage", "model-a", "/v1/chat/completions", 3, 4, -1, nil, "")
	eventuallyCount(t, store.db, `SELECT COUNT(*) FROM request_log`, 3)

	var userID int64
	require.NoError(t, store.db.QueryRow(`SELECT id FROM users WHERE api_key = ?`, "model-usage").Scan(&userID))
	entries, err := store.GetUserUsage(userID, "24h")
	require.NoError(t, err)
	require.Len(t, entries, 2)

	assert.Equal(t, "model-a", entries[0].Model)
	assert.Equal(t, int64(13), entries[0].InputTokens)
	assert.Equal(t, int64(24), entries[0].OutputTokens)
	assert.Equal(t, int64(5), entries[0].CachedTokens)
	assert.Equal(t, int64(2), entries[0].RequestCount)
	assert.Equal(t, "model-b", entries[1].Model)
	assert.Equal(t, int64(1), entries[1].RequestCount)
}

func TestAuditStore_ListCaptures(t *testing.T) {
	store, _ := newTestAuditStore(t, 0, 1)
	defer closeStore(t, store)
	recordRequest(store, "capture-list", "model-a", "/v1/chat/completions", 1, 2, 0, []byte(`capture-a`), "fp-a")
	recordRequest(store, "capture-list", "model-b", "/v1/responses", 3, 4, 0, []byte(`capture-b`), "fp-b")
	eventuallyCount(t, store.db, `SELECT COUNT(*) FROM captures`, 2)

	var userID int64
	require.NoError(t, store.db.QueryRow(`SELECT id FROM users WHERE api_key = ?`, "capture-list").Scan(&userID))
	captures, err := store.ListCaptures(userID, 1, 0)
	require.NoError(t, err)
	require.Len(t, captures, 1)
	assert.NotZero(t, captures[0].ID)
	assert.NotZero(t, captures[0].SessionID)
	assert.NotEmpty(t, captures[0].Model)
	assert.NotEmpty(t, captures[0].ReqPath)
	assert.Equal(t, 1, captures[0].SeqNum)
	assert.NotEmpty(t, captures[0].CreatedAt)

	captures, err = store.ListCaptures(userID, 0, -1)
	require.NoError(t, err)
	assert.Len(t, captures, 2)
}

func TestAuditStore_GetCaptureData(t *testing.T) {
	store, _ := newTestAuditStore(t, 0, 1)
	defer closeStore(t, store)
	recordRequest(store, "capture-data", "model-a", "/v1/chat/completions", 1, 2, 0, []byte(`capture-payload`), "fp-data")
	eventuallyCount(t, store.db, `SELECT COUNT(*) FROM captures`, 1)

	var captureID int64
	require.NoError(t, store.db.QueryRow(`SELECT id FROM captures`).Scan(&captureID))
	data, err := store.GetCaptureData(captureID)
	require.NoError(t, err)
	assert.Equal(t, []byte(`capture-payload`), data)

	_, err = store.GetCaptureData(captureID + 1)
	assert.ErrorIs(t, err, sql.ErrNoRows)
}

func TestAuditStore_ListUsers_APIKeyMasking(t *testing.T) {
	store, _ := newTestAuditStore(t, 0, 1)
	defer closeStore(t, store)
	recordRequest(store, "short", "model-a", "/v1/chat/completions", 1, 2, 0, nil, "")
	recordRequest(store, "12345678", "model-b", "/v1/chat/completions", 1, 2, 0, nil, "")
	recordRequest(store, "123456789", "model-c", "/v1/chat/completions", 1, 2, 0, nil, "")
	eventuallyCount(t, store.db, `SELECT COUNT(*) FROM request_log`, 3)

	users, err := store.ListUsers()
	require.NoError(t, err)
	require.Len(t, users, 3)

	keys := make(map[string]bool)
	for _, user := range users {
		keys[user.APIKey] = true
	}
	assert.True(t, keys["short"])
	assert.True(t, keys["12345678"])
	assert.True(t, keys["12345678..."])
	assert.False(t, keys["123456789"])
}

func TestAuditStore_UserNameUpdate(t *testing.T) {
	store, _ := newTestAuditStore(t, 0, 1)
	defer closeStore(t, store)

	// First request with no name
	store.RecordRequest(0, "key-alice", "", "model-a", "/v1/chat/completions", 200, 1, 2, 0, 10, 5.0, 3.0, nil, "")
	eventuallyCount(t, store.db, `SELECT COUNT(*) FROM request_log`, 1)

	users, err := store.ListUsers()
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, "", users[0].Name)

	// Second request with a name — should update
	store.RecordRequest(0, "key-alice", "Alice", "model-a", "/v1/chat/completions", 200, 3, 4, 0, 10, 5.0, 3.0, nil, "")
	eventuallyCount(t, store.db, `SELECT COUNT(*) FROM request_log`, 2)

	users, err = store.ListUsers()
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, "Alice", users[0].Name)

	// Third request with updated name — should update again
	store.RecordRequest(0, "key-alice", "Alice Updated", "model-a", "/v1/chat/completions", 200, 5, 6, 0, 10, 5.0, 3.0, nil, "")
	eventuallyCount(t, store.db, `SELECT COUNT(*) FROM request_log`, 3)

	users, err = store.ListUsers()
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, "Alice Updated", users[0].Name)
}
