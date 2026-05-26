package codex

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRefresher_StartStop(t *testing.T) {
	store := newTestAuthStore(t)
	refresher := NewRefresher(store, nil)

	refresher.Start()
	stopped := make(chan struct{})
	go func() {
		refresher.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("refresher did not stop within 2 seconds")
	}
}

func TestRefresher_RefreshesExpiredToken(t *testing.T) {
	store := newTestAuthStore(t)
	expiresAt := time.Now().Add(10 * time.Minute).Unix()
	require.NoError(t, store.SetToken("acct1", &TokenData{
		AccessToken:  "valid-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    expiresAt,
	}))

	refresher := NewRefresher(store, nil)
	retryAfter := make(map[string]time.Time)
	retryDelay := make(map[string]time.Duration)

	wait := refresher.computeNextRefresh(retryAfter, retryDelay)

	got, err := store.GetToken("acct1")
	require.NoError(t, err)
	assert.Equal(t, "valid-token", got.AccessToken)
	assert.Empty(t, retryAfter)
	assert.Empty(t, retryDelay)
	assert.Greater(t, wait, 7*time.Minute)
	assert.Less(t, wait, 9*time.Minute)
}

func TestRefresher_NoAccounts(t *testing.T) {
	store := newTestAuthStore(t)
	refresher := NewRefresher(store, nil)

	assert.Equal(t, time.Minute, refresher.computeNextRefresh(nil, nil))

	refresher.Start()
	stopped := make(chan struct{})
	go func() {
		refresher.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("refresher with no accounts did not stop within 2 seconds")
	}
}

func TestRefresher_BackoffOnFailure(t *testing.T) {
	store := newTestAuthStore(t)
	require.NoError(t, store.SetToken("acct1", &TokenData{
		AccessToken:  "expired-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    time.Now().Add(-time.Minute).Unix(),
	}))

	var logs []string
	refresher := NewRefresher(store, func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	})
	refreshAttempts := 0
	refresher.getValidToken = func(accountName string) (*TokenData, error) {
		refreshAttempts++
		return nil, fmt.Errorf("forced refresh failure for %s", accountName)
	}

	retryAfter := make(map[string]time.Time)
	retryDelay := make(map[string]time.Duration)

	firstWait := refresher.computeNextRefresh(retryAfter, retryDelay)
	assert.Equal(t, 1, refreshAttempts)
	assert.Equal(t, minRefreshRetryDelay, retryDelay["acct1"])
	assert.GreaterOrEqual(t, firstWait, minRefreshRetryDelay-time.Second)
	assert.LessOrEqual(t, firstWait, minRefreshRetryDelay+time.Second)
	require.Len(t, logs, 1)
	assert.True(t, strings.Contains(logs[0], "failed for account \"acct1\""))

	secondWait := refresher.computeNextRefresh(retryAfter, retryDelay)
	assert.Equal(t, 1, refreshAttempts)
	assert.Equal(t, minRefreshRetryDelay, retryDelay["acct1"])
	assert.Greater(t, secondWait, time.Second)

	retryAfter["acct1"] = time.Now().Add(-time.Second)
	thirdWait := refresher.computeNextRefresh(retryAfter, retryDelay)
	assert.Equal(t, 2, refreshAttempts)
	assert.Equal(t, 2*minRefreshRetryDelay, retryDelay["acct1"])
	assert.GreaterOrEqual(t, thirdWait, 2*minRefreshRetryDelay-time.Second)
	assert.LessOrEqual(t, thirdWait, 2*minRefreshRetryDelay+time.Second)
}
