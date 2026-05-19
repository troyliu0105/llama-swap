package codex

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestProxy(t *testing.T, accountNames []string) *Proxy {
	t.Helper()
	dir := t.TempDir()
	authPath := filepath.Join(dir, "codex-auth.json")

	store := NewAuthStore(authPath)
	farFuture := time.Now().Add(24 * time.Hour).Unix()
	for _, name := range accountNames {
		require.NoError(t, store.SetToken(name, &TokenData{
			AccessToken:  "tok-" + name,
			RefreshToken: "refresh-" + name,
			ExpiresAt:    farFuture,
			AccountID:    "acctid-" + name,
		}))
	}

	return &Proxy{
		peerID:     "test-peer",
		auth:       store,
		balancer:   NewBalancer(accountNames, "round-robin"),
		logger:     func(format string, args ...any) {},
		quotaStore: NewQuotaStore(),
	}
}

func TestProxy_PrepareRequest_URLRewrite(t *testing.T) {
	p := newTestProxy(t, []string{"acct1"})

	paths := []string{
		"/v1/chat/completions",
		"/chat/completions",
		"/v1/responses",
		"/responses",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, nil)
			account, err := p.PrepareRequest("model-a", req)
			require.NoError(t, err)
			assert.Equal(t, "acct1", account)
			assert.Equal(t, "https", req.URL.Scheme)
			assert.Equal(t, "chatgpt.com", req.URL.Host)
			assert.Equal(t, "/backend-api/codex/responses", req.URL.Path)
		})
	}
}

func TestProxy_PrepareRequest_Headers(t *testing.T) {
	p := newTestProxy(t, []string{"acct1"})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	_, err := p.PrepareRequest("model-a", req)
	require.NoError(t, err)

	assert.Equal(t, "Bearer tok-acct1", req.Header.Get("Authorization"))
	assert.Equal(t, OpenCodeUserAgent(), req.Header.Get("User-Agent"))
	assert.Equal(t, "opencode", req.Header.Get("originator"))
	assert.NotEmpty(t, req.Header.Get("session_id"))
}

func TestProxy_PrepareRequest_AccountID(t *testing.T) {
	p := newTestProxy(t, []string{"acct1"})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	_, err := p.PrepareRequest("model-a", req)
	require.NoError(t, err)

	assert.Equal(t, "acctid-acct1", req.Header.Get("ChatGPT-Account-Id"))
}

func TestProxy_PrepareRequest_AccountIDEmpty(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "codex-auth.json")
	store := NewAuthStore(authPath)
	farFuture := time.Now().Add(24 * time.Hour).Unix()
	require.NoError(t, store.SetToken("noacct", &TokenData{
		AccessToken:  "tok-noacct",
		RefreshToken: "refresh-noacct",
		ExpiresAt:    farFuture,
	}))

	p := &Proxy{
		peerID:     "test-peer",
		auth:       store,
		balancer:   NewBalancer([]string{"noacct"}, "round-robin"),
		logger:     func(format string, args ...any) {},
		quotaStore: NewQuotaStore(),
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	_, err := p.PrepareRequest("model-a", req)
	require.NoError(t, err)
	assert.Empty(t, req.Header.Get("ChatGPT-Account-Id"))
}

func TestProxy_PrepareRequest_StripProxyHeaders(t *testing.T) {
	p := newTestProxy(t, []string{"acct1"})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("x-api-key", "should-be-removed")
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("X-Forwarded-Host", "evil.host")

	_, err := p.PrepareRequest("model-a", req)
	require.NoError(t, err)

	assert.Empty(t, req.Header.Get("x-api-key"))
	assert.Empty(t, req.Header.Get("X-Forwarded-For"))
	assert.Empty(t, req.Header.Get("X-Forwarded-Host"))
}

func TestProxy_DetectCacheHit(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		expected bool
	}{
		{
			name:     "cached_tokens > 0",
			body:     `{"usage":{"prompt_tokens_details":{"cached_tokens":500}}}`,
			expected: true,
		},
		{
			name:     "cached_tokens = 0",
			body:     `{"usage":{"prompt_tokens_details":{"cached_tokens":0}}}`,
			expected: false,
		},
		{
			name:     "empty body",
			body:     "",
			expected: false,
		},
		{
			name:     "missing cached_tokens field",
			body:     `{"usage":{"prompt_tokens_details":{}}}`,
			expected: false,
		},
		{
			name:     "missing usage field",
			body:     `{"model":"gpt-4"}`,
			expected: false,
		},
		{
			name:     "invalid JSON",
			body:     "not json at all but has cached_tokens in it",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, detectCacheHit([]byte(tt.body)))
		})
	}
}

func TestProxy_RecordResponse_RecordsSuccessAndFailure(t *testing.T) {
	p := newTestProxy(t, []string{"acct1", "acct2"})
	p.balancer = NewBalancer([]string{"acct1", "acct2"}, "sticky")

	account, err := p.balancer.Select("model-a")
	require.NoError(t, err)

	p.RecordResponse("model-a", account, http.StatusOK, []byte(`{"usage":{"prompt_tokens_details":{"cached_tokens":1}}}`), nil)
	stats := p.Stats()
	require.Len(t, stats, 2)
	assert.Equal(t, int64(1), stats[0].TotalReqs)
	assert.Equal(t, int64(1), stats[0].CacheHits)

	for i := 0; i < p.balancer.maxFails; i++ {
		p.RecordResponse("model-a", account, http.StatusInternalServerError, nil, nil)
	}

	selected, err := p.balancer.Select("model-a")
	require.NoError(t, err)
	assert.NotEqual(t, account, selected)
}

func TestProxy_RecordStreamingResponseAndError_RecordFailures(t *testing.T) {
	p := newTestProxy(t, []string{"acct1", "acct2"})
	p.balancer = NewBalancer([]string{"acct1", "acct2"}, "sticky")

	account, err := p.balancer.Select("model-a")
	require.NoError(t, err)

	p.RecordStreamingResponse("model-a", account, http.StatusOK, nil)
	stats := p.Stats()
	require.Len(t, stats, 2)
	assert.Equal(t, int64(1), stats[0].TotalReqs)

	for i := 0; i < p.balancer.maxFails; i++ {
		p.RecordError("model-a", account)
	}

	selected, err := p.balancer.Select("model-a")
	require.NoError(t, err)
	assert.NotEqual(t, account, selected)
}

func TestProxy_RecordResponse_CapturesQuotaStats(t *testing.T) {
	p := newTestProxy(t, []string{"acct1"})
	headers := http.Header{
		"X-Codex-Plan-Type":                                      {"pro"},
		"X-Codex-Active-Limit":                                   {"primary"},
		"X-Codex-Primary-Used-Percent":                           {"42"},
		"X-Codex-Credits-Has-Credits":                            {"True"},
		"X-Codex-Bengalfox-Limit-Name":                           {"gpt-5"},
		"X-Codex-Bengalfox-Primary-Over-Secondary-Limit-Percent": {"75"},
	}

	p.RecordResponse("model-a", "acct1", http.StatusOK, nil, headers)

	stats := p.QuotaStats()
	snapshot, ok := stats["acct1"]
	require.True(t, ok)
	assert.Equal(t, "pro", snapshot.PlanType)
	assert.Equal(t, "primary", snapshot.ActiveLimit)
	assert.Equal(t, 42, snapshot.Primary.UsedPercent)
	assert.True(t, snapshot.Credits.HasCredits)
	require.Contains(t, snapshot.ModelLimits, "gpt-5")
	assert.Equal(t, 75, snapshot.ModelLimits["gpt-5"].PrimaryOverSecondaryLimitPct)
}

func TestProxy_RecordStreamingResponse_CapturesQuotaStats(t *testing.T) {
	p := newTestProxy(t, []string{"acct1"})
	headers := http.Header{
		"X-Codex-Plan-Type":              {"team"},
		"X-Codex-Secondary-Used-Percent": {"33"},
		"X-Codex-Credits-Unlimited":      {"True"},
	}

	p.RecordStreamingResponse("model-a", "acct1", http.StatusOK, headers)

	snapshot, ok := p.quotaStore.Get("acct1")
	require.True(t, ok)
	assert.Equal(t, "team", snapshot.PlanType)
	assert.Equal(t, 33, snapshot.Secondary.UsedPercent)
	assert.True(t, snapshot.Credits.Unlimited)
}

func TestProxy_IsFailureStatus(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		expected   bool
	}{
		{"200 OK", http.StatusOK, false},
		{"400 Bad Request", http.StatusBadRequest, false},
		{"401 Unauthorized", http.StatusUnauthorized, true},
		{"403 Forbidden", http.StatusForbidden, true},
		{"429 Too Many Requests", http.StatusTooManyRequests, true},
		{"500 Internal Server Error", http.StatusInternalServerError, true},
		{"502 Bad Gateway", http.StatusBadGateway, true},
		{"503 Service Unavailable", http.StatusServiceUnavailable, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isFailureStatus(tt.statusCode))
		})
	}
}

func TestProxy_RecordResponse_AuthFailuresCauseFailover(t *testing.T) {
	for _, statusCode := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			p := newTestProxy(t, []string{"acct1", "acct2"})
			p.balancer = NewBalancer([]string{"acct1", "acct2"}, "sticky")

			account, err := p.balancer.Select("model-a")
			require.NoError(t, err)

			for i := 0; i < p.balancer.maxFails; i++ {
				p.RecordResponse("model-a", account, statusCode, nil, nil)
			}

			selected, err := p.balancer.Select("model-a")
			require.NoError(t, err)
			assert.NotEqual(t, account, selected, "balancer should fail over after repeated %d responses", statusCode)
		})
	}
}

func TestProxy_RecordStreamingResponse_AuthFailuresCauseFailover(t *testing.T) {
	for _, statusCode := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			p := newTestProxy(t, []string{"acct1", "acct2"})
			p.balancer = NewBalancer([]string{"acct1", "acct2"}, "sticky")

			account, err := p.balancer.Select("model-a")
			require.NoError(t, err)

			for i := 0; i < p.balancer.maxFails; i++ {
				p.RecordStreamingResponse("model-a", account, statusCode, nil)
			}

			selected, err := p.balancer.Select("model-a")
			require.NoError(t, err)
			assert.NotEqual(t, account, selected, "balancer should fail over after repeated %d streaming responses", statusCode)
		})
	}
}

func TestProxy_GenerateSessionID(t *testing.T) {
	p := newTestProxy(t, []string{"acct1"})

	id1 := p.generateSessionID()
	id2 := p.generateSessionID()

	assert.NotEqual(t, id1, id2)
	assert.Contains(t, id1, "sess_")
	assert.Regexp(t, `^sess_[0-9a-f]{8}_\d+$`, id1)
}

func TestProxy_GenerateSessionID_Format(t *testing.T) {
	p := newTestProxy(t, []string{"acct1"})

	for i := 0; i < 10; i++ {
		id := p.generateSessionID()
		assert.Regexp(t, `^sess_[0-9a-f]{8}_\d+$`, id)
	}
}

func TestProxy_RecordResponse_QuotaExhaustion_ImmediateFailover(t *testing.T) {
	p := newTestProxy(t, []string{"acct1", "acct2"})
	p.balancer = NewBalancer([]string{"acct1", "acct2"}, "sticky")

	account, err := p.balancer.Select("model-a")
	require.NoError(t, err)

	resetAt := time.Now().Add(1 * time.Hour).Unix()
	headers := http.Header{
		"X-Codex-Active-Limit":           {"primary"},
		"X-Codex-Primary-Used-Percent":   {"100"},
		"X-Codex-Primary-Reset-At":       {strconv.FormatInt(resetAt, 10)},
		"X-Codex-Primary-Window-Minutes": {"300"},
	}

	p.RecordResponse("model-a", account, http.StatusTooManyRequests, nil, headers)

	assert.True(t, p.balancer.IsQuotaExhausted(account))

	selected, err := p.balancer.Select("model-a")
	require.NoError(t, err)
	assert.NotEqual(t, account, selected)
}

func TestProxy_RecordStreamingResponse_QuotaExhaustion_ImmediateFailover(t *testing.T) {
	p := newTestProxy(t, []string{"acct1", "acct2"})
	p.balancer = NewBalancer([]string{"acct1", "acct2"}, "sticky")

	account, err := p.balancer.Select("model-a")
	require.NoError(t, err)

	resetAt := time.Now().Add(1 * time.Hour).Unix()
	headers := http.Header{
		"X-Codex-Active-Limit":             {"secondary"},
		"X-Codex-Secondary-Used-Percent":   {"100"},
		"X-Codex-Secondary-Reset-At":       {strconv.FormatInt(resetAt, 10)},
		"X-Codex-Secondary-Window-Minutes": {"10080"},
	}

	p.RecordStreamingResponse("model-a", account, http.StatusTooManyRequests, headers)

	assert.True(t, p.balancer.IsQuotaExhausted(account))

	selected, err := p.balancer.Select("model-a")
	require.NoError(t, err)
	assert.NotEqual(t, account, selected)
}

func TestProxy_RecordResponse_NonQuotaFailure_NoExhaustion(t *testing.T) {
	p := newTestProxy(t, []string{"acct1", "acct2"})
	p.balancer = NewBalancer([]string{"acct1", "acct2"}, "sticky")

	account, err := p.balancer.Select("model-a")
	require.NoError(t, err)

	p.RecordResponse("model-a", account, http.StatusInternalServerError, nil, nil)

	assert.False(t, p.balancer.IsQuotaExhausted(account))
}

func TestProxy_RecordResponse_PartialQuota_NoExhaustion(t *testing.T) {
	p := newTestProxy(t, []string{"acct1", "acct2"})
	p.balancer = NewBalancer([]string{"acct1", "acct2"}, "sticky")

	account, err := p.balancer.Select("model-a")
	require.NoError(t, err)

	resetAt := time.Now().Add(1 * time.Hour).Unix()
	headers := http.Header{
		"X-Codex-Active-Limit":           {"primary"},
		"X-Codex-Primary-Used-Percent":   {"80"},
		"X-Codex-Primary-Reset-At":       {strconv.FormatInt(resetAt, 10)},
		"X-Codex-Primary-Window-Minutes": {"300"},
	}

	p.RecordResponse("model-a", account, http.StatusTooManyRequests, nil, headers)

	assert.False(t, p.balancer.IsQuotaExhausted(account))
}

func TestProxy_RecordResponse_SuccessClearsQuotaExhaustion(t *testing.T) {
	p := newTestProxy(t, []string{"acct1", "acct2"})
	p.balancer = NewBalancer([]string{"acct1", "acct2"}, "sticky")

	account, err := p.balancer.Select("model-a")
	require.NoError(t, err)

	p.balancer.RecordQuotaExhaustion(account, time.Now().Add(1*time.Hour))
	assert.True(t, p.balancer.IsQuotaExhausted(account))

	p.RecordResponse("model-a", account, http.StatusOK, []byte(`{"usage":{"prompt_tokens_details":{"cached_tokens":0}}}`), nil)

	assert.False(t, p.balancer.IsQuotaExhausted(account))
}
