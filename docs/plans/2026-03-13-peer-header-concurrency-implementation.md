# Peer Header Override and Concurrency Limit Implementation Plan

**Date**: 2026-03-13
**Goal**: Add header override and per-peer concurrency control with FIFO queuing to llama-swap's peer functionality
**Architecture**: Go proxy server with per-peer concurrency semaphores and header manipulation
**Tech Stack**: Go 1.21+, httputil.ReverseProxy, buffered channels for queuing

---

## Overview

This plan implements two features for peer proxies:

1. **Header Override**: Add/override/delete HTTP headers when forwarding to peers
2. **Concurrency Limit**: Per-peer request limiting with FIFO queue and timeout

### Files to Modify

| File | Changes |
|------|---------|
| `proxy/config/peer.go` | Add Headers, MaxConcurrent, QueueSize, QueueTimeout fields |
| `proxy/peerproxy.go` | Add header application and concurrency control logic |
| `proxy/peerproxy_test.go` | Add unit tests for both features |
| `config.example.yaml` | Add peer header/concurrency examples |
| `config-schema.json` | Add JSON schema for new peer properties |

---

## Task 1: Update PeerConfig Struct

**File**: `proxy/config/peer.go`
**Time**: 2 minutes

Add new fields to the PeerConfig struct at line 9:

```go
type PeerConfig struct {
	Proxy         string            `yaml:"proxy"`
	ProxyURL      *url.URL          `yaml:"-"`
	ApiKey        string            `yaml:"apiKey"`
	Models        []string          `yaml:"models"`
	Filters       Filters           `yaml:"filters"`
	Headers       map[string]string `yaml:"headers"`       // NEW: header overrides
	MaxConcurrent int               `yaml:"maxConcurrent"` // NEW: max concurrent requests (0=unlimited)
	QueueSize     int               `yaml:"queueSize"`     // NEW: queue size for waiting requests
	QueueTimeout  time.Duration     `yaml:"queueTimeout"`  // NEW: max wait time in queue
}
```

Add import for `time` at line 5:

```go
import (
	"fmt"
	"net/url"
	"time"
)
```

Update UnmarshalYAML defaults at line 19:

```go
func (c *PeerConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawPeerConfig PeerConfig
	defaults := rawPeerConfig{
		Proxy:         "",
		ApiKey:        "",
		Models:        []string{},
		Filters:       Filters{},
		Headers:       map[string]string{},  // NEW
		MaxConcurrent: 0,                     // NEW: 0 = unlimited
		QueueSize:     32,                    // NEW: default queue size
		QueueTimeout:  60 * time.Second,      // NEW: default timeout
	}
	// ... rest of function unchanged
}
```

---

## Task 2: Add Config Unit Tests

**File**: `proxy/config/peer_test.go` (create if needed, or add to existing)
**Time**: 3 minutes

```go
package config

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPeerConfig_Defaults(t *testing.T) {
	yaml := `
proxy: http://example.com:8080
models:
  - model-a
`
	var pc PeerConfig
	err := unmarshalYAML([]byte(yaml), &pc)
	require.NoError(t, err)
	assert.Equal(t, "", pc.ApiKey)
	assert.Empty(t, pc.Headers)
	assert.Equal(t, 0, pc.MaxConcurrent)
	assert.Equal(t, 32, pc.QueueSize)
	assert.Equal(t, 60*time.Second, pc.QueueTimeout)
}

func TestPeerConfig_WithHeaders(t *testing.T) {
	yaml := `
proxy: http://example.com:8080
models:
  - model-a
headers:
  X-Custom: "value"
  X-Remove: ""
`
	var pc PeerConfig
	err := unmarshalYAML([]byte(yaml), &pc)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"X-Custom": "value", "X-Remove": ""}, pc.Headers)
}

func TestPeerConfig_WithConcurrency(t *testing.T) {
	yaml := `
proxy: http://example.com:8080
models:
  - model-a
maxConcurrent: 5
queueSize: 64
queueTimeout: 30s
`
	var pc PeerConfig
	err := unmarshalYAML([]byte(yaml), &pc)
	require.NoError(t, err)
	assert.Equal(t, 5, pc.MaxConcurrent)
	assert.Equal(t, 64, pc.QueueSize)
	assert.Equal(t, 30*time.Second, pc.QueueTimeout)
}

// Helper for testing YAML unmarshaling
func unmarshalYAML(data []byte, out interface{}) error {
	return yaml.Unmarshal(data, out)
}
```

**Test command**:
```bash
go test -v -run TestPeerConfig ./proxy/config/
```

---

## Task 3: Update peerProxyMember Struct

**File**: `proxy/peerproxy.go`
**Time**: 3 minutes

Update the struct at line 16:

```go
type peerProxyMember struct {
	peerID       string
	reverseProxy *httputil.ReverseProxy
	apiKey       string
	headers      map[string]string // NEW: custom headers

	// NEW: Concurrency control
	maxConcurrent int
	sem           chan struct{} // semaphore for concurrency limit
	queue         chan *queuedRequest
	queueTimeout  time.Duration
}

// NEW: queued request for FIFO queue
type queuedRequest struct {
	writer  http.ResponseWriter
	request *http.Request
	done    chan struct{}
}
```

---

## Task 4: Initialize Concurrency Control in NewPeerProxy

**File**: `proxy/peerproxy.go`
**Time**: 5 minutes

Update the peerProxyMember creation around line 82:

```go
pp := &peerProxyMember{
	peerID:        peerID,
	reverseProxy:  reverseProxy,
	apiKey:        peer.ApiKey,
	headers:       peer.Headers,
	maxConcurrent: peer.MaxConcurrent,
	queueTimeout:  peer.QueueTimeout,
}

// Initialize concurrency control if maxConcurrent > 0
if peer.MaxConcurrent > 0 {
	pp.sem = make(chan struct{}, peer.MaxConcurrent)
	pp.queue = make(chan *queuedRequest, peer.QueueSize)
}
```

---

## Task 5: Implement Header Application

**File**: `proxy/peerproxy.go`
**Time**: 3 minutes

Add new helper function after the peerProxyMember struct (around line 35):

```go
// applyHeaders applies custom headers to the request.
// Empty string values delete the header.
func applyHeaders(req *http.Request, headers map[string]string) {
	for key, value := range headers {
		if value == "" {
			req.Header.Del(key)
		} else {
			req.Header.Set(key, value)
		}
	}
}
```

---

## Task 6: Update ProxyRequest for Headers

**File**: `proxy/peerproxy.go`
**Time**: 2 minutes

Update ProxyRequest function at line 127 to apply headers after API key:

```go
func (p *PeerProxy) ProxyRequest(model_id string, writer http.ResponseWriter, request *http.Request) error {
	pp, found := p.proxyMap[model_id]
	if !found {
		return fmt.Errorf("no peer proxy found for model %s", model_id)
	}

	// Inject API key if configured for this peer
	if pp.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+pp.apiKey)
		request.Header.Set("x-api-key", pp.apiKey)
	}

	// Apply custom headers (after API key, so headers can override)
	if len(pp.headers) > 0 {
		applyHeaders(request, pp.headers)
	}

	// Check if concurrency control is enabled
	if pp.maxConcurrent > 0 {
		return pp.proxyWithConcurrency(writer, request)
	}

	pp.reverseProxy.ServeHTTP(writer, request)
	return nil
}
```

---

## Task 7: Implement Concurrency Control Method

**File**: `proxy/peerproxy.go`
**Time**: 5 minutes

Add new method to peerProxyMember after ProxyRequest function:

```go
// proxyWithConcurrency handles request with concurrency limiting and queuing.
func (pp *peerProxyMember) proxyWithConcurrency(writer http.ResponseWriter, request *http.Request) error {
	// Try to acquire semaphore (non-blocking)
	select {
	case pp.sem <- struct{}{}:
		// Got slot, process immediately
		defer func() { <-pp.sem }()
		pp.reverseProxy.ServeHTTP(writer, request)
		return nil
	default:
		// Slot not available, try to queue
	}

	// Try to queue the request
	qr := &queuedRequest{
		writer:  writer,
		request: request,
		done:    make(chan struct{}),
	}

	select {
	case pp.queue <- qr:
		// Queued successfully, wait for processing or timeout
		select {
		case <-qr.done:
			// Request was processed
			return nil
		case <-time.After(pp.queueTimeout):
			// Timeout waiting in queue
			// Remove from queue if still there (best effort)
			select {
			case <-pp.queue:
				// Removed from queue
			default:
				// Already being processed
			}
			http.Error(writer, "request timed out waiting in queue", http.StatusServiceUnavailable)
			return nil
		}
	default:
		// Queue is full
		http.Error(writer, "peer concurrency queue full", http.StatusServiceUnavailable)
		return nil
	}
}

// processQueue runs in a goroutine to process queued requests.
// Must be started when maxConcurrent > 0.
func (pp *peerProxyMember) startQueueProcessor(logger *LogMonitor) {
	go func() {
		for qr := range pp.queue {
			// Wait for a slot
			pp.sem <- struct{}{}
			
			// Signal that we're processing
			close(qr.done)
			
			// Process the request
			pp.reverseProxy.ServeHTTP(qr.writer, qr.request)
			
			// Release the slot
			<-pp.sem
		}
	}()
}
```

---

## Task 8: Start Queue Processor in NewPeerProxy

**File**: `proxy/peerproxy.go`
**Time**: 2 minutes

Update NewPeerProxy signature to accept logger and start queue processor:

```go
func NewPeerProxy(peers config.PeerDictionaryConfig, proxyLogger *LogMonitor) (*PeerProxy, error) {
	// ... existing code ...

	for _, peerID := range peerIDs {
		peer := peers[peerID]
		// ... existing reverse proxy setup ...

		pp := &peerProxyMember{
			peerID:        peerID,
			reverseProxy:  reverseProxy,
			apiKey:        peer.ApiKey,
			headers:       peer.Headers,
			maxConcurrent: peer.MaxConcurrent,
			queueTimeout:  peer.QueueTimeout,
		}

		// Initialize concurrency control if maxConcurrent > 0
		if peer.MaxConcurrent > 0 {
			pp.sem = make(chan struct{}, peer.MaxConcurrent)
			pp.queue = make(chan *queuedRequest, peer.QueueSize)
			pp.startQueueProcessor(proxyLogger)
		}

		// ... rest of existing code ...
	}
	// ...
}
```

---

## Task 9: Write Header Override Tests

**File**: `proxy/peerproxy_test.go`
**Time**: 5 minutes

Add these tests after existing tests:

```go
func TestProxyRequest_HeaderOverride_Add(t *testing.T) {
	var receivedHeaders http.Header
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			Models:   []string{"test-model"},
			Headers:  map[string]string{"X-Custom-Header": "custom-value"},
		},
	}

	pm, err := NewPeerProxy(peers, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("test-model", w, req)
	assert.NoError(t, err)
	assert.Equal(t, "custom-value", receivedHeaders.Get("X-Custom-Header"))
}

func TestProxyRequest_HeaderOverride_Delete(t *testing.T) {
	var receivedHeaders http.Header
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			Models:   []string{"test-model"},
			Headers:  map[string]string{"X-Remove-Me": ""}, // empty = delete
		},
	}

	pm, err := NewPeerProxy(peers, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-Remove-Me", "should-be-removed")
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("test-model", w, req)
	assert.NoError(t, err)
	assert.Empty(t, receivedHeaders.Get("X-Remove-Me"))
}

func TestProxyRequest_HeaderOverride_OverridesApiKey(t *testing.T) {
	var authHeader string
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			ApiKey:   "auto-api-key",
			Models:   []string{"test-model"},
			Headers:  map[string]string{"Authorization": "Bearer custom-override"},
		},
	}

	pm, err := NewPeerProxy(peers, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("test-model", w, req)
	assert.NoError(t, err)
	assert.Equal(t, "Bearer custom-override", authHeader)
}
```

**Test command**:
```bash
go test -v -run TestProxyRequest_Header ./proxy/
```

---

## Task 10: Write Concurrency Limit Tests

**File**: `proxy/peerproxy_test.go`
**Time**: 10 minutes

Add these tests:

```go
func TestProxyRequest_ConcurrencyLimit_Enforced(t *testing.T) {
	// Create a server that blocks until we release it
	blockChan := make(chan struct{})
	activeCount := int32(0)
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&activeCount, 1)
		<-blockChan // Block until test releases
		atomic.AddInt32(&activeCount, -1)
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:         testServer.URL,
			ProxyURL:      proxyURL,
			Models:        []string{"test-model"},
			MaxConcurrent: 2,
			QueueSize:     10,
			QueueTimeout:  5 * time.Second,
		},
	}

	pm, err := NewPeerProxy(peers, testLogger)
	require.NoError(t, err)

	// Start 4 concurrent requests (maxConcurrent=2, so 2 should queue)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			w := httptest.NewRecorder()
			pm.ProxyRequest("test-model", w, req)
		}()
	}

	// Wait a bit for requests to start
	time.Sleep(100 * time.Millisecond)

	// Should have exactly 2 active requests (maxConcurrent)
	assert.Equal(t, int32(2), atomic.LoadInt32(&activeCount))

	// Release all blocked requests
	close(blockChan)
	wg.Wait()

	// All should complete
	assert.Equal(t, int32(0), atomic.LoadInt32(&activeCount))
}

func TestProxyRequest_ConcurrencyLimit_QueueFull(t *testing.T) {
	blockChan := make(chan struct{})
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blockChan
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:         testServer.URL,
			ProxyURL:      proxyURL,
			Models:        []string{"test-model"},
			MaxConcurrent: 1,
			QueueSize:     2, // Only 2 can queue
			QueueTimeout:  10 * time.Second,
		},
	}

	pm, err := NewPeerProxy(peers, testLogger)
	require.NoError(t, err)

	// Start maxConcurrent + queueSize + 1 requests
	// 1 active + 2 queued = 3, 4th should get 503
	results := make(chan int, 4)
	var wg sync.WaitGroup
	
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			w := httptest.NewRecorder()
			pm.ProxyRequest("test-model", w, req)
			results <- w.Code
		}()
	}

	// Wait for all to complete or be rejected
	time.Sleep(200 * time.Millisecond)

	// Release the blocked server
	close(blockChan)
	wg.Wait()
	close(results)

	// Count results
	statusCodes := make(map[int]int)
	for code := range results {
		statusCodes[code]++
	}

	// Should have 3 successes (1 direct + 2 queued) and 1 queue-full (503)
	assert.Equal(t, 1, statusCodes[503], "expected one 503 response for queue full")
}

func TestProxyRequest_ConcurrencyLimit_QueueTimeout(t *testing.T) {
	blockChan := make(chan struct{})
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blockChan
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:         testServer.URL,
			ProxyURL:      proxyURL,
			Models:        []string{"test-model"},
			MaxConcurrent: 1,
			QueueSize:     10,
			QueueTimeout:  100 * time.Millisecond, // Short timeout for test
		},
	}

	pm, err := NewPeerProxy(peers, testLogger)
	require.NoError(t, err)

	// Start request that will block
	go func() {
		req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		w := httptest.NewRecorder()
		pm.ProxyRequest("test-model", w, req)
	}()

	// Wait for first request to start
	time.Sleep(50 * time.Millisecond)

	// This request should timeout in queue
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	pm.ProxyRequest("test-model", w, req)

	// Should get 503 due to timeout
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "timed out")

	// Cleanup
	close(blockChan)
}

func TestProxyRequest_ConcurrencyLimit_PerPeerIsolation(t *testing.T) {
	testServer1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer1.Close()

	testServer2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer2.Close()

	proxyURL1, _ := url.Parse(testServer1.URL)
	proxyURL2, _ := url.Parse(testServer2.URL)

	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:         testServer1.URL,
			ProxyURL:      proxyURL1,
			Models:        []string{"model-a"},
			MaxConcurrent: 1, // Only 1 for peer1
		},
		"peer2": config.PeerConfig{
			Proxy:    testServer2.URL,
			ProxyURL: proxyURL2,
			Models:   []string{"model-b"},
			// No concurrency limit for peer2
		},
	}

	pm, err := NewPeerProxy(peers, testLogger)
	require.NoError(t, err)

	// peer2 should not be affected by peer1's limit
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	err = pm.ProxyRequest("model-b", w, req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestProxyRequest_NoConcurrencyLimit(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			Models:   []string{"test-model"},
			// MaxConcurrent: 0 (default) = unlimited
		},
	}

	pm, err := NewPeerProxy(peers, testLogger)
	require.NoError(t, err)

	// Should work without any concurrency control
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	err = pm.ProxyRequest("test-model", w, req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)
}
```

**Test command**:
```bash
go test -v -run TestProxyRequest_Concurrency ./proxy/ -timeout 30s
```

---

## Task 11: Update config.example.yaml

**File**: `config.example.yaml`
**Time**: 2 minutes

Add header and concurrency examples to the peers section (around line 410):

```yaml
peers:
  # ... existing examples ...

  # Example with header overrides and concurrency limits
  openrouter:
    proxy: https://openrouter.ai/api
    apiKey: ${env.OPENROUTER_API_KEY}
    models:
      - meta-llama/llama-3.1-8b-instruct
    # NEW: Header overrides
    # - Set custom headers for authentication, routing, or metadata
    # - Empty string value removes the header
    headers:
      X-Custom-Header: "custom-value"
      X-Remove-Me: ""  # removes this header
    # NEW: Concurrency control
    # - maxConcurrent: max concurrent requests (0 = unlimited, default)
    # - queueSize: how many requests to queue when at capacity (default: 32)
    # - queueTimeout: max time to wait in queue (default: 60s)
    maxConcurrent: 5
    queueSize: 32
    queueTimeout: 60s
    filters:
      stripParams: "temperature, top_p"
      setParams:
        provider:
          data_collection: "deny"
```

---

## Task 12: Update config-schema.json

**File**: `config-schema.json`
**Time**: 3 minutes

Add new properties to the peers additionalProperties section (around line 335):

```json
"peers": {
    "type": "object",
    "additionalProperties": {
        "type": "object",
        "required": ["proxy", "models"],
        "properties": {
            "proxy": {
                "type": "string",
                "format": "uri",
                "description": "A valid base URL to proxy requests to."
            },
            "apiKey": {
                "type": "string",
                "default": "",
                "description": "API key injected into Authorization and x-api-key headers."
            },
            "models": {
                "type": "array",
                "items": {"type": "string", "minLength": 1},
                "description": "List of models served by this peer."
            },
            "headers": {
                "type": "object",
                "additionalProperties": {"type": "string"},
                "default": {},
                "description": "HTTP headers to add/override/delete. Empty string value deletes the header."
            },
            "maxConcurrent": {
                "type": "integer",
                "minimum": 0,
                "default": 0,
                "description": "Maximum concurrent requests to this peer. 0 = unlimited."
            },
            "queueSize": {
                "type": "integer",
                "minimum": 1,
                "default": 32,
                "description": "Queue size for requests when at capacity."
            },
            "queueTimeout": {
                "type": "string",
                "default": "60s",
                "description": "Maximum time a request waits in queue before returning 503.",
                "pattern": "^[0-9]+(s|m|h)$"
            },
            "filters": {
                "type": "object",
                "properties": {
                    "stripParams": {
                        "type": "string",
                        "default": "",
                        "pattern": "^[a-zA-Z0-9_, ]*$"
                    },
                    "setParams": {
                        "type": "object",
                        "additionalProperties": true,
                        "default": {}
                    }
                },
                "additionalProperties": false
            }
        }
    },
    "default": {},
    "description": "Remote peers and models they provide."
}
```

---

## Task 13: Run Full Test Suite

**Time**: 2 minutes

Run all tests to verify implementation:

```bash
# Run all proxy tests
go test -v ./proxy/ -timeout 60s

# Run specific new tests
go test -v -run "TestProxyRequest_Header|TestProxyRequest_Concurrency" ./proxy/ -timeout 30s

# Run staticcheck
make test-dev
```

---

## Task 14: Integration Test

**Time**: 5 minutes

Create an integration test file `proxy/peerproxy_integration_test.go`:

```go
//go:build integration
// +build integration

package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/proxy/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntegration_PeerConcurrencyAndHeaders(t *testing.T) {
	var requestCount int32
	var maxConcurrent int32
	var currentConcurrent int32
	var receivedHeaders http.Header

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		
		cur := atomic.AddInt32(&currentConcurrent, 1)
		for {
			old := atomic.LoadInt32(&maxConcurrent)
			if cur <= old || atomic.CompareAndSwapInt32(&maxConcurrent, old, cur) {
				break
			}
		}
		
		atomic.AddInt32(&requestCount, 1)
		time.Sleep(50 * time.Millisecond) // Simulate work
		atomic.AddInt32(&currentConcurrent, -1)
		
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:         testServer.URL,
			ProxyURL:      proxyURL,
			Models:        []string{"test-model"},
			Headers:       map[string]string{"X-Test-Header": "test-value"},
			MaxConcurrent: 3,
			QueueSize:     10,
			QueueTimeout:  5 * time.Second,
		},
	}

	pm, err := NewPeerProxy(peers, testLogger)
	require.NoError(t, err)

	// Send 10 concurrent requests
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			w := httptest.NewRecorder()
			pm.ProxyRequest("test-model", w, req)
		}()
	}
	wg.Wait()

	// Verify all requests completed
	assert.Equal(t, int32(10), atomic.LoadInt32(&requestCount))

	// Verify concurrency limit was respected
	assert.LessOrEqual(t, atomic.LoadInt32(&maxConcurrent), int32(3))

	// Verify header was set
	assert.Equal(t, "test-value", receivedHeaders.Get("X-Test-Header"))
}
```

**Test command**:
```bash
go test -v -tags=integration -run TestIntegration_Peer ./proxy/ -timeout 30s
```

---

## Verification Checklist

Before marking complete, verify:

- [ ] `go test -v ./proxy/` passes with 0 failures
- [ ] `go test -v ./proxy/config/` passes with 0 failures  
- [ ] `make test-dev` passes (includes staticcheck)
- [ ] All new config fields have defaults in UnmarshalYAML
- [ ] Header override works: add, override, delete
- [ ] Concurrency limit enforced: maxConcurrent requests active
- [ ] Queue full returns 503
- [ ] Queue timeout returns 503
- [ ] Per-peer isolation: one peer's limit doesn't affect others
- [ ] Zero maxConcurrent = unlimited (backward compatible)
- [ ] config.example.yaml updated with examples
- [ ] config-schema.json updated with new properties

---

## Rollback Plan

If issues arise:

1. Revert PeerConfig struct changes - old configs still work
2. Set `maxConcurrent: 0` to disable concurrency control
3. Remove `headers` key to disable header override

---

## Summary

| Component | Lines Changed | Complexity |
|-----------|---------------|------------|
| proxy/config/peer.go | ~20 | Low |
| proxy/peerproxy.go | ~80 | Medium |
| proxy/peerproxy_test.go | ~200 | Medium |
| config.example.yaml | ~15 | Low |
| config-schema.json | ~30 | Low |

**Total estimated time**: 45-60 minutes
