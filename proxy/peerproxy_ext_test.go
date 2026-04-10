package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/proxy/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnhancedPeerProxy_ConfigurableTimeout(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryExtConfig{
		"peer1": config.ExtendedPeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			Models:   []string{"test-model"},
			Timeout:  50 * time.Millisecond,
		},
	}

	pm, err := NewEnhancedPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	output := captureStdout(func() {
		err := pm.ProxyRequest("test-model", w, req)
		assert.NoError(t, err)
	})

	assert.Equal(t, http.StatusBadGateway, w.Code)
	assert.Contains(t, strings.ToLower(output), "timeout")
}

func TestEnhancedPeerProxy_DefaultTimeout(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryExtConfig{
		"peer1": config.ExtendedPeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			Models:   []string{"test-model"},
		},
	}

	pm, err := NewEnhancedPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("test-model", w, req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "success", w.Body.String())
}

func TestEnhancedPeerProxy_CustomHeaders(t *testing.T) {
	var receivedHeaders http.Header
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryExtConfig{
		"peer1": config.ExtendedPeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			Models:   []string{"test-model"},
			Headers: map[string]string{
				"X-Custom": "value",
				"X-Remove": "",
			},
		},
	}

	pm, err := NewEnhancedPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-Remove", "should-be-removed")
	req.Header.Set("X-Keep", "kept")
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("test-model", w, req)
	assert.NoError(t, err)
	assert.Equal(t, "value", receivedHeaders.Get("X-Custom"))
	assert.Empty(t, receivedHeaders.Get("X-Remove"))
	assert.Equal(t, "kept", receivedHeaders.Get("X-Keep"))
}

func TestEnhancedPeerProxy_StripV1Prefix(t *testing.T) {
	var receivedPath string
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryExtConfig{
		"peer1": config.ExtendedPeerConfig{
			Proxy:         testServer.URL,
			ProxyURL:      proxyURL,
			Models:        []string{"test-model"},
			StripV1Prefix: true,
		},
	}

	pm, err := NewEnhancedPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("test-model", w, req)
	assert.NoError(t, err)
	assert.Equal(t, "/chat/completions", receivedPath)
}

func TestEnhancedPeerProxy_PrefixPeerModels(t *testing.T) {
	proxyURL, _ := url.Parse("http://peer1.example.com:8080")
	peers := config.PeerDictionaryExtConfig{
		"peer1": config.ExtendedPeerConfig{
			Proxy:    "http://peer1.example.com:8080",
			ProxyURL: proxyURL,
			Models:   []string{"my-model"},
		},
	}

	pm, err := NewEnhancedPeerProxy(peers, true, testLogger)
	require.NoError(t, err)

	assert.True(t, pm.HasPeerModel("peer1/my-model"))
	assert.False(t, pm.HasPeerModel("my-model"))

	assert.Equal(t, "my-model", pm.GetOriginalModelName("peer1/my-model"))
}

func TestEnhancedPeerProxy_ConcurrencyControl(t *testing.T) {
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
	peers := config.PeerDictionaryExtConfig{
		"peer1": config.ExtendedPeerConfig{
			Proxy:         testServer.URL,
			ProxyURL:      proxyURL,
			Models:        []string{"test-model"},
			MaxConcurrent: 2,
			QueueSize:     5,
			QueueTimeout:  5 * time.Second,
		},
	}

	pm, err := NewEnhancedPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	var wg sync.WaitGroup
	var successCount int32
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			w := httptest.NewRecorder()
			pm.ProxyRequest("test-model", w, req)
			if w.Code == http.StatusOK {
				atomic.AddInt32(&successCount, 1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(5), atomic.LoadInt32(&successCount))
	assert.LessOrEqual(t, atomic.LoadInt32(&maxConcurrent), int32(2))
}

func TestEnhancedPeerProxy_RequestInterval(t *testing.T) {
	failCount := int32(0)
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&failCount, 1) <= 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryExtConfig{
		"peer1": config.ExtendedPeerConfig{
			Proxy:           testServer.URL,
			ProxyURL:        proxyURL,
			Models:          []string{"test-model"},
			RequestInterval: 400 * time.Millisecond,
		},
	}

	pm, err := NewEnhancedPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	// First request: no failure yet, should be instant
	req1 := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w1 := httptest.NewRecorder()
	start := time.Now()
	err = pm.ProxyRequest("test-model", w1, req1)
	require.NoError(t, err)
	firstReqTime := time.Since(start)
	assert.Less(t, firstReqTime, 50*time.Millisecond, "first request should have no delay")

	// Second request: after first failure (503), backoff kicks in
	req2 := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w2 := httptest.NewRecorder()
	start = time.Now()
	err = pm.ProxyRequest("test-model", w2, req2)
	require.NoError(t, err)
	secondReqTime := time.Since(start)
	assert.GreaterOrEqual(t, secondReqTime, 40*time.Millisecond,
		"second request should wait for backoff interval (requestInterval/8 = 50ms)")

	// Third request: backoff doubled
	req3 := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w3 := httptest.NewRecorder()
	start = time.Now()
	err = pm.ProxyRequest("test-model", w3, req3)
	require.NoError(t, err)
	thirdReqTime := time.Since(start)
	assert.GreaterOrEqual(t, thirdReqTime, 80*time.Millisecond,
		"third request should wait for doubled backoff")

	// Fourth request: succeeds, backoff starts decaying
	req4 := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w4 := httptest.NewRecorder()
	start = time.Now()
	err = pm.ProxyRequest("test-model", w4, req4)
	require.NoError(t, err)
	fourthReqTime := time.Since(start)
	assert.GreaterOrEqual(t, fourthReqTime, 80*time.Millisecond,
		"fourth request should wait for decayed interval")

	// Fifth request: succeeds again, interval shrinks further
	req5 := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w5 := httptest.NewRecorder()
	start = time.Now()
	err = pm.ProxyRequest("test-model", w5, req5)
	require.NoError(t, err)
	fifthReqTime := time.Since(start)
	assert.GreaterOrEqual(t, fifthReqTime, 30*time.Millisecond,
		"fifth request should wait for further decayed interval")

	// Sixth request: currentInterval=25ms, small wait. After this success: 25/2=12ms < 25ms threshold → resets to 0
	req6 := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w6 := httptest.NewRecorder()
	start = time.Now()
	err = pm.ProxyRequest("test-model", w6, req6)
	require.NoError(t, err)
	sixthReqTime := time.Since(start)
	assert.Less(t, sixthReqTime, 100*time.Millisecond,
		"sixth request should have no backoff delay (only HTTP round-trip time)")
}

func TestEnhancedPeerProxy_BackoffOnConnectionError(t *testing.T) {
	// Server that immediately rejects connections
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	testServer.Close() // close immediately so connections fail

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryExtConfig{
		"peer1": config.ExtendedPeerConfig{
			Proxy:           testServer.URL,
			ProxyURL:        proxyURL,
			Models:          []string{"test-model"},
			RequestInterval: 200 * time.Millisecond,
		},
	}

	pm, err := NewEnhancedPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	// First request: fails with connection error, backoff starts at requestInterval/8 = 25ms
	req1 := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w1 := httptest.NewRecorder()
	err = pm.ProxyRequest("test-model", w1, req1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadGateway, w1.Code)

	// Second request: should wait for backoff
	req2 := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w2 := httptest.NewRecorder()
	start := time.Now()
	pm.ProxyRequest("test-model", w2, req2)
	assert.GreaterOrEqual(t, time.Since(start), 20*time.Millisecond,
		"should wait for backoff after connection error")
}

func TestEnhancedPeerProxy_NoBackoffWithoutRequestInterval(t *testing.T) {
	failCount := int32(0)
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&failCount, 1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryExtConfig{
		"peer1": config.ExtendedPeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			Models:   []string{"test-model"},
			// No RequestInterval set
		},
	}

	pm, err := NewEnhancedPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	for i := 0; i < 4; i++ {
		req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		w := httptest.NewRecorder()
		start := time.Now()
		pm.ProxyRequest("test-model", w, req)
		assert.Less(t, time.Since(start), 20*time.Millisecond,
			"request %d should have no delay when requestInterval is not set", i+1)
	}
}

func TestEnhancedPeerProxy_QueueFull(t *testing.T) {
	blockChan := make(chan struct{})
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blockChan
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryExtConfig{
		"peer1": config.ExtendedPeerConfig{
			Proxy:         testServer.URL,
			ProxyURL:      proxyURL,
			Models:        []string{"test-model"},
			MaxConcurrent: 1,
			QueueSize:     0,
			QueueTimeout:  1 * time.Second,
		},
	}

	pm, err := NewEnhancedPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	var wg sync.WaitGroup
	results := make([]int, 2)
	var mu sync.Mutex

	for i := 0; i < 2; i++ {
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
	successCount := 0
	for _, code := range results {
		if code == http.StatusServiceUnavailable {
			serviceUnavailableCount++
		}
		if code == http.StatusOK {
			successCount++
		}
	}

	assert.Equal(t, 1, serviceUnavailableCount)
	assert.Equal(t, 1, successCount)
}
