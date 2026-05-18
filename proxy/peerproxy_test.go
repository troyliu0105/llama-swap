package proxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/proxy/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func captureStdout(f func()) string {
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w

	f()

	w.Close()
	os.Stdout = old

	var buf strings.Builder
	io.Copy(&buf, r)
	return buf.String()
}

func TestNewPeerProxy_EmptyPeers(t *testing.T) {
	peers := config.PeerDictionaryConfig{}
	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)
	assert.NotNil(t, pm)
	assert.Empty(t, pm.proxyMap)
}

func TestNewPeerProxy_SinglePeer(t *testing.T) {
	proxyURL, _ := url.Parse("http://peer1.example.com:8080")
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    "http://peer1.example.com:8080",
			ProxyURL: proxyURL,
			ApiKey:   "test-key",
			Models:   []string{"model-a", "model-b"},
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)
	assert.Len(t, pm.proxyMap, 2)
	assert.True(t, pm.HasPeerModel("model-a"))
	assert.True(t, pm.HasPeerModel("model-b"))
	assert.False(t, pm.HasPeerModel("model-c"))
}

func TestNewPeerProxy_MultiplePeers(t *testing.T) {
	proxyURL1, _ := url.Parse("http://peer1.example.com:8080")
	proxyURL2, _ := url.Parse("http://peer2.example.com:8080")
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    "http://peer1.example.com:8080",
			ProxyURL: proxyURL1,
			Models:   []string{"model-a", "model-b"},
		},
		"peer2": config.PeerConfig{
			Proxy:    "http://peer2.example.com:8080",
			ProxyURL: proxyURL2,
			Models:   []string{"model-c", "model-d"},
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)
	assert.Len(t, pm.proxyMap, 4)
	assert.True(t, pm.HasPeerModel("model-a"))
	assert.True(t, pm.HasPeerModel("model-b"))
	assert.True(t, pm.HasPeerModel("model-c"))
	assert.True(t, pm.HasPeerModel("model-d"))
}

func TestNewPeerProxy_DuplicateModelWarning(t *testing.T) {
	// When the same model is in multiple peers, only the first (lexicographically by peer ID)
	// should be mapped, and a warning should be logged
	proxyURL1, _ := url.Parse("http://peer1.example.com:8080")
	proxyURL2, _ := url.Parse("http://peer2.example.com:8080")
	peers := config.PeerDictionaryConfig{
		"alpha-peer": config.PeerConfig{
			Proxy:    "http://peer1.example.com:8080",
			ProxyURL: proxyURL1,
			Models:   []string{"duplicate-model"},
		},
		"beta-peer": config.PeerConfig{
			Proxy:    "http://peer2.example.com:8080",
			ProxyURL: proxyURL2,
			Models:   []string{"duplicate-model"},
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)
	// Should only have one entry for the duplicate model
	assert.Len(t, pm.proxyMap, 1)
	assert.True(t, pm.HasPeerModel("duplicate-model"))
}

func TestHasPeerModel(t *testing.T) {
	proxyURL, _ := url.Parse("http://peer1.example.com:8080")
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    "http://peer1.example.com:8080",
			ProxyURL: proxyURL,
			Models:   []string{"existing-model"},
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	assert.True(t, pm.HasPeerModel("existing-model"))
	assert.False(t, pm.HasPeerModel("non-existing-model"))
}

func TestProxyRequest_ModelNotFound(t *testing.T) {
	peers := config.PeerDictionaryConfig{}
	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("non-existing-model", w, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no peer proxy found for model non-existing-model")
}

func TestProxyRequest_Success(t *testing.T) {
	// Create a test server to act as the peer
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("response from peer"))
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			Models:   []string{"test-model"},
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("test-model", w, req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "response from peer", w.Body.String())
}

func TestProxyRequest_ApiKeyInjection(t *testing.T) {
	// Create a test server that checks for the Authorization header
	var receivedAuthHeader string
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			ApiKey:   "secret-api-key",
			Models:   []string{"test-model"},
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("test-model", w, req)
	assert.NoError(t, err)
	assert.Equal(t, "Bearer secret-api-key", receivedAuthHeader)
}

func TestProxyRequest_NoApiKey(t *testing.T) {
	// Create a test server that checks for the Authorization header
	var receivedAuthHeader string
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			ApiKey:   "", // No API key
			Models:   []string{"test-model"},
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("test-model", w, req)
	assert.NoError(t, err)
	assert.Empty(t, receivedAuthHeader)
}

func TestProxyRequest_HostHeaderSet(t *testing.T) {
	// Create a test server that checks the Host header
	var receivedHost string
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHost = r.Host
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			Models:   []string{"test-model"},
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("test-model", w, req)
	assert.NoError(t, err)
	// The Host header should be set to the target URL's host
	assert.True(t, strings.HasPrefix(receivedHost, "127.0.0.1:"))
}

func TestProxyRequest_SSEHeaderModification(t *testing.T) {
	// Create a test server that returns SSE content type
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			Models:   []string{"test-model"},
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("test-model", w, req)
	assert.NoError(t, err)
	assert.Equal(t, "no", w.Header().Get("X-Accel-Buffering"))
}

func TestProxyRequest_HeaderOverride(t *testing.T) {
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
			AddHeaders: map[string]string{
				"X-Custom-Header": "custom-value",
				"X-Override-Me":   "overridden",
			},
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-Override-Me", "original")
	req.Header.Set("X-Existing", "exists")
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("test-model", w, req)
	assert.NoError(t, err)
	assert.Equal(t, "custom-value", receivedHeaders.Get("X-Custom-Header"))
	assert.Equal(t, "overridden", receivedHeaders.Get("X-Override-Me"))
	assert.Equal(t, "exists", receivedHeaders.Get("X-Existing"))
}

func TestProxyRequest_HeaderDelete(t *testing.T) {
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
			RemoveHeaders: []string{
				"X-Delete-Me",
			},
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-Delete-Me", "should-be-deleted")
	req.Header.Set("X-Keep-Me", "kept")
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("test-model", w, req)
	assert.NoError(t, err)
	assert.Empty(t, receivedHeaders.Get("X-Delete-Me"))
	assert.Equal(t, "kept", receivedHeaders.Get("X-Keep-Me"))
}

func TestProxyRequest_HeaderOverrideAfterApiKey(t *testing.T) {
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
			ApiKey:   "api-key-value",
			Models:   []string{"test-model"},
			AddHeaders: map[string]string{
				"Authorization": "Custom-Auth",
			},
			RemoveHeaders: []string{
				"x-api-key",
			},
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("test-model", w, req)
	assert.NoError(t, err)
	assert.Equal(t, "Custom-Auth", receivedHeaders.Get("Authorization"))
	assert.Empty(t, receivedHeaders.Get("x-api-key"))
}

func TestProxyRequest_ConcurrencyLimit(t *testing.T) {
	var concurrentCount int32
	var maxConcurrent int32
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&concurrentCount, 1)
		for {
			old := atomic.LoadInt32(&maxConcurrent)
			if cur <= old || atomic.CompareAndSwapInt32(&maxConcurrent, old, cur) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		atomic.AddInt32(&concurrentCount, -1)
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

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			w := httptest.NewRecorder()
			pm.ProxyRequest("test-model", w, req)
		}()
	}
	wg.Wait()

	assert.LessOrEqual(t, atomic.LoadInt32(&maxConcurrent), int32(2))
}

func TestProxyRequest_QueueFull(t *testing.T) {
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
			QueueSize:     0,
			QueueTimeout:  1 * time.Second,
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	var wg sync.WaitGroup
	results := make([]int, 5)
	var mu sync.Mutex

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			w := httptest.NewRecorder()
			pm.ProxyRequest("test-model", w, req)
			mu.Lock()
			results[idx] = w.Code
			mu.Unlock()
		}(i)
	}

	time.Sleep(100 * time.Millisecond)
	close(blockChan)
	wg.Wait()

	serviceUnavailableCount := 0
	for _, code := range results {
		if code == http.StatusServiceUnavailable {
			serviceUnavailableCount++
		}
	}

	assert.GreaterOrEqual(t, serviceUnavailableCount, 3, "Expected at least 3 requests to get 503 when queue size is 0")
}

func TestProxyRequest_NoConcurrencyLimit(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:         testServer.URL,
			ProxyURL:      proxyURL,
			Models:        []string{"test-model"},
			MaxConcurrent: 0,
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			w := httptest.NewRecorder()
			err := pm.ProxyRequest("test-model", w, req)
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, w.Code)
		}()
	}
	wg.Wait()
}

func TestProxyRequest_QueueTiming(t *testing.T) {
	var activeCount int32
	var maxActive int32

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&activeCount, 1)
		for {
			old := atomic.LoadInt32(&maxActive)
			if cur <= old || atomic.CompareAndSwapInt32(&maxActive, old, cur) {
				break
			}
		}
		time.Sleep(150 * time.Millisecond)
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

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	start := time.Now()
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
	wg.Wait()
	elapsed := time.Since(start)

	assert.LessOrEqual(t, atomic.LoadInt32(&maxActive), int32(2), "max concurrent should not exceed 2")

	if elapsed > 500*time.Millisecond {
		t.Errorf("Queue timing issue: took %v, expected ~300ms for 4 requests with 2 slots and 150ms each", elapsed)
	}

	t.Logf("4 requests with 2 slots (150ms each) completed in %v", elapsed)
}

func TestProxyRequest_QueueFIFO(t *testing.T) {
	var mu sync.Mutex
	processOrder := []int{}

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idStr := r.Header.Get("X-Request-Id")
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		processOrder = append(processOrder, int(idStr[0]-'0'))
		mu.Unlock()
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
			QueueTimeout:  10 * time.Second,
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	var wg sync.WaitGroup

	for i := 1; i <= 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			req.Header.Set("X-Request-Id", fmt.Sprintf("%d", id))
			w := httptest.NewRecorder()
			pm.ProxyRequest("test-model", w, req)
		}(i)
		time.Sleep(5 * time.Millisecond)
	}

	wg.Wait()

	expected := []int{1, 2, 3, 4, 5}
	assert.Equal(t, expected, processOrder, "Requests should be processed in FIFO order")
	t.Logf("Process order: %v", processOrder)
}

func TestProxyRequest_StripV1Prefix(t *testing.T) {
	var receivedPath string
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:         testServer.URL,
			ProxyURL:      proxyURL,
			Models:        []string{"test-model"},
			StripV1Prefix: true,
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("test-model", w, req)
	assert.NoError(t, err)
	assert.Equal(t, "/chat/completions", receivedPath)
}

func TestProxyRequest_NoStripV1Prefix(t *testing.T) {
	var receivedPath string
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:         testServer.URL,
			ProxyURL:      proxyURL,
			Models:        []string{"test-model"},
			StripV1Prefix: false,
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("test-model", w, req)
	assert.NoError(t, err)
	assert.Equal(t, "/v1/chat/completions", receivedPath)
}

func TestNewPeerProxy_PrefixPeerModels(t *testing.T) {
	proxyURL, _ := url.Parse("http://peer1.example.com:8080")
	peers := config.PeerDictionaryConfig{
		"opencode": config.PeerConfig{
			Proxy:    "http://peer1.example.com:8080",
			ProxyURL: proxyURL,
			Models:   []string{"big-pickle", "small-pickle"},
		},
	}

	pm, err := NewPeerProxy(peers, true, testLogger)
	require.NoError(t, err)
	assert.Len(t, pm.proxyMap, 2)
	assert.True(t, pm.HasPeerModel("opencode/big-pickle"))
	assert.True(t, pm.HasPeerModel("opencode/small-pickle"))
	assert.False(t, pm.HasPeerModel("big-pickle"))
	assert.False(t, pm.HasPeerModel("small-pickle"))
	assert.True(t, pm.PrefixPeerModels())
}

func TestNewPeerProxy_NoPrefixPeerModels(t *testing.T) {
	proxyURL, _ := url.Parse("http://peer1.example.com:8080")
	peers := config.PeerDictionaryConfig{
		"opencode": config.PeerConfig{
			Proxy:    "http://peer1.example.com:8080",
			ProxyURL: proxyURL,
			Models:   []string{"big-pickle", "small-pickle"},
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)
	assert.Len(t, pm.proxyMap, 2)
	assert.True(t, pm.HasPeerModel("big-pickle"))
	assert.True(t, pm.HasPeerModel("small-pickle"))
	assert.False(t, pm.HasPeerModel("opencode/big-pickle"))
	assert.False(t, pm.PrefixPeerModels())
}

func TestGetOriginalModelName(t *testing.T) {
	proxyURL, _ := url.Parse("http://peer1.example.com:8080")
	peers := config.PeerDictionaryConfig{
		"opencode": config.PeerConfig{
			Proxy:    "http://peer1.example.com:8080",
			ProxyURL: proxyURL,
			Models:   []string{"big-pickle"},
		},
	}

	t.Run("with prefix enabled", func(t *testing.T) {
		pm, err := NewPeerProxy(peers, true, testLogger)
		require.NoError(t, err)
		assert.Equal(t, "big-pickle", pm.GetOriginalModelName("opencode/big-pickle"))
		assert.Equal(t, "no-slash", pm.GetOriginalModelName("no-slash"))
	})

	t.Run("with prefix disabled", func(t *testing.T) {
		pm, err := NewPeerProxy(peers, false, testLogger)
		require.NoError(t, err)
		assert.Equal(t, "big-pickle", pm.GetOriginalModelName("big-pickle"))
		assert.Equal(t, "opencode/big-pickle", pm.GetOriginalModelName("opencode/big-pickle"))
	})
}

func TestProxyRequest_RemoveHeaders(t *testing.T) {
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
			RemoveHeaders: []string{
				"X-Remove-Me",
				"X-Also-Remove",
			},
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-Remove-Me", "should-be-removed")
	req.Header.Set("X-Also-Remove", "should-also-be-removed")
	req.Header.Set("X-Keep-Me", "kept")
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("test-model", w, req)
	assert.NoError(t, err)
	assert.Empty(t, receivedHeaders.Get("X-Remove-Me"))
	assert.Empty(t, receivedHeaders.Get("X-Also-Remove"))
	assert.Equal(t, "kept", receivedHeaders.Get("X-Keep-Me"))
}

func TestProxyRequest_RemoveHeadersXForwardedFor(t *testing.T) {
	var receivedXFF string
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedXFF = r.Header.Get("X-Forwarded-For")
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			Models:   []string{"test-model"},
			RemoveHeaders: []string{
				"X-Forwarded-For",
			},
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("test-model", w, req)
	assert.NoError(t, err)
	assert.Empty(t, receivedXFF, "X-Forwarded-For should be stripped even though ServeHTTP injects it")
}

func TestProxyRequest_RemoveHeadersCombinedWithHeaders(t *testing.T) {
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
			AddHeaders: map[string]string{
				"User-Agent": "llama-swap/1.0",
			},
			RemoveHeaders: []string{
				"X-Forwarded-For",
				"Origin",
			},
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("Origin", "http://client.example.com")
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("test-model", w, req)
	assert.NoError(t, err)
	assert.Equal(t, "llama-swap/1.0", receivedHeaders.Get("User-Agent"))
	assert.Empty(t, receivedHeaders.Get("X-Forwarded-For"))
	assert.Empty(t, receivedHeaders.Get("Origin"))
}

func TestNewPeerProxy_PerPeerPrefix(t *testing.T) {
	proxyURL1, _ := url.Parse("http://peer1.example.com:8080")
	proxyURL2, _ := url.Parse("http://peer2.example.com:8080")
	trueVal := true
	falseVal := false

	peers := config.PeerDictionaryConfig{
		"opencode": config.PeerConfig{
			Proxy:            "http://peer1.example.com:8080",
			ProxyURL:         proxyURL1,
			Models:           []string{"big-pickle"},
			PrefixPeerModels: &trueVal,
		},
		"anthropic": config.PeerConfig{
			Proxy:            "http://peer2.example.com:8080",
			ProxyURL:         proxyURL2,
			Models:           []string{"claude-3"},
			PrefixPeerModels: &falseVal,
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	assert.True(t, pm.HasPeerModel("opencode/big-pickle"))
	assert.False(t, pm.HasPeerModel("big-pickle"))
	assert.True(t, pm.IsModelPrefixed("opencode/big-pickle"))

	assert.True(t, pm.HasPeerModel("claude-3"))
	assert.False(t, pm.HasPeerModel("anthropic/claude-3"))
	assert.False(t, pm.IsModelPrefixed("claude-3"))

	assert.Equal(t, "big-pickle", pm.GetOriginalModelName("opencode/big-pickle"))
	assert.Equal(t, "claude-3", pm.GetOriginalModelName("claude-3"))
}

func TestNewPeerProxy_PerPeerPrefixOverrideGlobal(t *testing.T) {
	proxyURL1, _ := url.Parse("http://peer1.example.com:8080")
	proxyURL2, _ := url.Parse("http://peer2.example.com:8080")
	falseVal := false

	peers := config.PeerDictionaryConfig{
		"opencode": config.PeerConfig{
			Proxy:    "http://peer1.example.com:8080",
			ProxyURL: proxyURL1,
			Models:   []string{"big-pickle"},
		},
		"anthropic": config.PeerConfig{
			Proxy:            "http://peer2.example.com:8080",
			ProxyURL:         proxyURL2,
			Models:           []string{"claude-3"},
			PrefixPeerModels: &falseVal,
		},
	}

	pm, err := NewPeerProxy(peers, true, testLogger)
	require.NoError(t, err)

	assert.True(t, pm.HasPeerModel("opencode/big-pickle"))
	assert.True(t, pm.IsModelPrefixed("opencode/big-pickle"))

	assert.True(t, pm.HasPeerModel("claude-3"))
	assert.False(t, pm.HasPeerModel("anthropic/claude-3"))
	assert.False(t, pm.IsModelPrefixed("claude-3"))
}

func TestPeerProxy_LogsNon200ResponseToStdout(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error": "peer overloaded", "code": 503}`))
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			Models:   []string{"test-model"},
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	output := captureStdout(func() {
		err := pm.ProxyRequest("test-model", w, req)
		assert.NoError(t, err)
	})

	assert.Contains(t, output, "peer1")
	assert.Contains(t, output, "503")
	assert.Contains(t, output, "peer overloaded")
}

func TestPeerProxy_LogsBusinessError200ToStdout(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"error": {"message": "invalid model", "type": "invalid_request_error"}}`))
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			Models:   []string{"test-model"},
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	output := captureStdout(func() {
		err := pm.ProxyRequest("test-model", w, req)
		assert.NoError(t, err)
	})

	assert.Contains(t, output, "peer1")
	assert.Contains(t, output, "invalid model")
}

func TestPeerProxy_LogsTimeoutToStdout(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			Models:   []string{"test-model"},
			Timeouts: config.TimeoutsConfig{
				ResponseHeader: 1,
			},
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)

	w := httptest.NewRecorder()

	output := captureStdout(func() {
		err := pm.ProxyRequest("test-model", w, req)
		assert.NoError(t, err)
	})

	assert.Contains(t, output, "peer1")
	assert.Contains(t, output, "timeout")
}

func TestProxyRequest_QueueFIFO_MultipleSlots(t *testing.T) {
	var mu sync.Mutex
	processOrder := []int{}

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idStr := r.Header.Get("X-Request-Id")
		id, _ := strconv.Atoi(idStr)
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		processOrder = append(processOrder, id)
		mu.Unlock()
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
			QueueTimeout:  10 * time.Second,
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	var wg sync.WaitGroup

	for i := 1; i <= 6; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			req.Header.Set("X-Request-Id", strconv.Itoa(id))
			w := httptest.NewRecorder()
			pm.ProxyRequest("test-model", w, req)
		}(i)
		time.Sleep(5 * time.Millisecond)
	}

	wg.Wait()

	t.Logf("Process order: %v", processOrder)
	t.Logf("Expected FIFO: [1 2 3 4 5 6]")

	expected := []int{1, 2, 3, 4, 5, 6}
	assert.Equal(t, expected, processOrder, "Requests should be processed in FIFO order even with multiple slots")
}

func TestProxyRequest_ContextCancellation_WhileQueued(t *testing.T) {
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
			QueueTimeout:  5 * time.Second,
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	var wg sync.WaitGroup
	results := make([]int, 3)
	var mu sync.Mutex

	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			req = req.WithContext(ctx)
			w := httptest.NewRecorder()

			if idx == 2 {
				time.Sleep(50 * time.Millisecond)
				cancel()
			}

			pm.ProxyRequest("test-model", w, req)

			mu.Lock()
			results[idx] = w.Code
			mu.Unlock()
		}(i)
	}

	time.Sleep(200 * time.Millisecond)
	close(blockChan)
	wg.Wait()

	successCount := 0
	cancelledCount := 0
	for _, code := range results {
		if code == http.StatusOK {
			successCount++
		}
		if code == 499 {
			cancelledCount++
		}
	}

	assert.Equal(t, 2, successCount, "Expected 2 successful requests")
	assert.Equal(t, 1, cancelledCount, "Expected 1 cancelled request (499)")
	t.Logf("Results: %v (success=%d, cancelled=%d)", results, successCount, cancelledCount)
}

func TestProxyRequest_ContextCancellation_WhileWaitingForSlot(t *testing.T) {
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
			QueueTimeout:  5 * time.Second,
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	var wg sync.WaitGroup
	results := make([]int, 2)
	var mu sync.Mutex

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			req = req.WithContext(ctx)
			w := httptest.NewRecorder()

			if idx == 1 {
				time.Sleep(50 * time.Millisecond)
				cancel()
			}

			pm.ProxyRequest("test-model", w, req)

			mu.Lock()
			results[idx] = w.Code
			mu.Unlock()
		}(i)
	}

	time.Sleep(150 * time.Millisecond)
	close(blockChan)
	wg.Wait()

	assert.Equal(t, http.StatusOK, results[0], "First request should succeed")
	assert.Equal(t, 499, results[1], "Second request should be cancelled while queued")
	t.Logf("Results: %v", results)
}

func TestProxyRequest_ContextCancellation_DirectPath(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			Models:   []string{"test-model"},
		},
	}

	pm, err := NewPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		pm.ProxyRequest("test-model", w, req)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	wg.Wait()

	assert.Equal(t, 499, w.Code, "Expected 499 Client Closed Request when context cancelled during proxy")
}

func TestNewPeerProxy_CustomTimeouts(t *testing.T) {
	proxyURL, _ := url.Parse("http://localhost:8080")

	peers := config.PeerDictionaryConfig{
		"test-peer": config.PeerConfig{
			Proxy:    "http://localhost:8080",
			ProxyURL: proxyURL,
			Models:   []string{"model1"},
			Timeouts: config.TimeoutsConfig{
				Connect:        45,
				ResponseHeader: 300,
				TLSHandshake:   15,
				ExpectContinue: 2,
				IdleConn:       120,
			},
		},
	}

	peerProxy, err := NewPeerProxy(peers, false, testLogger)

	assert.NoError(t, err)
	assert.NotNil(t, peerProxy)
	assert.True(t, peerProxy.HasPeerModel("model1"))

	// Verify the timeout values are actually applied to the transport
	member, found := peerProxy.proxyMap["model1"]
	require.True(t, found, "model1 should exist in proxyMap")
	assert.NotNil(t, member.reverseProxy)
	assert.NotNil(t, member.reverseProxy.Transport)

	transport, ok := member.reverseProxy.Transport.(*http.Transport)
	require.True(t, ok, "Transport should be *http.Transport")

	// Verify all timeout values are correctly applied
	assert.Equal(t, 300*time.Second, transport.ResponseHeaderTimeout)
	assert.Equal(t, 15*time.Second, transport.TLSHandshakeTimeout)
	assert.Equal(t, 2*time.Second, transport.ExpectContinueTimeout)
	assert.Equal(t, 120*time.Second, transport.IdleConnTimeout)
	// ForceAttemptHTTP2 should be enabled
	assert.True(t, transport.ForceAttemptHTTP2)
}
