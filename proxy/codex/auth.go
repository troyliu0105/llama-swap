package codex

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	IssuerURL   = "https://auth.openai.com"
	ClientID    = "app_EMoamEEZ73f0CkXaXp7hrann"
	CodexAPIURL = "https://chatgpt.com/backend-api/codex/responses"

	tokenExpiryLeeway            = 120 // seconds before actual expiry to consider expired
	oauthPollingSafetyMarginSecs = 3   // extra wait on top of interval (matches OpenCode)
	httpTimeout                  = 30 * time.Second
)

var httpClient = &http.Client{Timeout: httpTimeout}

// OpenCodeUserAgent returns the User-Agent string matching OpenCode's format.
func OpenCodeUserAgent() string {
	return fmt.Sprintf("opencode/1.0.0 (%s %s; %s)", runtime.GOOS, "unknown", runtime.GOARCH)
}

type TokenData struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
	AccountID    string `json:"account_id,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

type DeviceAuthResponse struct {
	DeviceAuthID string `json:"device_auth_id"`
	UserCode     string `json:"user_code"`
	ExpiresAt    string `json:"expires_at"`
	Interval     int
}

func (r *DeviceAuthResponse) UnmarshalJSON(data []byte) error {
	type alias struct {
		DeviceAuthID string  `json:"device_auth_id"`
		UserCode     string  `json:"user_code"`
		ExpiresAt    string  `json:"expires_at"`
		Interval     flexInt `json:"interval"`
	}
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	r.DeviceAuthID = a.DeviceAuthID
	r.UserCode = a.UserCode
	r.ExpiresAt = a.ExpiresAt
	r.Interval = int(a.Interval)
	return nil
}

func (r *DeviceAuthResponse) VerificationURL() string {
	return IssuerURL + "/codex/device"
}

func (r *DeviceAuthResponse) ExpiresTime() time.Time {
	if r.ExpiresAt == "" {
		return time.Now().Add(15 * time.Minute)
	}
	t, err := time.Parse(time.RFC3339, r.ExpiresAt)
	if err != nil {
		return time.Now().Add(15 * time.Minute)
	}
	return t
}

// flexInt decodes JSON numbers and strings as int.
type flexInt int

func (f *flexInt) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("codex auth: cannot parse %q as int: %w", s, err)
		}
		*f = flexInt(n)
		return nil
	}
	var n int
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	*f = flexInt(n)
	return nil
}

type AuthStore struct {
	mu        sync.RWMutex
	filePath  string
	tokens    map[string]*TokenData
	refreshMu map[string]*sync.Mutex
	deleted   map[string]bool
}

func DefaultAuthPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".llama-swap", "codex-auth.json")
}

func NewAuthStore(filePath string) *AuthStore {
	return &AuthStore{
		filePath:  filePath,
		tokens:    make(map[string]*TokenData),
		refreshMu: make(map[string]*sync.Mutex),
		deleted:   make(map[string]bool),
	}
}

func (s *AuthStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			s.tokens = make(map[string]*TokenData)
			return nil
		}
		return fmt.Errorf("codex auth: failed to read token file: %w", err)
	}

	if len(data) == 0 {
		s.tokens = make(map[string]*TokenData)
		return nil
	}

	var tokens map[string]*TokenData
	if err := json.Unmarshal(data, &tokens); err != nil {
		return fmt.Errorf("codex auth: failed to parse token file: %w", err)
	}

	s.tokens = tokens
	return nil
}

func (s *AuthStore) Save() error {
	s.mu.RLock()
	data, err := json.MarshalIndent(s.tokens, "", "  ")
	s.mu.RUnlock()

	if err != nil {
		return fmt.Errorf("codex auth: failed to marshal tokens: %w", err)
	}

	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("codex auth: failed to create directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".codex-auth-*.tmp")
	if err != nil {
		return fmt.Errorf("codex auth: failed to create temp file: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("codex auth: failed to write temp file: %w", err)
	}

	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("codex auth: failed to sync temp file: %w", err)
	}
	tmp.Close()

	if err := os.Chmod(tmpName, 0600); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("codex auth: failed to set permissions: %w", err)
	}

	if err := os.Rename(tmpName, s.filePath); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("codex auth: failed to rename temp file: %w", err)
	}

	return nil
}

func (s *AuthStore) GetToken(accountName string) (*TokenData, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	token, ok := s.tokens[accountName]
	if !ok {
		return nil, fmt.Errorf("codex account %q not found", accountName)
	}
	if token == nil {
		return nil, fmt.Errorf("codex account %q has nil token entry", accountName)
	}
	return token, nil
}

func (s *AuthStore) SetToken(accountName string, token *TokenData) error {
	if token == nil {
		return fmt.Errorf("codex account %q token can not be nil", accountName)
	}

	s.mu.Lock()
	delete(s.deleted, accountName)
	s.tokens[accountName] = token
	s.mu.Unlock()

	return s.Save()
}

func (s *AuthStore) ListAccounts() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0, len(s.tokens))
	for name := range s.tokens {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (s *AuthStore) RemoveToken(accountName string) error {
	// Hold the per-account refresh mutex to prevent a concurrent refresh
	// from writing back the token after deletion (no "token resurrection").
	refreshMu := s.getRefreshMutex(accountName)
	refreshMu.Lock()
	defer refreshMu.Unlock()

	s.mu.Lock()
	_, ok := s.tokens[accountName]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("codex account %q not found", accountName)
	}
	delete(s.tokens, accountName)
	s.deleted[accountName] = true
	s.mu.Unlock()

	return s.Save()
}

func (s *AuthStore) getRefreshMutex(accountName string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()

	mu, ok := s.refreshMu[accountName]
	if !ok {
		mu = &sync.Mutex{}
		s.refreshMu[accountName] = mu
	}
	return mu
}

func (s *AuthStore) GetValidToken(accountName string) (*TokenData, error) {
	token, err := s.GetToken(accountName)
	if err != nil {
		return nil, err
	}

	if time.Now().Unix() < token.ExpiresAt-tokenExpiryLeeway {
		return token, nil
	}

	refreshMu := s.getRefreshMutex(accountName)
	refreshMu.Lock()
	defer refreshMu.Unlock()

	// Re-read after acquiring lock; another goroutine may have refreshed
	token, err = s.GetToken(accountName)
	if err != nil {
		return nil, err
	}
	if time.Now().Unix() < token.ExpiresAt-tokenExpiryLeeway {
		return token, nil
	}

	newToken, err := refreshToken(context.Background(), token.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf(
			"codex account %q token expired, run: llama-swap codex-login --account %s",
			accountName, accountName,
		)
	}

	s.mu.RLock()
	isDeleted := s.deleted[accountName]
	s.mu.RUnlock()
	if isDeleted {
		return nil, fmt.Errorf("codex account %q has been removed", accountName)
	}

	if err := s.SetToken(accountName, newToken); err != nil {
		return nil, fmt.Errorf("codex auth: failed to persist refreshed token: %w", err)
	}

	return newToken, nil
}

func refreshToken(ctx context.Context, refreshTokenStr string) (*TokenData, error) {
	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshTokenStr},
		"client_id":     {ClientID},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", IssuerURL+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("codex auth: failed to create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("codex auth: refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("codex auth: failed to read refresh response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("codex auth: refresh failed (status %d): %s", resp.StatusCode, truncate(string(respBody), 256))
	}

	var result tokenResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("codex auth: failed to parse refresh response: %w", err)
	}

	newRefresh := result.RefreshToken
	if newRefresh == "" {
		newRefresh = refreshTokenStr
	}

	return &TokenData{
		AccessToken:  result.AccessToken,
		RefreshToken: newRefresh,
		ExpiresAt:    time.Now().Unix() + int64(result.ExpiresIn),
		AccountID:    extractAccountIDFromTokenResponse(&result),
	}, nil
}

func StartDeviceAuth(ctx context.Context) (*DeviceAuthResponse, error) {
	body, _ := json.Marshal(map[string]string{
		"client_id": ClientID,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", IssuerURL+"/api/accounts/deviceauth/usercode", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("codex auth: failed to create device auth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", OpenCodeUserAgent())

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("codex auth: device auth request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("codex auth: failed to read device auth response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("codex auth: device auth failed (status %d): %s", resp.StatusCode, truncate(string(respBody), 256))
	}

	var deviceAuth DeviceAuthResponse
	if err := json.Unmarshal(respBody, &deviceAuth); err != nil {
		return nil, fmt.Errorf("codex auth: failed to parse device auth response: %w", err)
	}

	return &deviceAuth, nil
}

func PollDeviceAuth(ctx context.Context, deviceAuth *DeviceAuthResponse) (*TokenData, error) {
	interval := deviceAuth.Interval
	if interval <= 0 {
		interval = 5
	}
	// Match OpenCode: sleep interval + safety margin
	waitDuration := time.Duration(interval+oauthPollingSafetyMarginSecs) * time.Second

	deadline := deviceAuth.ExpiresTime()
	pollURL := IssuerURL + "/api/accounts/deviceauth/token"

	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("codex auth: device auth expired, please try again")
		}

		// Step 1: Poll for authorization_code (not tokens directly)
		body, _ := json.Marshal(map[string]string{
			"device_auth_id": deviceAuth.DeviceAuthID,
			"user_code":      deviceAuth.UserCode,
		})

		req, err := http.NewRequestWithContext(ctx, "POST", pollURL, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("codex auth: failed to create poll request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", OpenCodeUserAgent())

		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("codex auth: poll request failed: %w", err)
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("codex auth: failed to read poll response: %w", err)
		}

		if resp.StatusCode == http.StatusOK {
			// Got authorization_code + code_verifier, now exchange for real tokens
			var authCode struct {
				AuthorizationCode string `json:"authorization_code"`
				CodeVerifier      string `json:"code_verifier"`
			}
			if err := json.Unmarshal(respBody, &authCode); err != nil {
				return nil, fmt.Errorf("codex auth: failed to parse authorization code response: %w", err)
			}

			// Step 2: Exchange authorization_code for tokens via /oauth/token
			return exchangeAuthToken(ctx, authCode.AuthorizationCode, authCode.CodeVerifier)
		}

		// 403/404 = user hasn't completed login yet, keep polling (matches OpenCode)
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
			select {
			case <-time.After(waitDuration):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		return nil, fmt.Errorf("codex auth: device auth poll failed (status %d): %s", resp.StatusCode, truncate(string(respBody), 256))
	}
}

// exchangeAuthToken exchanges an authorization_code (from device auth) for real OAuth tokens.
// This is step 2 of OpenCode's headless device auth flow.
func exchangeAuthToken(ctx context.Context, code, codeVerifier string) (*TokenData, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {IssuerURL + "/deviceauth/callback"},
		"client_id":     {ClientID},
		"code_verifier": {codeVerifier},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", IssuerURL+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("codex auth: failed to create token exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("codex auth: token exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("codex auth: failed to read token exchange response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("codex auth: token exchange failed (status %d): %s", resp.StatusCode, truncate(string(respBody), 256))
	}

	var tokens tokenResponse
	if err := json.Unmarshal(respBody, &tokens); err != nil {
		return nil, fmt.Errorf("codex auth: failed to parse token exchange response: %w", err)
	}

	return &TokenData{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		ExpiresAt:    time.Now().Unix() + int64(tokens.ExpiresIn),
		AccountID:    extractAccountIDFromTokenResponse(&tokens),
	}, nil
}

// tokenResponse matches OpenCode's TokenResponse shape.
type tokenResponse struct {
	IDToken      string  `json:"id_token"`
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	ExpiresIn    flexInt `json:"expires_in"`
}

// extractAccountIDFromTokenResponse extracts the ChatGPT account ID from JWT claims,
// matching OpenCode's extractAccountId logic.
func extractAccountIDFromTokenResponse(tokens *tokenResponse) string {
	for _, raw := range []string{tokens.IDToken, tokens.AccessToken} {
		if raw == "" {
			continue
		}
		claims := parseJWTClaims(raw)
		if id := extractAccountIDFromClaims(claims); id != "" {
			return id
		}
	}
	return ""
}

type jwtClaims struct {
	ChatGPTAccountID string `json:"chatgpt_account_id"`
	Auth             *struct {
		ChatGPTAccountID string `json:"chatgpt_account_id"`
	} `json:"https://api.openai.com/auth"`
	Organizations []struct {
		ID string `json:"id"`
	} `json:"organizations"`
}

func parseJWTClaims(token string) *jwtClaims {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	// base64url decode (no padding)
	s := parts[1]
	s = strings.TrimRight(s, "=")
	b, err := base64URLDecode(s)
	if err != nil {
		return nil
	}
	var claims jwtClaims
	if json.Unmarshal(b, &claims) != nil {
		return nil
	}
	return &claims
}

func extractAccountIDFromClaims(claims *jwtClaims) string {
	if claims == nil {
		return ""
	}
	if claims.ChatGPTAccountID != "" {
		return claims.ChatGPTAccountID
	}
	if claims.Auth != nil && claims.Auth.ChatGPTAccountID != "" {
		return claims.Auth.ChatGPTAccountID
	}
	if len(claims.Organizations) > 0 && claims.Organizations[0].ID != "" {
		return claims.Organizations[0].ID
	}
	return ""
}

func base64URLDecode(s string) ([]byte, error) {
	// Replace URL-safe chars and add padding
	s = strings.ReplaceAll(s, "-", "+")
	s = strings.ReplaceAll(s, "_", "/")
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.StdEncoding.DecodeString(s)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
