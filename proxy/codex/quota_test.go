package codex

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseQuotaHeaders_FullHeaders(t *testing.T) {
	headers := http.Header{
		"X-Codex-Plan-Type":                     {"pro"},
		"X-Codex-Active-Limit":                  {"secondary"},
		"X-Codex-Primary-Used-Percent":          {"12"},
		"X-Codex-Primary-Reset-After-Seconds":   {"300"},
		"X-Codex-Primary-Reset-At":              {"1710000000"},
		"X-Codex-Primary-Window-Minutes":        {"60"},
		"X-Codex-Secondary-Used-Percent":        {"34"},
		"X-Codex-Secondary-Reset-After-Seconds": {"600"},
		"X-Codex-Secondary-Reset-At":            {"1710000600"},
		"X-Codex-Secondary-Window-Minutes":      {"120"},
		"X-Codex-Credits-Balance":               {"42.50"},
		"X-Codex-Credits-Has-Credits":           {"True"},
		"X-Codex-Credits-Unlimited":             {"False"},
	}

	snapshot := ParseQuotaHeaders(headers)

	assert.Equal(t, "pro", snapshot.PlanType)
	assert.Equal(t, "secondary", snapshot.ActiveLimit)
	assert.Equal(t, 12, snapshot.Primary.UsedPercent)
	assert.Equal(t, 300, snapshot.Primary.ResetAfterSeconds)
	assert.Equal(t, int64(1710000000), snapshot.Primary.ResetAt)
	assert.Equal(t, 60, snapshot.Primary.WindowMinutes)
	assert.Equal(t, 34, snapshot.Secondary.UsedPercent)
	assert.Equal(t, 600, snapshot.Secondary.ResetAfterSeconds)
	assert.Equal(t, int64(1710000600), snapshot.Secondary.ResetAt)
	assert.Equal(t, 120, snapshot.Secondary.WindowMinutes)
	assert.Equal(t, "42.50", snapshot.Credits.Balance)
	assert.True(t, snapshot.Credits.HasCredits)
	assert.False(t, snapshot.Credits.Unlimited)
	assert.WithinDuration(t, time.Now(), snapshot.CapturedAt, time.Second)
}

func TestParseQuotaHeaders_EmptyHeaders(t *testing.T) {
	snapshot := ParseQuotaHeaders(http.Header{})

	assert.Empty(t, snapshot.PlanType)
	assert.Empty(t, snapshot.ActiveLimit)
	assert.Zero(t, snapshot.Primary)
	assert.Zero(t, snapshot.Secondary)
	assert.Zero(t, snapshot.Credits)
	assert.Nil(t, snapshot.ModelLimits)
	assert.WithinDuration(t, time.Now(), snapshot.CapturedAt, time.Second)
}

func TestParseQuotaHeaders_PartialHeaders(t *testing.T) {
	headers := http.Header{
		"X-Codex-Plan-Type":              {"free"},
		"X-Codex-Primary-Used-Percent":   {"not-an-int"},
		"X-Codex-Secondary-Used-Percent": {"45"},
		"X-Codex-Credits-Has-Credits":    {"False"},
		"X-Codex-Credits-Unlimited":      {"not-a-bool"},
	}

	snapshot := ParseQuotaHeaders(headers)

	assert.Equal(t, "free", snapshot.PlanType)
	assert.Zero(t, snapshot.Primary.UsedPercent)
	assert.Equal(t, 45, snapshot.Secondary.UsedPercent)
	assert.False(t, snapshot.Credits.HasCredits)
	assert.False(t, snapshot.Credits.Unlimited)
}

func TestParseBengalfoxHeaders_Present(t *testing.T) {
	headers := http.Header{
		"X-Codex-Bengalfox-Limit-Name":                           {"gpt-5"},
		"X-Codex-Bengalfox-Primary-Over-Secondary-Limit-Percent": {"80"},
		"X-Codex-Bengalfox-Primary-Used-Percent":                 {"25"},
		"X-Codex-Bengalfox-Primary-Reset-After-Seconds":          {"111"},
		"X-Codex-Bengalfox-Primary-Reset-At":                     {"1710000001"},
		"X-Codex-Bengalfox-Primary-Window-Minutes":               {"15"},
		"X-Codex-Bengalfox-Secondary-Used-Percent":               {"50"},
		"X-Codex-Bengalfox-Secondary-Reset-After-Seconds":        {"222"},
		"X-Codex-Bengalfox-Secondary-Reset-At":                   {"1710000002"},
		"X-Codex-Bengalfox-Secondary-Window-Minutes":             {"30"},
	}

	limits := ParseBengalfoxHeaders(headers)

	require.Contains(t, limits, "gpt-5")
	limit := limits["gpt-5"]
	assert.Equal(t, "gpt-5", limit.LimitName)
	assert.Equal(t, 80, limit.PrimaryOverSecondaryLimitPct)
	assert.Equal(t, 25, limit.PrimaryUsedPct)
	assert.Equal(t, 111, limit.PrimaryResetAfterSec)
	assert.Equal(t, int64(1710000001), limit.PrimaryResetAt)
	assert.Equal(t, 15, limit.PrimaryWindowMin)
	assert.Equal(t, 50, limit.SecondaryUsedPct)
	assert.Equal(t, 222, limit.SecondaryResetAfterSec)
	assert.Equal(t, int64(1710000002), limit.SecondaryResetAt)
	assert.Equal(t, 30, limit.SecondaryWindowMin)
}

func TestParseBengalfoxHeaders_WithoutHeaders(t *testing.T) {
	limits := ParseBengalfoxHeaders(http.Header{})

	assert.Empty(t, limits)
}

func TestQuotaStore_UpdateGetListConcurrentSafety(t *testing.T) {
	store := NewQuotaStore()
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			account := fmt.Sprintf("acct-%d", i%5)
			store.Update(account, QuotaSnapshot{
				PlanType: fmt.Sprintf("plan-%d", i),
				Primary:  WindowSnapshot{UsedPercent: i},
				ModelLimits: map[string]ModelLimitSnapshot{
					"gpt-5": {LimitName: "gpt-5", PrimaryUsedPct: i},
				},
				CapturedAt: time.Unix(int64(i), 0),
			})
			_, _ = store.Get(account)
			_ = store.List()
		}()
	}
	wg.Wait()

	list := store.List()
	require.Len(t, list, 5)

	for account, snapshot := range list {
		fromGet, ok := store.Get(account)
		require.True(t, ok)
		assert.Equal(t, snapshot, *fromGet)
	}

	first, ok := store.Get("acct-0")
	require.True(t, ok)
	first.PlanType = "mutated"
	first.ModelLimits["gpt-5"] = ModelLimitSnapshot{LimitName: "mutated"}

	second, ok := store.Get("acct-0")
	require.True(t, ok)
	assert.NotEqual(t, "mutated", second.PlanType)
	assert.NotEqual(t, "mutated", second.ModelLimits["gpt-5"].LimitName)
}

func TestQuotaStore_GetMissingAccount(t *testing.T) {
	store := NewQuotaStore()

	snapshot, ok := store.Get("missing")

	assert.False(t, ok)
	assert.Nil(t, snapshot)
}
