package codex

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

const (
	OpenCodeVersion = "1.0.0"
)

type QuotaPersistFunc func(account string, snapshot QuotaSnapshot)

type Proxy struct {
	peerID         string
	auth           *AuthStore
	balancer       *Balancer
	transport      *http.Transport
	logger         func(format string, args ...any)
	quotaStore     *QuotaStore
	onQuotaPersist QuotaPersistFunc

	sessionCounter atomic.Uint64
}

func NewProxy(
	peerID string,
	authPath string,
	accountNames []string,
	strategy string,
	timeout time.Duration,
	logger func(format string, args ...any),
) (*Proxy, error) {
	if logger == nil {
		logger = func(format string, args ...any) {
			fmt.Printf(format, args...)
		}
	}

	auth := NewAuthStore(authPath)
	if err := auth.Load(); err != nil {
		return nil, fmt.Errorf("codex proxy: failed to load auth store: %w", err)
	}

	balancer := NewBalancer(accountNames, strategy)

	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		ExpectContinueTimeout: 1 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
	}

	return &Proxy{
		peerID:     peerID,
		auth:       auth,
		balancer:   balancer,
		transport:  transport,
		logger:     logger,
		quotaStore: NewQuotaStore(),
	}, nil
}

func (p *Proxy) PrepareRequest(model string, req *http.Request) (string, error) {
	account, err := p.balancer.Select(model)
	if err != nil {
		return "", fmt.Errorf("codex proxy: account selection: %w", err)
	}

	token, err := p.auth.GetValidToken(account)
	if err != nil {
		return "", fmt.Errorf("codex proxy: token for account %q: %w", account, err)
	}

	p.rewriteURL(req)
	p.injectHeaders(req, token)
	p.stripProxyHeaders(req)

	p.logger("[CODEX] ▶ %s | %s | %s", account, model, req.URL.Path)
	return account, nil
}

func (p *Proxy) rewriteURL(req *http.Request) {
	path := req.URL.Path
	if path == "/v1/responses" || path == "/responses" || path == "/v1/chat/completions" || path == "/chat/completions" {
		req.URL.Scheme = "https"
		req.URL.Host = "chatgpt.com"
		req.URL.Path = "/backend-api/codex/responses"
		req.Host = "chatgpt.com"
	}
}

func (p *Proxy) injectHeaders(req *http.Request, token *TokenData) {
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("User-Agent", OpenCodeUserAgent())
	req.Header.Set("originator", "opencode")
	req.Header.Set("session_id", p.generateSessionID())
	if token.AccountID != "" {
		req.Header.Set("ChatGPT-Account-Id", token.AccountID)
	}
}

func (p *Proxy) stripProxyHeaders(req *http.Request) {
	req.Header.Del("x-api-key")
	req.Header.Del("X-Forwarded-For")
	req.Header.Del("X-Forwarded-Host")
	req.Header.Del("X-Forwarded-Proto")
	req.Header.Del("X-Forwarded-Server")
	req.Header.Del("X-Real-IP")
	req.Header.Del("Forwarded")
	req.Header.Del("Via")
}

func (p *Proxy) RecordResponse(model, account string, statusCode int, responseBody []byte, headers http.Header) {
	cacheHit := detectCacheHit(responseBody)
	p.balancer.RecordResult(account, cacheHit)

	if statusCode == http.StatusOK {
		p.balancer.RecordSuccess(model, account)
	} else {
		p.recordQuotaHeaders(account, headers)
		if resetAt, exhausted := p.detectQuotaExhaustion(account); exhausted {
			p.balancer.RecordQuotaExhaustion(account, resetAt)
			p.logger("[CODEX] ⛔ %s | %s | quota_exhausted | reset_at=%d", account, model, resetAt.Unix())
		} else {
			p.balancer.RecordFailure(model, account)
		}
	}

	size := len(responseBody)
	p.logger("[CODEX] ◀ %s | %s | %d | %s | cache=%v", account, model, statusCode, humanSize(size), cacheHit)
	if statusCode == http.StatusOK {
		p.recordQuotaHeaders(account, headers)
	}
}

func (p *Proxy) RecordStreamingResponse(model, account string, statusCode int, headers http.Header) {
	p.balancer.RecordRequestOnly(account)
	if statusCode == http.StatusOK {
		p.balancer.RecordSuccess(model, account)
	} else {
		p.recordQuotaHeaders(account, headers)
		if resetAt, exhausted := p.detectQuotaExhaustion(account); exhausted {
			p.balancer.RecordQuotaExhaustion(account, resetAt)
			p.logger("[CODEX] ⛔ %s | %s | quota_exhausted | reset_at=%d", account, model, resetAt.Unix())
		} else {
			p.balancer.RecordFailure(model, account)
		}
	}
	p.logger("[CODEX] ◀ %s | %s | %d SSE | %s", account, model, statusCode, p.peerID)
	if statusCode == http.StatusOK {
		p.recordQuotaHeaders(account, headers)
	}
}

func (p *Proxy) RecordError(model, account string) {
	p.balancer.RecordFailure(model, account)
	p.logger("[CODEX] ✗ %s | %s | connection_error", account, model)
}

func (p *Proxy) GetTransport() *http.Transport {
	return p.transport
}

func (p *Proxy) Stats() []AccountStatsSnapshot {
	return p.balancer.Stats()
}

func (p *Proxy) QuotaStats() map[string]QuotaSnapshot {
	if p.quotaStore == nil {
		return map[string]QuotaSnapshot{}
	}
	return p.quotaStore.List()
}

// AccountTokenStatus returns token validity info for an account.
type AccountTokenStatus struct {
	Name       string `json:"name"`
	TokenValid bool   `json:"token_valid"`
	ExpiresAt  int64  `json:"token_expires_at"`
	AccountID  string `json:"account_id,omitempty"`
}

// ListAccountStatuses returns token validity for all known accounts.
func (p *Proxy) ListAccountStatuses() []AccountTokenStatus {
	names := p.auth.ListAccounts()
	statuses := make([]AccountTokenStatus, 0, len(names))
	now := time.Now().Unix()
	for _, name := range names {
		status := AccountTokenStatus{Name: name}
		if token, err := p.auth.GetToken(name); err == nil && token != nil {
			status.ExpiresAt = token.ExpiresAt
			status.AccountID = token.AccountID
			status.TokenValid = now < token.ExpiresAt
		}
		statuses = append(statuses, status)
	}
	return statuses
}

// ListAccounts returns all known account names.
func (p *Proxy) ListAccounts() []string {
	return p.auth.ListAccounts()
}

func (p *Proxy) SetOnQuotaPersist(fn QuotaPersistFunc) {
	p.onQuotaPersist = fn
}

func (p *Proxy) RestoreQuotaSnapshots(snapshots map[string]QuotaSnapshot) {
	for account, snapshot := range snapshots {
		p.quotaStore.Restore(account, snapshot)
	}
}

func (p *Proxy) recordQuotaHeaders(account string, headers http.Header) {
	if headers == nil || account == "" || p.quotaStore == nil {
		return
	}

	snapshot := ParseQuotaHeaders(headers)
	if modelLimits := ParseBengalfoxHeaders(headers); len(modelLimits) > 0 {
		snapshot.ModelLimits = modelLimits
	}
	p.quotaStore.Update(account, snapshot)
	if p.onQuotaPersist != nil {
		p.onQuotaPersist(account, snapshot)
	}
}

// detectQuotaExhaustion checks whether the account's active quota window is
// exhausted. Returns the reset time and true if the account should be paused.
// Must be called AFTER recordQuotaHeaders so the snapshot is up to date.
func (p *Proxy) detectQuotaExhaustion(account string) (resetAt time.Time, exhausted bool) {
	if p.quotaStore == nil || account == "" {
		return time.Time{}, false
	}

	snapshot, ok := p.quotaStore.Get(account)
	if !ok {
		return time.Time{}, false
	}

	switch snapshot.ActiveLimit {
	case "primary", "":
		if snapshot.Primary.UsedPercent >= 100 && snapshot.Primary.ResetAt > 0 {
			return time.Unix(snapshot.Primary.ResetAt, 0), true
		}
	case "secondary":
		if snapshot.Secondary.UsedPercent >= 100 && snapshot.Secondary.ResetAt > 0 {
			return time.Unix(snapshot.Secondary.ResetAt, 0), true
		}
	}

	return time.Time{}, false
}

func (p *Proxy) generateSessionID() string {
	n := p.sessionCounter.Add(1)
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("sess_%s_%d", hex.EncodeToString(b), n)
}

func (p *Proxy) Close() {
	p.transport.CloseIdleConnections()
}

// detectCacheHit checks the response body for Codex prompt cache indicators.
// Uses bytes.Contains as a fast path before falling back to JSON parsing.
func detectCacheHit(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	if !bytes.Contains(body, []byte("cached_tokens")) {
		return false
	}

	var resp struct {
		Usage struct {
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return false
	}
	return resp.Usage.PromptTokensDetails.CachedTokens > 0
}

func isFailureStatus(statusCode int) bool {
	return statusCode >= 500 ||
		statusCode == http.StatusTooManyRequests ||
		statusCode == http.StatusUnauthorized ||
		statusCode == http.StatusForbidden
}

func humanSize(size int) string {
	const kb = 1024
	const mb = kb * 1024
	switch {
	case size >= mb:
		return fmt.Sprintf("%.1fMB", float64(size)/float64(mb))
	case size >= kb:
		return fmt.Sprintf("%.1fKB", float64(size)/float64(kb))
	default:
		return fmt.Sprintf("%dB", size)
	}
}
