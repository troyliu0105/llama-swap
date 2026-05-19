package codex

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// AccountStats tracks per-account request metrics for load balancing decisions.
type AccountStats struct {
	totalReqs    atomic.Int64
	cacheHits    atomic.Int64
	lastUsedUnix atomic.Int64
}

func (s *AccountStats) RecordRequest(cacheHit bool) {
	s.totalReqs.Add(1)
	if cacheHit {
		s.cacheHits.Add(1)
	}
	s.lastUsedUnix.Store(time.Now().Unix())
}

func (s *AccountStats) CacheHitRate() float64 {
	total := s.totalReqs.Load()
	if total == 0 {
		return 0
	}
	return float64(s.cacheHits.Load()) / float64(total)
}

func (s *AccountStats) TotalRequests() int64 {
	return s.totalReqs.Load()
}

func (s *AccountStats) CacheHits() int64 {
	return s.cacheHits.Load()
}

// Per-model state for sticky routing.
type modelState struct {
	account    string
	failCount  int
	lastFailAt map[string]time.Time
}

// Balancer selects accounts for Codex requests.
//
// Strategy "sticky" (default) keeps each model on a single account and fails
// that model over independently after repeated failures. "cache-hit" is kept as
// an alias for sticky routing because the sticky behavior preserves cache reuse.
// Strategy "round-robin" remains simple round-robin across all accounts.
type Balancer struct {
	mu       sync.RWMutex
	accounts []string
	stats    map[string]*AccountStats

	models   map[string]*modelState
	strategy string
	maxFails int
	cooldown time.Duration

	// quotaExhaustedUntil tracks per-account quota exhaustion. An account
	// entry means the account should not be selected until the given time.
	// The time is derived from the upstream reset_at header so the account
	// automatically becomes available again when the quota window resets.
	quotaExhaustedUntil map[string]time.Time

	rrCounter atomic.Uint64
}

// NewBalancer creates a Balancer for the given accounts and strategy.
// Empty and "cache-hit" both default to sticky routing.
func NewBalancer(accounts []string, strategy string) *Balancer {
	stats := make(map[string]*AccountStats, len(accounts))
	for _, name := range accounts {
		stats[name] = &AccountStats{}
	}
	if strategy == "" || strategy == "cache-hit" {
		strategy = "sticky"
	}
	return &Balancer{
		accounts:            append([]string(nil), accounts...),
		stats:               stats,
		models:              make(map[string]*modelState),
		strategy:            strategy,
		maxFails:            3,
		cooldown:            5 * time.Minute,
		quotaExhaustedUntil: make(map[string]time.Time),
	}
}

// Select picks the best account for a request to the given model.
func (b *Balancer) Select(model string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.accounts) == 0 {
		return "", fmt.Errorf("no codex accounts available")
	}

	switch b.strategy {
	case "round-robin":
		return b.selectRoundRobinLocked(), nil
	default:
		return b.selectStickyLocked(model), nil
	}
}

func (b *Balancer) selectStickyLocked(model string) string {
	state := b.models[model]
	if state == nil {
		state = &modelState{lastFailAt: make(map[string]time.Time)}
		b.models[model] = state
	}

	now := time.Now()
	if state.account != "" {
		if _, exists := b.stats[state.account]; !exists {
			state.account = ""
			state.failCount = 0
		} else if b.isQuotaExhaustedLocked(state.account, now) {
			state.account = ""
			state.failCount = 0
		} else if state.failCount < b.maxFails {
			return state.account
		} else if !b.inCooldownLocked(state, state.account, now) {
			state.failCount = 0
			return state.account
		}
	}

	selected := b.nextAvailableAccountLocked(state, now)
	if selected == "" {
		selected = b.earliestAvailableAccountLocked(state, now)
	}
	state.account = selected
	state.failCount = 0
	return selected
}

func (b *Balancer) selectRoundRobinLocked() string {
	n := b.rrCounter.Add(1)
	return b.accounts[(n-1)%uint64(len(b.accounts))]
}

func (b *Balancer) nextAvailableAccountLocked(state *modelState, now time.Time) string {
	start := 0
	if state.account != "" {
		if index := b.accountIndexLocked(state.account); index >= 0 {
			start = (index + 1) % len(b.accounts)
		} else {
			start = int((b.rrCounter.Add(1) - 1) % uint64(len(b.accounts)))
		}
	} else {
		start = int((b.rrCounter.Add(1) - 1) % uint64(len(b.accounts)))
	}

	for i := 0; i < len(b.accounts); i++ {
		account := b.accounts[(start+i)%len(b.accounts)]
		if b.isQuotaExhaustedLocked(account, now) {
			continue
		}
		if !b.inCooldownLocked(state, account, now) {
			return account
		}
	}
	return ""
}

func (b *Balancer) earliestAvailableAccountLocked(state *modelState, now time.Time) string {
	selected := b.accounts[0]
	selectedExpiry := time.Time{}
	for _, account := range b.accounts {
		if until, ok := b.quotaExhaustedUntil[account]; ok && now.Before(until) {
			if selectedExpiry.IsZero() || until.Before(selectedExpiry) {
				selected = account
				selectedExpiry = until
			}
			continue
		}
		lastFail, ok := state.lastFailAt[account]
		if !ok {
			return account
		}
		expiry := lastFail.Add(b.cooldown)
		if selectedExpiry.IsZero() || expiry.Before(selectedExpiry) {
			selected = account
			selectedExpiry = expiry
		}
	}
	return selected
}

func (b *Balancer) accountIndexLocked(account string) int {
	for i, name := range b.accounts {
		if name == account {
			return i
		}
	}
	return -1
}

func (b *Balancer) inCooldownLocked(state *modelState, account string, now time.Time) bool {
	lastFail, ok := state.lastFailAt[account]
	if !ok || b.cooldown <= 0 {
		return false
	}
	return now.Before(lastFail.Add(b.cooldown))
}

func (b *Balancer) isQuotaExhaustedLocked(account string, now time.Time) bool {
	until, ok := b.quotaExhaustedUntil[account]
	if !ok {
		return false
	}
	return now.Before(until)
}

// RecordQuotaExhaustion marks an account as quota-exhausted until the given
// reset time. The account will not be selected by any model until then.
func (b *Balancer) RecordQuotaExhaustion(account string, resetAt time.Time) {
	if account == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.stats[account]; !exists {
		return
	}
	b.quotaExhaustedUntil[account] = resetAt
}

// IsQuotaExhausted reports whether an account is currently quota-exhausted.
func (b *Balancer) IsQuotaExhausted(account string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.isQuotaExhaustedLocked(account, time.Now())
}

// QuotaExhaustedAccounts returns accounts currently quota-exhausted with
// their reset times.
func (b *Balancer) QuotaExhaustedAccounts() map[string]time.Time {
	b.mu.RLock()
	defer b.mu.RUnlock()

	now := time.Now()
	result := make(map[string]time.Time)
	for account, until := range b.quotaExhaustedUntil {
		if now.Before(until) {
			result[account] = until
		}
	}
	return result
}

// RecordSuccess clears consecutive failures for the model/account pair.
func (b *Balancer) RecordSuccess(model, account string) {
	if account == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.stats[account]; !exists {
		return
	}
	state := b.models[model]
	if state == nil {
		state = &modelState{account: account, lastFailAt: make(map[string]time.Time)}
		b.models[model] = state
	}
	delete(state.lastFailAt, account)
	delete(b.quotaExhaustedUntil, account)
	if state.account == "" || state.account == account {
		state.account = account
		state.failCount = 0
	}
}

// RecordFailure increments consecutive failures for the current model account.
func (b *Balancer) RecordFailure(model, account string) {
	if account == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.stats[account]; !exists {
		return
	}
	state := b.models[model]
	if state == nil {
		state = &modelState{account: account, lastFailAt: make(map[string]time.Time)}
		b.models[model] = state
	}
	if state.account == "" {
		state.account = account
	}
	if state.account != account {
		return
	}
	state.failCount++
	if state.failCount >= b.maxFails {
		state.lastFailAt[account] = time.Now()
	}
}

// RecordResult records the outcome of a request for future load balancing.
func (b *Balancer) RecordResult(account string, cacheHit bool) {
	b.mu.RLock()
	s, ok := b.stats[account]
	b.mu.RUnlock()
	if ok {
		s.RecordRequest(cacheHit)
	}
}

// RecordRequestOnly increments the request counter without a cache-hit sample.
// Use for streaming responses where cache status cannot be determined.
func (b *Balancer) RecordRequestOnly(account string) {
	b.mu.RLock()
	s, ok := b.stats[account]
	b.mu.RUnlock()
	if ok {
		s.totalReqs.Add(1)
		s.lastUsedUnix.Store(time.Now().Unix())
	}
}

// AccountStatsSnapshot is a read-only view of account stats for API/UI.
type AccountStatsSnapshot struct {
	Name         string  `json:"name"`
	TotalReqs    int64   `json:"totalRequests"`
	CacheHits    int64   `json:"cacheHits"`
	CachedTokens int64   `json:"cachedTokens"`
	InputTokens  int64   `json:"inputTokens"`
	CacheHitRate float64 `json:"cacheHitRate"`
}

// Stats returns a snapshot of all account stats.
func (b *Balancer) Stats() []AccountStatsSnapshot {
	b.mu.RLock()
	defer b.mu.RUnlock()

	result := make([]AccountStatsSnapshot, 0, len(b.accounts))
	for _, name := range b.accounts {
		s := b.stats[name]
		result = append(result, AccountStatsSnapshot{
			Name:         name,
			TotalReqs:    s.TotalRequests(),
			CacheHits:    s.CacheHits(),
			CacheHitRate: s.CacheHitRate(),
		})
	}
	return result
}

// ResetAffinity clears all model→account affinity mappings.
// Useful when an account is removed or tokens are refreshed.
func (b *Balancer) ResetAffinity() {
	b.mu.Lock()
	b.models = make(map[string]*modelState)
	b.mu.Unlock()
}

// RemoveAccount removes an account from the balancer.
func (b *Balancer) RemoveAccount(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.stats, name)
	delete(b.quotaExhaustedUntil, name)
	for i, n := range b.accounts {
		if n == name {
			b.accounts = append(b.accounts[:i], b.accounts[i+1:]...)
			break
		}
	}
	for model, state := range b.models {
		delete(state.lastFailAt, name)
		if state.account == name {
			delete(b.models, model)
		}
	}
}
