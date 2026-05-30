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

Status in this matrix is for the declared conversion scope on this page.

| Source | Target | Request | Response | Streaming | Status |
|---|---|---|---|---|---|
| Chat Completions | Responses | Yes | Yes | Yes | Full (Scoped) |
| Responses | Chat Completions | Yes | Yes | Yes | Full (Scoped) |
| Chat Completions | Messages | Yes | Yes | Yes | Full (Scoped) |
| Messages | Chat Completions | Yes | Yes | Yes | Full (Scoped) |
| Responses | Messages | Yes | Yes | Yes | Full (Scoped) |
| Messages | Responses | Yes | Yes | Yes | Full (Scoped) |

## What Works Well

- Standard text chat request/response conversion
- Common sampling and control fields
- Deprecated OpenAI Chat `functions` / `function_call` request compatibility on the supported request paths
- Ordered aggregation of multiple OpenAI `system` / `developer` instruction messages on the supported request paths
- Function tool definitions and tool-choice mapping on the supported paths
- Explicit cross-protocol rejection for non-portable or provider-native tool families that cannot be represented safely
- Explicit cross-protocol rejection for unsupported stateful or provider-specific request fields that cannot be represented safely on stateless targets
- Explicit cross-protocol rejection for unsupported Responses request controls such as `text.verbosity` when no safe target equivalent exists
- Explicit validation and rejection for unsupported structured-output request shapes when a cross-protocol target only supports a narrower `response_format` / `text.format` subset
- Assistant tool-call / tool-use conversion on the common request and response shapes
- Portable file/document request conversion between OpenAI/Responses file parts and Anthropic document blocks
- Preservation of additional portable OpenAI Chat request controls on the Chat → Responses path, including penalties, seed, and logprobs flags
- Explicit rejection for additional OpenAI Chat request families that do not have safe cross-protocol equivalents yet, including multi-choice `n > 1`, `logit_bias`, `prediction`, top-level `audio` request configuration, and Anthropic-incompatible sampling/debug controls on the Chat → Messages path
- Explicit rejection for multi-choice OpenAI Chat completion responses on Responses/Anthropic targets when more than one `choice` would otherwise be collapsed silently
- Streamed text deltas
- Streamed refusal deltas on the supported Responses → Chat path
- Streamed reasoning/thinking delta conversion on the supported paths; non-streaming `thinking` / `redacted_thinking` response blocks are rejected explicitly when no safe target representation exists
- Usage conversion for the supported non-streaming and streaming shapes
- Terminal state mapping for common cases such as normal stop, token-limit stop, tool-call stop, and content filtering
- Non-streaming refusal preservation across Chat Completions, Responses, and Messages conversions

## Scope Exclusions (Intentional)

### 1. Protocol-complete provider parity

This page does not claim protocol-complete parity for every provider-native family. It defines a conversion contract: preserve portable conversational families and reject non-portable families explicitly.

### 2. Built-in/native hosted tool ecosystems

- Responses-native hosted tools are not converted into Chat Completions or Messages equivalents.
- Anthropic-provided built-in tool families are not converted into OpenAI protocol equivalents.
- These families are intentionally out of the scoped-full conversion contract and rejected explicitly on incompatible paths.

### 3. Non-portable typed content families

Multimodal and long-tail typed content families without safe equivalents are out of the scoped-full contract. Unsupported or malformed block families are rejected explicitly instead of degrading to a lossy fallback or disappearing silently.

### 4. Long-tail stream semantics are not fully preserved

When a stream event family has no valid equivalent in the target protocol, llama-swap now uses explicit fail-fast rejection for known semantic families (for example Responses reasoning-summary/annotation families and Anthropic citations/signature/error/server-tool families on OpenAI-target conversion paths). Unknown event types may still be dropped defensively rather than forwarding mixed-protocol SSE payloads.

### 5. Non-lossless semantic output families

Some Responses output item types, status-rich response states, message-content subparts, and some Anthropic extension fields do not have exact equivalents in the other protocols. In this scoped-full contract, when a safe representation is unavailable, the converter fails explicitly instead of silently dropping or normalizing data.

### 6. `messages/count_tokens` is out of scope

`/v1/messages/count_tokens` is intentionally not treated as normal Messages conversion traffic.

## Detailed TODO Checklist

For the code-backed checklist that defines and verifies this scoped-full conversion contract, see [`conversion-todos.md`](./conversion-todos.md).

In short:

- The main conversational conversion paths between Chat Completions, Responses, and Messages are implemented and tested.
- Provider-native ecosystems and other non-portable families are intentionally excluded from this scoped-full contract and handled via explicit rejection policy.
- The support matrix above should be read as **Full (Scoped)** for the declared conversion contract on this page.

## Recommended Reading Order

- Start here if you want the high-level support picture.
- Read [chat-completions.md](./chat-completions.md) for OpenAI Chat conversion details.
- Read [responses.md](./responses.md) for Responses-specific caveats, especially around hosted tools and output items.
- Read [messages.md](./messages.md) for Anthropic-specific caveats, especially around built-in tools, typed blocks, and `count_tokens`.
