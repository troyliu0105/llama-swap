# /v1/completions

OpenAI's legacy text completions endpoint. Given a text prompt, the model generates one or more continuations. This is the original OpenAI API surface, superseded by Chat Completions and the Responses API. It remains useful for simple text-in, text-out tasks and is supported by llama.cpp.

## Endpoint

| Property    | Value                    |
|-------------|--------------------------|
| Method      | `POST`                   |
| Path        | `/v1/completions`        |
| Content-Type | `application/json`      |

## Request Body

### Required Fields

| Field  | Type                                        | Default | Description                    |
|--------|---------------------------------------------|---------|--------------------------------|
| `model` | `string`                                   | (none)  | Model ID to use for completion |

### Optional Fields

| Field               | Type                      | Default | Description                                                         |
|----------------------|---------------------------|---------|---------------------------------------------------------------------|
| `prompt`             | `string` \| `string[]` \| `int[]` \| `int[][]` \| `null` | `""`    | The prompt text. Can be a single string, array of strings, or token arrays. |
| `suffix`             | `string` \| `null`        | `null`  | Text appended after the completion. Only supported by `gpt-3.5-turbo-instruct`. |
| `max_tokens`         | `integer` \| `null`       | `16`    | Maximum number of tokens to generate.                               |
| `temperature`        | `number` \| `null`        | `1`     | Sampling randomness. Range: 0 to 2. Higher values produce more random output. |
| `top_p`              | `number` \| `null`        | `1`     | Nucleus sampling threshold. Range: 0 to 1. Alternative to `temperature`. |
| `n`                  | `integer` \| `null`       | `1`     | Number of completions to generate. Range: 1 to 128.                 |
| `stream`             | `boolean` \| `null`       | `false` | If true, stream partial progress as server-sent events.             |
| `stream_options`     | `object` \| `null`        | `null`  | Streaming options. Supports `include_usage` and `include_obfuscation`. |
| `logprobs`           | `integer` \| `null`       | `null`  | Include log probabilities for the top N tokens. Range: 0 to 5.      |
| `echo`               | `boolean` \| `null`       | `false` | Echo the prompt back alongside the completion.                      |
| `stop`               | `string` \| `string[]` \| `null` | `null` | Up to 4 sequences where the model should stop generating.          |
| `presence_penalty`   | `number` \| `null`        | `0`     | Penalize new tokens based on whether they appear in text so far. Range: -2 to 2. |
| `frequency_penalty`  | `number` \| `null`        | `0`     | Penalize new tokens based on their frequency in text so far. Range: -2 to 2. |
| `best_of`            | `integer` \| `null`       | `1`     | Generate `best_of` completions server-side, return the best. Cannot be used with `stream`. Range: 1 to 20. |
| `logit_bias`         | `object` \| `null`        | `null`  | Map of token IDs to bias values (-100 to 100). Modifies likelihood of specific tokens. |
| `user`               | `string`                  | (none)  | Identifier for abuse monitoring.                                    |
| `seed`               | `integer` (int64) \| `null` | (none) | Hint for deterministic sampling. Same seed and params should produce similar results. |

### Example Request

```json
{
  "model": "gpt-3.5-turbo-instruct",
  "prompt": "Say this is a test",
  "max_tokens": 64,
  "temperature": 0.7,
  "top_p": 1,
  "stream": false
}
```

## Response Object

### Fields

| Field               | Type     | Description                                              |
|----------------------|----------|----------------------------------------------------------|
| `id`                 | `string` | Unique completion identifier (e.g. `cmpl-abc123`).       |
| `object`             | `string` | Always `"text_completion"`.                              |
| `created`            | `integer`| Unix timestamp of creation.                              |
| `model`              | `string` | Model ID used.                                           |
| `system_fingerprint` | `string` | Backend fingerprint for reproducibility.                 |
| `choices`            | `array`  | List of completion choices. See below.                   |
| `usage`              | `object` | Token usage statistics. See [Usage Tracking](#usage-tracking). |

### Choice Object

| Field           | Type               | Description                                                     |
|------------------|--------------------|-----------------------------------------------------------------|
| `text`           | `string`           | The generated text.                                             |
| `index`          | `integer`          | Choice index (0-based).                                         |
| `logprobs`       | `object` \| `null` | Log probability info if requested.                              |
| `finish_reason`  | `string` \| `null` | Why generation stopped. Values: `"stop"`, `"length"`, `"content_filter"`. `null` while streaming. |

### Example Response

```json
{
  "id": "cmpl-abc123def456",
  "object": "text_completion",
  "created": 1589478378,
  "model": "gpt-3.5-turbo-instruct",
  "system_fingerprint": "fp_44709d6fcb",
  "choices": [
    {
      "text": "\n\nThis is a test.",
      "index": 0,
      "logprobs": null,
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 5,
    "completion_tokens": 7,
    "total_tokens": 12,
    "completion_tokens_details": {
      "reasoning_tokens": 0,
      "accepted_prediction_tokens": 0,
      "rejected_prediction_tokens": 0,
      "audio_tokens": 0
    },
    "prompt_tokens_details": {
      "cached_tokens": 0,
      "audio_tokens": 0
    }
  }
}
```

## Streaming SSE Format

When `stream` is `true`, the server sends server-sent events (SSE). Each event is a `data:` line containing a JSON chunk, followed by a blank line. The stream terminates with `data: [DONE]`.

Each chunk contains a `choices` array where `text` holds the incremental token(s) and `finish_reason` is `null` until the final chunk for that choice.

### Example Stream

```
data: {"id":"cmpl-abc123","object":"text_completion","created":1589478378,"model":"gpt-3.5-turbo-instruct","choices":[{"text":"\n\n","index":0,"logprobs":null,"finish_reason":null}]}

data: {"id":"cmpl-abc123","object":"text_completion","created":1589478378,"model":"gpt-3.5-turbo-instruct","choices":[{"text":"This","index":0,"logprobs":null,"finish_reason":null}]}

data: {"id":"cmpl-abc123","object":"text_completion","created":1589478378,"model":"gpt-3.5-turbo-instruct","choices":[{"text":" is","index":0,"logprobs":null,"finish_reason":null}]}

data: {"id":"cmpl-abc123","object":"text_completion","created":1589478378,"model":"gpt-3.5-turbo-instruct","choices":[{"text":" a","index":0,"logprobs":null,"finish_reason":null}]}

data: {"id":"cmpl-abc123","object":"text_completion","created":1589478378,"model":"gpt-3.5-turbo-instruct","choices":[{"text":" test.","index":0,"logprobs":null,"finish_reason":"stop"}]}

data: [DONE]
```

### Streaming with Usage

When `stream_options.include_usage` is `true`, an extra chunk is sent just before `data: [DONE]`. This chunk contains an empty `choices` array and the full `usage` object for the entire request.

```
data: {"id":"cmpl-abc123","object":"text_completion","created":1589478378,"model":"gpt-3.5-turbo-instruct","choices":[{"text":" test.","index":0,"logprobs":null,"finish_reason":"stop"}]}

data: {"id":"cmpl-abc123","object":"text_completion","created":1589478378,"model":"gpt-3.5-turbo-instruct","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":7,"total_tokens":12}}

data: [DONE]
```

## Usage Tracking

The `usage` object in the response reports token consumption:

| Field                          | Type      | Description                                              |
|--------------------------------|-----------|----------------------------------------------------------|
| `prompt_tokens`                | `integer` | Tokens in the prompt.                                    |
| `completion_tokens`            | `integer` | Tokens in the generated completion(s).                   |
| `total_tokens`                 | `integer` | Sum of prompt and completion tokens.                     |
| `completion_tokens_details`    | `object`  | Breakdown of completion tokens.                          |
| `completion_tokens_details.reasoning_tokens`           | `integer` | Tokens used for reasoning.                    |
| `completion_tokens_details.accepted_prediction_tokens` | `integer` | Accepted prediction tokens.                   |
| `completion_tokens_details.rejected_prediction_tokens` | `integer` | Rejected prediction tokens.                   |
| `completion_tokens_details.audio_tokens`               | `integer` | Audio tokens in the completion.               |
| `prompt_tokens_details`        | `object`  | Breakdown of prompt tokens.                              |
| `prompt_tokens_details.cached_tokens` | `integer` | Prompt tokens served from cache.                  |
| `prompt_tokens_details.audio_tokens`  | `integer` | Audio tokens in the prompt.                      |

For non-streaming requests, `usage` appears in the final response. For streaming requests, `usage` is only included when `stream_options.include_usage` is set to `true`, and appears in the penultimate chunk (before `[DONE]`).

## Deprecation Status

The `/v1/completions` endpoint is a **legacy API**. OpenAI's final update to this endpoint was in July 2023. The following models still work with it:

- `gpt-3.5-turbo-instruct`
- `davinci-002`
- `babbage-002`

### Migration Path

New development should use one of these alternatives:

1. **Chat Completions** (`/v1/chat/completions`). Supports conversation context, system messages, tool use, and structured outputs. The closest drop-in replacement for most use cases.
2. **Responses API** (`/v1/responses`). The newest API surface, designed for multi-turn agent workflows with built-in tool use.

To migrate, restructure your `prompt` string into a `messages` array. A single-turn completion like:

```json
{
  "model": "gpt-3.5-turbo-instruct",
  "prompt": "Translate to French: Hello"
}
```

becomes:

```json
{
  "model": "gpt-4o",
  "messages": [
    {"role": "user", "content": "Translate to French: Hello"}
  ]
}
```

## llama.cpp Compatibility

llama.cpp registers both `/v1/completions` and `/completions` as valid routes for this endpoint.

### Supported Parameters

These OpenAI parameters work as expected:

- `prompt`, `max_tokens`, `temperature`, `top_p`
- `n`, `stream`, `stream_options.include_usage`
- `seed`, `logprobs`
- `presence_penalty`, `frequency_penalty`
- `stop`, `logit_bias`

### Extra Parameters

llama.cpp accepts these parameters beyond the OpenAI spec:

| Parameter          | Type      | Description                                    |
|--------------------|-----------|------------------------------------------------|
| `top_k`            | `integer` | Limit sampling to top K tokens.                |
| `min_p`            | `number`  | Minimum probability threshold for sampling.    |
| `typical_p`        | `number`  | Locally typical sampling threshold.            |
| `repeat_penalty`   | `number`  | Penalty for repeated tokens.                   |
| `mirostat`         | `integer` | Mirostat mode (0=disabled, 1, 2).              |
| `mirostat_tau`     | `number`  | Mirostat target entropy.                       |
| `mirostat_eta`     | `number`  | Mirostat learning rate.                        |
| `grammar`          | `string`  | GBNF grammar to constrain output.              |
| `json_schema`      | `object`  | JSON schema to constrain output.               |
| `cache_prompt`     | `boolean` | Cache the prompt for faster reprocessing.      |
| `return_tokens`    | `boolean` | Return individual tokens in the response.      |
| `timings_per_token`| `boolean` | Include timing info per token.                 |

### Unsupported or Ignored

These OpenAI parameters are not supported by llama.cpp and will be silently ignored:

- `suffix`
- `echo`
- `best_of`
- `user`

### Response Format

llama.cpp returns responses in the standard OpenAI format: `object` is `"text_completion"`, completions appear in `choices[].text`, and usage/token counts are reported in `usage`. The `system_fingerprint` field is populated with llama.cpp build info.

## llama-swap Proxy Notes

llama-swap proxies `/v1/completions` directly to the upstream llama.cpp server. The route is registered in `proxy/proxymanager.go` alongside the `/v1/chat/completions` endpoint.

Key points:

- **No protocol conversion.** The request body is forwarded as-is to the llama.cpp backend. The response passes back unmodified.
- **Route variants.** llama-swap also registers `/v/completions` for compatibility with clients that omit the `1` from the path prefix.
- **Model swapping.** llama-swap reads the `model` field from the request body to determine which llama.cpp process should handle the request. If the target model is not running, llama-swap starts it (potentially swapping out another model first to free resources).
- **Streaming passthrough.** SSE chunks flow through llama-swap without buffering. The proxy inspects stream chunks for metrics collection but does not modify them.
- **Inflight tracking.** The endpoint is wrapped with `trackInflight()` middleware, which keeps the target model process alive while requests are in progress.
