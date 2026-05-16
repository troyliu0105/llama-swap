# Persistent Usage Analytics & Request Capture Enhancement

Upstream llama-swap provides in-memory request metrics and request/response captures — data is lost on process restart, with no per-user token usage tracking.

This enhancement adds a **SQLite persistence layer** to llama-swap, enabling:

- **User Identity System** — per-API-key user identification with display names and hot-reload support
- **Token Usage Analytics** — per-user, per-model, per-period Input/Output/Cached token statistics
- **Persistent Request/Response Captures** — zstd + CBOR compressed storage in SQLite, surviving restarts
- **Activity Page Fix** — "View" button works correctly under audit mode
- **Session Grouping** — automatic conversation grouping via request fingerprinting
- **Data Retention Policy** — configurable expiry with automatic cascading cleanup
- **Audit UI Page** — web interface for usage analytics, user details, and capture listing

---

## Quick Start

Add the `audit` config block to `config.yaml` and set `name` on your API keys:

```yaml
apiKeys:
  my-key:
    key: "sk-1234567890abcdef"
    name: "Alice"               # user display name
  another-key:
    key: "sk-abcdef1234567890"
    name: "Bob"

audit:
  enabled: true
  database: "./data/audit.db"   # SQLite database path
  retentionDays: 90             # keep 90 days, 0 = keep forever
  captureFlushInterval: 30      # flush every 30 seconds
  captureFlushSize: 50          # flush when batch reaches 50
```

After starting llama-swap:

- **Activity page** (`/ui`) — each captured request shows a "View" button; click to inspect full request/response content
- **Audit page** (`/ui/audit`) — view per-user, per-model, per-period token usage and capture listings

> **Prerequisite**: Set `name` on each API key. Without it, the UI shows the masked API key instead of a username.

---

## Feature Details

### 1. User Identity System

Upstream `apiKeys` only handles access control — validating whether a request is authorized, not *who* is making it.

This enhancement adds a `name` field to the dictionary-format `apiKeys`, mapping API keys to user identities:

```yaml
apiKeys:
  alice-key:
    key: "sk-alice-secret-key"
    name: "Alice"
  bob-key:
    key: "sk-bob-secret-key"
    name: "Bob"
    models: ["model-a"]          # existing model access restriction unchanged
```

Behavior:

- **Auto-registration** — the first request from an API key creates a user record in the database
- **Hot-reload** — changing `name` and reloading config (SIGHUP) updates the username on existing records
- **Per-request attribution** — each request carries the current `name` into the database, ensuring historical traceability
- **API key masking** — all API responses show only the first 8 characters of the key

### 2. Token Usage Analytics

Each request with token consumption generates a log record containing:

| Field | Description |
|---|---|
| `input_tokens` | Input token count |
| `output_tokens` | Output token count |
| `cached_tokens` | Cache-hit token count |
| `model` | Model name used |
| `req_path` | Request path (e.g. `/v1/chat/completions`) |
| `status_code` | HTTP status code |
| `duration_ms` | Request duration in milliseconds |
| `tokens_per_second` | Generation speed |
| `prompt_per_second` | Prompt processing speed |

Supported endpoints:

- `/v1/chat/completions` (OpenAI)
- `/v1/responses` (OpenAI)
- `/v1/messages` (Anthropic)
- `/v1/completions` (OpenAI legacy)

> Only requests with token consumption or non-200 responses are recorded. Health checks, model listings, etc. are skipped.

### 3. Persistent Request/Response Captures

Upstream stores captures in an optional in-memory cache (`captureBuffer`), lost on restart.

This enhancement persists captures to SQLite:

- **Storage format** — CBOR serialization + zstd compression, compact and efficient
- **Capture contents** — request headers, request body, response headers, response body (configurable per route)
- **Relational linking** — each capture links to its request log via `request_log_id`
- **Activity page integration** — when enabled, the Activity page "View" button reads from SQLite instead of memory

Captures are automatically enabled for these endpoints (no additional `captureBuffer` config needed):

- `/v1/chat/completions`
- `/v1/responses`
- `/v1/messages`
- `/v1/completions`
- `/v1/embeddings`
- `/v1/audio/speech`
- `/v1/audio/transcriptions`
- `/v1/images/generations`
- `/v1/images/edits`

### 4. Activity Page Fix

The upstream Activity page resolves `HasCapture` from an in-memory `capturedIDs` map. Under audit mode, captures are stored in SQLite — not the in-memory map — causing `HasCapture` to remain `false`. The "View" button doesn't appear (shows `-` instead).

This enhancement fixes the issue: when the audit store captures data, it also writes to `capturedIDs`, ensuring the Activity page correctly shows the "View" button.

### 5. Session Grouping

For chat endpoints (`/v1/chat/completions`, `/v1/responses`, `/v1/messages`), this enhancement computes a fingerprint from the request content, automatically grouping requests from the same conversation into a session.

- **Fingerprint computation** — based on request path and `messages` field content
- **Session deduplication** — the `(user_id, fingerprint)` pair maps to one session
- **Non-chat endpoints** — each request gets a random fingerprint and is stored independently

Session grouping is used to organize captures in the Audit page.

### 6. Data Retention Policy

When `retentionDays` is set, a background goroutine performs hourly cleanup:

1. Deletes `request_log` records older than the retention period
2. Cascading deletes orphaned `captures`
3. Cascading deletes orphaned `sessions`
4. Cascading deletes orphaned `users`

All cleanup runs inside a transaction for data integrity. `retentionDays: 0` means keep forever.

---

## Configuration

### `audit` block

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable persistence features |
| `database` | string | — | SQLite database file path; parent directories are auto-created |
| `retentionDays` | int | `0` (forever) | Days to retain data; expired records are automatically purged |
| `captureFlushInterval` | int | `30` (seconds) | Time interval for batch-writing capture data |
| `captureFlushSize` | int | `50` | Maximum batch size; flushes immediately when reached |

### `apiKeys` `name` field

| Field | Type | Description |
|---|---|---|
| `name` | string | User display name, used in usage analytics and the Audit page |

```yaml
apiKeys:
  alice-key:
    key: "sk-alice-secret-key"
    name: "Alice"          # new field
  bob-key:
    key: "sk-bob-secret-key"
    name: "Bob"
    models: ["model-a"]    # existing model restriction unaffected
```

---

## Architecture

### Data Flow

```
HTTP Request
  ↓
ProxyManager.wrapHandler()
  ├── Parse token usage (Input/Output/Cached)
  ├── Capture request/response data (headers + body)
  ├── Compute session fingerprint (chat endpoints)
  ├── Set HasCapture → Activity page shows "View"
  └── Enqueue to persistence channel (buffered, capacity 1024)
        ↓
Writer Goroutine (single writer)
  ├── Transaction 1: INSERT users + request_log
  └── Transaction 2: Batch INSERT sessions + captures
        ↓
SQLite Database (WAL mode)
  ↓
REST API ← queries
Web UI  ← display
```

### Core Components

| Component | File | Responsibility |
|---|---|---|
| AuditStore | `internal/audit/store.go` | Database lifecycle, write channel, drain on close |
| Writer | `internal/audit/writer.go` | Async event consumption, transactional writes, capture batch flush |
| Session | `internal/audit/session.go` | Fingerprint computation, chat endpoint detection |
| Queries | `internal/audit/queries.go` | User listing, usage statistics, capture queries |
| Retention | `internal/audit/retention.go` | Periodic expired data cleanup (cascading deletes) |
| Migrations | `internal/audit/migrations/` | Schema version management (golang-migrate) |

### Database Schema

```sql
-- Users (deduplicated by API key, ON CONFLICT updates name)
users (id, api_key [UNIQUE], name, created_at)

-- Request log (one row per request with token usage or non-200 status)
request_log (id, metric_id, user_id [FK], model, req_path,
             status_code, input_tokens, output_tokens, cached_tokens,
             duration_ms, tokens_per_second, prompt_per_second, created_at)

-- Sessions (deduplicated by user_id + fingerprint)
sessions (id, user_id [FK], model, fingerprint, req_path,
          created_at, updated_at, UNIQUE(user_id, fingerprint))

-- Captures (full request/response per request, zstd+CBOR compressed BLOB)
captures (id, session_id [FK], request_log_id [FK], data [BLOB], created_at)
```

### Write Strategy

- **Single writer goroutine** — all database writes are serialized through one goroutine, avoiding lock contention
- **Bounded channel** — write channel capacity 1024; drops events under backpressure instead of blocking proxy requests
- **Batch flush** — captures are committed when `captureFlushSize` (default 50) is reached or `captureFlushInterval` (default 30s) elapses
- **Transactional consistency** — `ensureUser` + `insertRequestLog` execute in a single transaction
- **Graceful shutdown** — on process exit, the writer drains all pending events from the channel and flushes before closing the database

---

## REST API

All `/api/audit/*` endpoints are registered when `audit.enabled: true` and are protected by API key authentication. The existing `/api/captures/:id` endpoint behavior changes (prefers SQLite).

### List Users

```
GET /api/audit/users
```

Response:
```json
[
  {
    "id": 1,
    "api_key": "sk-12345...",
    "name": "Alice",
    "created_at": "2025-01-01T00:00:00.000Z",
    "total_requests": 42,
    "last_request_at": "2025-06-01T12:00:00.000Z"
  }
]
```

### Usage Overview

```
GET /api/audit/usage?period=24h
```

| Param | Values | Default |
|---|---|---|
| `period` | `24h` / `7d` / `30d` | `24h` |

Response:
```json
[
  {
    "user_id": 1,
    "name": "Alice",
    "input_tokens": 15000,
    "output_tokens": 8000,
    "cached_tokens": 3000
  }
]
```

### Per-User Model Usage

```
GET /api/audit/usage/:user_id?period=7d
```

Response:
```json
[
  {
    "model": "qwen3-30b-a3b",
    "input_tokens": 10000,
    "output_tokens": 5000,
    "cached_tokens": 2000,
    "request_count": 28
  }
]
```

### User Capture Listing

```
GET /api/audit/captures?user_id=1&limit=20&offset=0
```

| Param | Default | Description |
|---|---|---|
| `user_id` | required | User ID |
| `limit` | `50` | Page size |
| `offset` | `0` | Pagination offset |

Response:
```json
[
  {
    "id": 101,
    "session_id": 5,
    "model": "qwen3-30b-a3b",
    "req_path": "/v1/chat/completions",
    "created_at": "2025-06-01T12:00:00.000Z"
  }
]
```

### Capture Detail (existing endpoint, enhanced)

```
GET /api/captures/:id
```

`:id` is the metric ID from the Activity page.

Behavior change:

| Condition | Data Source |
|---|---|
| `audit.enabled: true` | Queried from SQLite via `GetCaptureByMetricID` |
| `audit.enabled: false` | Queried from in-memory cache (upstream behavior unchanged) |

Response: full `ReqRespCapture` JSON (request headers, request body, response headers, response body).

---

## Web UI

### Activity Page (`/ui`)

- No new features, but fixes the "View" button not appearing under audit mode
- Clicking "View" opens a capture detail dialog with Pretty/Raw/Chat view modes

### Audit Page (`/ui/audit`)

New page, accessible from the navigation bar:

- **Usage overview table** — per-user token usage (Input/Output/Cached), switchable between `24h` / `7d` / `30d` periods
- **User detail panel** — click a user row to expand per-model usage breakdown (token counts + request count)
- **Capture listing** — lists all request captures for the selected user with paginated "Load More"
- **Key masking** — API keys are automatically masked (first 4 chars + `...` + last 4 chars)

> When audit is not enabled, the page shows an "Audit Not Enabled" message.

---

## Comparison with Upstream

| Capability | Upstream | This Enhancement |
|---|---|---|
| Request metrics storage | In-memory ring buffer, lost on restart | SQLite persistent, survives restarts |
| Request/response captures | Optional in-memory cache (`captureBuffer`), lost on restart | SQLite persistent (zstd + CBOR), survives restarts |
| `/api/captures/:id` | Queries in-memory cache | Prefers SQLite, falls back to memory when audit is off |
| User identity | API keys for access control only | Per-key user identification with display names and hot-reload |
| Token usage statistics | Real-time display only, no history | Per-user/model/period persistent analytics |
| Session grouping | None | Fingerprint-based automatic chat conversation grouping |
| Data expiry cleanup | None | Configurable retention days with automatic cascading purge |
| Activity page | Real-time metrics + in-memory captures | Fixed: "View" button works correctly under audit mode |
| Audit page | None | New `/ui/audit` page for historical usage and captures |

### New Dependencies

| Dependency | Purpose | Status |
|---|---|---|
| `modernc.org/sqlite` | SQLite driver (pure Go, no CGo) | **Newly introduced** |
| `github.com/golang-migrate/migrate/v4` | Schema migration management | **Newly introduced** |
| `github.com/fxamacker/cbor/v2` | CBOR serialization | Already used upstream (in-memory captures) |
| `github.com/klauspost/compress` | zstd compression | Already used upstream (in-memory captures) |
| `github.com/tidwall/gjson` | JSON path queries | Already used upstream (metrics parsing) |

> Only `modernc.org/sqlite` and `golang-migrate` are newly introduced. The others already exist upstream for in-memory capture and metrics parsing.

---

## Notes

1. **Database file location** — ensure the `database` path directory is writable. Parent directories are auto-created.
2. **Performance impact** — all writes are asynchronous via a buffered channel and never block proxy requests. Events are dropped (not blocked) when the channel is full.
3. **Disk space** — captured request/response data is zstd-compressed but still consumes disk. Set a reasonable `retentionDays`.
4. **No CGo dependency** — uses `modernc.org/sqlite` (pure Go), no C toolchain required for compilation. Cross-compilation is straightforward.
5. **Schema migrations** — database schema is managed by golang-migrate. New migrations are applied automatically on upgrade.
6. **Config hot-reload** — after changing `name` in `apiKeys`, send SIGHUP or trigger a config reload. Usernames take effect immediately on subsequent requests.
