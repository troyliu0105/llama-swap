package proxy

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/proxy/config"
)

type peerProxyMember struct {
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
	logger        *LogMonitor
	stopCh        chan struct{}
}

type serveResult int

const (
	serveHandled serveResult = iota
	serveContinue
)

type queuedRequest struct {
	writer  http.ResponseWriter
	request *http.Request
	done    chan struct{}
}

type PeerProxy struct {
	peers            config.PeerDictionaryConfig
	proxyMap         map[string]*peerProxyMember
	prefixedModels   map[string]bool
	prefixPeerModels bool
}

func NewPeerProxy(peers config.PeerDictionaryConfig, prefixPeerModels bool, proxyLogger *LogMonitor) (*PeerProxy, error) {
	proxyMap := make(map[string]*peerProxyMember)
	prefixedModels := make(map[string]bool)

	// Sort peer IDs for consistent iteration order
	peerIDs := make([]string, 0, len(peers))
	for peerID := range peers {
		peerIDs = append(peerIDs, peerID)
	}
	sort.Strings(peerIDs)

	// Create a shared transport with reasonable timeouts for peer connections
	// these can be tuned with feedback later
	peerTransport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second, // Connection timeout
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second, // Time to wait for response headers
		ExpectContinueTimeout: 1 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
	}

	for _, peerID := range peerIDs {
		peer := peers[peerID]
		// Create reverse proxy for this peer
		reverseProxy := httputil.NewSingleHostReverseProxy(peer.ProxyURL)
		reverseProxy.Transport = peerTransport

		// Wrap Director to set Host header for remote hosts (not localhost)
		originalDirector := reverseProxy.Director
		reverseProxy.Director = func(req *http.Request) {
			originalDirector(req)
			// Ensure Host header matches target URL for remote proxying
			req.Host = req.URL.Host
		}

		reverseProxy.ModifyResponse = func(resp *http.Response) error {
			if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
				resp.Header.Set("X-Accel-Buffering", "no")
			}
			return nil
		}

		reverseProxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			proxyLogger.Warnf("peer %s: proxy error: %v", peerID, err)
			errMsg := fmt.Sprintf("peer proxy error: %v", err)
			if runtime.GOOS == "darwin" && strings.Contains(err.Error(), "connect: no route to host") {
				errMsg += " (hint: on macOS, check System Settings > Privacy & Security > Local Network permissions)"
			}
			http.Error(w, errMsg, http.StatusBadGateway)
		}

		pp := &peerProxyMember{
			peerID:        peerID,
			reverseProxy:  reverseProxy,
			apiKey:        peer.ApiKey,
			headers:       peer.Headers,
			stripV1Prefix: peer.StripV1Prefix,
			maxConcurrent: peer.MaxConcurrent,
			queueSize:     peer.QueueSize,
			queueTimeout:  peer.QueueTimeout,
			logger:        proxyLogger,
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

func (pp *peerProxyMember) processQueue() {
	defer func() {
		if r := recover(); r != nil {
			pp.logger.Warnf("peer %s: processQueue panic recovered: %v", pp.peerID, r)
		}
	}()

	for {
		select {
		case req := <-pp.queue:
			select {
			case pp.sem <- struct{}{}:
				pp.reverseProxy.ServeHTTP(req.writer, req.request)
				<-pp.sem
			case <-time.After(pp.queueTimeout):
				http.Error(req.writer, "request timed out waiting in queue", http.StatusServiceUnavailable)
			}
			close(req.done)
		case <-pp.stopCh:
			for len(pp.queue) > 0 {
				req := <-pp.queue
				http.Error(req.writer, "server shutting down", http.StatusServiceUnavailable)
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

	select {
	case pp.sem <- struct{}{}:
		pp.reverseProxy.ServeHTTP(writer, request)
		<-pp.sem
		return serveHandled
	default:
	}

	done := make(chan struct{}, 1)
	qr := &queuedRequest{
		writer:  writer,
		request: request,
		done:    done,
	}

	select {
	case pp.queue <- qr:
		<-done
		return serveHandled
	default:
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

	pp.reverseProxy.ServeHTTP(writer, request)
	return nil
}
