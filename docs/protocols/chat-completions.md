# Chat Completions Protocol

Reference for the OpenAI `/v1/chat/completions` endpoint. Covers the full request and response schema, streaming behavior, tool calling, usage tracking, and compatibility notes for llama.cpp and llama-swap.

## Endpoint

| Field | Value |
|-------|-------|
| Method | `POST` |
| Path | `/v1/chat/completions` |
| Content-Type | `application/json` |
| Authentication | `Authorization: Bearer <key>` (when API keys are configured) |

This is the primary chat endpoint. It accepts a list of messages and returns a model-generated response. Supports text, images, audio, tool calls, and structured output.

## Request Body

### Required Fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `model` | string | Yes | | Model ID to use. Maps to a configured model in llama-swap. |
| `messages` | array | Yes | | Ordered list of message objects. See Message Types below. |

### Optional Fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `max_completion_tokens` | integer \| null | No | | Upper bound on total output tokens, including reasoning tokens. Preferred over `max_tokens`. |
| `max_tokens` | integer \| null | No | | Deprecated. Use `max_completion_tokens` instead. |
| `temperature` | number \| null | No | `1` | Sampling randomness. Range 0 to 2. Higher values produce more varied output. |
| `top_p` | number \| null | No | `1` | Nucleus sampling threshold. The model considers tokens with top_p probability mass. Range 0 to 1. |
| `n` | integer \| null | No | `1` | Number of chat completion choices to generate. |
| `stream` | boolean \| null | No | `false` | When true, sends partial message deltas as SSE chunks. |
| `stream_options` | object \| null | No | `null` | Streaming options. See Streaming SSE section. |
| `logprobs` | boolean \| null | No | `false` | Whether to return log probabilities of output tokens. |
| `top_logprobs` | integer \| null | No | | Number of top log probabilities to return per token. Range 0 to 20. Requires `logprobs: true`. |
| `response_format` | object | No | | Output format control. Supports `text`, `json_object`, and `json_schema` types. |
| `stop` | string \| string[] \| null | No | `null` | Up to 4 sequences where the API stops generating further tokens. |
| `presence_penalty` | number \| null | No | `0` | Penalize new tokens based on whether they appear in the text so far. Range -2 to 2. |
| `frequency_penalty` | number \| null | No | `0` | Penalize new tokens based on their frequency in the text so far. Range -2 to 2. |
| `logit_bias` | object \| null | No | `null` | Map of token IDs to bias values (-100 to 100). Use to encourage or discourage specific tokens. |
| `seed` | number \| null | No | | Deprecated. Was used for deterministic sampling. |
| `tools` | array | No | | List of tool definitions the model may call. See Tool Calling section. |
| `tool_choice` | string \| object | No | `"auto"` | Controls tool use: `"none"`, `"auto"`, `"required"`, or `{"type":"function","function":{"name":"..."}}` for a specific tool. |
| `functions` | array | No | | Deprecated. Use `tools` instead. |
| `function_call` | string \| object | No | | Deprecated. Use `tool_choice` instead. |
| `parallel_tool_calls` | boolean | No | `true` | Whether to allow parallel function calls. |
| `service_tier` | enum \| null | No | `"auto"` | Processing priority: `"auto"`, `"default"`, `"flex"`, `"scale"`, or `"priority"`. |
| `user` | string | No | | Deprecated. Use `safety_identifier` instead. Was a stable identifier for the end user. |

## Message Types

Each message object has a `role` field that determines its structure. Here are the supported roles.

### system

Sets the behavior and persona for the assistant. Typically sent as the first message.

```json
{
  "role": "system",
  "content": "You are a helpful assistant.",
  "name": "optional-identifier"
}
```

The `content` field accepts either a string or an array of text content parts.

### developer

A newer alternative to `system`. Provides developer-level instructions that take higher priority than `system` messages.

```json
{
  "role": "developer",
  "content": "Always respond in JSON format.",
  "name": "optional-identifier"
}
```

### user

Input from the end user. The `content` field can be a plain string or an array of content parts for multimodal input.

```json
{
  "role": "user",
  "content": "What is the weather in Paris?",
  "name": "optional-identifier"
}
```

With multimodal content parts:

```json
{
  "role": "user",
  "content": [
    { "type": "text", "text": "Describe this image." },
    { "type": "image_url", "image_url": { "url": "data:image/png;base64,...", "detail": "auto" } }
  ]
}
```

### assistant

A response from the model. Can contain text content, tool calls, or both. The `content` field may be `null` when tool calls are present.

```json
{
  "role": "assistant",
  "content": "The weather in Paris is sunny.",
  "tool_calls": null,
  "refusal": null,
  "name": "optional-identifier"
}
```

With tool calls:

```json
{
  "role": "assistant",
  "content": null,
  "tool_calls": [
    {
      "id": "call_abc123",
      "type": "function",
      "function": {
        "name": "get_weather",
        "arguments": "{\"location\": \"Paris\"}"
      }
    }
  ],
  "refusal": null
}
```

### tool

The result of a tool call. Each tool result must include a `tool_call_id` that matches the corresponding `tool_calls[].id` from the assistant message.

```json
{
  "role": "tool",
  "content": "{\"temperature\": 22, \"condition\": \"sunny\"}",
  "tool_call_id": "call_abc123"
}
```

## Content Part Types

When a message `content` field is an array, each element is a content part with a `type` field.

### text

Plain text content.

```json
{ "type": "text", "text": "Describe what you see." }
```

### image_url

An image supplied via URL or base64 data URI.

```json
{
  "type": "image_url",
  "image_url": {
    "url": "https://example.com/photo.jpg",
    "detail": "auto"
  }
}
```

The `detail` field controls resolution: `"auto"` (server decides), `"low"`, or `"high"`.

For base64 images, encode the image as a data URI:

```json
{
  "type": "image_url",
  "image_url": {
    "url": "data:image/png;base64,iVBORw0KGgo..."
  }
}
```

### input_audio

Audio input for multimodal models.

```json
{
  "type": "input_audio",
  "input_audio": {
    "data": "base64-encoded-audio-data",
    "format": "wav"
  }
}
```

The `format` field can be `"wav"`, `"mp3"`, `"aac"`, or `"opus"`.

### file

A file attachment for models that support file input.

```json
{
  "type": "file",
  "file": {
    "file_data": "base64-encoded-file-data",
    "file_id": "file-abc123",
    "filename": "document.pdf"
  }
}
```

## Response Object

The API returns a chat completion object with the model's output.

```json
{
  "id": "chatcmpl-abc123",
  "object": "chat.completion",
  "created": 1677652288,
  "model": "gpt-4o",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "Hello! How can I help you today?",
        "refusal": null,
        "tool_calls": null
      },
      "finish_reason": "stop",
      "logprobs": null
    }
  ],
  "usage": {
    "prompt_tokens": 9,
    "completion_tokens": 12,
    "total_tokens": 21,
    "prompt_tokens_details": {
      "cached_tokens": 0,
      "audio_tokens": 0
    },
    "completion_tokens_details": {
      "reasoning_tokens": 0,
      "audio_tokens": 0,
      "accepted_prediction_tokens": 0,
      "rejected_prediction_tokens": 0
    }
  },
  "service_tier": "auto",
  "system_fingerprint": "fp_4afc0959c4"
}
```

### Response Fields

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Unique identifier for the completion. |
| `object` | string | Always `"chat.completion"`. |
| `created` | integer | Unix timestamp of when the completion was created. |
| `model` | string | Model ID used for the completion. |
| `choices` | array | List of completion choices. Length matches the `n` request parameter. |
| `usage` | object | Token usage statistics. See Usage Tracking section. |
| `service_tier` | string | Processing tier used. |
| `system_fingerprint` | string | Backend configuration fingerprint. |

### finish_reason Values

| Value | Meaning |
|-------|---------|
| `"stop"` | Model finished naturally or hit a stop sequence. |
| `"length"` | Hit the maximum token limit. |
| `"tool_calls"` | Model decided to call one or more tools. |
| `"content_filter"` | Output was filtered due to content policy. |
| `"function_call"` | Deprecated. Model called a function (use `tool_calls` instead). |

## Streaming SSE

When `stream: true` is set, the response is delivered as a series of Server-Sent Events. Each event is prefixed with `data: ` and chunks are separated by blank lines.

### Chunk Schema

```json
{
  "id": "chatcmpl-abc123",
  "object": "chat.completion.chunk",
  "created": 1677652288,
  "model": "gpt-4o",
  "choices": [
    {
      "index": 0,
      "delta": {
        "role": "assistant",
        "content": "Hello"
      },
      "finish_reason": null,
      "logprobs": null
    }
  ],
  "usage": null
}
```

The `delta` field contains incremental content. The first chunk typically includes `{"role": "assistant"}`. Subsequent chunks contain content fragments. The final chunk has a non-null `finish_reason`.

### Stream Lifecycle

```
data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"!"},"finish_reason":null}]}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]
```

The stream always ends with `data: [DONE]`.

### stream_options

```json
{
  "stream": true,
  "stream_options": {
    "include_usage": true
  }
}
```

When `include_usage` is true, the final chunk before `[DONE]` contains full token usage statistics. This chunk has an empty `choices` array:

```json
{
  "id": "chatcmpl-abc123",
  "object": "chat.completion.chunk",
  "created": 1677652288,
  "model": "gpt-4o",
  "choices": [],
  "usage": {
    "prompt_tokens": 9,
    "completion_tokens": 12,
    "total_tokens": 21,
    "prompt_tokens_details": {
      "cached_tokens": 0,
      "audio_tokens": 0
    },
    "completion_tokens_details": {
      "reasoning_tokens": 0,
      "audio_tokens": 0,
      "accepted_prediction_tokens": 0,
      "rejected_prediction_tokens": 0
    }
  }
}
```

## Tool Calling

Tool calling lets the model request function execution during a conversation. The flow has three parts: defining tools in the request, receiving tool call instructions in the response, and sending tool results back.

### Defining Tools in the Request

Pass tool definitions in the `tools` array:

```json
{
  "model": "gpt-4o",
  "messages": [
    { "role": "user", "content": "What is the weather in Paris?" }
  ],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "get_weather",
        "description": "Get the current weather in a given location",
        "parameters": {
          "type": "object",
          "properties": {
            "location": {
              "type": "string",
              "description": "City name, e.g. Paris"
            },
            "unit": {
              "type": "string",
              "enum": ["celsius", "fahrenheit"]
            }
          },
          "required": ["location"]
        },
        "strict": true
      }
    }
  ],
  "tool_choice": "auto"
}
```

When `strict` is true, the model guarantees the generated arguments match the schema exactly.

### Receiving Tool Calls in the Response

When the model decides to call a tool, the response includes `tool_calls` in the assistant message:

```json
{
  "id": "chatcmpl-abc123",
  "object": "chat.completion",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": null,
        "tool_calls": [
          {
            "id": "call_abc123",
            "type": "function",
            "function": {
              "name": "get_weather",
              "arguments": "{\"location\": \"Paris\", \"unit\": \"celsius\"}"
            }
          }
        ]
      },
      "finish_reason": "tool_calls"
    }
  ]
}
```

The `arguments` field is a JSON-encoded string, not a parsed object.

### Sending Tool Results Back

After executing the tool, send the result as a message with `role: "tool"`:

```json
{
  "model": "gpt-4o",
  "messages": [
    { "role": "user", "content": "What is the weather in Paris?" },
    {
      "role": "assistant",
      "content": null,
      "tool_calls": [
        {
          "id": "call_abc123",
          "type": "function",
          "function": {
            "name": "get_weather",
            "arguments": "{\"location\": \"Paris\", \"unit\": \"celsius\"}"
          }
        }
      ]
    },
    {
      "role": "tool",
      "content": "{\"temperature\": 22, \"condition\": \"sunny\"}",
      "tool_call_id": "call_abc123"
    }
  ]
}
```

The `tool_call_id` must match the `id` from the corresponding tool call. The model then uses the tool result to generate its final response.

## Usage Tracking

The `usage` object in the response provides token consumption details.

### Top-Level Fields

| Field | Type | Description |
|-------|------|-------------|
| `prompt_tokens` | integer | Total tokens in the prompt, including tool definitions and conversation history. |
| `completion_tokens` | integer | Tokens generated by the model. |
| `total_tokens` | integer | Sum of prompt and completion tokens. |

### prompt_tokens_details

Breakdown of prompt token usage.

| Field | Type | Description |
|-------|------|-------------|
| `cached_tokens` | integer | Tokens served from the prompt cache. Reduce latency and cost. |
| `audio_tokens` | integer | Audio tokens included in the prompt. |

### completion_tokens_details

Breakdown of completion token usage.

| Field | Type | Description |
|-------|------|-------------|
| `reasoning_tokens` | integer | Tokens used for internal reasoning by reasoning models (e.g. o1). |
| `audio_tokens` | integer | Audio tokens generated in the completion. |
| `accepted_prediction_tokens` | integer | Tokens from speculative decoding that were accepted. |
| `rejected_prediction_tokens` | integer | Tokens from speculative decoding that were rejected. |

## llama.cpp Compatibility Notes

llama.cpp's server (`llama-server`) implements the `/v1/chat/completions` endpoint with some differences from the OpenAI API.

### Supported Features

- Both `/v1/chat/completions` and `/chat/completions` paths work.
- `messages` is required and must be an array.
- Content part types: `text`, `image_url`, `input_audio` are supported.
- `response_format` supports `json_object` and `json_schema` types.
- `stream_options.include_usage` is honored for streaming usage reporting.
- Multimodal image input works with both base64 data URIs and remote URLs.
- `max_completion_tokens` maps to `n_predict` internally. `max_tokens` is used as a fallback when `max_completion_tokens` is not set.

### Tool Calling Requirement

Tool calling requires the `--jinja` flag when starting llama-server. Without it, tools are not processed correctly. Make sure your llama-swap configuration passes this flag:

```yaml
models:
  my-model:
    cmd: llama-server --jinja --port ${PORT} --model /path/to/model.gguf
```

### Extra Parameters

llama-server accepts parameters beyond the OpenAI specification:

| Parameter | Type | Description |
|-----------|------|-------------|
| `top_k` | integer | Limit sampling to the top K tokens. |
| `min_p` | number | Minimum probability threshold for sampling. |
| `typical_p` | number | Locally typical sampling threshold. |
| `repeat_penalty` | number | Penalty for repeating tokens. |
| `mirostat` | integer | Mirostat sampling mode (0 = disabled, 1 = Mirostat, 2 = Mirostat 2.0). |
| `grammar` | string | Grammar to constrain generation (GBNF format). |
| `json_schema` | string | JSON schema for structured output (alternative to `response_format`). |
| `cache_prompt` | boolean | Whether to cache the prompt for faster subsequent requests. |
| `timings` | boolean | Whether to include timing information in the response. |

## llama-swap Proxy Notes

llama-swap proxies `/v1/chat/completions` requests directly to the configured upstream llama-server instance. The route is registered in `proxy/proxymanager.go`.

Key points:

- No request or response conversion is needed for local backends. The request passes through as-is.
- The `model` field in the request body is used to determine which configured model to load. llama-swap extracts it, matches it against the configuration, and starts or swaps the upstream server as needed.
- Streaming responses are forwarded directly. The proxy does not buffer SSE chunks.
- Token usage statistics from the upstream server pass through unchanged.
- For clients behind a reverse proxy (nginx, Caddy), make sure response buffering is disabled for streaming to work. llama-swap sets the `X-Accel-Buffering: no` header as a safeguard, but explicit proxy configuration is more reliable.
