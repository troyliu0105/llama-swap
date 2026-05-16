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

// Balancer selects accounts for Codex requests using cache-hit affinity.
//
// Strategy "cache-hit" (default):
//   - Maintains model→account affinity: once a model is routed to an account,
//     it stays there to maximize prompt cache reuse.
//   - For new models without affinity, selects the account with the highest
//     cache-hit rate. Ties broken by fewest total requests.
//   - Falls back to round-robin when no accounts have stats yet.
//
// Strategy "round-robin":
//   - Simple round-robin across all accounts.
type Balancer struct {
	mu       sync.RWMutex
	accounts []string // account names in config order
	stats    map[string]*AccountStats

	// model→account affinity for sticky routing.
	affinity  map[string]string // model → account name
	strategy  string            // "cache-hit" or "round-robin"
	rrCounter atomic.Uint64     // round-robin counter
}

// NewBalancer creates a Balancer for the given accounts and strategy.
// strategy must be "cache-hit" or "round-robin". Empty defaults to "cache-hit".
func NewBalancer(accounts []string, strategy string) *Balancer {
	stats := make(map[string]*AccountStats, len(accounts))
	for _, name := range accounts {
		stats[name] = &AccountStats{}
	}
	if strategy == "" {
		strategy = "cache-hit"
	}
	return &Balancer{
		accounts: accounts,
		stats:    stats,
		affinity: make(map[string]string),
		strategy: strategy,
	}
}

// Select picks the best account for a request to the given model.
func (b *Balancer) Select(model string) (string, error) {
	if len(b.accounts) == 0 {
		return "", fmt.Errorf("no codex accounts available")
	}
	if len(b.accounts) == 1 {
		return b.accounts[0], nil
	}

	switch b.strategy {
	case "round-robin":
		return b.selectRoundRobin(), nil
	default:
		return b.selectCacheHit(model), nil
	}
}

// selectCacheHit uses model affinity + cache-hit rate for selection.
func (b *Balancer) selectCacheHit(model string) string {
	b.mu.RLock()
	if preferred, ok := b.affinity[model]; ok {
		if _, exists := b.stats[preferred]; exists {
			b.mu.RUnlock()
			return preferred
		}
	}
	b.mu.RUnlock()

	// Select by highest cache-hit rate, tie-break by fewest requests
	var best string
	var bestRate float64 = -1
	var bestTotal int64

	for _, name := range b.accounts {
		s := b.stats[name]
		rate := s.CacheHitRate()
		total := s.TotalRequests()

		if rate > bestRate || (rate == bestRate && total < bestTotal) {
			best = name
			bestRate = rate
			bestTotal = total
		}
	}

	// If no account has stats yet (all rates = 0), use round-robin
	if bestRate == 0 && bestTotal == 0 {
		best = b.selectRoundRobin()
	}

	// Set affinity for this model
	b.mu.Lock()
	b.affinity[model] = best
	b.mu.Unlock()

	return best
}

func (b *Balancer) selectRoundRobin() string {
	n := b.rrCounter.Add(1)
	return b.accounts[(n-1)%uint64(len(b.accounts))]
}

// RecordResult records the outcome of a request for future load balancing.
func (b *Balancer) RecordResult(account string, cacheHit bool) {
	if s, ok := b.stats[account]; ok {
		s.RecordRequest(cacheHit)
	}
}

// RecordRequestOnly increments the request counter without a cache-hit sample.
// Use for streaming responses where cache status cannot be determined.
func (b *Balancer) RecordRequestOnly(account string) {
	if s, ok := b.stats[account]; ok {
		s.totalReqs.Add(1)
		s.lastUsedUnix.Store(time.Now().Unix())
	}
}

// AccountStatsSnapshot is a read-only view of account stats for API/UI.
type AccountStatsSnapshot struct {
	Name         string  `json:"name"`
	TotalReqs    int64   `json:"totalRequests"`
	CacheHits    int64   `json:"cacheHits"`
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
	b.affinity = make(map[string]string)
	b.mu.Unlock()
}

// RemoveAccount removes an account from the balancer.
func (b *Balancer) RemoveAccount(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.stats, name)
	for i, n := range b.accounts {
		if n == name {
			b.accounts = append(b.accounts[:i], b.accounts[i+1:]...)
			break
		}
	}
	// Clear affinity entries pointing to removed account
	for model, acc := range b.affinity {
		if acc == name {
			delete(b.affinity, model)
		}
	}
}
