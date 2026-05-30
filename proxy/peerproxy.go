package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
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
	"github.com/mostlygeek/llama-swap/proxy/config"
)

type peerProxyMember struct {
	peerID        string
	reverseProxy  *httputil.ReverseProxy
	apiKey        string
	addHeaders    map[string]string
	removeHeaders []string
	stripV1Prefix bool

	maxConcurrent   int
	queueSize       int
	queueTimeout    time.Duration
	requestInterval time.Duration
	sem             chan struct{}
	queue           chan *queuedRequest
	waitingCount    int32
	logger          *logmon.Monitor
	stopCh          chan struct{}

	lastRequestMu   sync.Mutex
	lastRequestTime time.Time
}

type serveResult int

const (
	serveHandled serveResult = iota
	serveContinue
)

type queuedRequest struct {
	writer    http.ResponseWriter
	request   *http.Request
	done      chan struct{}
	cancelled atomic.Bool
	responded atomic.Bool
}

type PeerProxy struct {
	peers            config.PeerDictionaryConfig
	proxyMap         map[string]*peerProxyMember
	prefixedModels   map[string]bool
	prefixPeerModels bool
}

func NewPeerProxy(peers config.PeerDictionaryConfig, prefixPeerModels bool, proxyLogger *logmon.Monitor) (*PeerProxy, error) {
	proxyMap := make(map[string]*peerProxyMember)
	prefixedModels := make(map[string]bool)

	// Sort peer IDs for consistent iteration order
	peerIDs := make([]string, 0, len(peers))
	for peerID := range peers {
		peerIDs = append(peerIDs, peerID)
	}
	sort.Strings(peerIDs)

	for _, peerID := range peerIDs {
		peer := peers[peerID]

		// Create a transport with per-peer timeout configuration
		peerTransport := &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   time.Duration(peer.Timeouts.Connect) * time.Second,
				KeepAlive: time.Duration(peer.Timeouts.KeepAlive) * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   time.Duration(peer.Timeouts.TLSHandshake) * time.Second,
			ResponseHeaderTimeout: time.Duration(peer.Timeouts.ResponseHeader) * time.Second,
			ExpectContinueTimeout: time.Duration(peer.Timeouts.ExpectContinue) * time.Second,
			ForceAttemptHTTP2:     false,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       time.Duration(peer.Timeouts.IdleConn) * time.Second,
		}
		disablePeerHTTP2(peerTransport)

		// Create reverse proxy for this peer
		reverseProxy := &httputil.ReverseProxy{Transport: peerTransport}

		currentPeerID := peerID // capture for closure
		reverseProxy.Rewrite = func(pr *httputil.ProxyRequest) {
			pr.SetURL(peer.ProxyURL)
			pr.SetXForwarded()
			pr.Out.Host = pr.Out.URL.Host
		}

		reverseProxy.ModifyResponse = func(resp *http.Response) error {
			contentType := strings.ToLower(resp.Header.Get("Content-Type"))
			isSSE := strings.Contains(contentType, "text/event-stream")

			// SSE 响应设置 header 并直接返回，不读取 body 以保持 streaming
			if isSSE {
				resp.Header.Set("X-Accel-Buffering", "no")
				return nil
			}

			// 非 SSE 响应：读取 body 进行日志记录
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
				logBody := string(body)
				if len(logBody) > 512 {
					logBody = logBody[:512] + "..."
				}
				logBody = strings.ReplaceAll(logBody, "\n", " ")
				fmt.Printf("[PEER ERROR] peer=%s status=%d method=%s path=%s error=%s\n",
					currentPeerID, resp.StatusCode, resp.Request.Method, resp.Request.URL.Path, logBody)
			} else if resp.StatusCode == http.StatusOK && len(body) > 0 {
				contentType := strings.ToLower(resp.Header.Get("Content-Type"))
				if strings.Contains(contentType, "application/json") {
					if bytes.Contains(body, []byte("\"error\"")) {
						logBody := string(body)
						if len(logBody) > 512 {
							logBody = logBody[:512] + "..."
						}
						logBody = strings.ReplaceAll(logBody, "\n", " ")
						fmt.Printf("[PEER ERROR] peer=%s business_error=%s\n", currentPeerID, logBody)
					}
				}
			}

			if proxyLogger.IsLevelEnabled(logmon.LevelTrace) && readErr == nil {
				contentType := strings.ToLower(resp.Header.Get("Content-Type"))
				if strings.Contains(contentType, "application/json") || strings.Contains(contentType, "text/") {
					logBody := string(body)
					if len(logBody) > 1024 {
						logBody = logBody[:1024] + "... (truncated)"
					}
					proxyLogger.Tracef("Peer %s response body: %s", currentPeerID, logBody)
				} else {
					proxyLogger.Tracef("Peer %s response body: [binary data, type=%s, size=%d]", currentPeerID, contentType, len(body))
				}
			}
			return nil
		}

		reverseProxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			// Check if client disconnected first
			if r.Context().Err() == context.Canceled {
				fmt.Printf("[PEER] peer=%s client_disconnect_during_proxy path=%s\n",
					peerID, r.URL.Path)
				http.Error(w, "client disconnected", 499)
				return
			}

			proxyLogger.Warnf("peer %s: proxy error: %v", peerID, err)

			errStr := err.Error()
			timeoutIndicator := ""
			lowerErr := strings.ToLower(errStr)
			if strings.Contains(lowerErr, "timeout") ||
				strings.Contains(lowerErr, "deadline exceeded") ||
				strings.Contains(lowerErr, "context deadline") ||
				strings.Contains(lowerErr, "read timed out") {
				timeoutIndicator = "timeout "
			}

			fmt.Printf("[PEER ERROR] peer=%s %serror=%s\n", peerID, timeoutIndicator, errStr)

			errMsg := fmt.Sprintf("peer proxy error: %v", err)
			if runtime.GOOS == "darwin" && strings.Contains(err.Error(), "connect: no route to host") {
				errMsg += " (hint: on macOS, check System Settings > Privacy & Security > Local Network permissions)"
			}
			http.Error(w, errMsg, http.StatusBadGateway)
		}

		// ServeHTTP injects X-Forwarded-For after Director, so strip via Transport.
		if len(peer.RemoveHeaders) > 0 {
			removeHeaders := peer.RemoveHeaders
			reverseProxy.Transport = &headerStrippingRoundTripper{
				Transport:    peerTransport,
				RemoveHeader: removeHeaders,
			}
		}

		pp := &peerProxyMember{
			peerID:          peerID,
			reverseProxy:    reverseProxy,
			apiKey:          peer.ApiKey,
			addHeaders:      peer.AddHeaders,
			removeHeaders:   peer.RemoveHeaders,
			stripV1Prefix:   peer.StripV1Prefix,
			maxConcurrent:   peer.MaxConcurrent,
			queueSize:       peer.QueueSize,
			queueTimeout:    peer.QueueTimeout,
			requestInterval: peer.RequestInterval,
			logger:          proxyLogger,
		}

		if peer.MaxConcurrent > 0 {
			pp.sem = make(chan struct{}, peer.MaxConcurrent)
			pp.queue = make(chan *queuedRequest, peer.QueueSize)
			pp.stopCh = make(chan struct{})
			go pp.processQueue()
		}

		// Map each model to this peer's proxy
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

	return &PeerProxy{
		peers:            peers,
		proxyMap:         proxyMap,
		prefixedModels:   prefixedModels,
		prefixPeerModels: prefixPeerModels,
	}, nil
}

func (p *PeerProxy) HasPeerModel(modelID string) bool {
	_, found := p.proxyMap[modelID]
	return found
}

// GetPeerFilters returns the filters for a peer model, or empty filters if not found
func (p *PeerProxy) GetPeerFilters(modelID string) config.Filters {
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

func (p *PeerProxy) ListPeers() config.PeerDictionaryConfig {
	return p.peers
}

func (p *PeerProxy) PrefixPeerModels() bool {
	return p.prefixPeerModels
}

func (p *PeerProxy) IsModelPrefixed(modelID string) bool {
	return p.prefixedModels[modelID]
}

func (p *PeerProxy) GetOriginalModelName(modelID string) string {
	if !p.prefixedModels[modelID] {
		return modelID
	}
	if _, after, found := strings.Cut(modelID, "/"); found {
		return after
	}
	return modelID
}

func (pp *peerProxyMember) waitForRequestInterval(ctx context.Context, requestPath string) error {
	if pp.requestInterval <= 0 {
		return nil
	}

	pp.lastRequestMu.Lock()
	elapsed := time.Since(pp.lastRequestTime)
	var waitTime time.Duration
	if elapsed < pp.requestInterval {
		waitTime = pp.requestInterval - elapsed
	}
	pp.lastRequestMu.Unlock()

	if waitTime > 0 {
		fmt.Printf("[PEER] peer=%s rate_limit waiting=%dms path=%s\n",
			pp.peerID, waitTime.Milliseconds(), requestPath)
		select {
		case <-time.After(waitTime):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (pp *peerProxyMember) markRequestComplete() {
	pp.lastRequestMu.Lock()
	pp.lastRequestTime = time.Now()
	pp.lastRequestMu.Unlock()
}

func (pp *peerProxyMember) processQueue() {
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
					fmt.Printf("[PEER ERROR] peer=%s rate_limit_cancelled path=%s error=%s\n",
						pp.peerID, qr.request.URL.Path, err)
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
						fmt.Printf("[PEER] peer=%s cancelled_while_waiting path=%s\n",
							pp.peerID, qr.request.URL.Path)
					default:
						fmt.Printf("[PEER] peer=%s dequeued active=%d/%d queue=%d path=%s\n",
							pp.peerID, len(pp.sem), pp.maxConcurrent, int(atomic.LoadInt32(&pp.waitingCount)), qr.request.URL.Path)
						if qr.cancelled.Load() {
							fmt.Printf("[PEER] peer=%s cancelled_before_serve path=%s\n",
								pp.peerID, qr.request.URL.Path)
						} else {
							qr.responded.Store(true)
							pp.reverseProxy.ServeHTTP(qr.writer, qr.request)
							pp.markRequestComplete()
						}
					}
				case <-time.After(pp.queueTimeout):
					fmt.Printf("[PEER ERROR] peer=%s queue_timeout waiting=%ds path=%s\n",
						pp.peerID, int(pp.queueTimeout.Seconds()), qr.request.URL.Path)
					if qr.responded.CompareAndSwap(false, true) {
						http.Error(qr.writer, "request timed out waiting in queue", http.StatusServiceUnavailable)
					}
				case <-qr.request.Context().Done():
					fmt.Printf("[PEER] peer=%s cancelled_in_queue path=%s\n",
						pp.peerID, qr.request.URL.Path)
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

func (pp *peerProxyMember) serveWithConcurrencyControl(writer http.ResponseWriter, request *http.Request) serveResult {
	if pp.maxConcurrent == 0 {
		return serveContinue
	}

	fmt.Printf("[PEER] peer=%s recv active=%d/%d queue=%d/%d path=%s\n",
		pp.peerID, len(pp.sem), pp.maxConcurrent, int(atomic.LoadInt32(&pp.waitingCount)), pp.queueSize, request.URL.Path)

	// Wait for request interval BEFORE acquiring semaphore, so the wait doesn't block other requests
	if err := pp.waitForRequestInterval(request.Context(), request.URL.Path); err != nil {
		fmt.Printf("[PEER ERROR] peer=%s rate_limit_cancelled path=%s error=%s\n",
			pp.peerID, request.URL.Path, err)
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
			fmt.Printf("[PEER] peer=%s client_disconnect_queued path=%s\n",
				pp.peerID, request.URL.Path)
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

func (p *PeerProxy) ProxyRequest(model_id string, writer http.ResponseWriter, request *http.Request) error {
	pp, found := p.proxyMap[model_id]
	if !found {
		return fmt.Errorf("no peer proxy found for model %s", model_id)
	}

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

	if pp.serveWithConcurrencyControl(writer, request) == serveHandled {
		return nil
	}

	if err := pp.waitForRequestInterval(request.Context(), request.URL.Path); err != nil {
		fmt.Printf("[PEER ERROR] peer=%s rate_limit_cancelled path=%s error=%s\n",
			pp.peerID, request.URL.Path, err)
		http.Error(writer, err.Error(), http.StatusServiceUnavailable)
		return nil
	}
	pp.reverseProxy.ServeHTTP(writer, request)
	pp.markRequestComplete()
	return nil
}

type headerStrippingRoundTripper struct {
	Transport    http.RoundTripper
	RemoveHeader []string
}

func (rt *headerStrippingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	for _, key := range rt.RemoveHeader {
		req.Header.Del(key)
	}
	return rt.Transport.RoundTrip(req)
}

func disablePeerHTTP2(transport *http.Transport) {
	// Some upstream peer APIs reset long-running HTTP/2 response streams with
	// INTERNAL_ERROR. ReverseProxy reports those resets as body-copy read errors
	// and the client loses the generation mid-stream. Peer traffic is more
	// reliable over HTTP/1.1, where each streaming request has its own connection.
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
}
