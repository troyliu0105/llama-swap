# Protocol Conversion Support Overview

Overview of the current cross-protocol conversion support in llama-swap.

This page summarizes what the convert layer supports today when a peer model is configured with `upstreamFormat`. It describes the **current implementation status**, not the full theoretical capability of the OpenAI or Anthropic APIs.

For full protocol references, see:

- [Chat Completions](./chat-completions.md)
- [Responses](./responses.md)
- [Messages](./messages.md)

## Scope

The convert layer is focused on these protocol families:

- OpenAI Chat Completions: `/v1/chat/completions`
- OpenAI Responses: `/v1/responses`
- Anthropic Messages: `/v1/messages`

It is **not** a general conversion layer for unrelated endpoints such as embeddings, images, audio, rerank, or `messages/count_tokens`.

## Support Matrix

| Source | Target | Request | Response | Streaming | Status |
|---|---|---|---|---|---|
| Chat Completions | Responses | Yes | Yes | Yes | Partial |
| Responses | Chat Completions | Yes | Yes | Yes | Partial |
| Chat Completions | Messages | Yes | Yes | Yes | Partial |
| Messages | Chat Completions | Yes | Yes | Yes | Partial |
| Responses | Messages | Yes | Yes | Yes | Partial |
| Messages | Responses | Yes | Yes | Yes | Partial |

## What Works Well

- Standard text chat request/response conversion
- Common sampling and control fields
- Function tool definitions and tool-choice mapping on the supported paths
- Assistant tool-call / tool-use conversion on the common request and response shapes
- Streamed text deltas
- Streamed reasoning/thinking conversion on the supported paths
- Usage conversion for the supported non-streaming and streaming shapes
- Terminal state mapping for common cases such as normal stop, token-limit stop, tool-call stop, and content filtering

## Current Limitations

### 1. Support is partial, not protocol-complete

All protocol pairs above are marked **Partial** because the conversion layer covers the main conversational paths, but not every field, event family, or feature from the source protocols.

### 2. Built-in tool ecosystems are not fully bridged

- Responses-native hosted tools are not converted into Chat Completions or Messages equivalents.
- Anthropic-provided built-in tool families are not converted into OpenAI protocol equivalents.

### 3. Some typed content families are still incomplete

Multimodal and long-tail typed content support is not fully symmetrical across all protocol pairs. Unsupported block types may fail conversion instead of degrading to a lossy fallback.

### 4. Unknown stream events are dropped intentionally

When a stream event has no valid equivalent in the target protocol, llama-swap drops it rather than forwarding mixed-protocol SSE payloads. This avoids corrupt or misleading event streams, but it also means long-tail semantic events are not preserved.

### 5. Not every semantic output shape is lossless

Some Responses output item types and some Anthropic extension fields do not have exact equivalents in the other protocols, so conversion is safe but not fully lossless.

### 6. `messages/count_tokens` is out of scope

`/v1/messages/count_tokens` is intentionally not treated as normal Messages conversion traffic.

## Recommended Reading Order

- Start here if you want the high-level support picture.
- Read [chat-completions.md](./chat-completions.md) for OpenAI Chat conversion details.
- Read [responses.md](./responses.md) for Responses-specific caveats, especially around hosted tools and output items.
- Read [messages.md](./messages.md) for Anthropic-specific caveats, especially around built-in tools, typed blocks, and `count_tokens`.
