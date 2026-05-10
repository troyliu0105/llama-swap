# proxy/ MODULE

Core proxy logic for llama-swap: model swapping, process management, request routing.

## OVERVIEW

Transparent proxy that auto-swaps AI model processes based on incoming OpenAI-compatible requests.

## STRUCTURE

```
proxy/
├── proxymanager.go       # Central orchestrator, Gin router
├── proxymanager_api.go   # API endpoints (/models, /running, /health)
├── proxymanager_loghandlers.go  # Log streaming endpoints
├── processgroup.go       # Group management (swap/persistent/exclusive)
├── process.go            # Individual process lifecycle
├── peerproxy.go          # Remote peer routing
├── config/               # Config parsing (see below)
├── events.go             # Event type definitions
├── logMonitor.go         # Log aggregation
├── metrics_monitor.go    # Request/response metrics capture
└── helpers_test.go       # TestMain + shared test utilities
```

## KEY TYPES

| Type            | File                | Purpose                                    |
| --------------- | ------------------- | ------------------------------------------ |
| `ProxyManager`  | proxymanager.go     | Main orchestrator, holds processGroups map |
| `ProcessGroup`  | processgroup.go     | Swap vs persistent mode, exclusive groups  |
| `Process`       | process.go          | Single model server lifecycle              |
| `PeerProxy`     | peerproxy.go        | Remote peer routing with queuing           |
| `Config`        | config/config.go    | YAML config structure                      |
| `Filters`       | config/filters.go   | Request param rewriting                    |

## REQUEST FLOW

```
HTTP Request → ProxyManager.ServeHTTP()
    ↓
Extract "model" param from body/path
    ↓
Find/swap ProcessGroup (swapProcessGroup)
    ↓
Stop competing groups (if exclusive)
    ↓
Start Process → wait for ready
    ↓
Process.ProxyRequest() → httputil.ReverseProxy → upstream server
    ↓
Response with filters applied
```

## MODEL SWAPPING

**Swap Mode (default):** One model per group. New request → stop current → start new.

**Persistent Mode:** Multiple models run simultaneously. No stopping.

**Exclusive Groups:** When activated, stops all non-persistent groups.

**Peer Mode:** Routes to remote servers instead of local processes.

## CONFIG PACKAGE

```
config/
├── config.go        # Config, ModelConfig, GroupConfig, Macros
├── model_config.go  # Model-specific settings
├── filters.go       # StripParams, SetParams, SetParamsByID
├── peer.go          # Peer server configuration
```

**Macros:** `${PORT}`, `${MODEL_ID}`, `${env.VAR}` - substituted at load time.

**Filters:** Rewrite request params. `"model"` param is protected (never removed).

## ANTI-PATTERNS

- `LogRequests` config deprecated → use `logLevel`
- ReverseProxy errors don't bubble (handle in ErrorHandler)
- `"model"` param cannot be stripped by filters

## TESTING

```bash
# Run specific test
go test -v -run TestProxyManager_SwapProcess ./proxy/...

# Dev cycle (cached)
make test-dev

# Full suite with race detection
make test-all
```

**Test Helpers:** `helpers_test.go` has `TestMain`, binary path setup, HTTP test utilities.

## NOTES

- Process states: `stopped → starting → ready → stopping → shutdown`
- Health checks use configured `healthcheck` endpoint or default path
- TTL-based auto-unload when configured
- Metrics captured from server logs + request/response inspection
