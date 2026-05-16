package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestAuthStore(t *testing.T) *AuthStore {
	t.Helper()
	dir := t.TempDir()
	return NewAuthStore(filepath.Join(dir, "auth.json"))
}

func TestAuthStore_SaveAndLoad(t *testing.T) {
	store := newTestAuthStore(t)

	token := &TokenData{
		AccessToken:  "test-access-token",
		RefreshToken: "test-refresh-token",
		ExpiresAt:    time.Now().Add(time.Hour).Unix(),
		AccountID:    "acct-123",
		Scope:        "openid",
	}

	err := store.SetToken("myaccount", token)
	require.NoError(t, err)

	store2 := NewAuthStore(store.filePath)
	require.NoError(t, store2.Load())

	got, err := store2.GetToken("myaccount")
	require.NoError(t, err)
	assert.Equal(t, "test-access-token", got.AccessToken)
	assert.Equal(t, "test-refresh-token", got.RefreshToken)
	assert.Equal(t, token.ExpiresAt, got.ExpiresAt)
	assert.Equal(t, "acct-123", got.AccountID)
	assert.Equal(t, "openid", got.Scope)
}

func TestAuthStore_GetToken(t *testing.T) {
	store := newTestAuthStore(t)

	_, err := store.GetToken("missing")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	token := &TokenData{AccessToken: "tok123", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, store.SetToken("acct1", token))

	got, err := store.GetToken("acct1")
	require.NoError(t, err)
	assert.Equal(t, "tok123", got.AccessToken)
}

func TestAuthStore_RemoveToken(t *testing.T) {
	store := newTestAuthStore(t)

	token := &TokenData{AccessToken: "tok", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, store.SetToken("acct1", token))

	err := store.RemoveToken("acct1")
	require.NoError(t, err)

	_, err = store.GetToken("acct1")
	assert.Error(t, err)

	err = store.RemoveToken("nope")
	assert.Error(t, err)
}

func TestAuthStore_ListAccounts(t *testing.T) {
	store := newTestAuthStore(t)

	token := &TokenData{AccessToken: "tok", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, store.SetToken("charlie", token))
	require.NoError(t, store.SetToken("alpha", token))
	require.NoError(t, store.SetToken("bravo", token))

	accounts := store.ListAccounts()
	assert.Equal(t, []string{"alpha", "bravo", "charlie"}, accounts)
}

func TestAuthStore_GetValidToken_NotExpired(t *testing.T) {
	store := newTestAuthStore(t)

	token := &TokenData{
		AccessToken:  "valid-tok",
		RefreshToken: "refresh-tok",
		ExpiresAt:    time.Now().Add(1 * time.Hour).Unix(),
	}
	require.NoError(t, store.SetToken("acct1", token))

	got, err := store.GetValidToken("acct1")
	require.NoError(t, err)
	assert.Equal(t, "valid-tok", got.AccessToken)
}

func TestAuthStore_GetValidToken_ExpiredRefreshFails(t *testing.T) {
	store := newTestAuthStore(t)

	token := &TokenData{
		AccessToken:  "expired-tok",
		RefreshToken: "bad-refresh",
		ExpiresAt:    time.Now().Add(-1 * time.Hour).Unix(),
	}
	require.NoError(t, store.SetToken("acct1", token))

	_, err := store.GetValidToken("acct1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "codex-login")
}

func TestAuthStore_FilePermissions(t *testing.T) {
	store := newTestAuthStore(t)

	token := &TokenData{AccessToken: "tok", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, store.SetToken("acct1", token))

	info, err := os.Stat(store.filePath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func TestAuthStore_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "auth.json")
	require.NoError(t, os.WriteFile(fp, []byte{}, 0600))

	store := NewAuthStore(fp)
	require.NoError(t, store.Load())
	assert.Empty(t, store.ListAccounts())
}

func TestAuthStore_MissingFile(t *testing.T) {
	store := NewAuthStore("/tmp/nonexistent_codex_test_auth_12345.json")
	require.NoError(t, store.Load())
	assert.Empty(t, store.ListAccounts())
}

func TestAuthStore_RoundTripJSON(t *testing.T) {
	store := newTestAuthStore(t)

	tokens := map[string]*TokenData{
		"acct1": {
			AccessToken:  "access1",
			RefreshToken: "refresh1",
			ExpiresAt:    1000,
			AccountID:    "id-1",
			Scope:        "scope1",
		},
		"acct2": {
			AccessToken:  "access2",
			RefreshToken: "refresh2",
			ExpiresAt:    2000,
		},
	}

	for name, td := range tokens {
		require.NoError(t, store.SetToken(name, td))
	}

	data, err := os.ReadFile(store.filePath)
	require.NoError(t, err)

	var loaded map[string]*TokenData
	require.NoError(t, json.Unmarshal(data, &loaded))

	assert.Equal(t, "access1", loaded["acct1"].AccessToken)
	assert.Equal(t, "id-1", loaded["acct1"].AccountID)
	assert.Equal(t, "access2", loaded["acct2"].AccessToken)
	assert.Empty(t, loaded["acct2"].AccountID)
}
