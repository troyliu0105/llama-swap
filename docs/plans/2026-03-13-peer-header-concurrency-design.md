# Peer Header Override and Concurrency Limit Design

**Date**: 2026-03-13  
**Status**: Approved  
**Author**: Sisyphus

## Overview

Add two new features to llama-swap's peer functionality:

1. **Header Override**: Allow users to add, override, or remove HTTP headers when forwarding requests to peers
2. **Concurrency Limit**: Add per-peer concurrency control with FIFO queuing for requests exceeding the limit

## Motivation

- **Header Override**: Some peer services require custom headers for authentication, routing, or metadata. Users need flexibility to modify headers without modifying the core code.
- **Concurrency Limit**: Prevents overwhelming downstream peer services and provides graceful degradation under load.

## Design Decisions

### Header Override

- **Empty string = delete**: Headers with empty string values will be removed from the request
- **No variable substitution**: Keep it simple, no `${VAR}` syntax support
- **Applied after API key injection**: Headers override any automatically injected headers

### Concurrency Limit

- **Per-peer isolation**: Each peer has its own concurrency counter and queue
- **FIFO queue**: Requests are processed in order they arrive
- **Queue full = 503**: When queue is full, return HTTP 503 Service Unavailable
- **Queue timeout**: Requests wait up to configured timeout, then return 503

## Configuration Schema

```yaml
peers:
  my-peer:
    proxy: http://example.com
    models: [model-a, model-b]
    apiKey: "${env.API_KEY}"  # existing
    filters: {}               # existing
    
    # NEW: Header overrides
    headers:
      X-Custom-Header: "custom-value"
      X-Remove-Me: ""        # empty string = delete this header
    
    # NEW: Concurrency control
    maxConcurrent: 5          # max concurrent requests (default: 0 = unlimited)
    queueSize: 32             # queue size for waiting requests (default: 32)
    queueTimeout: 60s         # max wait time in queue (default: 60s)
```

## Implementation Details

### Data Structures

**proxy/config/peer.go**:
```go
type PeerConfig struct {
    Proxy           string            `yaml:"proxy"`
    ProxyURL        *url.URL          `yaml:"-"`
    ApiKey          string            `yaml:"apiKey"`
    Models          []string          `yaml:"models"`
    Filters         Filters           `yaml:"filters"`
    Headers         map[string]string `yaml:"headers"`         // NEW
    MaxConcurrent   int               `yaml:"maxConcurrent"`   // NEW
    QueueSize       int               `yaml:"queueSize"`       // NEW
    QueueTimeout    time.Duration     `yaml:"queueTimeout"`    // NEW
}
```

**proxy/peerproxy.go**:
```go
type peerProxyMember struct {
    peerID          string
    reverseProxy    *httputil.ReverseProxy
    apiKey          string
    headers         map[string]string
    maxConcurrent   int
    queueSize       int
    queueTimeout    time.Duration
    
    // Concurrency control
    sem             chan struct{}     // semaphore for concurrency limit
    queue           chan *queuedRequest
}

type queuedRequest struct {
    writer  http.ResponseWriter
    request *http.Request
    done    chan struct{}
}
```

### Request Flow

1. **Header Application** (in `ProxyRequest`):
   ```
   1. Inject API key headers (existing)
   2. Apply custom headers (new):
      - If value != "": Set header
      - If value == "": Delete header
   ```

2. **Concurrency Control** (in `ProxyRequest`):
   ```
   1. Try to acquire semaphore (non-blocking)
   2. If acquired:
      - Process request
      - Release semaphore when done
   3. If not acquired:
      - Try to queue request (non-blocking)
      - If queue full: Return 503
      - If queued: Wait for timeout or processing
      - When dequeued: Process request
   ```

### Error Handling

- **Queue full**: HTTP 503 with body "peer concurrency queue full"
- **Queue timeout**: HTTP 503 with body "request timed out waiting in queue"
- **Both cases**: Log warning with peer ID

### Logging

New log messages:
- `[WARN] peer %s: queue full, rejecting request`
- `[WARN] peer %s: request timed out in queue`
- `[INFO] peer %s: request queued (queue length: %d)` (debug level)

## Testing Strategy

### Unit Tests

1. **Header Override Tests**:
   - Test adding new headers
   - Test overriding existing headers
   - Test deleting headers with empty string
   - Test header application order (after API key)

2. **Concurrency Limit Tests**:
   - Test maxConcurrent enforcement
   - Test FIFO ordering of queued requests
   - Test queue full returns 503
   - Test queue timeout
   - Test per-peer isolation (peer A's limit doesn't affect peer B)

### Integration Tests

- End-to-end test with concurrent requests to peer
- Verify headers are correctly forwarded to upstream

## Files to Modify

| File | Changes |
|------|---------|
| `proxy/config/peer.go` | Add new config fields |
| `proxy/peerproxy.go` | Implement header override and concurrency control |
| `proxy/peerproxy_test.go` | Add unit tests |
| `config.example.yaml` | Add configuration examples |
| `config-schema.json` | Update JSON schema |

## Backward Compatibility

- All new fields are optional with sensible defaults
- `maxConcurrent: 0` (default) means unlimited concurrency (backward compatible)
- Empty `headers` map means no header modifications

## Future Considerations

- Metrics: Queue depth histogram, wait time percentiles
- Variable substitution in headers if needed later
- Priority queuing (e.g., streaming vs non-streaming)
