package codex

import (
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type WindowSnapshot struct {
	UsedPercent       int   `json:"used_percent"`
	ResetAfterSeconds int   `json:"reset_after_seconds"`
	ResetAt           int64 `json:"reset_at"`
	WindowMinutes     int   `json:"window_minutes"`
}

type CreditsSnapshot struct {
	Balance    string `json:"balance"`
	HasCredits bool   `json:"has_credits"`
	Unlimited  bool   `json:"unlimited"`
}

type ModelLimitSnapshot struct {
	LimitName                    string `json:"limit_name"`
	PrimaryOverSecondaryLimitPct int    `json:"primary_over_secondary_limit_percent"`
	PrimaryUsedPct               int    `json:"primary_used_percent"`
	PrimaryResetAfterSec         int    `json:"primary_reset_after_seconds"`
	PrimaryResetAt               int64  `json:"primary_reset_at"`
	PrimaryWindowMin             int    `json:"primary_window_minutes"`
	SecondaryUsedPct             int    `json:"secondary_used_percent"`
	SecondaryResetAfterSec       int    `json:"secondary_reset_after_seconds"`
	SecondaryResetAt             int64  `json:"secondary_reset_at"`
	SecondaryWindowMin           int    `json:"secondary_window_minutes"`
}

type QuotaSnapshot struct {
	PlanType    string                        `json:"plan_type"`
	ActiveLimit string                        `json:"active_limit"`
	Primary     WindowSnapshot                `json:"primary"`
	Secondary   WindowSnapshot                `json:"secondary"`
	Credits     CreditsSnapshot               `json:"credits"`
	ModelLimits map[string]ModelLimitSnapshot `json:"model_limits"`
	CapturedAt  time.Time                     `json:"captured_at"`
}

type QuotaStore struct {
	mu        sync.RWMutex
	snapshots map[string]*QuotaSnapshot
}

func NewQuotaStore() *QuotaStore {
	return &QuotaStore{
		snapshots: make(map[string]*QuotaSnapshot),
	}
}

func (s *QuotaStore) Update(account string, snapshot QuotaSnapshot) {
	if s == nil || account == "" {
		return
	}

	copied := cloneQuotaSnapshot(snapshot)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[account] = &copied
}

func (s *QuotaStore) Get(account string) (*QuotaSnapshot, bool) {
	if s == nil {
		return nil, false
	}

	s.mu.RLock()
	snapshot, ok := s.snapshots[account]
	s.mu.RUnlock()
	if !ok || snapshot == nil {
		return nil, false
	}

	copied := cloneQuotaSnapshot(*snapshot)
	return &copied, true
}

func (s *QuotaStore) List() map[string]QuotaSnapshot {
	if s == nil {
		return map[string]QuotaSnapshot{}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make(map[string]QuotaSnapshot, len(s.snapshots))
	for account, snapshot := range s.snapshots {
		if snapshot == nil {
			continue
		}
		out[account] = cloneQuotaSnapshot(*snapshot)
	}
	return out
}

func ParseQuotaHeaders(headers http.Header) QuotaSnapshot {
	snapshot := QuotaSnapshot{
		PlanType:    headers.Get("X-Codex-Plan-Type"),
		ActiveLimit: headers.Get("X-Codex-Active-Limit"),
		Credits: CreditsSnapshot{
			Balance: headers.Get("X-Codex-Credits-Balance"),
		},
		CapturedAt: time.Now(),
	}

	parseIntHeader(headers, "X-Codex-Primary-Used-Percent", &snapshot.Primary.UsedPercent)
	parseIntHeader(headers, "X-Codex-Primary-Reset-After-Seconds", &snapshot.Primary.ResetAfterSeconds)
	parseInt64Header(headers, "X-Codex-Primary-Reset-At", &snapshot.Primary.ResetAt)
	parseIntHeader(headers, "X-Codex-Primary-Window-Minutes", &snapshot.Primary.WindowMinutes)
	parseIntHeader(headers, "X-Codex-Secondary-Used-Percent", &snapshot.Secondary.UsedPercent)
	parseIntHeader(headers, "X-Codex-Secondary-Reset-After-Seconds", &snapshot.Secondary.ResetAfterSeconds)
	parseInt64Header(headers, "X-Codex-Secondary-Reset-At", &snapshot.Secondary.ResetAt)
	parseIntHeader(headers, "X-Codex-Secondary-Window-Minutes", &snapshot.Secondary.WindowMinutes)
	parseBoolHeader(headers, "X-Codex-Credits-Has-Credits", &snapshot.Credits.HasCredits)
	parseBoolHeader(headers, "X-Codex-Credits-Unlimited", &snapshot.Credits.Unlimited)

	return snapshot
}

func ParseBengalfoxHeaders(headers http.Header) map[string]ModelLimitSnapshot {
	limitName := headers.Get("X-Codex-Bengalfox-Limit-Name")
	if limitName == "" {
		return map[string]ModelLimitSnapshot{}
	}

	snapshot := ModelLimitSnapshot{LimitName: limitName}
	parseIntHeader(headers, "X-Codex-Bengalfox-Primary-Over-Secondary-Limit-Percent", &snapshot.PrimaryOverSecondaryLimitPct)
	parseIntHeader(headers, "X-Codex-Bengalfox-Primary-Used-Percent", &snapshot.PrimaryUsedPct)
	parseIntHeader(headers, "X-Codex-Bengalfox-Primary-Reset-After-Seconds", &snapshot.PrimaryResetAfterSec)
	parseInt64Header(headers, "X-Codex-Bengalfox-Primary-Reset-At", &snapshot.PrimaryResetAt)
	parseIntHeader(headers, "X-Codex-Bengalfox-Primary-Window-Minutes", &snapshot.PrimaryWindowMin)
	parseIntHeader(headers, "X-Codex-Bengalfox-Secondary-Used-Percent", &snapshot.SecondaryUsedPct)
	parseIntHeader(headers, "X-Codex-Bengalfox-Secondary-Reset-After-Seconds", &snapshot.SecondaryResetAfterSec)
	parseInt64Header(headers, "X-Codex-Bengalfox-Secondary-Reset-At", &snapshot.SecondaryResetAt)
	parseIntHeader(headers, "X-Codex-Bengalfox-Secondary-Window-Minutes", &snapshot.SecondaryWindowMin)

	return map[string]ModelLimitSnapshot{limitName: snapshot}
}

func parseIntHeader(headers http.Header, name string, target *int) {
	value := headers.Get(name)
	if value == "" {
		return
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		log.Printf("codex quota: cannot parse %s=%q as int: %v", name, value, err)
		return
	}
	*target = n
}

func parseInt64Header(headers http.Header, name string, target *int64) {
	value := headers.Get(name)
	if value == "" {
		return
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		log.Printf("codex quota: cannot parse %s=%q as int64: %v", name, value, err)
		return
	}
	*target = n
}

func parseBoolHeader(headers http.Header, name string, target *bool) {
	value := headers.Get(name)
	if value == "" {
		return
	}
	b, err := strconv.ParseBool(value)
	if err != nil {
		log.Printf("codex quota: cannot parse %s=%q as bool: %v", name, value, err)
		return
	}
	*target = b
}

func cloneQuotaSnapshot(snapshot QuotaSnapshot) QuotaSnapshot {
	if snapshot.ModelLimits == nil {
		return snapshot
	}

	modelLimits := make(map[string]ModelLimitSnapshot, len(snapshot.ModelLimits))
	for model, limit := range snapshot.ModelLimits {
		modelLimits[model] = limit
	}
	snapshot.ModelLimits = modelLimits
	return snapshot
}
