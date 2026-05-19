package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/proxy/codex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApiCodexAccounts_NoPeerProxyData(t *testing.T) {
	cfg := testConfigFromYAML(t, `
healthCheckTimeout: 15
logLevel: error
models:
  model1:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model1
`)

	proxy := New(cfg)
	defer proxy.Shutdown(context.Background())

	req := httptest.NewRequest(http.MethodGet, "/api/codex/accounts", nil)
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var response []interface{}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Empty(t, response)
}

func TestApiCodexUsage_WithAudit(t *testing.T) {
	cfg := testConfigFromYAML(t, `
healthCheckTimeout: 15
logLevel: error
audit:
  enabled: true
  database: `+t.TempDir()+`/audit.db
models:
  model1:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model1
`)

	proxy := New(cfg)
	defer proxy.Shutdown(context.Background())

	req := httptest.NewRequest(http.MethodGet, "/api/codex/usage?period=7d", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var usage []interface{}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &usage))
	assert.Empty(t, usage)

	req = httptest.NewRequest(http.MethodGet, "/api/codex/usage/not-a-user?period=7d", nil)
	w = httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.JSONEq(t, `{"error":"invalid user_id"}`, w.Body.String())
}

func TestCollectCodexAccounts_WithProxy(t *testing.T) {
	authPath := t.TempDir() + "/nonexistent-auth.json"
	codexProxy, err := codex.NewProxy(
		"test-peer",
		authPath,
		[]string{"account1", "account2"},
		"round-robin",
		30*time.Second,
		func(format string, args ...any) {},
	)
	require.NoError(t, err)

	codexProxies := map[string]*codex.Proxy{
		"codex-mini": codexProxy,
	}

	accounts := collectCodexAccounts(codexProxies, nil, "7d")
	require.Len(t, accounts, 2, "collectCodexAccounts should return accounts from balancer even with empty auth store")

	names := make(map[string]bool)
	for _, acct := range accounts {
		names[acct.Name] = true
	}
	assert.True(t, names["account1"], "account1 should be present")
	assert.True(t, names["account2"], "account2 should be present")
}

func TestCollectCodexAccounts_WithAuthAndQuota(t *testing.T) {
	authDir := t.TempDir()
	authPath := authDir + "/codex-auth.json"
	futureExpiry := time.Now().Add(1 * time.Hour).Unix()

	authData := map[string]*codex.TokenData{
		"account1": {
			AccessToken:  "test-access-token",
			RefreshToken: "test-refresh-token",
			ExpiresAt:    futureExpiry,
			AccountID:    "acct_12345",
		},
	}
	authJSON, err := json.Marshal(authData)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(authPath, authJSON, 0644))

	codexProxy, err := codex.NewProxy(
		"test-peer",
		authPath,
		[]string{"account1"},
		"round-robin",
		30*time.Second,
		func(format string, args ...any) {},
	)
	require.NoError(t, err)

	codexProxy.RecordResponse("codex-mini", "account1", 200, []byte(`{}`), http.Header{
		"X-Codex-Active-Limit":                  {"5h"},
		"X-Codex-Primary-Used-Percent":          {"42"},
		"X-Codex-Primary-Reset-After-Seconds":   {"1800"},
		"X-Codex-Primary-Reset-At":              {"1700000000"},
		"X-Codex-Primary-Window-Minutes":        {"300"},
		"X-Codex-Secondary-Used-Percent":        {"10"},
		"X-Codex-Secondary-Reset-After-Seconds": {"3600"},
		"X-Codex-Secondary-Reset-At":            {"1700001000"},
		"X-Codex-Secondary-Window-Minutes":      {"300"},
		"X-Codex-Credits-Balance":               {"unlimited"},
		"X-Codex-Credits-Has-Credits":           {"true"},
		"X-Codex-Credits-Unlimited":             {"true"},
	})

	codexProxies := map[string]*codex.Proxy{
		"codex-mini": codexProxy,
	}

	accounts := collectCodexAccounts(codexProxies, nil, "7d")
	require.Len(t, accounts, 1)

	acct := accounts[0]
	assert.Equal(t, "account1", acct.Name)
	assert.Equal(t, "acct_12345", acct.AccountID)
	assert.True(t, acct.TokenValid)
	assert.Equal(t, "5h", acct.ActiveLimit)
	assert.Equal(t, 42, acct.Primary.UsedPercent)
	assert.Equal(t, 10, acct.Secondary.UsedPercent)
	assert.True(t, acct.Credits.Unlimited)
	assert.NotNil(t, acct.Stats)
	assert.Equal(t, int64(1), acct.Stats.TotalReqs)
}
