package codex

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBalancer_StickyRouting(t *testing.T) {
	b := NewBalancer([]string{"acct1", "acct2"}, "sticky")

	first, err := b.Select("model-x")
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		selected, err := b.Select("model-x")
		require.NoError(t, err)
		assert.Equal(t, first, selected)
	}
}

func TestBalancer_Failover(t *testing.T) {
	b := NewBalancer([]string{"acct1", "acct2"}, "sticky")

	first, err := b.Select("model-x")
	require.NoError(t, err)

	b.RecordFailure("model-x", first)
	b.RecordFailure("model-x", first)

	selected, err := b.Select("model-x")
	require.NoError(t, err)
	assert.Equal(t, first, selected)

	b.RecordFailure("model-x", first)

	selected, err = b.Select("model-x")
	require.NoError(t, err)
	assert.NotEqual(t, first, selected)
}

func TestBalancer_PerModelIsolation(t *testing.T) {
	b := NewBalancer([]string{"acct1", "acct2"}, "sticky")
	b.models["model-a"] = &modelState{account: "acct1", lastFailAt: make(map[string]time.Time)}
	b.models["model-b"] = &modelState{account: "acct1", lastFailAt: make(map[string]time.Time)}

	b.RecordFailure("model-a", "acct1")
	b.RecordFailure("model-a", "acct1")
	b.RecordFailure("model-a", "acct1")

	selectedA, err := b.Select("model-a")
	require.NoError(t, err)
	selectedB, err := b.Select("model-b")
	require.NoError(t, err)

	assert.Equal(t, "acct2", selectedA)
	assert.Equal(t, "acct1", selectedB)
}

func TestBalancer_TTLRecovery(t *testing.T) {
	b := NewBalancer([]string{"acct1", "acct2"}, "sticky")
	b.cooldown = 10 * time.Millisecond

	first, err := b.Select("model-x")
	require.NoError(t, err)
	for i := 0; i < b.maxFails; i++ {
		b.RecordFailure("model-x", first)
	}

	second, err := b.Select("model-x")
	require.NoError(t, err)
	require.NotEqual(t, first, second)

	time.Sleep(20 * time.Millisecond)
	for i := 0; i < b.maxFails; i++ {
		b.RecordFailure("model-x", second)
	}

	selected, err := b.Select("model-x")
	require.NoError(t, err)
	assert.Equal(t, first, selected)
}

func TestBalancer_AllAccountsFailedPicksEarliestExpiry(t *testing.T) {
	b := NewBalancer([]string{"acct1", "acct2", "acct3"}, "sticky")
	b.cooldown = time.Hour
	now := time.Now()
	b.models["model-x"] = &modelState{
		account:   "acct3",
		failCount: b.maxFails,
		lastFailAt: map[string]time.Time{
			"acct1": now.Add(-10 * time.Minute),
			"acct2": now.Add(-30 * time.Minute),
			"acct3": now.Add(-5 * time.Minute),
		},
	}

	selected, err := b.Select("model-x")
	require.NoError(t, err)
	assert.Equal(t, "acct2", selected)
}

func TestBalancer_RoundRobin(t *testing.T) {
	b := NewBalancer([]string{"a", "b"}, "round-robin")

	results := make(map[string]int)
	for i := 0; i < 4; i++ {
		selected, err := b.Select("model")
		require.NoError(t, err)
		results[selected]++
	}

	assert.Equal(t, 2, results["a"])
	assert.Equal(t, 2, results["b"])
}

func TestBalancer_SingleAccount(t *testing.T) {
	b := NewBalancer([]string{"only-one"}, "sticky")

	selected, err := b.Select("any-model")
	require.NoError(t, err)
	assert.Equal(t, "only-one", selected)

	for i := 0; i < b.maxFails; i++ {
		b.RecordFailure("any-model", "only-one")
	}

	selected, err = b.Select("any-model")
	require.NoError(t, err)
	assert.Equal(t, "only-one", selected)
}

func TestBalancer_RemoveAccount(t *testing.T) {
	b := NewBalancer([]string{"a", "b", "c"}, "sticky")

	selected, err := b.Select("model-x")
	require.NoError(t, err)
	b.RemoveAccount(selected)

	for i := 0; i < 10; i++ {
		got, err := b.Select("model-x")
		require.NoError(t, err)
		assert.NotEqual(t, selected, got)
	}
}

func TestBalancer_NoAccounts(t *testing.T) {
	b := NewBalancer(nil, "sticky")

	_, err := b.Select("model")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no codex accounts available")
}

func TestBalancer_CacheHitStrategyUsesStickyRouting(t *testing.T) {
	b := NewBalancer([]string{"acct1", "acct2"}, "cache-hit")

	first, err := b.Select("model-x")
	require.NoError(t, err)
	selected, err := b.Select("model-x")
	require.NoError(t, err)

	assert.Equal(t, first, selected)
}

func TestBalancer_DefaultStrategyUsesStickyRouting(t *testing.T) {
	b := NewBalancer([]string{"acct1", "acct2"}, "")

	first, err := b.Select("model-x")
	require.NoError(t, err)
	selected, err := b.Select("model-x")
	require.NoError(t, err)

	assert.Equal(t, first, selected)
}

func TestBalancer_RecordResult(t *testing.T) {
	b := NewBalancer([]string{"acct"}, "sticky")

	b.RecordResult("acct", true)
	b.RecordResult("acct", false)
	b.RecordResult("acct", true)

	stats := b.Stats()
	require.Len(t, stats, 1)
	assert.Equal(t, int64(3), stats[0].TotalReqs)
	assert.Equal(t, int64(2), stats[0].CacheHits)
}

func TestBalancer_Stats(t *testing.T) {
	b := NewBalancer([]string{"alpha", "beta"}, "sticky")

	b.RecordResult("alpha", true)
	b.RecordResult("beta", false)
	b.RecordResult("beta", true)

	stats := b.Stats()
	require.Len(t, stats, 2)

	assert.Equal(t, "alpha", stats[0].Name)
	assert.Equal(t, int64(1), stats[0].TotalReqs)
	assert.Equal(t, int64(1), stats[0].CacheHits)

	assert.Equal(t, "beta", stats[1].Name)
	assert.Equal(t, int64(2), stats[1].TotalReqs)
	assert.Equal(t, int64(1), stats[1].CacheHits)
}

func TestBalancer_ResetAffinity(t *testing.T) {
	b := NewBalancer([]string{"a", "b"}, "sticky")

	first, err := b.Select("model-x")
	require.NoError(t, err)
	b.ResetAffinity()

	selected, err := b.Select("model-x")
	require.NoError(t, err)
	assert.NotEmpty(t, selected)
	assert.NotContains(t, b.models, "missing-model")
	assert.NotPanics(t, func() { b.RecordSuccess("model-x", first) })
}

func TestBalancer_QuotaExhaustion_SkipsAccount(t *testing.T) {
	b := NewBalancer([]string{"acct1", "acct2"}, "sticky")

	resetAt := time.Now().Add(1 * time.Hour)
	b.RecordQuotaExhaustion("acct1", resetAt)

	selected, err := b.Select("model-x")
	require.NoError(t, err)
	assert.Equal(t, "acct2", selected)
}

func TestBalancer_QuotaExhaustion_AllExhausted(t *testing.T) {
	b := NewBalancer([]string{"acct1", "acct2"}, "sticky")

	resetAt1 := time.Now().Add(1 * time.Hour)
	resetAt2 := time.Now().Add(30 * time.Minute)
	b.RecordQuotaExhaustion("acct1", resetAt1)
	b.RecordQuotaExhaustion("acct2", resetAt2)

	selected, err := b.Select("model-x")
	require.NoError(t, err)
	assert.Equal(t, "acct2", selected)
}

func TestBalancer_QuotaExhaustion_Recovery(t *testing.T) {
	b := NewBalancer([]string{"acct1", "acct2"}, "sticky")

	resetAt := time.Now().Add(10 * time.Millisecond)
	b.RecordQuotaExhaustion("acct1", resetAt)

	selected, err := b.Select("model-x")
	require.NoError(t, err)
	assert.Equal(t, "acct2", selected)

	time.Sleep(20 * time.Millisecond)

	assert.False(t, b.IsQuotaExhausted("acct1"))
}

func TestBalancer_QuotaExhaustion_ClearedOnSuccess(t *testing.T) {
	b := NewBalancer([]string{"acct1", "acct2"}, "sticky")

	b.RecordQuotaExhaustion("acct1", time.Now().Add(1*time.Hour))

	assert.True(t, b.IsQuotaExhausted("acct1"))

	b.RecordSuccess("model-x", "acct1")

	assert.False(t, b.IsQuotaExhausted("acct1"))
}

func TestBalancer_QuotaExhaustion_RemovedWithAccount(t *testing.T) {
	b := NewBalancer([]string{"acct1", "acct2"}, "sticky")

	b.RecordQuotaExhaustion("acct1", time.Now().Add(1*time.Hour))
	b.RemoveAccount("acct1")

	assert.False(t, b.IsQuotaExhausted("acct1"))
}

func TestBalancer_QuotaExhaustion_StickyFailsOver(t *testing.T) {
	b := NewBalancer([]string{"acct1", "acct2"}, "sticky")

	selected, err := b.Select("model-x")
	require.NoError(t, err)
	require.Equal(t, "acct1", selected)

	b.RecordQuotaExhaustion("acct1", time.Now().Add(1*time.Hour))

	selected, err = b.Select("model-x")
	require.NoError(t, err)
	assert.Equal(t, "acct2", selected)
}

func TestBalancer_IsQuotaExhausted(t *testing.T) {
	b := NewBalancer([]string{"acct1"}, "sticky")

	assert.False(t, b.IsQuotaExhausted("acct1"))
	assert.False(t, b.IsQuotaExhausted("nonexistent"))

	b.RecordQuotaExhaustion("acct1", time.Now().Add(1*time.Hour))
	assert.True(t, b.IsQuotaExhausted("acct1"))
}

func TestBalancer_QuotaExhaustedAccounts(t *testing.T) {
	b := NewBalancer([]string{"acct1", "acct2"}, "sticky")

	resetAt := time.Now().Add(1 * time.Hour)
	b.RecordQuotaExhaustion("acct1", resetAt)

	exhausted := b.QuotaExhaustedAccounts()
	assert.Contains(t, exhausted, "acct1")
	assert.NotContains(t, exhausted, "acct2")
	assert.Equal(t, resetAt, exhausted["acct1"])
}
