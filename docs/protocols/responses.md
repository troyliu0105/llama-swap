# /v1/responses

The newest OpenAI API endpoint, designed for agentic workflows. Unlike the Chat Completions API, the Responses API provides a top-level response object with a polymorphic output array, built-in hosted tools (web search, file search, code interpreter, MCP), configurable reasoning, and semantic streaming events. It is intended as the primary interface for building agents that reason, call tools, and iterate over multiple turns.

## Endpoint

| Property     | Value                |
|-------------|----------------------|
| Method      | `POST`               |
| Path        | `/v1/responses`      |
| Content-Type | `application/json`  |

## Request Body

### Required Fields

| Field   | Type     | Default | Description          |
|---------|----------|---------|----------------------|
| `model` | `string` | (none)  | Model ID to use      |
| `input` | `string` \| `array` | (none) | The prompt. A string is treated as a single user text message. An array allows multi-turn conversations with typed content parts. |

### Optional Fields

| Field | Type | Default | Description |
|---|---|---|---|
| `instructions` | `string` | `null` | System/developer message. Not carried forward by `previous_response_id`. Set it each turn if you need it. |
| `tools` | `array` | `[]` | Tool definitions. Mix of `function`, `web_search`, `file_search`, `code_interpreter`, and `mcp`. |
| `tool_choice` | `string` \| `object` | `"auto"` | `"none"`, `"auto"`, `"required"`, or `{ type: "function", name: "..." }` to force a specific tool. |
| `temperature` | `number` | `1` | Sampling randomness. Range: 0 to 2. |
| `top_p` | `number` | `1` | Nucleus sampling threshold. Range: 0 to 1. |
| `max_output_tokens` | `integer` | model default | Caps the combined output and reasoning tokens. |
| `stream` | `boolean` | `false` | Stream partial results as server-sent events. |
| `stream_options` | `object` | `null` | Streaming options. Supports `include_obfuscation` (boolean) to normalize payload sizes. |
| `store` | `boolean` | `false` | Store the response for later retrieval via the API. |
| `reasoning` | `object` | `null` | Reasoning configuration. See [Reasoning](#reasoning-configuration). |
| `text` | `object` | `null` | Output text configuration. `{ format: { type: "text"\|"json_object"\|"json_schema" } }`, optional `verbosity: "low"\|"medium"\|"high"`. |
| `metadata` | `object` | `null` | Up to 16 key/value string pairs attached to the response. |
| `previous_response_id` | `string` | `null` | Carry multi-turn state from a prior response. Mutually exclusive with `conversation`. |
| `conversation` | `string` \| `object` | `null` | Adds items to an existing conversation. |
| `truncation` | `enum` | `"auto"` | `"auto"` drops old messages when context overflows. `"disabled"` returns a 400 error instead. |
| `include` | `string[]` | `[]` | Extra data in the response. Values: file search results, web sources, logprobs, encrypted reasoning content. |
| `parallel_tool_calls` | `boolean` | `true` | Allow the model to issue multiple tool calls in a single turn. |
| `prompt` | `object` | `null` | Reusable prompt template reference. |
| `background` | `boolean` | `false` | Run the response asynchronously in the background. |
| `max_tool_calls` | `integer` | model default | Cap on built-in tool calls per response. |
| `top_logprobs` | `integer` | `0` | Number of top log probabilities to return per token. Range: 0 to 20. |
| `safety_identifier` | `string` | `null` | Identifier for abuse detection systems. |
| `prompt_cache_key` | `string` | `null` | Cache bucketing key for prompt caching. |
| `service_tier` | `enum` | `"auto"` | Priority tier: `"auto"`, `"default"`, `"flex"`, `"scale"`, `"priority"`. |
| `context_management` | `array` | `null` | Context compaction configuration. |

## Input Formats

The `input` field accepts three shapes.

### String

A plain string is equivalent to a single user text message.

```json
{
  "model": "gpt-5",
  "input": "What is the capital of France?"
}
```

### Message Array

An array of messages with roles. Supported roles: `developer`, `user`, `assistant`.

```json
{
  "model": "gpt-5",
  "input": [
    { "role": "developer", "content": "You are a helpful assistant." },
    { "role": "user", "content": "Explain quantum entanglement." }
  ]
}
```

### Content Parts

Messages can contain typed content parts for multimodal input.

```json
{
  "model": "gpt-5",
  "input": [
    {
      "role": "developer",
      "content": [
        { "type": "input_text", "text": "Be concise." }
      ]
    },
    {
      "role": "user",
      "content": [
        { "type": "input_text", "text": "Describe this image." },
        { "type": "input_image", "image_url": "https://example.com/photo.jpg" }
      ]
    }
  ]
}
```

Content part types:

| Type | Fields | Description |
|---|---|---|
| `input_text` | `text` | Plain text input. |
| `input_image` | `image_url` or `image_data` | Image from URL or base64 data. |
| `input_file` | `file_url` or `file_data` | File attachment for supported tools. |

## Tool Types

### function

A developer-defined function the model can call. Mirrors the Chat Completions function tool schema.

```json
{
  "type": "function",
  "name": "get_weather",
  "description": "Get the current weather for a location.",
  "parameters": {
    "type": "object",
    "properties": {
      "location": { "type": "string", "description": "City name" }
    },
    "required": ["location"]
  },
  "strict": true
}
```

### web_search

A built-in tool that searches the web and injects results into the model context.

```json
{
  "type": "web_search",
  "search_context_size": "medium",
  "filters": {
    "allowed_domains": ["wikipedia.org", "reuters.com"]
  },
  "user_location": {
    "type": "approximate",
    "city": "San Francisco",
    "country": "US"
  }
}
```

| Field | Type | Values | Description |
|---|---|---|---|
| `search_context_size` | `string` | `"low"`, `"medium"`, `"high"` | Amount of search context to include. |
| `filters` | `object` | `allowed_domains`, `blocked_domains` | Restrict which domains appear in results. |
| `user_location` | `object` | `type`, `city`, `country`, `region`, `timezone` | Approximate user location for local results. |

### file_search

Searches attached vector stores and returns relevant chunks.

```json
{
  "type": "file_search",
  "vector_store_ids": ["vs_abc123"],
  "max_num_results": 10,
  "ranking_options": {
    "ranker": "auto",
    "score_threshold": 0.0
  }
}
```

### code_interpreter

Runs code in a sandboxed container and returns results.

```json
{
  "type": "code_interpreter",
  "container": "auto"
}
```

### mcp

Connects to an external MCP (Model Context Protocol) server for tool discovery and invocation.

```json
{
  "type": "mcp",
  "server_label": "my-tools",
  "server_url": "https://mcp.example.com/sse",
  "allowed_tools": ["search", "lookup"],
  "require_approval": "never"
}
```

| Field | Type | Values | Description |
|---|---|---|---|
| `server_label` | `string` | (any) | Identifier for the MCP server. |
| `server_url` | `string` | URL | SSE endpoint for the MCP server. |
| `allowed_tools` | `string[]` | (any) | Restrict which tools the model can use. |
| `require_approval` | `string` | `"always"`, `"never"` | Whether tool calls need human approval. |

## Reasoning Configuration

Control how the model reasons before answering. Set via the `reasoning` object on the request.

```json
{
  "model": "o3",
  "input": "Solve this puzzle: ...",
  "reasoning": {
    "effort": "high",
    "summary": "detailed"
  }
}
```

### Effort Levels

| Level | Description |
|---|---|
| `"none"` | No reasoning. Equivalent to a standard model response. |
| `"minimal"` | Light reasoning, fastest latency. |
| `"low"` | Brief reasoning chain. |
| `"medium"` | Balanced reasoning depth and latency. |
| `"high"` | Deep reasoning for complex tasks. |
| `"xhigh"` | Maximum reasoning effort. Highest token cost and latency. |

### Summary

| Value | Description |
|---|---|
| `"auto"` | Model decides whether to include a summary. |
| `"concise"` | Short reasoning summary. |
| `"detailed"` | Full reasoning summary with step-by-step detail. |

## Response Object

The response is a top-level object with a polymorphic `output` array. Each item in `output` has a `type` field that determines its shape.

```json
{
  "id": "resp_abc123",
  "object": "response",
  "created_at": 1710000000,
  "status": "completed",
  "error": null,
  "instructions": "You are a helpful assistant.",
  "model": "gpt-5",
  "output": [
    {
      "type": "message",
      "id": "msg_001",
      "role": "assistant",
      "content": [
        {
          "type": "output_text",
          "text": "Hello! How can I help you?",
          "annotations": []
        }
      ]
    }
  ],
  "parallel_tool_calls": true,
  "temperature": 1,
  "top_p": 1,
  "tools": [],
  "tool_choice": "auto",
  "completed_at": 1710000001,
  "max_output_tokens": 1024,
  "reasoning": {},
  "usage": {
    "input_tokens": 20,
    "input_tokens_details": { "cached_tokens": 0 },
    "output_tokens": 37,
    "output_tokens_details": { "reasoning_tokens": 0 },
    "total_tokens": 57
  }
}
```

### Status Values

| Status | Meaning |
|---|---|
| `"completed"` | Finished successfully. |
| `"failed"` | Ended with an error. Check `error` field. |
| `"in_progress"` | Still generating (seen in streaming). |
| `"cancelled"` | Cancelled by the client or system. |
| `"queued"` | Waiting to be processed. |
| `"incomplete"` | Hit a limit (tokens, tool calls) before finishing. |

## Output Item Types

Each item in the `output` array has a `type` field. The common types:

| Type | Description |
|---|---|
| `message` | An assistant message with `content` array of output parts. |
| `function_call` | A function tool call with `name`, `call_id`, and `arguments` (JSON string). |
| `function_call_output` | Result of a function call, sent back as input in the next turn. |
| `reasoning` | Reasoning chain content. Only visible when reasoning is enabled. |
| `web_search_call` | A web search invocation with status and search context. |
| `file_search_call` | A file search invocation over vector stores. |
| `code_interpreter_call` | A code execution in a sandboxed container. |
| `computer_call` | A computer use action (click, type, screenshot). |
| `mcp_call` | An MCP tool invocation. |
| `mcp_list_tools` | Result of listing available MCP tools. |
| `image_generation_call` | An image generation request. |
| `local_shell_call` | A local shell command execution. |
| `apply_patch_call` | A code patch application. |
| `custom_tool_call` | A custom tool invocation. |
| `compaction` | Context compaction summary. |

## Streaming SSE Events

When `stream: true`, the server emits semantic events as server-sent events. Each SSE `data` line contains a JSON object with a `type` field, a `sequence_number`, and type-specific fields.

Every event follows this shape:

```json
{
  "type": "response.output_text.delta",
  "sequence_number": 3,
  "item_id": "msg_001",
  "output_index": 0,
  "content_index": 0,
  "delta": "Hello"
}
```

### Lifecycle Events

These track the overall response state.

**response.created** - The response object has been created.

```json
{ "type": "response.created", "response": { "id": "resp_abc123", "status": "queued", "..." : "..." }, "sequence_number": 0 }
```

**response.in_progress** - Generation has started.

```json
{ "type": "response.in_progress", "response": { "id": "resp_abc123", "status": "in_progress", "..." : "..." }, "sequence_number": 1 }
```

**response.completed** - Generation finished. Contains the full response object.

```json
{ "type": "response.completed", "response": { "id": "resp_abc123", "status": "completed", "output": ["..."], "usage": { "..." } }, "sequence_number": 43 }
```

**response.failed** - The response ended with an error.

```json
{ "type": "response.failed", "response": { "id": "resp_abc123", "status": "failed", "error": { "..." } }, "sequence_number": 5 }
```

**response.incomplete** - The response hit a limit before finishing.

```json
{ "type": "response.incomplete", "response": { "id": "resp_abc123", "status": "incomplete", "..." : "..." }, "sequence_number": 12 }
```

**response.queued** - The response is queued for processing.

```json
{ "type": "response.queued", "response": { "id": "resp_abc123", "status": "queued", "..." : "..." }, "sequence_number": 0 }
```

### Output Structure Events

These signal when output items and content parts are added or finalized.

**response.output_item.added** - A new item added to the output array.

```json
{ "type": "response.output_item.added", "output_index": 0, "item": { "type": "message", "role": "assistant", "content": [] }, "sequence_number": 2 }
```

**response.output_item.done** - An output item is complete.

```json
{ "type": "response.output_item.done", "output_index": 0, "item": { "type": "message", "role": "assistant", "content": ["..."] }, "sequence_number": 42 }
```

**response.content_part.added** - A content part added to an output item.

```json
{ "type": "response.content_part.added", "item_id": "msg_001", "output_index": 0, "content_index": 0, "part": { "type": "output_text", "text": "" }, "sequence_number": 3 }
```

**response.content_part.done** - A content part is finalized.

```json
{ "type": "response.content_part.done", "item_id": "msg_001", "output_index": 0, "content_index": 0, "part": { "type": "output_text", "text": "Hello!" }, "sequence_number": 41 }
```

### Text Events

**response.output_text.delta** - Incremental text chunk.

```json
{ "type": "response.output_text.delta", "item_id": "msg_001", "output_index": 0, "content_index": 0, "delta": "hello", "logprobs": [], "sequence_number": 3 }
```

**response.output_text.done** - Full text for this content part is ready.

```json
{ "type": "response.output_text.done", "item_id": "msg_001", "output_index": 0, "content_index": 0, "text": "Hello! How can I help?", "sequence_number": 40 }
```

**response.output_text.annotation.added** - An annotation was attached to output text.

```json
{ "type": "response.output_text.annotation.added", "item_id": "msg_001", "output_index": 0, "content_index": 0, "annotation_index": 0, "annotation": { "type": "url_citation", "url": "https://..." }, "sequence_number": 15 }
```

### Refusal Events

**response.refusal.delta** - Incremental refusal text.

```json
{ "type": "response.refusal.delta", "item_id": "msg_001", "output_index": 0, "content_index": 0, "delta": "I can't", "sequence_number": 5 }
```

**response.refusal.done** - Refusal text is complete.

```json
{ "type": "response.refusal.done", "item_id": "msg_001", "output_index": 0, "content_index": 0, "refusal": "I can't assist with that.", "sequence_number": 8 }
```

### Function Call Events

**response.function_call_arguments.delta** - Incremental function call arguments.

```json
{ "type": "response.function_call_arguments.delta", "item_id": "fc_001", "output_index": 0, "call_id": "call_abc", "delta": "{\"locat", "sequence_number": 4 }
```

**response.function_call_arguments.done** - Full function call arguments.

```json
{ "type": "response.function_call_arguments.done", "item_id": "fc_001", "output_index": 0, "call_id": "call_abc", "arguments": "{\"location\": \"Paris\"}", "sequence_number": 10 }
```

### Reasoning Events

**response.reasoning_text.delta** - Incremental reasoning text.

```json
{ "type": "response.reasoning_text.delta", "item_id": "rs_001", "output_index": 0, "content_index": 0, "delta": "Let me think", "sequence_number": 3 }
```

**response.reasoning_text.done** - Full reasoning text.

```json
{ "type": "response.reasoning_text.done", "item_id": "rs_001", "output_index": 0, "content_index": 0, "text": "Let me think about this step by step...", "sequence_number": 20 }
```

**response.reasoning_summary_part.added** - A reasoning summary part was added.

```json
{ "type": "response.reasoning_summary_part.added", "item_id": "rs_001", "output_index": 0, "content_index": 0, "part": { "type": "summary_text", "text": "" }, "sequence_number": 21 }
```

**response.reasoning_summary_part.done** - A reasoning summary part is complete.

```json
{ "type": "response.reasoning_summary_part.done", "item_id": "rs_001", "output_index": 0, "content_index": 0, "part": { "type": "summary_text", "text": "The problem reduces to..." }, "sequence_number": 25 }
```

**response.reasoning_summary_text.delta** - Incremental reasoning summary text.

```json
{ "type": "response.reasoning_summary_text.delta", "item_id": "rs_001", "output_index": 0, "content_index": 0, "delta": "The problem", "sequence_number": 22 }
```

**response.reasoning_summary_text.done** - Full reasoning summary text.

```json
{ "type": "response.reasoning_summary_text.done", "item_id": "rs_001", "output_index": 0, "content_index": 0, "text": "The problem reduces to a simple substitution.", "sequence_number": 24 }
```

### Web Search Events

**response.web_search_call.in_progress** - Web search started.

```json
{ "type": "response.web_search_call.in_progress", "item_id": "ws_001", "output_index": 0, "sequence_number": 4 }
```

**response.web_search_call.searching** - Web search is actively querying.

```json
{ "type": "response.web_search_call.searching", "item_id": "ws_001", "output_index": 0, "sequence_number": 5 }
```

**response.web_search_call.completed** - Web search finished.

```json
{ "type": "response.web_search_call.completed", "item_id": "ws_001", "output_index": 0, "sequence_number": 8 }
```

### File Search Events

**response.file_search_call.in_progress** - File search started.

```json
{ "type": "response.file_search_call.in_progress", "item_id": "fs_001", "output_index": 0, "sequence_number": 3 }
```

**response.file_search_call.searching** - File search is querying vector stores.

```json
{ "type": "response.file_search_call.searching", "item_id": "fs_001", "output_index": 0, "sequence_number": 4 }
```

**response.file_search_call.completed** - File search finished.

```json
{ "type": "response.file_search_call.completed", "item_id": "fs_001", "output_index": 0, "sequence_number": 7 }
```

### Code Interpreter Events

**response.code_interpreter_call.in_progress** - Code execution started.

```json
{ "type": "response.code_interpreter_call.in_progress", "item_id": "ci_001", "output_index": 0, "sequence_number": 4 }
```

**response.code_interpreter_call.interpreting** - Code is being executed.

```json
{ "type": "response.code_interpreter_call.interpreting", "item_id": "ci_001", "output_index": 0, "sequence_number": 5 }
```

**response.code_interpreter_call.completed** - Code execution finished.

```json
{ "type": "response.code_interpreter_call.completed", "item_id": "ci_001", "output_index": 0, "sequence_number": 10 }
```

**response.code_interpreter_call_code.delta** - Incremental code being generated.

```json
{ "type": "response.code_interpreter_call_code.delta", "item_id": "ci_001", "output_index": 0, "delta": "import pandas", "sequence_number": 5 }
```

**response.code_interpreter_call_code.done** - Full code is ready.

```json
{ "type": "response.code_interpreter_call_code.done", "item_id": "ci_001", "output_index": 0, "code": "import pandas as pd\ndf = pd.read_csv('data.csv')", "sequence_number": 7 }
```

### MCP Events

**response.mcp_call.in_progress** - MCP tool call started.

```json
{ "type": "response.mcp_call.in_progress", "item_id": "mcp_001", "output_index": 0, "sequence_number": 4 }
```

**response.mcp_call.completed** - MCP tool call finished.

```json
{ "type": "response.mcp_call.completed", "item_id": "mcp_001", "output_index": 0, "sequence_number": 8 }
```

**response.mcp_call.failed** - MCP tool call failed.

```json
{ "type": "response.mcp_call.failed", "item_id": "mcp_001", "output_index": 0, "error": "Connection refused", "sequence_number": 6 }
```

**response.mcp_call_arguments.delta** - Incremental MCP call arguments.

```json
{ "type": "response.mcp_call_arguments.delta", "item_id": "mcp_001", "output_index": 0, "delta": "{\"quer", "sequence_number": 5 }
```

**response.mcp_call_arguments.done** - Full MCP call arguments.

```json
{ "type": "response.mcp_call_arguments.done", "item_id": "mcp_001", "output_index": 0, "arguments": "{\"query\": \"test\"}", "sequence_number": 6 }
```

**response.mcp_list_tools.in_progress** - MCP tool listing started.

```json
{ "type": "response.mcp_list_tools.in_progress", "item_id": "mcp_lt_001", "output_index": 0, "sequence_number": 2 }
```

**response.mcp_list_tools.completed** - MCP tool listing finished.

```json
{ "type": "response.mcp_list_tools.completed", "item_id": "mcp_lt_001", "output_index": 0, "tools": ["search", "lookup"], "sequence_number": 3 }
```

**response.mcp_list_tools.failed** - MCP tool listing failed.

```json
{ "type": "response.mcp_list_tools.failed", "item_id": "mcp_lt_001", "output_index": 0, "error": "Server unreachable", "sequence_number": 3 }
```

### Image Generation Events

**response.image_gen_call.in_progress** - Image generation started.

```json
{ "type": "response.image_gen_call.in_progress", "item_id": "ig_001", "output_index": 0, "sequence_number": 3 }
```

**response.image_gen_call.generating** - Image is being rendered.

```json
{ "type": "response.image_gen_call.generating", "item_id": "ig_001", "output_index": 0, "sequence_number": 4 }
```

**response.image_gen_call.completed** - Image generation finished.

```json
{ "type": "response.image_gen_call.completed", "item_id": "ig_001", "output_index": 0, "sequence_number": 8 }
```

**response.image_gen_call.partial_image** - Partial image preview.

```json
{ "type": "response.image_gen_call.partial_image", "item_id": "ig_001", "output_index": 0, "data": "<base64>", "sequence_number": 6 }
```

### Audio Events

**response.audio.delta** - Incremental audio data.

```json
{ "type": "response.audio.delta", "item_id": "au_001", "output_index": 0, "content_index": 0, "delta": "<base64>", "sequence_number": 5 }
```

**response.audio.done** - Full audio data for this part.

```json
{ "type": "response.audio.done", "item_id": "au_001", "output_index": 0, "content_index": 0, "sequence_number": 20 }
```

**response.audio_transcript.delta** - Incremental audio transcript.

```json
{ "type": "response.audio_transcript.delta", "item_id": "au_001", "output_index": 0, "content_index": 0, "delta": "Hello", "sequence_number": 5 }
```

**response.audio_transcript.done** - Full audio transcript.

```json
{ "type": "response.audio_transcript.done", "item_id": "au_001", "output_index": 0, "content_index": 0, "transcript": "Hello, how can I help?", "sequence_number": 19 }
```

### Custom Tool Events

**response.custom_tool_call_input.delta** - Incremental custom tool input.

```json
{ "type": "response.custom_tool_call_input.delta", "item_id": "ct_001", "output_index": 0, "delta": "{\"input", "sequence_number": 4 }
```

**response.custom_tool_call_input.done** - Full custom tool input.

```json
{ "type": "response.custom_tool_call_input.done", "item_id": "ct_001", "output_index": 0, "input": "{\"input\": \"value\"}", "sequence_number": 6 }
```

### Error Event

**error** - A stream-level error.

```json
{ "type": "error", "code": "rate_limit_exceeded", "message": "Too many requests.", "sequence_number": 2 }
```

## Usage Tracking

Every completed response includes a `usage` object. For streaming, it arrives inside the `response.completed` event via `response.usage`.

```json
{
  "input_tokens": 123,
  "input_tokens_details": {
    "cached_tokens": 100
  },
  "output_tokens": 45,
  "output_tokens_details": {
    "reasoning_tokens": 10
  },
  "total_tokens": 168
}
```

| Field | Type | Description |
|---|---|---|
| `input_tokens` | `integer` | Total input tokens consumed. |
| `input_tokens_details.cached_tokens` | `integer` | Tokens served from the prompt cache. |
| `output_tokens` | `integer` | Total output tokens generated. |
| `output_tokens_details.reasoning_tokens` | `integer` | Tokens used for the reasoning chain. |
| `total_tokens` | `integer` | Sum of input and output tokens. |

## Tool Calling Flow

The Responses API uses a multi-turn pattern for function calling. The model emits a `function_call` output item, the client executes the function locally, then sends the result back as a `function_call_output` input item.

### Step-by-step

**1. Send the request with tool definitions.**

```json
{
  "model": "gpt-5",
  "input": "What's the weather in Tokyo?",
  "tools": [
    {
      "type": "function",
      "name": "get_weather",
      "description": "Get current weather for a location.",
      "parameters": {
        "type": "object",
        "properties": {
          "location": { "type": "string" }
        },
        "required": ["location"]
      }
    }
  ]
}
```

**2. Model returns a function_call output item.**

```json
{
  "id": "resp_001",
  "status": "completed",
  "output": [
    {
      "type": "function_call",
      "id": "fc_001",
      "call_id": "call_abc",
      "name": "get_weather",
      "arguments": "{\"location\": \"Tokyo\"}"
    }
  ]
}
```

**3. Client executes the function and sends the result back.**

```json
{
  "model": "gpt-5",
  "previous_response_id": "resp_001",
  "input": [
    {
      "type": "function_call_output",
      "call_id": "call_abc",
      "output": "{\"temperature\": 22, \"condition\": \"Cloudy\"}"
    }
  ]
}
```

**4. Model continues with the tool result and produces a final answer.**

```json
{
  "id": "resp_002",
  "status": "completed",
  "output": [
    {
      "type": "message",
      "role": "assistant",
      "content": [
        { "type": "output_text", "text": "The weather in Tokyo is currently cloudy with a temperature of 22 degrees Celsius." }
      ]
    }
  ]
}
```

## Key Differences from Chat Completions

The Responses API is not just a renamed Chat Completions. It introduces structural differences that make it better suited for agentic workflows.

**Response structure.** Chat Completions returns `choices[0].message`. The Responses API returns a top-level `response` object with a polymorphic `output` array. Each output item has a `type` field (message, function_call, reasoning, etc.).

**Instructions are separate.** The `instructions` field sits at the top level of the request. It is a developer/system prompt, but it is not carried forward by `previous_response_id`. You must include it on each turn if you want it to persist.

**Semantic streaming events.** Chat Completions streams `chat.completion.chunk` objects with generic delta fields. The Responses API streams named events like `response.output_text.delta`, `response.function_call_arguments.done`, and `response.web_search_call.completed`. Each event type has a specific schema.

**Built-in hosted tools.** Web search, file search, code interpreter, and MCP are first-class tool types. They run on OpenAI infrastructure and their lifecycle events appear in the stream. Chat Completions only supports function tools.

**Agentic loop design.** The output array can contain reasoning items, tool calls, and messages in a single response. Combined with `previous_response_id` for state management, this is built for multi-step agent loops.

## llama.cpp Compatibility

llama.cpp provides partial compatibility with the Responses API through an internal conversion shim. It translates Responses requests into Chat Completions format, runs the model, then formats the output back into a Responses-like structure.

**What works:**

- `POST /v1/responses` and `POST /responses` routes are registered.
- String input and message arrays.
- `input_text` and `input_image` content parts.
- Function tool definitions are converted to Chat Completions function tools.
- `max_output_tokens` is mapped to `max_tokens`.
- Streaming emits a subset of the standard SSE events.

**Streaming events emitted by llama.cpp:**

| Event | Phase |
|---|---|
| `response.created` | Start |
| `response.in_progress` | Start |
| `response.output_item.added` | Start of output |
| `response.content_part.added` | Start of content |
| `response.output_text.delta` | Text generation |
| `response.reasoning_text.delta` | Reasoning (if enabled) |
| `response.function_call_arguments.delta` | Function calls |
| `response.content_part.done` | Content done |
| `response.output_item.done` | Output done |
| `response.completed` | End |

**What does not work:**

- `previous_response_id` is not supported. There is no server-side state.
- `input_file` content parts are rejected.
- Built-in tools (`web_search`, `file_search`, `code_interpreter`, `mcp`) are skipped. Only `function` tools are converted.
- The conversion shim may not preserve all response fields exactly as the OpenAI API returns them.

## llama-swap Proxy Notes

llama-swap proxies `/v1/responses` requests to the upstream llama.cpp server. The route is registered in `proxy/proxymanager.go`.

Since llama.cpp provides the conversion shim internally, llama-swap does not need to transform the request or response itself. The request passes through as-is, and the streaming events flow back to the client unchanged.

Usage token tracking works through the `response.completed` event's `response.usage` path. llama-swap can capture these tokens the same way it tracks usage for other endpoints.
