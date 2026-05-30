package proxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/proxy/codex"
	"github.com/mostlygeek/llama-swap/proxy/config"
	"github.com/mostlygeek/llama-swap/proxy/protocol"
)

type peerCtxKey struct{}

type codexAccountKey struct{}

type codexAccountHolderKey struct{}

type codexAccountHolder struct {
	Value string
}

func peerLog(format string, args ...any) {
	ts := time.Now().Format("15:04:05.000")
	fmt.Printf("[%s] "+format, append([]any{ts}, args...)...)
}

type peerRequestInfo struct {
	model string
	reqID string
	start time.Time
}

func generateReqID() string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 4)
	rand.Read(b)
	for i := range b {
		b[i] = charset[int(b[i])%len(charset)]
	}
	return string(b)
}

func getPeerReqID(r *http.Request) string {
	if info, ok := r.Context().Value(peerCtxKey{}).(*peerRequestInfo); ok {
		return info.reqID
	}
	return "????"
}

func getPeerReqIDFromCtx(ctx context.Context) string {
	if info, ok := ctx.Value(peerCtxKey{}).(*peerRequestInfo); ok {
		return info.reqID
	}
	return "????"
}

func getPeerModel(r *http.Request) string {
	if info, ok := r.Context().Value(peerCtxKey{}).(*peerRequestInfo); ok {
		return info.model
	}
	return "?"
}

func getPeerModelFromCtx(ctx context.Context) string {
	if info, ok := ctx.Value(peerCtxKey{}).(*peerRequestInfo); ok {
		return info.model
	}
	return "?"
}

func formatSize(bytes int) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// enhancedPeerMember holds the proxy configuration for a single peer
type enhancedPeerMember struct {
	peerID        string
	reverseProxy  *httputil.ReverseProxy
	apiKey        string
	addHeaders    map[string]string
	removeHeaders []string
	stripV1Prefix bool
	maxConcurrent int
	queueSize     int
	queueTimeout  time.Duration
	sem           chan struct{}
	queue         chan *queuedRequest
	waitingCount  int32
	logger        *logmon.Monitor
	stopCh        chan struct{}
	codexProxy    *codex.Proxy
	stopped       atomic.Bool

	backoffMu       sync.Mutex
	lastRequestMu   sync.Mutex
	lastRequestTime time.Time
	requestInterval time.Duration
	// currentInterval tracks the adaptive backoff interval.
	// On failure it grows exponentially (capped at requestInterval).
	// On success it decays back toward 0.
	currentInterval time.Duration

	// upstreamFormat is the wire protocol the upstream server expects.
	// Only used when convertProtocol is true.
	upstreamFormat  protocol.Format
	convertProtocol bool
	// converters caches pre-built converters keyed by client format.
	converters map[protocol.Format]*protocol.Converter
}

// EnhancedPeerProxy manages proxying requests to remote peer servers with extended features
type EnhancedPeerProxy struct {
	peers            config.PeerDictionaryExtConfig
	proxyMap         map[string]*enhancedPeerMember
	prefixedModels   map[string]bool
	prefixPeerModels bool

	codexAuth      *codex.AuthStore
	codexRefresher *codex.Refresher
}

// NewEnhancedPeerProxy creates a new EnhancedPeerProxy with the given configuration
func NewEnhancedPeerProxy(peers config.PeerDictionaryExtConfig, prefixPeerModels bool, proxyLogger *logmon.Monitor) (*EnhancedPeerProxy, error) {
	proxyMap := make(map[string]*enhancedPeerMember)
	prefixedModels := make(map[string]bool)

	// Sort peer IDs for consistent iteration order
	peerIDs := make([]string, 0, len(peers))
	for peerID := range peers {
		peerIDs = append(peerIDs, peerID)
	}
	sort.Strings(peerIDs)

	var sharedCodexAuth *codex.AuthStore

	for _, peerID := range peerIDs {
		peer := peers[peerID]

		peerTimeout := peer.Timeout
		if peerTimeout <= 0 {
			peerTimeout = 60 * time.Second
		}
		peerTransport := &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: peerTimeout,
			ExpectContinueTimeout: 1 * time.Second,
			ForceAttemptHTTP2:     false,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
		}
		disablePeerHTTP2(peerTransport)

		reverseProxy := &httputil.ReverseProxy{Transport: peerTransport}

		pp := &enhancedPeerMember{
			peerID:          peerID,
			apiKey:          peer.ApiKey,
			addHeaders:      peer.AddHeaders,
			removeHeaders:   peer.RemoveHeaders,
			stripV1Prefix:   peer.StripV1Prefix,
			maxConcurrent:   peer.MaxConcurrent,
			queueSize:       peer.QueueSize,
			queueTimeout:    peer.QueueTimeout,
			requestInterval: peer.RequestInterval,
			logger:          proxyLogger,
			upstreamFormat:  protocol.ParseFormat(peer.UpstreamFormat),
			convertProtocol: peer.UpstreamFormat != "",
		}

		// Pre-build converters for all possible client formats
		if pp.convertProtocol && pp.upstreamFormat != protocol.FormatUnknown {
			pp.converters = make(map[protocol.Format]*protocol.Converter)
			for _, clientFmt := range []protocol.Format{protocol.FormatOpenAI, protocol.FormatResponses, protocol.FormatAnthropic} {
				if clientFmt != pp.upstreamFormat {
					if conv, err := protocol.NewConverter(clientFmt, pp.upstreamFormat); err == nil {
						pp.converters[clientFmt] = conv
					}
				}
			}
		}

		if peer.Type == "codex" && peer.Codex != nil {
			accountNames := make([]string, len(peer.Codex.Accounts))
			for i, acct := range peer.Codex.Accounts {
				accountNames[i] = acct.Name
			}
			strategy := peer.Codex.LoadBalance.Strategy
			if sharedCodexAuth == nil {
				sharedCodexAuth = codex.NewAuthStore(codex.DefaultAuthPath())
				if err := sharedCodexAuth.Load(); err != nil {
					return nil, fmt.Errorf("failed to load codex auth store: %w", err)
				}
			}

			codexProxy, err := codex.NewProxy(
				peerID,
				sharedCodexAuth,
				accountNames,
				strategy,
				peerTimeout,
				func(format string, args ...any) {
					proxyLogger.Infof(format, args...)
				},
			)
			if err != nil {
				return nil, fmt.Errorf("peer %s: failed to create codex proxy: %w", peerID, err)
			}
			pp.codexProxy = codexProxy
		}

		reverseProxy.Rewrite = func(pr *httputil.ProxyRequest) {
			pr.SetURL(peer.ProxyURL)
			pr.SetXForwarded()
			pr.Out.Host = pr.Out.URL.Host
		}

		if pp.codexProxy != nil {
			reverseProxy.Rewrite = func(pr *httputil.ProxyRequest) {
				pr.SetURL(peer.ProxyURL)
				pr.Out.Host = pr.Out.URL.Host
				// Strip forwarding headers that ReverseProxy would otherwise add.
				// httputil.ReverseProxy automatically injects X-Forwarded-For,
				// which would leak the client IP to chatgpt.com.
				pr.Out.Header["X-Forwarded-For"] = nil
				pr.Out.Header["X-Forwarded-Host"] = nil
				pr.Out.Header.Del("X-Forwarded-Proto")
				pr.Out.Header.Del("X-Forwarded-Server")
				pr.Out.Header.Del("X-Real-IP")
				pr.Out.Header.Del("Forwarded")
				pr.Out.Header.Del("Via")
			}
		}

		reverseProxy.ModifyResponse = func(resp *http.Response) error {
			contentType := strings.ToLower(resp.Header.Get("Content-Type"))
			isSSE := strings.Contains(contentType, "text/event-stream")
			isStreamingRequest, _ := resp.Request.Context().Value(proxyCtxKey("streaming")).(bool)

			// Codex API stream responses can arrive as text/plain or with no content-type;
			// normalize them so metrics, audit captures, and the UI all treat them as SSE.
			if !isSSE && shouldNormalizeCodexStreamContentType(contentType, pp.codexProxy != nil, isStreamingRequest) {
				isSSE = true
				resp.Header.Set("Content-Type", "text/event-stream; charset=utf-8")
			}

			model := getPeerModel(resp.Request)

			if isSSE {
				resp.Header.Set("X-Accel-Buffering", "no")
				if resp.StatusCode >= 400 {
					pp.recordFailure()
				} else {
					pp.recordSuccess()
				}
				if pp.codexProxy != nil {
					account := ""
					if acc, ok := resp.Request.Context().Value(codexAccountKey{}).(string); ok {
						account = acc
					}
					pp.codexProxy.RecordStreamingResponse(model, account, resp.StatusCode, resp.Header)
				}
				peerLog("[PEER] ◀ %s | %s | %d SSE | %s\n", getPeerReqID(resp.Request), model, resp.StatusCode, pp.peerID)
				return nil
			}

			var body []byte
			var readErr error
			if resp.Body != nil {
				body, readErr = io.ReadAll(resp.Body)
				if readErr == nil {
					resp.Body.Close()
					resp.Body = io.NopCloser(bytes.NewReader(body))
				}
			}

			if resp.StatusCode != http.StatusOK {
				pp.recordFailure()
				logBody := string(body)
				if len(logBody) > 512 {
					logBody = logBody[:512] + "..."
				}
				logBody = strings.ReplaceAll(logBody, "\n", " ")
				peerLog("[PEER ERROR] ◀ %s | %s | %d | %s | %s\n",
					getPeerReqID(resp.Request), model, resp.StatusCode, pp.peerID, logBody)
			} else {
				pp.recordSuccess()
				if info, ok := resp.Request.Context().Value(peerCtxKey{}).(*peerRequestInfo); ok {
					peerLog("[PEER] ◀ %s | %s | %d | %s | %s | %s\n",
						info.reqID, model, resp.StatusCode, formatSize(len(body)), pp.peerID, formatDuration(time.Since(info.start)))
				} else {
					peerLog("[PEER] ◀ %s | %d | %s | %s\n",
						model, resp.StatusCode, formatSize(len(body)), pp.peerID)
				}
				if len(body) > 0 {
					contentType := strings.ToLower(resp.Header.Get("Content-Type"))
					if strings.Contains(contentType, "application/json") {
						if bytes.Contains(body, []byte("\"error\"")) {
							logBody := string(body)
							if len(logBody) > 512 {
								logBody = logBody[:512] + "..."
							}
							logBody = strings.ReplaceAll(logBody, "\n", " ")
							peerLog("[PEER ERROR] ◀ %s | %s | business_error | %s | %s\n", getPeerReqID(resp.Request), model, pp.peerID, logBody)
						}
					}
				}
			}

			if pp.codexProxy != nil && readErr == nil {
				account := ""
				if acc, ok := resp.Request.Context().Value(codexAccountKey{}).(string); ok {
					account = acc
				}
				pp.codexProxy.RecordResponse(model, account, resp.StatusCode, body, resp.Header)
			}

			if proxyLogger.IsLevelEnabled(logmon.LevelTrace) && readErr == nil {
				contentType := strings.ToLower(resp.Header.Get("Content-Type"))
				if strings.Contains(contentType, "application/json") || strings.Contains(contentType, "text/") {
					logBody := string(body)
					if len(logBody) > 1024 {
						logBody = logBody[:1024] + "... (truncated)"
					}
					proxyLogger.Tracef("Peer %s response body: %s", pp.peerID, logBody)
				} else {
					proxyLogger.Tracef("Peer %s response body: [binary data, type=%s, size=%d]", pp.peerID, contentType, len(body))
				}
			}
			return nil
		}

		reverseProxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			model := getPeerModel(r)

			if r.Context().Err() == context.Canceled {
				peerLog("[PEER] ◀ %s | %s | CANCEL | %s\n", getPeerReqID(r), model, pp.peerID)
				http.Error(w, "client disconnected", 499)
				return
			}

			pp.recordFailure()
			if pp.codexProxy != nil {
				account := ""
				if acc, ok := r.Context().Value(codexAccountKey{}).(string); ok {
					account = acc
				}
				pp.codexProxy.RecordError(model, account)
			}

			proxyLogger.Warnf("peer %s: proxy error: %v", pp.peerID, err)

			errStr := err.Error()
			timeoutIndicator := ""
			lowerErr := strings.ToLower(errStr)
			if strings.Contains(lowerErr, "timeout") ||
				strings.Contains(lowerErr, "deadline exceeded") ||
				strings.Contains(lowerErr, "context deadline") ||
				strings.Contains(lowerErr, "read timed out") {
				timeoutIndicator = "timeout "
			}

			peerLog("[PEER ERROR] ◀ %s | %s | CONN | %s | %serror=%s\n",
				getPeerReqID(r), model, pp.peerID, timeoutIndicator, errStr)

			errMsg := fmt.Sprintf("peer proxy error: %v", err)
			if runtime.GOOS == "darwin" && strings.Contains(err.Error(), "connect: no route to host") {
				errMsg += " (hint: on macOS, check System Settings > Privacy & Security > Local Network permissions)"
			}
			http.Error(w, errMsg, http.StatusBadGateway)
		}

		pp.reverseProxy = reverseProxy

		if len(peer.RemoveHeaders) > 0 {
			pp.reverseProxy.Transport = &headerStrippingRoundTripper{
				Transport:    peerTransport,
				RemoveHeader: peer.RemoveHeaders,
			}
		}

		if peer.MaxConcurrent > 0 {
			pp.sem = make(chan struct{}, peer.MaxConcurrent)
			pp.queue = make(chan *queuedRequest, peer.QueueSize)
			pp.stopCh = make(chan struct{})
			go pp.processQueue()
		}

		peerPrefix := prefixPeerModels
		if peer.PrefixPeerModels != nil {
			peerPrefix = *peer.PrefixPeerModels
		}
		for _, modelID := range peer.Models {
			lookupKey := modelID
			if peerPrefix {
				lookupKey = peerID + "/" + modelID
			}
			if _, found := proxyMap[lookupKey]; found {
				proxyLogger.Warnf("peer %s: model %s already mapped to another peer, skipping", peerID, lookupKey)
				continue
			}
			proxyMap[lookupKey] = pp
			prefixedModels[lookupKey] = peerPrefix
		}
	}

	// Start proactive token refresh if we have codex peers
	var refresher *codex.Refresher
	if sharedCodexAuth != nil {
		refresher = codex.NewRefresher(sharedCodexAuth, func(format string, args ...any) {
			proxyLogger.Infof(format, args...)
		})
		refresher.Start()
		proxyLogger.Infof("started codex auth token refresher")
	}

	return &EnhancedPeerProxy{
		peers:            peers,
		proxyMap:         proxyMap,
		prefixedModels:   prefixedModels,
		prefixPeerModels: prefixPeerModels,
		codexAuth:        sharedCodexAuth,
		codexRefresher:   refresher,
	}, nil
}

func shouldNormalizeCodexStreamContentType(contentType string, isCodex bool, isStreamingRequest bool) bool {
	if !isCodex || !isStreamingRequest {
		return false
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	return contentType == "" || strings.Contains(contentType, "text/plain")
}

// HasPeerModel checks if a model exists in the peer proxy map
func (p *EnhancedPeerProxy) HasPeerModel(modelID string) bool {
	_, found := p.proxyMap[modelID]
	return found
}

// GetPeerFilters returns the filters for a peer model, or empty filters if not found
func (p *EnhancedPeerProxy) GetPeerFilters(modelID string) config.Filters {
	pp, found := p.proxyMap[modelID]
	if !found {
		return config.Filters{}
	}
	// Get the peer config using the peerID
	peer, found := p.peers[pp.peerID]
	if !found {
		return config.Filters{}
	}
	return peer.Filters
}

// ListPeers returns the peer dictionary configuration
func (p *EnhancedPeerProxy) ListPeers() config.PeerDictionaryExtConfig {
	return p.peers
}

func (p *EnhancedPeerProxy) GetCodexProxies() map[string]*codex.Proxy {
	out := make(map[string]*codex.Proxy)
	for id, member := range p.proxyMap {
		if member.codexProxy != nil {
			out[id] = member.codexProxy
		}
	}
	return out
}

// GetOriginalModelName returns the original model name, stripping peer prefix if applicable
func (p *EnhancedPeerProxy) GetOriginalModelName(modelID string) string {
	if !p.prefixedModels[modelID] {
		return modelID
	}
	if _, after, found := strings.Cut(modelID, "/"); found {
		return after
	}
	return modelID
}

func (p *EnhancedPeerProxy) Shutdown() {
	// Stop the proactive token refresher first to prevent
	// it from writing back tokens after accounts are deleted.
	if p.codexRefresher != nil {
		p.codexRefresher.Stop()
	}

	seen := make(map[*enhancedPeerMember]struct{})
	for _, pp := range p.proxyMap {
		if _, ok := seen[pp]; ok {
			continue
		}
		seen[pp] = struct{}{}
		pp.stopped.Store(true)
		if pp.stopCh != nil {
			close(pp.stopCh)
			pp.stopCh = nil
		}
		pp.queue = nil
		if pp.codexProxy != nil {
			pp.codexProxy.Close()
		}
	}
}

func (p *EnhancedPeerProxy) ProxyRequest(modelID string, writer http.ResponseWriter, request *http.Request) error {
	pp, found := p.proxyMap[modelID]
	if !found {
		return fmt.Errorf("no peer proxy found for model %s", modelID)
	}
	if pp.stopped.Load() {
		return fmt.Errorf("peer proxy for model %s is shutting down", modelID)
	}

	ctx := context.WithValue(request.Context(), peerCtxKey{}, &peerRequestInfo{
		model: modelID,
		reqID: generateReqID(),
		start: time.Now(),
	})
	request = request.WithContext(ctx)

	peerLog("[PEER] ▶ %s | %s | %s %s | %s\n", modelID, getPeerReqID(request), request.Method, request.URL.Path, pp.peerID)

	// Protocol conversion: detect client format and convert if needed
	if pp.convertProtocol && pp.upstreamFormat != protocol.FormatUnknown {
		clientFormat := protocol.DetectClientFormat(request.URL.Path)
		if protocol.NeedsConversion(clientFormat, pp.upstreamFormat) {
			converter := pp.converters[clientFormat].Clone()
			if converter == nil {
				peerLog("[PEER ERROR] %s | %s | protocol_convert_error | no converter for %s→%s\n",
					getPeerReqID(request), modelID, clientFormat, pp.upstreamFormat)
				http.Error(writer, "protocol conversion error: unsupported direction", http.StatusInternalServerError)
				return nil
			}

			// Reject oversized bodies before reading
			const maxRequestBodySize int64 = 100 << 20
			if request.ContentLength > maxRequestBodySize {
				http.Error(writer, "request body too large for conversion", http.StatusRequestEntityTooLarge)
				return nil
			}

			// Read body with size limit as a safety net for unknown Content-Length
			bodyBytes, err := io.ReadAll(io.LimitReader(request.Body, maxRequestBodySize+1))
			if err != nil {
				http.Error(writer, "failed to read request body for conversion", http.StatusBadRequest)
				return nil
			}
			request.Body.Close()
			if int64(len(bodyBytes)) > maxRequestBodySize {
				http.Error(writer, "request body too large for conversion", http.StatusRequestEntityTooLarge)
				return nil
			}

			newBody, newPath, err := converter.ConvertRequest(bodyBytes, request.URL.Path)
			if err != nil {
				peerLog("[PEER ERROR] %s | %s | request_convert_error | %s\n",
					getPeerReqID(request), modelID, err)
				http.Error(writer, fmt.Sprintf("request conversion error: %v", err), http.StatusBadRequest)
				return nil
			}

			request.Body = io.NopCloser(bytes.NewReader(newBody))
			request.ContentLength = int64(len(newBody))
			request.Header.Set("Content-Length", fmt.Sprintf("%d", len(newBody)))

			// If stripV1Prefix is set, the rewrite will strip the prefix later.
			// Apply the path rewrite here first, then let stripV1Prefix do its thing.
			if !pp.stripV1Prefix {
				request.URL.Path = newPath
			} else {
				// Rewrite to the target format path, then strip /v1
				request.URL.Path = strings.TrimPrefix(newPath, "/v1")
				if request.URL.Path == "" {
					request.URL.Path = "/"
				}
			}

			// Wrap the response writer for response conversion. Flush via defer so
			// every serve path (direct, queued, serialized, or error) releases any
			// buffered non-streaming body before ProxyRequest returns.
			convertingWriter := protocol.NewTransformingWriter(writer, converter)
			defer convertingWriter.Flush()
			writer = convertingWriter

			peerLog("[PEER] ▶ %s | %s | convert %s→%s | %s\n",
				modelID, getPeerReqID(request), clientFormat, pp.upstreamFormat, pp.peerID)
		}
	}
	if pp.codexProxy != nil {
		account, err := pp.codexProxy.PrepareRequest(modelID, request)
		if err != nil {
			peerLog("[CODEX ERROR] %s | %s | auth_error | %s | %s\n", getPeerReqID(request), modelID, pp.peerID, err)
			http.Error(writer, err.Error(), http.StatusServiceUnavailable)
			return nil
		}
		ctx := context.WithValue(request.Context(), codexAccountKey{}, account)
		request = request.WithContext(ctx)
		if h, _ := request.Context().Value(codexAccountHolderKey{}).(*codexAccountHolder); h != nil {
			h.Value = account
		}
	} else {
		if pp.apiKey != "" {
			request.Header.Set("Authorization", "Bearer "+pp.apiKey)
			request.Header.Set("x-api-key", pp.apiKey)
		}

		for key, value := range pp.addHeaders {
			if value != "" {
				request.Header.Set(key, value)
			}
		}

		for _, key := range pp.removeHeaders {
			request.Header.Del(key)
		}

		if pp.stripV1Prefix {
			request.URL.Path = strings.TrimPrefix(request.URL.Path, "/v1")
			if request.URL.Path == "" {
				request.URL.Path = "/"
			}
		}
	}

	if pp.serveWithConcurrencyControl(writer, request) == serveHandled {
		return nil
	}

	if pp.isInBackoff() {
		pp.serveSerialized(writer, request)
		return nil
	}

	if err := pp.waitForRequestInterval(request.Context(), request.URL.Path); err != nil {
		peerLog("[PEER ERROR] %s | %s | rate_limit_cancelled | %s | %s\n",
			getPeerReqID(request), modelID, request.URL.Path, err)
		http.Error(writer, err.Error(), http.StatusServiceUnavailable)
		return nil
	}
	pp.reverseProxy.ServeHTTP(writer, request)
	pp.markRequestComplete()

	return nil
}

// isInBackoff returns true when adaptive backoff is active (currentInterval > 0).
// During backoff, requests are serialized to ensure the interval between
// consecutive request completions is respected.
func (pp *enhancedPeerMember) isInBackoff() bool {
	pp.lastRequestMu.Lock()
	defer pp.lastRequestMu.Unlock()
	return pp.currentInterval > 0
}

// serveSerialized handles a request with full serialization during backoff.
// It holds backoffMu across the entire request lifecycle (wait + serve + mark),
// ensuring that the next request sees the real lastRequestTime after this one completes.
func (pp *enhancedPeerMember) serveSerialized(writer http.ResponseWriter, request *http.Request) {
	peerLog("[PEER] %s | %s | backoff_wait_lock | %s\n",
		getPeerReqID(request), getPeerModel(request), pp.peerID)
	pp.backoffMu.Lock()
	defer pp.backoffMu.Unlock()

	if err := pp.waitForRequestInterval(request.Context(), request.URL.Path); err != nil {
		peerLog("[PEER ERROR] %s | %s | backoff_rate_limit_cancelled | %s\n",
			getPeerReqID(request), getPeerModel(request), err)
		http.Error(writer, err.Error(), http.StatusServiceUnavailable)
		return
	}

	peerLog("[PEER] %s | %s | backoff_serial | %s\n",
		getPeerReqID(request), getPeerModel(request), pp.peerID)
	pp.reverseProxy.ServeHTTP(writer, request)
	pp.markRequestComplete()
}

func (pp *enhancedPeerMember) recordFailure() {
	pp.lastRequestMu.Lock()
	defer pp.lastRequestMu.Unlock()

	if pp.requestInterval <= 0 {
		return
	}

	if pp.currentInterval == 0 {
		pp.currentInterval = pp.requestInterval / 8
		if pp.currentInterval == 0 {
			pp.currentInterval = time.Millisecond
		}
	} else {
		pp.currentInterval *= 2
	}
	if pp.currentInterval > pp.requestInterval {
		pp.currentInterval = pp.requestInterval
	}

	peerLog("[PEER BACKOFF] %s ↑ %s (cap %s)\n",
		pp.peerID, formatDuration(pp.currentInterval), formatDuration(pp.requestInterval))
}

func (pp *enhancedPeerMember) recordSuccess() {
	pp.lastRequestMu.Lock()
	defer pp.lastRequestMu.Unlock()

	if pp.currentInterval == 0 {
		return
	}

	pp.currentInterval = time.Duration(float64(pp.currentInterval) / 1.15)
	if pp.currentInterval < pp.requestInterval/16 {
		pp.currentInterval = 0
		peerLog("[PEER BACKOFF] %s ✓ recovered\n", pp.peerID)
	} else {
		peerLog("[PEER BACKOFF] %s ↓ %s\n",
			pp.peerID, formatDuration(pp.currentInterval))
	}
}

func (pp *enhancedPeerMember) waitForRequestInterval(ctx context.Context, requestPath string) error {
	if pp.requestInterval <= 0 {
		return nil
	}

	pp.lastRequestMu.Lock()
	effectiveInterval := pp.currentInterval
	elapsed := time.Since(pp.lastRequestTime)
	var waitTime time.Duration
	if effectiveInterval > 0 && elapsed < effectiveInterval {
		waitTime = effectiveInterval - elapsed
	}
	pp.lastRequestMu.Unlock()

	if waitTime > 0 {
		peerLog("[PEER] %s | %s | wait %s | %s\n",
			getPeerReqIDFromCtx(ctx), getPeerModelFromCtx(ctx), formatDuration(waitTime), pp.peerID)
		select {
		case <-time.After(waitTime):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (pp *enhancedPeerMember) markRequestComplete() {
	pp.lastRequestMu.Lock()
	pp.lastRequestTime = time.Now()
	pp.lastRequestMu.Unlock()
}

// processQueue processes queued requests in a goroutine
func (pp *enhancedPeerMember) processQueue() {
	queue := pp.queue
	stopCh := pp.stopCh
	defer func() {
		if r := recover(); r != nil {
			pp.logger.Warnf("peer %s: processQueue panic recovered: %v", pp.peerID, r)
		}
	}()

	for {
		select {
		case req := <-queue:
			go func(qr *queuedRequest) {
				defer func() {
					if r := recover(); r != nil {
						if r == http.ErrAbortHandler {
							pp.logger.Warnf("peer %s: recovered from client disconnection during streaming", pp.peerID)
						} else {
							pp.logger.Warnf("peer %s: recovered from panic: %v", pp.peerID, r)
						}
					}
				}()
				defer atomic.AddInt32(&pp.waitingCount, -1)

				if qr.cancelled.Load() {
					close(qr.done)
					return
				}

				if pp.isInBackoff() {
					qr.responded.Store(true)
					timeoutCtx, cancel := context.WithTimeout(qr.request.Context(), pp.queueTimeout)
					req := qr.request.WithContext(timeoutCtx)
					pp.serveSerialized(qr.writer, req)
					cancel()
					close(qr.done)
					return
				}

				select {
				case pp.sem <- struct{}{}:
					defer func() { <-pp.sem }()
					ctx := qr.request.Context()
					select {
					case <-ctx.Done():
						peerLog("[PEER] %s | %s | cancelled | %s\n",
							getPeerReqID(qr.request), getPeerModel(qr.request), pp.peerID)
					default:
						peerLog("[PEER] %s | %s | dequeue | %d/%d slots | %s\n",
							getPeerReqID(qr.request), getPeerModel(qr.request), len(pp.sem), pp.maxConcurrent, pp.peerID)
						if qr.cancelled.Load() {
							peerLog("[PEER] %s | %s | cancelled_before_serve | %s\n",
								getPeerReqID(qr.request), getPeerModel(qr.request), pp.peerID)
						} else {
							qr.responded.Store(true)
							pp.reverseProxy.ServeHTTP(qr.writer, qr.request)
							pp.markRequestComplete()
						}
					}
				case <-time.After(pp.queueTimeout):
					peerLog("[PEER ERROR] %s | %s | queue_timeout | %s\n",
						getPeerReqID(qr.request), getPeerModel(qr.request), pp.peerID)
					if qr.responded.CompareAndSwap(false, true) {
						http.Error(qr.writer, "request timed out waiting in queue", http.StatusServiceUnavailable)
					}
				case <-qr.request.Context().Done():
					peerLog("[PEER] %s | %s | cancelled_in_queue | %s\n",
						getPeerReqID(qr.request), getPeerModel(qr.request), pp.peerID)
				}
				close(qr.done)
			}(req)
		case <-stopCh:
			for len(queue) > 0 {
				req := <-queue
				atomic.AddInt32(&pp.waitingCount, -1)
				if req.responded.CompareAndSwap(false, true) {
					http.Error(req.writer, "server shutting down", http.StatusServiceUnavailable)
				}
				close(req.done)
			}
			return
		}
	}
}

// serveWithConcurrencyControl handles request concurrency with semaphore and queue.
// During backoff, requests are serialized via serveSerialized instead.
func (pp *enhancedPeerMember) serveWithConcurrencyControl(writer http.ResponseWriter, request *http.Request) serveResult {
	if pp.stopped.Load() {
		http.Error(writer, "peer proxy is shutting down", http.StatusServiceUnavailable)
		return serveHandled
	}
	if pp.maxConcurrent == 0 {
		return serveContinue
	}

	if pp.isInBackoff() {
		pp.serveSerialized(writer, request)
		return serveHandled
	}

	peerLog("[PEER] %s | %s | %d/%d slots | %s\n",
		getPeerReqID(request), getPeerModel(request), len(pp.sem), pp.maxConcurrent, pp.peerID)

	select {
	case pp.sem <- struct{}{}:
		defer func() { <-pp.sem }()
		pp.reverseProxy.ServeHTTP(writer, request)
		pp.markRequestComplete()
		return serveHandled
	default:
	}

	if int(atomic.LoadInt32(&pp.waitingCount)) >= pp.queueSize {
		pp.logger.Warnf("peer %s: queue full, rejecting request", pp.peerID)
		http.Error(writer, "peer concurrency queue full", http.StatusServiceUnavailable)
		return serveHandled
	}

	atomic.AddInt32(&pp.waitingCount, 1)
	if pp.stopped.Load() {
		atomic.AddInt32(&pp.waitingCount, -1)
		http.Error(writer, "peer proxy is shutting down", http.StatusServiceUnavailable)
		return serveHandled
	}
	done := make(chan struct{}, 1)
	qr := &queuedRequest{
		writer:  writer,
		request: request,
		done:    done,
	}

	select {
	case pp.queue <- qr:
		select {
		case <-done:
			return serveHandled
		case <-request.Context().Done():
			qr.cancelled.Store(true)
			peerLog("[PEER] %s | %s | disconnect_queued | %s\n",
				getPeerReqID(request), getPeerModel(request), pp.peerID)
			<-done
			if qr.responded.CompareAndSwap(false, true) {
				http.Error(writer, "client disconnected", 499)
			}
			return serveHandled
		}
	default:
		atomic.AddInt32(&pp.waitingCount, -1)
		pp.logger.Warnf("peer %s: queue full, rejecting request", pp.peerID)
		http.Error(writer, "peer concurrency queue full", http.StatusServiceUnavailable)
		return serveHandled
	}
}
