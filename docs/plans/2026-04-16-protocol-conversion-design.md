# Protocol Conversion Design

**Date:** 2026-04-16
**Status:** Approved

## Overview

Add OpenAI → Anthropic protocol conversion to `EnhancedPeerProxy`. When `convertProtocol: true` is set on a peer, all requests are converted from OpenAI format to Anthropic format before forwarding, and all responses are converted back to OpenAI format.

## Scope

- **Direction:** OpenAI → Anthropic (request), Anthropic → OpenAI (response)
- **Coverage:** Complete — all commonly used parameters
- **Streaming:** Full SSE support with real-time chunk conversion
- **Implementation:** Single file (`peerproxy_ext.go`), struct-based conversion using `goccy/go-json`

## Architecture

### Peer Config Extension

```yaml
peers:
  anthropic-peer:
    proxy: "http://localhost:3001"
    models: ["claude-sonnet-4-20250514"]
    convertProtocol: true
```

### Conversion Pipeline

```
Client (OpenAI format)
    ↓
ProxyRequest: ConvertOpenAItoAnthropicReq()
    ↓
ReverseProxy → Anthropic peer
    ↓
ModifyResponse / sseConverterWriter: ConvertAnthropictoOpenAIResp()
    ↓
Client (OpenAI format)
```

### Key Components

1. **JSON Structs** (~200 lines): OpenAI and Anthropic request/response struct definitions
2. **Message Converter**: `convertMessagesOpenAIToAnthropic()` / `convertMessagesAnthropicToOpenAI()`
   - `system` message extraction/injection
   - `tool_calls` ↔ `tool_use` content blocks conversion
3. **SSE Converter**: `sseConverterWriter` wraps `http.ResponseWriter`
   - Parses SSE events, converts JSON data, rewrites as OpenAI SSE format
   - Handles `message_delta` + `message_stop` → OpenAI `usage` accumulation
4. **Parameter Mapping**: Hardcoded maps for field name translation

### Error Handling

- JSON parse failure → log error, pass through original data
- Unknown fields → forward unchanged
- Missing required fields → return 400 error
- SSE format anomaly → log warn, pass through original chunk

## Testing

- `TestProtocolConverter_RequestConversion` — JSON → JSON request conversion
- `TestProtocolConverter_ResponseConversion` — JSON → JSON response conversion
- `TestProtocolConverter_MessagesConversion` — messages array conversion
- `TestProtocolConverter_ToolCallsConversion` — tool_calls ↔ tool_use blocks
- `TestProtocolConverter_SystemMessageConversion` — system message extraction/injection
- `TestProtocolConverter_SSEStreaming` — SSE chunk conversion
- `TestProtocolConverter_EdgeCases` — empty body, invalid JSON, missing fields

## Files Modified

- `proxy/peerproxy_ext.go` — all new code, no new files
