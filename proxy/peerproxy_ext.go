package proxy

import (
	"bytes"
	"context"
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

	"github.com/mostlygeek/llama-swap/proxy/config"
)

type peerCtxKey struct{}

type peerRequestInfo struct {
	model string
	start time.Time
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
	headers       map[string]string
	stripV1Prefix bool
	maxConcurrent int
	queueSize     int
	queueTimeout  time.Duration
	sem           chan struct{}
	queue         chan *queuedRequest
	waitingCount  int32
	logger        *LogMonitor
	stopCh        chan struct{}

	lastRequestMu   sync.Mutex
	lastRequestTime time.Time
	requestInterval time.Duration
	// currentInterval tracks the adaptive backoff interval.
	// On failure it grows exponentially (capped at requestInterval).
	// On success it decays back toward 0.
	currentInterval time.Duration
}

// EnhancedPeerProxy manages proxying requests to remote peer servers with extended features
type EnhancedPeerProxy struct {
	peers            config.PeerDictionaryExtConfig
	proxyMap         map[string]*enhancedPeerMember
	prefixedModels   map[string]bool
	prefixPeerModels bool
}

// NewEnhancedPeerProxy creates a new EnhancedPeerProxy with the given configuration
func NewEnhancedPeerProxy(peers config.PeerDictionaryExtConfig, prefixPeerModels bool, proxyLogger *LogMonitor) (*EnhancedPeerProxy, error) {
	proxyMap := make(map[string]*enhancedPeerMember)
	prefixedModels := make(map[string]bool)

	// Sort peer IDs for consistent iteration order
	peerIDs := make([]string, 0, len(peers))
	for peerID := range peers {
		peerIDs = append(peerIDs, peerID)
	}
	sort.Strings(peerIDs)

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
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
		}

		reverseProxy := httputil.NewSingleHostReverseProxy(peer.ProxyURL)
		reverseProxy.Transport = peerTransport

		pp := &enhancedPeerMember{
			peerID:          peerID,
			apiKey:          peer.ApiKey,
			headers:         peer.Headers,
			stripV1Prefix:   peer.StripV1Prefix,
			maxConcurrent:   peer.MaxConcurrent,
			queueSize:       peer.QueueSize,
			queueTimeout:    peer.QueueTimeout,
			requestInterval: peer.RequestInterval,
			logger:          proxyLogger,
		}

		originalDirector := reverseProxy.Director
		reverseProxy.Director = func(req *http.Request) {
			originalDirector(req)
			req.Host = req.URL.Host
		}

		reverseProxy.ModifyResponse = func(resp *http.Response) error {
			contentType := strings.ToLower(resp.Header.Get("Content-Type"))
			isSSE := strings.Contains(contentType, "text/event-stream")
			model := getPeerModel(resp.Request)

			if isSSE {
				resp.Header.Set("X-Accel-Buffering", "no")
				pp.recordSuccess()
				fmt.Printf("[PEER] ◀ %s | %d SSE | %s\n", model, resp.StatusCode, pp.peerID)
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
				fmt.Printf("[PEER ERROR] ◀ %s | %d | %s | %s\n",
					model, resp.StatusCode, pp.peerID, logBody)
			} else {
				pp.recordSuccess()
				if info, ok := resp.Request.Context().Value(peerCtxKey{}).(*peerRequestInfo); ok {
					fmt.Printf("[PEER] ◀ %s | %d | %s | %s | %s\n",
						model, resp.StatusCode, formatSize(len(body)), pp.peerID, formatDuration(time.Since(info.start)))
				} else {
					fmt.Printf("[PEER] ◀ %s | %d | %s | %s\n",
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
							fmt.Printf("[PEER ERROR] ◀ %s | business_error | %s | %s\n", model, pp.peerID, logBody)
						}
					}
				}
			}

			if proxyLogger.IsLevelEnabled(LevelTrace) && readErr == nil {
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
				fmt.Printf("[PEER] ◀ %s | CANCEL | %s\n", model, pp.peerID)
				http.Error(w, "client disconnected", 499)
				return
			}

			pp.recordFailure()

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

			fmt.Printf("[PEER ERROR] ◀ %s | CONN | %s | %serror=%s\n",
				model, pp.peerID, timeoutIndicator, errStr)

			errMsg := fmt.Sprintf("peer proxy error: %v", err)
			if runtime.GOOS == "darwin" && strings.Contains(err.Error(), "connect: no route to host") {
				errMsg += " (hint: on macOS, check System Settings > Privacy & Security > Local Network permissions)"
			}
			http.Error(w, errMsg, http.StatusBadGateway)
		}

		pp.reverseProxy = reverseProxy

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

	return &EnhancedPeerProxy{
		peers:            peers,
		proxyMap:         proxyMap,
		prefixedModels:   prefixedModels,
		prefixPeerModels: prefixPeerModels,
	}, nil
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

func (p *EnhancedPeerProxy) ProxyRequest(modelID string, writer http.ResponseWriter, request *http.Request) error {
	pp, found := p.proxyMap[modelID]
	if !found {
		return fmt.Errorf("no peer proxy found for model %s", modelID)
	}

	fmt.Printf("[PEER] ▶ %s | %s %s | %s\n", modelID, request.Method, request.URL.Path, pp.peerID)

	ctx := context.WithValue(request.Context(), peerCtxKey{}, &peerRequestInfo{
		model: modelID,
		start: time.Now(),
	})
	request = request.WithContext(ctx)

	if pp.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+pp.apiKey)
		request.Header.Set("x-api-key", pp.apiKey)
	}

	for key, value := range pp.headers {
		if value == "" {
			request.Header.Del(key)
		} else {
			request.Header.Set(key, value)
		}
	}

	if pp.stripV1Prefix {
		request.URL.Path = strings.TrimPrefix(request.URL.Path, "/v1")
		if request.URL.Path == "" {
			request.URL.Path = "/"
		}
	}

	if pp.serveWithConcurrencyControl(writer, request) == serveHandled {
		return nil
	}

	if err := pp.waitForRequestInterval(request.Context(), request.URL.Path); err != nil {
		fmt.Printf("[PEER ERROR] %s | rate_limit_cancelled | %s | %s\n",
			modelID, request.URL.Path, err)
		http.Error(writer, err.Error(), http.StatusServiceUnavailable)
		return nil
	}
	pp.reverseProxy.ServeHTTP(writer, request)
	pp.markRequestComplete()
	return nil
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

	fmt.Printf("[PEER BACKOFF] %s ↑ %s (cap %s)\n",
		pp.peerID, formatDuration(pp.currentInterval), formatDuration(pp.requestInterval))
}

func (pp *enhancedPeerMember) recordSuccess() {
	pp.lastRequestMu.Lock()
	defer pp.lastRequestMu.Unlock()

	if pp.currentInterval == 0 {
		return
	}

	pp.currentInterval /= 2
	if pp.currentInterval < pp.requestInterval/16 {
		pp.currentInterval = 0
		fmt.Printf("[PEER BACKOFF] %s ✓ recovered\n", pp.peerID)
	} else {
		fmt.Printf("[PEER BACKOFF] %s ↓ %s\n",
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
		fmt.Printf("[PEER] %s | wait %s | %s\n",
			getPeerModelFromCtx(ctx), formatDuration(waitTime), pp.peerID)
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
	defer func() {
		if r := recover(); r != nil {
			pp.logger.Warnf("peer %s: processQueue panic recovered: %v", pp.peerID, r)
		}
	}()

	for {
		select {
		case req := <-pp.queue:
			go func(qr *queuedRequest) {
				// recover from http.ErrAbortHandler panics that can occur when the client
				// disconnects before the response is sent
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

				// Wait for request interval BEFORE acquiring semaphore
				if err := pp.waitForRequestInterval(qr.request.Context(), qr.request.URL.Path); err != nil {
					fmt.Printf("[PEER ERROR] %s | rate_limit_cancelled | %s\n",
						getPeerModel(qr.request), err)
					if qr.responded.CompareAndSwap(false, true) {
						http.Error(qr.writer, err.Error(), http.StatusServiceUnavailable)
					}
					close(qr.done)
					return
				}

				select {
				case pp.sem <- struct{}{}:
					defer func() { <-pp.sem }()
					ctx := qr.request.Context()
					select {
					case <-ctx.Done():
						fmt.Printf("[PEER] %s | cancelled | %s\n",
							getPeerModel(qr.request), pp.peerID)
					default:
						fmt.Printf("[PEER] %s | dequeue | %d/%d slots | %s\n",
							getPeerModel(qr.request), len(pp.sem), pp.maxConcurrent, pp.peerID)
						if qr.cancelled.Load() {
							fmt.Printf("[PEER] %s | cancelled_before_serve | %s\n",
								getPeerModel(qr.request), pp.peerID)
						} else {
							qr.responded.Store(true)
							pp.reverseProxy.ServeHTTP(qr.writer, qr.request)
							pp.markRequestComplete()
						}
					}
				case <-time.After(pp.queueTimeout):
					fmt.Printf("[PEER ERROR] %s | queue_timeout | %s\n",
						getPeerModel(qr.request), pp.peerID)
					if qr.responded.CompareAndSwap(false, true) {
						http.Error(qr.writer, "request timed out waiting in queue", http.StatusServiceUnavailable)
					}
				case <-qr.request.Context().Done():
					fmt.Printf("[PEER] %s | cancelled_in_queue | %s\n",
						getPeerModel(qr.request), pp.peerID)
				}
				close(qr.done)
			}(req)
		case <-pp.stopCh:
			for len(pp.queue) > 0 {
				req := <-pp.queue
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

// serveWithConcurrencyControl handles request concurrency with semaphore and queue
func (pp *enhancedPeerMember) serveWithConcurrencyControl(writer http.ResponseWriter, request *http.Request) serveResult {
	if pp.maxConcurrent == 0 {
		return serveContinue
	}

	fmt.Printf("[PEER] %s | %d/%d slots | %s\n",
		getPeerModel(request), len(pp.sem), pp.maxConcurrent, pp.peerID)

	if err := pp.waitForRequestInterval(request.Context(), request.URL.Path); err != nil {
		fmt.Printf("[PEER ERROR] %s | rate_limit_cancelled | %s\n",
			getPeerModel(request), err)
		http.Error(writer, err.Error(), http.StatusServiceUnavailable)
		return serveHandled
	}

	select {
	case pp.sem <- struct{}{}:
		// Use defer to guarantee semaphore release even if ServeHTTP panics
		// (e.g., http.ErrAbortHandler when client disconnects during streaming)
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
			fmt.Printf("[PEER] %s | disconnect_queued | %s\n",
				getPeerModel(request), pp.peerID)
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
