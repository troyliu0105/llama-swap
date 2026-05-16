package codex

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBalancer_SingleAccount(t *testing.T) {
	b := NewBalancer([]string{"only-one"}, "cache-hit")

	selected, err := b.Select("any-model")
	require.NoError(t, err)
	assert.Equal(t, "only-one", selected)
}

func TestBalancer_NoAccounts(t *testing.T) {
	b := NewBalancer(nil, "cache-hit")

	_, err := b.Select("model")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no codex accounts available")
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

func TestBalancer_CacheHitAffinity(t *testing.T) {
	b := NewBalancer([]string{"acct1", "acct2"}, "cache-hit")

	first, err := b.Select("model-x")
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		selected, err := b.Select("model-x")
		require.NoError(t, err)
		assert.Equal(t, first, selected, "affinity should keep model on same account")
	}
}

func TestBalancer_CacheHitSelection(t *testing.T) {
	b := NewBalancer([]string{"low", "high"}, "cache-hit")

	b.RecordResult("high", true)
	b.RecordResult("high", true)
	b.RecordResult("high", true)
	b.RecordResult("low", false)

	b.ResetAffinity()

	selected, err := b.Select("new-model")
	require.NoError(t, err)
	assert.Equal(t, "high", selected, "should prefer account with higher cache-hit rate")
}

func TestBalancer_RecordResult(t *testing.T) {
	b := NewBalancer([]string{"acct"}, "cache-hit")

	b.RecordResult("acct", true)
	b.RecordResult("acct", false)
	b.RecordResult("acct", true)

	stats := b.Stats()
	require.Len(t, stats, 1)
	assert.Equal(t, int64(3), stats[0].TotalReqs)
	assert.Equal(t, int64(2), stats[0].CacheHits)
}

func TestBalancer_Stats(t *testing.T) {
	b := NewBalancer([]string{"alpha", "beta"}, "cache-hit")

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
	b := NewBalancer([]string{"a", "b"}, "cache-hit")

	_, err := b.Select("model-x")
	require.NoError(t, err)

	b.RecordResult("b", true)
	b.RecordResult("b", true)

	b.ResetAffinity()

	selected, err := b.Select("model-x")
	require.NoError(t, err)
	assert.Equal(t, "b", selected, "after reset, affinity should follow stats")
}

func TestBalancer_RemoveAccount(t *testing.T) {
	b := NewBalancer([]string{"a", "b", "c"}, "round-robin")

	b.RemoveAccount("b")

	for i := 0; i < 10; i++ {
		selected, err := b.Select("model")
		require.NoError(t, err)
		assert.NotEqual(t, "b", selected)
	}
}

func TestBalancer_DefaultStrategy(t *testing.T) {
	b := NewBalancer([]string{"a", "b"}, "")
	_, err := b.Select("model")
	require.NoError(t, err)
}

func TestBalancer_RemoveAccountClearsAffinity(t *testing.T) {
	b := NewBalancer([]string{"a", "b"}, "cache-hit")

	selected, err := b.Select("model-x")
	require.NoError(t, err)

	b.RemoveAccount(selected)

	for i := 0; i < 10; i++ {
		got, err := b.Select("model-x")
		require.NoError(t, err)
		assert.NotEqual(t, selected, got, "model should not route to removed account")
	}
}
