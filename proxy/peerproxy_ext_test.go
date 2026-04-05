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
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

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

	req1 := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w1 := httptest.NewRecorder()
	start := time.Now()
	err = pm.ProxyRequest("test-model", w1, req1)
	require.NoError(t, err)
	firstReqTime := time.Since(start)

	req2 := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w2 := httptest.NewRecorder()
	start = time.Now()
	err = pm.ProxyRequest("test-model", w2, req2)
	require.NoError(t, err)
	secondReqTime := time.Since(start)

	assert.Less(t, firstReqTime, 50*time.Millisecond)
	assert.GreaterOrEqual(t, secondReqTime, 180*time.Millisecond)
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
