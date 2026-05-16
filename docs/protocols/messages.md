# Anthropic Messages Protocol

Reference for the Anthropic `/v1/messages` endpoint. This is Anthropic's native API for interacting with Claude models. It has a different design than the OpenAI Chat Completions API: the system prompt is a top-level parameter rather than a message role, content is structured as typed blocks instead of plain strings, and tool results are content blocks rather than a separate message role. Covers the full request and response schema, streaming behavior, extended thinking, tool calling, usage tracking, and compatibility notes for llama.cpp and llama-swap.

## Endpoint

| Field         | Value                                |
|---------------|--------------------------------------|
| Method        | `POST`                               |
| Path          | `/v1/messages`                       |
| Content-Type  | `application/json`                   |
| Authentication| `x-api-key: <ANTHROPIC_API_KEY>`     |

The API is stateless. The full conversation history must be sent with every request.

## Authentication

Requests must include these headers:

```http
x-api-key: <ANTHROPIC_API_KEY>
anthropic-version: 2023-06-01
content-type: application/json
```

An alternative auth method is the `Authorization` header:

```http
Authorization: Bearer <token>
```

Beta features require an additional header with the beta name:

```http
anthropic-beta: mcp-client-2025-11-20
```

Multiple beta flags can be comma-separated in a single header.

## Request Body

### Required Fields

| Field         | Type   | Required | Default | Description                                       |
|---------------|--------|----------|---------|---------------------------------------------------|
| `model`       | string | Yes      |         | Model ID (e.g. `claude-sonnet-4-6`).              |
| `messages`    | array  | Yes      |         | Ordered list of message objects. See Message Format. |
| `max_tokens`  | number | Yes      |         | Maximum output tokens. Can be set to `0` for cache warming. |

### Optional Fields

| Field             | Type                    | Default          | Description                                                                 |
|-------------------|-------------------------|------------------|-----------------------------------------------------------------------------|
| `system`          | string \| TextBlockParam[] | none          | System prompt. Sent as a top-level parameter, not a message role.           |
| `temperature`     | number                  | model-dependent  | Sampling randomness. Deprecated for models after Claude Opus 4.6.           |
| `top_p`           | number                  | model-dependent  | Nucleus sampling. Deprecated for newer models; values below 0.99 are rejected. |
| `top_k`           | number                  | model-dependent  | Top-k sampling. Deprecated for newer models.                                |
| `stop_sequences`  | string[]                | none             | Custom strings that cause the model to stop generating.                     |
| `stream`          | boolean                 | `false`          | When true, sends SSE events instead of a single response.                   |
| `metadata`        | object                  | none             | Request metadata. Supports `{ "user_id": "..." }`.                          |
| `tools`           | ToolUnion[]             | none             | Tool definitions the model may call.                                        |
| `tool_choice`     | object                  | `auto`           | Controls when and how the model uses tools. See Tool Choice.                |
| `thinking`        | object                  | disabled         | Extended thinking configuration. See Extended Thinking.                     |
| `cache_control`   | object                  | none             | Prompt cache marker.                                                        |
| `container`       | string \| null          | none             | Reuse a code-execution container across requests.                           |
| `service_tier`    | string                  | `default`        | Processing tier: `auto` or `standard_only`.                                 |
| `output_config`   | object                  | none             | Structured output options.                                                  |
| `inference_geo`   | string \| null          | workspace default| Region for inference processing.                                            |

## Message Format

Each message has a `role` and `content`:

```json
{
  "role": "user",
  "content": "Hello, how are you?"
}
```

The string form of `content` is shorthand for a typed block array:

```json
{
  "role": "user",
  "content": "hello"
}
```

is equivalent to:

```json
{
  "role": "user",
  "content": [{ "type": "text", "text": "hello" }]
}
```

**Rules:**

- Only `user` and `assistant` roles are valid in the `messages` array. The system prompt goes in the top-level `system` parameter.
- Consecutive turns with the same role may be combined by the API.
- Messages must alternate between `user` and `assistant`, starting with `user`.

## Content Block Types

The `content` field in messages and responses is an array of typed blocks. Each block has a `type` field that determines its structure.

### text

Plain text content. Used in both user and assistant messages.

```json
{
  "type": "text",
  "text": "What is the weather in Paris?",
  "cache_control": {},
  "citations": []
}
```

The `cache_control` and `citations` fields are optional.

### image

Image content in user messages. Supports base64-encoded data and URLs.

Base64 source:

```json
{
  "type": "image",
  "source": {
    "type": "base64",
    "media_type": "image/png",
    "data": "iVBORw0KGgoAAAANSUhEUg..."
  }
}
```

URL source:

```json
{
  "type": "image",
  "source": {
    "type": "url",
    "url": "https://example.com/photo.png"
  }
}
```

Supported base64 media types: `image/jpeg`, `image/png`, `image/gif`, `image/webp`.

### tool_use

Returned by the model in assistant messages when it decides to call a tool.

```json
{
  "type": "tool_use",
  "id": "toolu_01A09q90qw90lq917835lq9",
  "name": "get_weather",
  "input": {
    "location": "Paris, France"
  }
}
```

The `id` field is prefixed with `toolu_` and must be preserved when sending the corresponding `tool_result`.

### tool_result

Sent by the client in a follow-up user message to provide the output of a tool call.

```json
{
  "type": "tool_result",
  "tool_use_id": "toolu_01A09q90qw90lq917835lq9",
  "content": "20C, sunny with light clouds",
  "is_error": false
}
```

The `content` field can be a string or an array of content blocks. The `tool_use_id` must match the `id` from the corresponding `tool_use` block. Set `is_error` to `true` when the tool execution failed.

### thinking

Extended thinking output, returned before text blocks when thinking is enabled.

```json
{
  "type": "thinking",
  "thinking": "Let me analyze this step by step...",
  "signature": "ErUBkao..."
}
```

The `signature` field is an opaque value used for continuity across multi-turn conversations with thinking enabled. Forward it back unchanged in subsequent requests.

## Tools

### Custom Tools

Define custom tools with a JSON Schema for the input:

```json
{
  "type": "custom",
  "name": "get_weather",
  "description": "Get the current weather in a given location",
  "input_schema": {
    "type": "object",
    "properties": {
      "location": {
        "type": "string",
        "description": "The city and country, e.g. Paris, France"
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
```

Setting `strict` to `true` enables strict schema validation.

### Anthropic-Provided Tools

These are built-in tools provided by Anthropic. Pass them in the `tools` array with only a `type` field.

| Category        | Tool types                                                  |
|-----------------|-------------------------------------------------------------|
| Bash            | `bash_20250124`                                             |
| Text editor     | `text_editor_20250124`, `text_editor_20250728`              |
| Computer use    | `computer_20250124`, `computer_20251124`                    |
| MCP connector   | `mcp_toolset`                                               |
| Web search      | `web_search_20250305`, `web_search_20260209`                |
| Web fetch       | `web_fetch_20250910`, `web_fetch_20260209`                  |
| Code execution  | `code_execution_20250825`, `code_execution_20260120`        |

### MCP Connector

The MCP connector lets the model interact with external tool servers. It requires the `anthropic-beta: mcp-client-2025-11-20` header.

```json
{
  "mcp_servers": [
    {
      "type": "url",
      "url": "https://my-mcp-server.example.com/sse",
      "name": "my-mcp"
    }
  ],
  "tools": [
    {
      "type": "mcp_toolset",
      "mcp_server_name": "my-mcp"
    }
  ]
}
```

## Tool Choice

Controls when and how the model uses tools. Specified as the `tool_choice` parameter in the request.

| Type     | Behavior                                       | Example                                              |
|----------|------------------------------------------------|------------------------------------------------------|
| `auto`   | Model decides whether to use a tool            | `{ "type": "auto" }`                                 |
| `any`    | Model must use at least one tool               | `{ "type": "any" }`                                  |
| `tool`   | Model must use the specified tool              | `{ "type": "tool", "name": "get_weather" }`          |
| `none`   | Model must not use any tools                   | `{ "type": "none" }`                                 |

All types except `none` support `disable_parallel_tool_use` to prevent the model from calling multiple tools in a single turn:

```json
{
  "type": "auto",
  "disable_parallel_tool_use": true
}
```

## Extended Thinking

Extended thinking lets the model reason through complex problems before producing its final answer. The model emits `thinking` blocks before `text` blocks in the response.

### Enabled Mode

The model always uses a fixed thinking budget:

```json
{
  "thinking": {
    "type": "enabled",
    "budget_tokens": 10000,
    "display": "summarized"
  }
}
```

- `budget_tokens` must be at least 1024 and less than `max_tokens`.
- Thinking tokens count toward the `max_tokens` limit.

### Adaptive Mode

The model decides whether to think and how much budget to use:

```json
{
  "thinking": {
    "type": "adaptive",
    "display": "omitted"
  }
}
```

### Disabled Mode

Thinking is turned off entirely:

```json
{
  "thinking": {
    "type": "disabled"
  }
}
```

### Display Options

| Value        | Behavior                                                     |
|--------------|--------------------------------------------------------------|
| `summarized` | Returns a condensed version of the thinking.                 |
| `omitted`    | Returns an empty `thinking` field with a `signature` for continuity. |

The `signature` field must be forwarded in subsequent turns when thinking is enabled, even if the thinking content itself is empty. This preserves the model's reasoning chain across the conversation.

## Response Object

A non-streaming response returns a single JSON object:

```json
{
  "id": "msg_01XFDUDYJgAACzvnptvVoYEL",
  "type": "message",
  "role": "assistant",
  "content": [
    {
      "type": "thinking",
      "thinking": "The user is asking about the weather...",
      "signature": "ErUBkao..."
    },
    {
      "type": "text",
      "text": "I'd be happy to check the weather for you."
    }
  ],
  "model": "claude-sonnet-4-6",
  "stop_reason": "end_turn",
  "stop_sequence": null,
  "usage": {
    "input_tokens": 100,
    "output_tokens": 50,
    "cache_creation_input_tokens": 0,
    "cache_read_input_tokens": 0
  }
}
```

**Fields:**

| Field            | Type    | Description                                                        |
|------------------|---------|--------------------------------------------------------------------|
| `id`             | string  | Message ID, prefixed with `msg_`.                                  |
| `type`           | string  | Always `"message"`.                                                |
| `role`           | string  | Always `"assistant"`.                                              |
| `content`        | array   | Array of content blocks (thinking, text, tool_use).                |
| `model`          | string  | The model ID that handled the request.                             |
| `stop_reason`    | string  | Why the model stopped generating. See values below.                |
| `stop_sequence`  | string  | The custom stop sequence that was hit, or `null`.                  |
| `usage`          | object  | Token usage breakdown. See Usage Tracking.                         |

**Stop reasons:**

| Value             | Meaning                                              |
|-------------------|------------------------------------------------------|
| `end_turn`        | Model finished its response naturally.               |
| `max_tokens`      | Output hit the `max_tokens` limit.                   |
| `stop_sequence`   | Model hit one of the `stop_sequences`.               |
| `tool_use`        | Model is requesting a tool call.                     |
| `pause_turn`      | Response was paused (extended thinking).             |
| `refusal`         | Model declined to respond.                           |
| `null`            | No stop reason (e.g., prefill-only request).         |

## Usage Tracking

The `usage` object in the response tracks token consumption across several dimensions:

```json
{
  "input_tokens": 100,
  "output_tokens": 50,
  "cache_creation_input_tokens": 0,
  "cache_read_input_tokens": 0,
  "cache_creation": null,
  "server_tool_use": null,
  "service_tier": "standard",
  "inference_geo": null
}
```

**Fields:**

| Field                            | Type    | Description                                                  |
|----------------------------------|---------|--------------------------------------------------------------|
| `input_tokens`                   | integer | Tokens in the request that were not served from cache.       |
| `output_tokens`                  | integer | Tokens generated by the model.                               |
| `cache_creation_input_tokens`    | integer | Tokens written to the prompt cache.                          |
| `cache_read_input_tokens`        | integer | Tokens read from the prompt cache.                           |
| `cache_creation`                 | object  | Detailed cache creation metrics, or `null`.                  |
| `server_tool_use`                | object  | Server-side tool usage metrics, or `null`.                   |
| `service_tier`                   | string  | The service tier used for the request.                       |
| `inference_geo`                  | string  | The geographic region used for inference, or `null`.         |

**Total input tokens** are the sum of three fields:

```
total_input = input_tokens + cache_creation_input_tokens + cache_read_input_tokens
```

This matters for billing and rate limiting. Cached tokens are cheaper than uncached ones but still count toward throughput.

In streaming mode, usage is split across two events:
- `message_start` contains input token counts (including cache fields).
- `message_delta` contains the final `output_tokens` count.

## Streaming SSE Events

When `stream: true` is set, the API sends a sequence of Server-Sent Events instead of a single JSON response. Each event is a JSON object with a `type` field.

**Event flow:**

1. `message_start` (contains the initial message object with input usage)
2. For each content block:
   - `content_block_start`
   - `content_block_delta` (zero or more)
   - `content_block_stop`
3. `message_delta` (contains output token usage and stop reason)
4. `message_stop`
5. `ping` events may appear at any time

### message_start

Opens the response. Contains the full message object with empty `content` and input token usage.

```json
{
  "type": "message_start",
  "message": {
    "id": "msg_01XFDUDYJgAACzvnptvVoYEL",
    "type": "message",
    "role": "assistant",
    "content": [],
    "model": "claude-sonnet-4-6",
    "stop_reason": null,
    "stop_sequence": null,
    "usage": {
      "input_tokens": 100,
      "output_tokens": 0,
      "cache_creation_input_tokens": 0,
      "cache_read_input_tokens": 0
    }
  }
}
```

### content_block_start

Signals the start of a new content block. The `index` field tracks position in the content array.

```json
{
  "type": "content_block_start",
  "index": 0,
  "content_block": {
    "type": "text",
    "text": ""
  }
}
```

For tool_use blocks:

```json
{
  "type": "content_block_start",
  "index": 1,
  "content_block": {
    "type": "tool_use",
    "id": "toolu_01A09q90qw90lq917835lq9",
    "name": "get_weather",
    "input": {}
  }
}
```

For thinking blocks:

```json
{
  "type": "content_block_start",
  "index": 0,
  "content_block": {
    "type": "thinking",
    "thinking": ""
  }
}
```

### content_block_delta

Incremental content for the current block. The delta type depends on the content block type.

**Text delta:**

```json
{
  "type": "content_block_delta",
  "index": 0,
  "delta": {
    "type": "text_delta",
    "text": "Hello"
  }
}
```

**Tool input JSON delta:**

```json
{
  "type": "content_block_delta",
  "index": 1,
  "delta": {
    "type": "input_json_delta",
    "partial_json": "{\"location\": \"Paris"
  }
}
```

The `partial_json` fragments must be concatenated to form the complete JSON input string.

**Thinking delta:**

```json
{
  "type": "content_block_delta",
  "index": 0,
  "delta": {
    "type": "thinking_delta",
    "thinking": "Let me think about this..."
  }
}
```

**Signature delta:**

```json
{
  "type": "content_block_delta",
  "index": 0,
  "delta": {
    "type": "signature_delta",
    "signature": "ErUBkao..."
  }
}
```

### content_block_stop

Signals the end of a content block. No further deltas will be sent for this index.

```json
{
  "type": "content_block_stop",
  "index": 0
}
```

### message_delta

Sent after all content blocks are complete. Contains the final stop reason and output token count.

```json
{
  "type": "message_delta",
  "delta": {
    "stop_reason": "end_turn",
    "stop_sequence": null
  },
  "usage": {
    "output_tokens": 15
  }
}
```

### message_stop

Signals that the entire message is complete. No more events will follow (except `ping`).

```json
{
  "type": "message_stop"
}
```

### ping

Keep-alive event that can appear at any point in the stream. Clients should ignore it.

```json
{
  "type": "ping"
}
```

### error

Sent when an error occurs during streaming. After this event, the stream ends.

```json
{
  "type": "error",
  "error": {
    "type": "overloaded_error",
    "message": "Overloaded"
  }
}
```

## Tool Calling Flow

Tool calling is a multi-turn interaction between the client and the model.

**Step 1: Send a request with tool definitions.**

```json
{
  "model": "claude-sonnet-4-6",
  "max_tokens": 1024,
  "tools": [
    {
      "type": "custom",
      "name": "get_weather",
      "description": "Get the current weather in a location",
      "input_schema": {
        "type": "object",
        "properties": {
          "location": { "type": "string" }
        },
        "required": ["location"]
      }
    }
  ],
  "messages": [
    { "role": "user", "content": "What is the weather in Paris?" }
  ]
}
```

**Step 2: Model responds with a tool_use block and `stop_reason: "tool_use"`.**

```json
{
  "id": "msg_01...",
  "type": "message",
  "role": "assistant",
  "content": [
    {
      "type": "text",
      "text": "I'll check the weather in Paris for you."
    },
    {
      "type": "tool_use",
      "id": "toolu_01A09q90qw90lq917835lq9",
      "name": "get_weather",
      "input": { "location": "Paris, France" }
    }
  ],
  "stop_reason": "tool_use",
  "usage": { "input_tokens": 200, "output_tokens": 30 }
}
```

**Step 3: Client executes the tool and sends the result back.**

The client sends a new request with the full conversation history, including the assistant's tool_use response and a new user message containing the `tool_result`:

```json
{
  "model": "claude-sonnet-4-6",
  "max_tokens": 1024,
  "tools": [
    {
      "type": "custom",
      "name": "get_weather",
      "description": "Get the current weather in a location",
      "input_schema": {
        "type": "object",
        "properties": {
          "location": { "type": "string" }
        },
        "required": ["location"]
      }
    }
  ],
  "messages": [
    { "role": "user", "content": "What is the weather in Paris?" },
    {
      "role": "assistant",
      "content": [
        { "type": "text", "text": "I'll check the weather in Paris for you." },
        {
          "type": "tool_use",
          "id": "toolu_01A09q90qw90lq917835lq9",
          "name": "get_weather",
          "input": { "location": "Paris, France" }
        }
      ]
    },
    {
      "role": "user",
      "content": [
        {
          "type": "tool_result",
          "tool_use_id": "toolu_01A09q90qw90lq917835lq9",
          "content": "20C, sunny with light clouds"
        }
      ]
    }
  ]
}
```

**Step 4: Model continues with the tool result and produces a final answer.**

```json
{
  "id": "msg_02...",
  "type": "message",
  "role": "assistant",
  "content": [
    {
      "type": "text",
      "text": "The weather in Paris is currently 20°C with sunny skies and light clouds."
    }
  ],
  "stop_reason": "end_turn",
  "usage": { "input_tokens": 250, "output_tokens": 20 }
}
```

The `tool_use_id` in the `tool_result` must match the `id` from the `tool_use` block. Mismatched IDs will cause an error.

## Key Differences from OpenAI Chat Completions

The Anthropic Messages API has a fundamentally different design than the OpenAI Chat Completions API. Here are the main differences:

**System prompt.** Anthropic uses a top-level `system` parameter. OpenAI uses a `system` role message in the `messages` array.

**Message roles.** Anthropic only supports `user` and `assistant` in the messages array. OpenAI supports `system`, `developer`, `user`, `assistant`, and `tool`.

**Content structure.** Anthropic content is an array of typed blocks (`text`, `image`, `tool_use`, `tool_result`, `thinking`). OpenAI content is a plain string for simple text, with tool calls in a separate `tool_calls` array at the message level.

**Tool calls.** Anthropic emits `tool_use` blocks inside the `content` array. OpenAI puts tool calls in a top-level `tool_calls` array on the assistant message.

**Tool results.** Anthropic sends tool results as `tool_result` content blocks inside a `user` message. OpenAI uses a `tool` role message with a `tool_call_id` field.

**Usage in streaming.** Anthropic splits usage across `message_start` (input tokens) and `message_delta` (output tokens). OpenAI sends usage in the final SSE chunk or via `stream_options.include_usage`.

**Extended thinking.** Anthropic supports a `thinking` parameter with a configurable token budget. The model emits thinking blocks before text blocks. OpenAI has no equivalent.

**Anthropic-specific tools.** Anthropic provides built-in tools for computer use, bash, text editing, web search, and code execution. These have no OpenAI equivalent.

**Authentication.** Anthropic uses the `x-api-key` header. OpenAI uses `Authorization: Bearer`.

## llama.cpp Compatibility

llama.cpp's server registers `/v1/messages` and `/v1/messages/count_tokens` endpoints to support Anthropic API clients. It translates the Anthropic format to its internal OpenAI-based representation.

### Translation Approach

| Anthropic             | OpenAI Internal                |
|-----------------------|--------------------------------|
| `system` parameter    | OpenAI `system` message        |
| `text` content blocks | Pass through as text           |
| `thinking` blocks     | `reasoning_content` field      |
| Base64/url images     | OpenAI `image_url` format      |
| `tool_use` blocks     | OpenAI `tool_calls` array      |
| `tool_result` blocks  | OpenAI `tool` role messages    |

### Supported Features

- Non-streaming responses: returns the full Anthropic response shape (`id`, `type: "message"`, `role: "assistant"`, `content`, `model`, `stop_reason`, `usage`).
- Streaming: emits Anthropic-style SSE events (`message_start`, `content_block_start`, `content_block_delta`, `content_block_stop`, `message_delta`, `message_stop`).
- Tool definitions and tool calling.
- Image inputs (base64 and URL sources).

### Differences and Limitations

- llama.cpp does not make a strong API compatibility claim. It supports many common use cases but edge cases may not work.
- The `--jinja` flag is required for tool support.
- Anthropic-specific built-in tools (computer use, bash, text editor) are not supported.
- Some response fields may be missing or have different values than the native Anthropic API.
- Extended thinking behavior depends on the underlying model's capabilities.

## llama-swap Proxy Notes

llama-swap proxies `/v1/messages` and `/v1/messages/count_tokens` requests to the upstream llama.cpp server. The route is registered in `proxy/proxymanager.go`.

### Usage Tracking

When collecting token usage from streaming responses, the input and output tokens arrive in separate SSE events:

- `message_start` contains `input_tokens`, `cache_creation_input_tokens`, and `cache_read_input_tokens`.
- `message_delta` contains `output_tokens`.

To get the full usage picture, accumulate values from both events. Total input is the sum of `input_tokens`, `cache_creation_input_tokens`, and `cache_read_input_tokens`.
