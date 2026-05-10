# Protocol Conversion Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add transparent OpenAI→Anthropic protocol conversion to EnhancedPeerProxy so clients can send OpenAI-format requests to Anthropic-format peer servers.

**Architecture:** Struct-based JSON conversion using `encoding/json`. Request bodies are converted (OpenAI→Anthropic) in `ProxyRequest` before forwarding. Response bodies are converted (Anthropic→OpenAI) in `ModifyResponse`. For SSE streaming, `sseConverterReader` wraps `resp.Body` — it parses Anthropic SSE events and emits OpenAI SSE chunks. Conversion is gated to `chat/completions` paths only. A peer-level `convertProtocol: true` config flag activates conversion for specific peers.

**Oracle Review:** Reviewed 2026-04-16. Fixed H1 (struct fields), H2 (body restore), H3 (error conversion), H4 (path gating), M2 (SSE dedup), M3 (dead context key), M4 (trace logging).

**Tech Stack:** Go stdlib `encoding/json`, `bufio.Scanner` for SSE parsing, existing `httputil.ReverseProxy` hooks (`Director`, `ModifyResponse`).

---

### Task 1: Add `ConvertProtocol` config field

**Files:**
- Modify: `proxy/config/peer_ext.go:11-28`

**Step 1: Add the field to ExtendedPeerConfig**

In `proxy/config/peer_ext.go`, add `ConvertProtocol` field to `ExtendedPeerConfig`:

```go
type ExtendedPeerConfig struct {
	// ... existing fields ...

	// ConvertProtocol enables OpenAI→Anthropic protocol conversion.
	// When true, requests are converted from OpenAI format to Anthropic
	// format before forwarding, and responses are converted back.
	ConvertProtocol bool `yaml:"convertProtocol"`
}
```

**Step 2: Run existing tests to verify nothing broke**

Run: `go test -v -run TestEnhancedPeerProxy ./proxy/...`
Expected: All existing tests PASS (new field has zero-value default `false`).

**Step 3: Commit**

```bash
git add proxy/config/peer_ext.go
git commit -m "config: add ConvertProtocol field to ExtendedPeerConfig"
```

---

### Task 2: Define OpenAI and Anthropic JSON structs

**Files:**
- Modify: `proxy/peerproxy_ext.go` (append after existing code, before the last closing brace if any — this file has no package-level closing brace, just append at end)

**Step 1: Add import for `encoding/json`**

Add `"encoding/json"` to the import block in `proxy/peerproxy_ext.go`.

**Step 2: Write the struct definitions**

Append these structs to `proxy/peerproxy_ext.go`:

```go
// --- Protocol conversion types ---

// openAIChatRequest represents an OpenAI /v1/chat/completions request body.
type openAIChatRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Tools       []openAITool    `json:"tools,omitempty"`
	ToolChoice  json.RawMessage `json:"tool_choice,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	MaxCompletionTokens int    `json:"max_completion_tokens,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	Stop        json.RawMessage `json:"stop,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
	StreamOptions *openAIStreamOptions `json:"stream_options,omitempty"`
	N           int             `json:"n,omitempty"`
	FrequencyPenalty *float64   `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64   `json:"presence_penalty,omitempty"`
	Seed        *int            `json:"seed,omitempty"`
}

type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

type openAIMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Name       string          `json:"name,omitempty"`
}

type openAIToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function openAIFunction   `json:"function"`
}

type openAIFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIToolDef  `json:"function"`
}

type openAIToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// openAIChatResponse represents an OpenAI /v1/chat/completions response body.
type openAIChatResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []openAIChoice `json:"choices"`
	Usage   *openAIUsage   `json:"usage,omitempty"`
}

type openAIChoice struct {
	Index        int            `json:"index"`
	Message      *openAIMessage `json:"message,omitempty"`
	Delta        *openAIMessage `json:"delta,omitempty"`
	FinishReason *string        `json:"finish_reason"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// openAISSEChunk represents a single SSE chunk in OpenAI streaming format.
type openAISSEChunk struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []openAIChoice `json:"choices"`
	Usage   *openAIUsage   `json:"usage,omitempty"`
}

// --- Anthropic types ---

// anthropicRequest represents an Anthropic /v1/messages request body.
type anthropicRequest struct {
	Model     string              `json:"model"`
	MaxTokens int                 `json:"max_tokens"`
	System    json.RawMessage     `json:"system,omitempty"`
	Messages  []anthropicMessage  `json:"messages"`
	Tools     []anthropicTool     `json:"tools,omitempty"`
	ToolChoice json.RawMessage    `json:"tool_choice,omitempty"`
	Temperature *float64          `json:"temperature,omitempty"`
	TopP      *float64            `json:"top_p,omitempty"`
	TopK      *int                `json:"top_k,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
	Stream    bool                `json:"stream,omitempty"`
	Metadata  json.RawMessage     `json:"metadata,omitempty"`
}

type anthropicMessage struct {
	Role    string               `json:"role"`
	Content json.RawMessage      `json:"content"`
}

// anthropicTextContent is a single text content block.
type anthropicTextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// anthropicToolUseContent is a tool_use content block (from assistant).
type anthropicToolUseContent struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// anthropicToolResultContent is a tool_result content block (from user).
type anthropicToolResultContent struct {
	Type      string          `json:"type"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

// anthropicTool represents a tool definition in Anthropic format.
type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// anthropicResponse represents an Anthropic /v1/messages response body.
type anthropicResponse struct {
	ID           string               `json:"id"`
	Type         string               `json:"type"`
	Role         string               `json:"role"`
	Content      []anthropicContentBlock `json:"content"`
	Model        string               `json:"model"`
	StopReason   *string              `json:"stop_reason"`
	StopSequence *string              `json:"stop_sequence,omitempty"`
	Usage        anthropicResponseUsage `json:"usage"`
}

type anthropicContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type anthropicResponseUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// --- Anthropic SSE streaming types ---

// anthropicSSEEvent represents a parsed Anthropic SSE event.
type anthropicSSEEvent struct {
	Type         string                `json:"type"`
	Index        int                   `json:"index,omitempty"`
	Delta        *anthropicSSEDelta    `json:"delta,omitempty"`
	ContentBlock *anthropicContentBlock `json:"content_block,omitempty"`
	Message      *anthropicSSEMessage  `json:"message,omitempty"`
	Usage        *anthropicResponseUsage `json:"usage,omitempty"`
}

type anthropicSSEDelta struct {
	StopReason   string `json:"stop_reason,omitempty"`
	StopSequence string `json:"stop_sequence,omitempty"`
	Text         string `json:"text,omitempty"`
	PartialJSON  string `json:"partial_json,omitempty"`
}

type anthropicSSEMessage struct {
	ID    string               `json:"id"`
	Type  string               `json:"type"`
	Role  string               `json:"role"`
	Model string               `json:"model"`
	Usage anthropicResponseUsage `json:"usage"`
}
```

**Step 3: Compile to verify no syntax errors**

Run: `go build ./proxy/...`
Expected: Builds successfully with no errors.

**Step 4: Commit**

```bash
git add proxy/peerproxy_ext.go
git commit -m "proxy: add OpenAI and Anthropic JSON struct definitions"
```

---

### Task 3: Implement message conversion (test + code)

**Files:**
- Modify: `proxy/peerproxy_ext.go` (append conversion functions)
- Modify: `proxy/peerproxy_ext_test.go` (append tests)

**Step 1: Write the failing test**

Append to `proxy/peerproxy_ext_test.go`:

```go
func TestConvertMessages_OpenAIToAnthropic(t *testing.T) {
	tests := []struct {
		name     string
		input    []openAIMessage
		expected []anthropicMessage
		system   json.RawMessage // expected extracted system prompt
	}{
		{
			name: "simple user/assistant messages",
			input: []openAIMessage{
				{Role: "user", Content: json.RawMessage(`"Hello"`)},
				{Role: "assistant", Content: json.RawMessage(`"Hi there"`)},
			},
			expected: []anthropicMessage{
				{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"Hello"}]`)},
				{Role: "assistant", Content: json.RawMessage(`[{"type":"text","text":"Hi there"}]`)},
			},
			system: nil,
		},
		{
			name: "system message extracted",
			input: []openAIMessage{
				{Role: "system", Content: json.RawMessage(`"You are helpful"`)},
				{Role: "user", Content: json.RawMessage(`"Hello"`)},
			},
			expected: []anthropicMessage{
				{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"Hello"}]`)},
			},
			system: json.RawMessage(`"You are helpful"`),
		},
		{
			name: "array content preserved",
			input: []openAIMessage{
				{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"Hello"},{"type":"text","text":"World"}]`)},
			},
			expected: []anthropicMessage{
				{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"Hello"},{"type":"text","text":"World"}]`)},
			},
			system: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msgs, system := convertMessagesOpenAIToAnthropic(tc.input)
			require.Equal(t, len(tc.expected), len(msgs), "message count mismatch")

			for i, msg := range msgs {
				assert.Equal(t, tc.expected[i].Role, msg.Role, "role mismatch at index %d", i)
				assert.JSONEq(t, string(tc.expected[i].Content), string(msg.Content), "content mismatch at index %d", i)
			}

			if tc.system == nil {
				assert.Nil(t, system)
			} else {
				assert.JSONEq(t, string(tc.system), string(system))
			}
		})
	}
}

func TestConvertMessages_AnthropicToOpenAI(t *testing.T) {
	tests := []struct {
		name     string
		input    []anthropicMessage
		system   json.RawMessage // system to inject
		expected []openAIMessage
	}{
		{
			name: "simple text messages",
			input: []anthropicMessage{
				{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"Hello"}]`)},
				{Role: "assistant", Content: json.RawMessage(`[{"type":"text","text":"Hi"}]`)},
			},
			expected: []openAIMessage{
				{Role: "user", Content: json.RawMessage(`"Hello"`)},
				{Role: "assistant", Content: json.RawMessage(`"Hi"`)},
			},
		},
		{
			name:   "system injected as first message",
			system: json.RawMessage(`"You are helpful"`),
			input: []anthropicMessage{
				{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"Hello"}]`)},
			},
			expected: []openAIMessage{
				{Role: "system", Content: json.RawMessage(`"You are helpful"`)},
				{Role: "user", Content: json.RawMessage(`"Hello"`)},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msgs := convertMessagesAnthropicToOpenAI(tc.input, tc.system)
			require.Equal(t, len(tc.expected), len(msgs), "message count mismatch")

			for i, msg := range msgs {
				assert.Equal(t, tc.expected[i].Role, msg.Role, "role mismatch at index %d", i)
				assert.JSONEq(t, string(tc.expected[i].Content), string(msg.Content), "content mismatch at index %d", i)
			}
		})
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test -v -run "TestConvertMessages" ./proxy/...`
Expected: FAIL — functions not defined yet.

**Step 3: Implement message conversion**

Append to `proxy/peerproxy_ext.go`:

```go
// convertMessagesOpenAIToAnthropic converts OpenAI messages to Anthropic format.
// It extracts the system message (if any) and returns it separately.
// OpenAI string content becomes Anthropic array content with a single text block.
func convertMessagesOpenAIToAnthropic(msgs []openAIMessage) ([]anthropicMessage, json.RawMessage) {
	var system json.RawMessage
	var result []anthropicMessage

	for _, msg := range msgs {
		if msg.Role == "system" {
			// Extract system message
			system = msg.Content
			continue
		}

		// Skip tool_calls messages for basic conversion
		if len(msg.ToolCalls) > 0 {
			// Convert tool_calls to Anthropic tool_use content blocks
			var blocks []anthropicContentBlock
			// If there's also text content, add it first
			if len(msg.Content) > 0 && string(msg.Content) != "null" {
				text := extractTextContent(msg.Content)
				if text != "" {
					blocks = append(blocks, anthropicContentBlock{Type: "text", Text: text})
				}
			}
			for _, tc := range msg.ToolCalls {
				blocks = append(blocks, anthropicContentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: json.RawMessage(tc.Function.Arguments),
				})
			}
			contentBytes, _ := json.Marshal(blocks)
			result = append(result, anthropicMessage{
				Role:    "assistant",
				Content: contentBytes,
			})
			continue
		}

		// Handle tool response messages (role=tool)
		if msg.Role == "tool" && msg.ToolCallID != "" {
			contentBytes, _ := json.Marshal([]anthropicToolResultContent{
				{
					Type:      "tool_result",
					ToolUseID: msg.ToolCallID,
					Content:   msg.Content,
				},
			})
			result = append(result, anthropicMessage{
				Role:    "user",
				Content: contentBytes,
			})
			continue
		}

		// Regular message: convert content
		anthropicContent := convertOpenAIContentToAnthropic(msg.Content)
		result = append(result, anthropicMessage{
			Role:    msg.Role,
			Content: anthropicContent,
		})
	}

	return result, system
}

// convertOpenAIContentToAnthropic converts OpenAI content to Anthropic content.
// OpenAI string content → Anthropic array with single text block.
// OpenAI array content → pass through as-is (already in block format).
func convertOpenAIContentToAnthropic(content json.RawMessage) json.RawMessage {
	if len(content) == 0 || string(content) == "null" {
		return json.RawMessage(`[]`)
	}

	// If already an array, pass through
	if content[0] == '[' {
		return content
	}

	// String content: unquote and wrap in array
	var text string
	if err := json.Unmarshal(content, &text); err != nil {
		// Not a simple string, pass through
		return content
	}

	blocks := []anthropicTextContent{{Type: "text", Text: text}}
	result, _ := json.Marshal(blocks)
	return result
}

// convertMessagesAnthropicToOpenAI converts Anthropic messages to OpenAI format.
// If system is non-nil, it's prepended as a system message.
func convertMessagesAnthropicToOpenAI(msgs []anthropicMessage, system json.RawMessage) []openAIMessage {
	var result []openAIMessage

	if system != nil {
		result = append(result, openAIMessage{
			Role:    "system",
			Content: system,
		})
	}

	for _, msg := range msgs {
		// Check for tool_use content blocks → convert to tool_calls
		var blocks []anthropicContentBlock
		if err := json.Unmarshal(msg.Content, &blocks); err == nil && len(blocks) > 0 {
			hasToolUse := false
			for _, b := range blocks {
				if b.Type == "tool_use" {
					hasToolUse = true
					break
				}
			}

			if hasToolUse {
				om := openAIMessage{Role: msg.Role}
				var toolCalls []openAIToolCall
				var texts []string
				for _, b := range blocks {
					if b.Type == "tool_use" {
						args, _ := json.Marshal(b.Input)
						toolCalls = append(toolCalls, openAIToolCall{
							ID:   b.ID,
							Type: "function",
							Function: openAIFunction{
								Name:      b.Name,
								Arguments: string(args),
							},
						})
					} else if b.Type == "text" && b.Text != "" {
						texts = append(texts, b.Text)
					}
				}
				om.ToolCalls = toolCalls
				if len(texts) > 0 {
					textBytes, _ := json.Marshal(strings.Join(texts, ""))
					om.Content = textBytes
				}
				result = append(result, om)
				continue
			}

			// Check for tool_result blocks → role=tool messages
			var toolResults []anthropicToolResultContent
			if err := json.Unmarshal(msg.Content, &toolResults); err == nil && len(toolResults) > 0 && toolResults[0].Type == "tool_result" {
				for _, tr := range toolResults {
					result = append(result, openAIMessage{
						Role:       "tool",
						Content:    tr.Content,
						ToolCallID: tr.ToolUseID,
					})
				}
				continue
			}
		}

		// Regular text message: simplify Anthropic array to OpenAI string
		content := convertAnthropicContentToOpenAI(msg.Content)
		result = append(result, openAIMessage{
			Role:    msg.Role,
			Content: content,
		})
	}

	return result
}

// convertAnthropicContentToOpenAI converts Anthropic array content to OpenAI string content.
// Single text block → string. Multiple blocks or non-text → keep as array.
func convertAnthropicContentToOpenAI(content json.RawMessage) json.RawMessage {
	if len(content) == 0 {
		return json.RawMessage(`""`)
	}

	// Try to parse as array of content blocks
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &blocks); err != nil || len(blocks) == 0 {
		return content
	}

	// Single text block → simple string
	if len(blocks) == 1 && blocks[0].Type == "text" {
		result, _ := json.Marshal(blocks[0].Text)
		return result
	}

	return content
}

// extractTextContent extracts plain text from OpenAI content.
// Handles both string and array formats.
func extractTextContent(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}

	// Try string
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}

	// Try array of text blocks
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "")
	}

	return ""
}
```

**Step 4: Run tests to verify they pass**

Run: `go test -v -run "TestConvertMessages" ./proxy/...`
Expected: PASS

**Step 5: Commit**

```bash
git add proxy/peerproxy_ext.go proxy/peerproxy_ext_test.go
git commit -m "proxy: add message conversion between OpenAI and Anthropic formats"
```

---

### Task 4: Implement request conversion (test + code)

**Files:**
- Modify: `proxy/peerproxy_ext.go` (append request conversion function)
- Modify: `proxy/peerproxy_ext_test.go` (append tests)

**Step 1: Write the failing test**

Append to `proxy/peerproxy_ext_test.go`:

```go
func TestConvertRequest_OpenAIToAnthropic(t *testing.T) {
	input := openAIChatRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []openAIMessage{
			{Role: "system", Content: json.RawMessage(`"Be helpful"`)},
			{Role: "user", Content: json.RawMessage(`"Hello"`)},
		},
		MaxTokens:   1024,
		Temperature: float64Ptr(0.7),
		TopP:        float64Ptr(0.9),
		Stream:      true,
		Tools: []openAITool{
			{
				Type: "function",
				Function: openAIToolDef{
					Name:        "get_weather",
					Description: "Get weather",
					Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
				},
			},
		},
		ToolChoice: json.RawMessage(`{"type":"function","function":{"name":"get_weather"}}`),
	}

	result, err := convertOpenAIRequestToAnthropic(input)
	require.NoError(t, err)

	assert.Equal(t, "claude-sonnet-4-20250514", result.Model)
	assert.Equal(t, 1024, result.MaxTokens)
	assert.Equal(t, float64Ptr(0.7), result.Temperature)
	assert.Equal(t, float64Ptr(0.9), result.TopP)
	assert.True(t, result.Stream)

	// System extracted
	assert.JSONEq(t, `"Be helpful"`, string(result.System))

	// Messages: only user
	require.Len(t, result.Messages, 1)
	assert.Equal(t, "user", result.Messages[0].Role)

	// Tools converted
	require.Len(t, result.Tools, 1)
	assert.Equal(t, "get_weather", result.Tools[0].Name)
	assert.Equal(t, "Get weather", result.Tools[0].Description)
	assert.JSONEq(t, `{"type":"object","properties":{"city":{"type":"string"}}}`, string(result.Tools[0].InputSchema))

	// ToolChoice converted
	assert.JSONEq(t, `{"type":"tool","name":"get_weather"}`, string(result.ToolChoice))
}

func TestConvertRequest_MaxCompletionTokens(t *testing.T) {
	input := openAIChatRequest{
		Model:               "test-model",
		Messages:            []openAIMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
		MaxCompletionTokens: 2048,
	}

	result, err := convertOpenAIRequestToAnthropic(input)
	require.NoError(t, err)
	assert.Equal(t, 2048, result.MaxTokens)
}

func TestConvertRequest_DefaultMaxTokens(t *testing.T) {
	input := openAIChatRequest{
		Model:    "test-model",
		Messages: []openAIMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	}

	result, err := convertOpenAIRequestToAnthropic(input)
	require.NoError(t, err)
	assert.Equal(t, 4096, result.MaxTokens) // default fallback
}

func TestConvertRequest_StopSequences(t *testing.T) {
	input := openAIChatRequest{
		Model:    "test-model",
		Messages: []openAIMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
		Stop:     json.RawMessage(`["STOP","END"]`),
	}

	result, err := convertOpenAIRequestToAnthropic(input)
	require.NoError(t, err)
	assert.Equal(t, []string{"STOP", "END"}, result.StopSequences)
}
```

**Step 2: Run tests to verify they fail**

Run: `go test -v -run "TestConvertRequest" ./proxy/...`
Expected: FAIL — function not defined.

**Step 3: Implement request conversion**

Append to `proxy/peerproxy_ext.go`:

```go
// float64Ptr returns a pointer to the given float64 value.
func float64Ptr(v float64) *float64 {
	return &v
}

// stringPtr returns a pointer to the given string.
func stringPtr(v string) *string {
	return &v
}

// convertAnthropicResponseToOpenAI converts an Anthropic response to OpenAI format.
func convertAnthropicResponseToOpenAI(resp anthropicResponse, model string) openAIChatResponse {
	choices := convertAnthropicContentToOpenAIChoices(resp)
	usage := &openAIUsage{
		PromptTokens:     resp.Usage.InputTokens,
		CompletionTokens: resp.Usage.OutputTokens,
		TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
	}

	return openAIChatResponse{
		ID:      "chatcmpl-" + strings.TrimPrefix(resp.ID, "msg_"),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: choices,
		Usage:   usage,
	}
}

// convertAnthropicContentToOpenAIChoices converts Anthropic content blocks
// to OpenAI choices.
func convertAnthropicContentToOpenAIChoices(resp anthropicResponse) []openAIChoice {
	var toolCalls []openAIToolCall
	var texts []string

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			texts = append(texts, block.Text)
		case "tool_use":
			args, _ := json.Marshal(block.Input)
			toolCalls = append(toolCalls, openAIToolCall{
				ID:   block.ID,
				Type: "function",
				Function: openAIFunction{
					Name:      block.Name,
					Arguments: string(args),
				},
			})
		}
	}

	var content json.RawMessage
	if len(texts) > 0 {
		joined := strings.Join(texts, "")
		content, _ = json.Marshal(joined)
	}

	msg := &openAIMessage{
		Role:      "assistant",
		Content:   content,
		ToolCalls: toolCalls,
	}

	finishReason := mapStopReason("")
	if resp.StopReason != nil {
		finishReason = mapStopReason(*resp.StopReason)
	}

	return []openAIChoice{
		{
			Index:        0,
			Message:      msg,
			FinishReason: &finishReason,
		},
	}
}

// mapStopReason maps Anthropic stop_reason to OpenAI finish_reason.
func mapStopReason(reason string) string {
	switch reason {
	case "end_turn":
		return "stop"
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	case "stop_sequence":
		return "stop"
	default:
		return "stop"
	}
}
```

**Step 4: Run tests to verify they pass**

Run: `go test -v -run "TestConvertResponse" ./proxy/...`
Expected: PASS

**Step 5: Commit**

```bash
git add proxy/peerproxy_ext.go proxy/peerproxy_ext_test.go
git commit -m "proxy: add Anthropic→OpenAI response conversion"
```

---

### Task 6: Implement SSE streaming conversion (test + code)

**Files:**
- Modify: `proxy/peerproxy_ext.go` (append SSE converter)
- Modify: `proxy/peerproxy_ext_test.go` (append tests)

**Step 1: Write the failing test**

Append to `proxy/peerproxy_ext_test.go`:

```go
func TestSSEConverter_AnthropicToOpenAI(t *testing.T) {
	// Simulate an Anthropic SSE stream and verify OpenAI SSE output
	var output bytes.Buffer

	// Build a mock Anthropic SSE stream
	anthropicEvents := []string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"id":"msg_abc","type":"message","role":"assistant","model":"claude-sonnet-4-20250514","usage":{"input_tokens":10,"output_tokens":0}}}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`,
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}`,
		`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
	}

	input := strings.Join(anthropicEvents, "\n\n") + "\n\n"

	converter := newSSEConverter(strings.NewReader(input), &output, "claude-sonnet-4-20250514")
	converter.process()

	result := output.String()
	assert.Contains(t, result, `"object":"chat.completion.chunk"`)
	assert.Contains(t, result, "Hello")
	assert.Contains(t, result, " world")
	assert.Contains(t, result, "stop")
}

func TestSSEConverter_ParsesEvents(t *testing.T) {
	raw := "event: test_event\n" +
		`data: {"type":"test_event","value":42}` + "\n\n"

	events, err := parseSSEEvents(strings.NewReader(raw))
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "test_event", events[0].eventType)
	assert.JSONEq(t, `{"type":"test_event","value":42}`, string(events[0].data))
}

func TestSSEConverter_MultipleEvents(t *testing.T) {
	raw := "event: one\n" +
		`data: {"a":1}` + "\n\n" +
		"event: two\n" +
		`data: {"b":2}` + "\n\n"

	events, err := parseSSEEvents(strings.NewReader(raw))
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, "one", events[0].eventType)
	assert.Equal(t, "two", events[1].eventType)
}
```

**Step 2: Run tests to verify they fail**

Run: `go test -v -run "TestSSEConverter" ./proxy/...`
Expected: FAIL — functions not defined.

**Step 3: Implement SSE conversion**

Append to `proxy/peerproxy_ext.go`. Add `"bufio"`, `"strconv"` to imports if not already present:

```go
// --- SSE streaming conversion ---

// sseEvent represents a parsed SSE event.
type sseEvent struct {
	eventType string
	data      json.RawMessage
}

// parseSSEEvents reads SSE events from a reader.
func parseSSEEvents(r io.Reader) ([]sseEvent, error) {
	var events []sseEvent
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var currentEvent string
	var currentData strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			// Empty line = end of event
			if currentData.Len() > 0 {
				events = append(events, sseEvent{
					eventType: currentEvent,
					data:      json.RawMessage(currentData.String()),
				})
				currentEvent = ""
				currentData.Reset()
			}
			continue
		}

		if strings.HasPrefix(line, "event:") {
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			currentData.WriteString(strings.TrimPrefix(line, "data:"))
		}
	}

	// Handle last event if no trailing newline
	if currentData.Len() > 0 {
		events = append(events, sseEvent{
			eventType: currentEvent,
			data:      json.RawMessage(currentData.String()),
		})
	}

	return events, scanner.Err()
}

	// M2 fix: sseConverter is for testing only. The production streaming converter
	// is sseConverterReader (Task 7). Both share the same event handling logic.
	// If adding new Anthropic event types, update BOTH handleEvent/processEvent methods.
	// Consider extracting a shared handleAnthropicSSEEvent() function in a refactor.

// sseConverter processes an Anthropic SSE stream and outputs OpenAI SSE chunks.
type sseConverter struct {
	scanner *bufio.Scanner
	writer  io.Writer
	model   string
	msgID   string
	created int64
	usage   openAIUsage
}

func newSSEConverter(r io.Reader, w io.Writer, model string) *sseConverter {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return &sseConverter{
		scanner: sc,
		writer:  w,
		model:   model,
		created: time.Now().Unix(),
		usage:   openAIUsage{},
	}
}

// process reads all Anthropic SSE events and writes OpenAI SSE chunks.
func (c *sseConverter) process() {
	var currentEvent string
	var currentData strings.Builder

	for c.scanner.Scan() {
		line := c.scanner.Text()

		if line == "" {
			if currentData.Len() > 0 {
				c.handleEvent(currentEvent, json.RawMessage(currentData.String()))
				currentEvent = ""
				currentData.Reset()
			}
			continue
		}

		if strings.HasPrefix(line, "event:") {
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			currentData.WriteString(strings.TrimPrefix(line, "data:"))
		}
	}

	// Handle final event
	if currentData.Len() > 0 {
		c.handleEvent(currentEvent, json.RawMessage(currentData.String()))
	}
}

func (c *sseConverter) handleEvent(eventType string, data json.RawMessage) {
	if len(data) == 0 {
		return
	}

	switch eventType {
	case "message_start":
		c.handleMessageStart(data)
	case "content_block_start":
		// No OpenAI equivalent, skip
	case "content_block_delta":
		c.handleContentBlockDelta(data)
	case "content_block_stop":
		// No OpenAI equivalent, skip
	case "message_delta":
		c.handleMessageDelta(data)
	case "message_stop":
		c.handleMessageStop()
	case "ping":
		// Skip ping events
	default:
		// Pass through unknown events unchanged
		c.writeSSEChunk(eventType, data)
	}
}

func (c *sseConverter) handleMessageStart(data json.RawMessage) {
	var event struct {
		Message struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Usage struct {
				InputTokens int `json:"input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return
	}

	c.msgID = "chatcmpl-" + strings.TrimPrefix(event.Message.ID, "msg_")
	c.usage.PromptTokens = event.Message.Usage.InputTokens

	// Emit initial chunk with role
	c.writeOpenAIChunk("", "assistant", nil, nil)
}

func (c *sseConverter) handleContentBlockDelta(data json.RawMessage) {
	var event struct {
		Index int `json:"index"`
		Delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return
	}

	switch event.Delta.Type {
	case "text_delta":
		c.writeOpenAIChunk(event.Delta.Text, "", nil, nil)
	case "input_json_delta":
		// Tool input streaming — accumulate partial JSON
		c.writeOpenAIChunk("", "", []openAIToolCall{
			{
				Index: event.Index,
				Function: openAIFunction{
					Arguments: event.Delta.PartialJSON,
				},
			},
		}, nil)
	}
}

func (c *sseConverter) handleMessageDelta(data json.RawMessage) {
	var event struct {
		Delta struct {
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Usage struct {
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return
	}

	c.usage.CompletionTokens = event.Usage.OutputTokens
	c.usage.TotalTokens = c.usage.PromptTokens + c.usage.CompletionTokens

	finishReason := mapStopReason(event.Delta.StopReason)
	usage := &openAIUsage{
		PromptTokens:     c.usage.PromptTokens,
		CompletionTokens: c.usage.CompletionTokens,
		TotalTokens:      c.usage.TotalTokens,
	}
	c.writeOpenAIChunk("", "", nil, &finishReason, usage)
}

func (c *sseConverter) handleMessageStop() {
	// Write [DONE] marker
	fmt.Fprintf(c.writer, "data: [DONE]\n\n")
}

// writeOpenAIChunk writes an OpenAI SSE chunk to the output.
func (c *sseConverter) writeOpenAIChunk(content string, role string, toolCalls []openAIToolCall, finishReason *string, extraUsage ...*openAIUsage) {
	delta := &openAIMessage{}
	if role != "" {
		delta.Role = role
		delta.Content = json.RawMessage(`null`)
	} else if content != "" {
		contentBytes, _ := json.Marshal(content)
		delta.Content = contentBytes
	}
	if len(toolCalls) > 0 {
		delta.ToolCalls = toolCalls
	}

	chunk := openAISSEChunk{
		ID:      c.msgID,
		Object:  "chat.completion.chunk",
		Created: c.created,
		Model:   c.model,
		Choices: []openAIChoice{
			{
				Index:        0,
				Delta:        delta,
				FinishReason: finishReason,
			},
		},
	}

	if len(extraUsage) > 0 && extraUsage[0] != nil {
		chunk.Usage = extraUsage[0]
	}

	data, err := json.Marshal(chunk)
	if err != nil {
		return
	}
	fmt.Fprintf(c.writer, "data: %s\n\n", string(data))
}

func (c *sseConverter) writeSSEChunk(eventType string, data json.RawMessage) {
	fmt.Fprintf(c.writer, "event: %s\ndata: %s\n\n", eventType, string(data))
}
```

**Important:** The `openAIToolCall` struct needs an `Index` field added for streaming. Update the struct:

```go
type openAIToolCall struct {
	Index    int            `json:"index,omitempty"`
	ID       string         `json:"id,omitempty"`
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}
```

**Step 4: Run tests to verify they pass**

Run: `go test -v -run "TestSSEConverter" ./proxy/...`
Expected: PASS

**Step 5: Commit**

```bash
git add proxy/peerproxy_ext.go proxy/peerproxy_ext_test.go
git commit -m "proxy: add SSE streaming conversion Anthropic→OpenAI"
```

---

### Task 7: Wire conversion into ProxyRequest and ModifyResponse

**Files:**
- Modify: `proxy/peerproxy_ext.go` (add converter field, modify ProxyRequest, ModifyResponse)
- Modify: `proxy/config/peer_ext.go` (already done in Task 1)

**Step 1: Add converter field to enhancedPeerMember**

In the `enhancedPeerMember` struct, add:

```go
type enhancedPeerMember struct {
	// ... existing fields ...
	convertProtocol bool
}
```

**Step 2: Set convertProtocol in NewEnhancedPeerProxy**

In the peer creation loop inside `NewEnhancedPeerProxy`, add:

```go
pp := &enhancedPeerMember{
	// ... existing fields ...
	convertProtocol: peer.ConvertProtocol,
}
```

**Step 3: ~~Add conversion context key~~ (M3 fix: removed — not needed)**

No context key needed. ModifyResponse checks `pp.convertProtocol` directly.

**Step 4: Modify ProxyRequest to convert outgoing requests**

In `ProxyRequest`, before `pp.reverseProxy.ServeHTTP(writer, request)`, add conversion logic:

```go
func (p *EnhancedPeerProxy) ProxyRequest(modelID string, writer http.ResponseWriter, request *http.Request) error {
	pp, found := p.proxyMap[modelID]
	if !found {
		return fmt.Errorf("no peer proxy found for model %s", modelID)
	}

	ctx := context.WithValue(request.Context(), peerCtxKey{}, &peerRequestInfo{
		model: modelID,
		reqID: generateReqID(),
		start: time.Now(),
	})
	request = request.WithContext(ctx)

	peerLog("[PEER] ▶ %s | %s | %s %s | %s\n", modelID, getPeerReqID(request), request.Method, request.URL.Path, pp.peerID)

	if pp.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+pp.apiKey)
		request.Header.Set("x-api-key", pp.apiKey)
	}

	for key, value := range pp.headers {
		if value == "" {
			request.Header.Del(key)
		} else {
			request.Header.Set(key, value)
		}
	}

	if pp.stripV1Prefix {
		request.URL.Path = strings.TrimPrefix(request.URL.Path, "/v1")
		if request.URL.Path == "" {
			request.URL.Path = "/"
		}
	}

	// --- Protocol conversion ---
	if pp.convertProtocol {
		converted, _, err := pp.convertOpenAIToAnthropicRequest(request)
		if err != nil {
			peerLog("[PEER ERROR] %s | %s | protocol_conversion_error | %s | %s\n",
				getPeerReqID(request), modelID, pp.peerID, err)
			http.Error(writer, fmt.Sprintf("protocol conversion error: %v", err), http.StatusBadRequest)
			return nil
		}
		request = converted
	}

	if pp.serveWithConcurrencyControl(writer, request) == serveHandled {
		return nil
	}

	if pp.isInBackoff() {
		pp.serveSerialized(writer, request)
		return nil
	}

	if err := pp.waitForRequestInterval(request.Context(), request.URL.Path); err != nil {
		peerLog("[PEER ERROR] %s | %s | rate_limit_cancelled | %s | %s\n",
			getPeerReqID(request), modelID, request.URL.Path, err)
		http.Error(writer, err.Error(), http.StatusServiceUnavailable)
		return nil
	}
	pp.reverseProxy.ServeHTTP(writer, request)
	pp.markRequestComplete()
	return nil
}
```

**Step 5: Add convertOpenAIToAnthropicRequest method**

```go
// isChatCompletionPath returns true for paths that should undergo protocol conversion.
func isChatCompletionPath(path string) bool {
	return path == "/v1/chat/completions" || path == "/chat/completions"
}

// convertOpenAIToAnthropicRequest reads the OpenAI request body and converts it
// to Anthropic format. Returns the modified request, whether it's streaming, and any error.
// IMPORTANT: Always restores the body on error paths to prevent data loss.
func (pp *enhancedPeerMember) convertOpenAIToAnthropicRequest(r *http.Request) (*http.Request, bool, error) {
	if r.Body == nil {
		return r, false, nil
	}

	// Only convert chat completion requests (H4: path gating)
	if !isChatCompletionPath(r.URL.Path) {
		return r, false, nil
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return r, false, fmt.Errorf("read body: %w", err)
	}
	r.Body.Close()

	// H2 fix: always restore body so error paths don't lose data
	r.Body = io.NopCloser(bytes.NewReader(body))

	var openAIReq openAIChatRequest
	if err := json.Unmarshal(body, &openAIReq); err != nil {
		return r, false, nil // Body already restored above
	}

	anthReq, err := convertOpenAIRequestToAnthropic(openAIReq)
	if err != nil {
		return r, false, fmt.Errorf("convert request: %w", err)
	}

	convertedBody, err := json.Marshal(anthReq)
	if err != nil {
		return r, false, fmt.Errorf("marshal converted request: %w", err)
	}

	// Rewrite path
	r.URL.Path = "/v1/messages"
	if pp.stripV1Prefix {
		r.URL.Path = "/messages"
	}

	r.Body = io.NopCloser(bytes.NewReader(convertedBody))
	r.ContentLength = int64(len(convertedBody))
	r.Header.Set("Content-Length", strconv.Itoa(len(convertedBody)))
	r.Header.Del("transfer-encoding")

	// Set Anthropic headers
	r.Header.Set("Content-Type", "application/json")
	if r.Header.Get("Anthropic-Version") == "" {
		r.Header.Set("Anthropic-Version", "2023-06-01")
	}

	return r, openAIReq.Stream, nil
}
```

**Step 6: Modify ModifyResponse to convert responses**

In the `ModifyResponse` closure inside `NewEnhancedPeerProxy`, add protocol conversion handling. The key change is: if `convertProtocol` is true and the response is JSON (non-SSE), convert the Anthropic response body back to OpenAI format. If SSE, wrap the writer with `sseConverterWriter`:

```go
reverseProxy.ModifyResponse = func(resp *http.Response) error {
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	isSSE := strings.Contains(contentType, "text/event-stream")
	model := getPeerModel(resp.Request)

	// --- Protocol conversion: response ---
	if pp.convertProtocol {
		if isSSE {
			// For SSE, we need to convert the streaming response
			// Wrap the body with an SSE converter
			resp.Header.Set("X-Accel-Buffering", "no")
			resp.Body = newSSEConverterReader(resp.Body, model)
			if resp.StatusCode >= 400 {
				pp.recordFailure()
			} else {
				pp.recordSuccess()
			}
			peerLog("[PEER] ◀ %s | %s | %d SSE(converted) | %s\n", getPeerReqID(resp.Request), model, resp.StatusCode, pp.peerID)
			return nil
		}

		// Non-SSE: read body, convert, replace
		var originalBody []byte // M4 fix: preserve original for trace logging
		if resp.Body != nil {
			originalBody, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
		}

		if len(originalBody) > 0 {
			if resp.StatusCode == http.StatusOK {
				// Success response: convert Anthropic → OpenAI
				var anthResp anthropicResponse
				if err := json.Unmarshal(originalBody, &anthResp); err == nil {
					openAIResp := convertAnthropicResponseToOpenAI(anthResp, model)
					convertedBody, _ := json.Marshal(openAIResp)
					resp.Body = io.NopCloser(bytes.NewReader(convertedBody))
					resp.ContentLength = int64(len(convertedBody))
					resp.Header.Set("Content-Length", strconv.Itoa(len(convertedBody)))
				} else {
					// Not valid Anthropic JSON, pass through original body
					resp.Body = io.NopCloser(bytes.NewReader(originalBody))
				}
			} else {
				// H3 fix: convert error response from Anthropic → OpenAI format
				var anthError struct {
					Type  string `json:"type"`
					Error struct {
						Type    string `json:"type"`
						Message string `json:"message"`
					} `json:"error"`
				}
				if err := json.Unmarshal(originalBody, &anthError); err == nil && anthError.Type == "error" {
					oaiError := map[string]interface{}{
						"error": map[string]interface{}{
							"message": anthError.Error.Message,
							"type":    anthError.Error.Type,
							"code":    nil,
						},
					}
					convertedBody, _ := json.Marshal(oaiError)
					resp.Body = io.NopCloser(bytes.NewReader(convertedBody))
					resp.ContentLength = int64(len(convertedBody))
					resp.Header.Set("Content-Length", strconv.Itoa(len(convertedBody)))
				} else {
					// Not Anthropic error format, pass through
					resp.Body = io.NopCloser(bytes.NewReader(originalBody))
				}
			}
		}
	}

	// ... rest of existing ModifyResponse logic (SSE handling, logging, etc.) ...
```

**Step 7: Add SSE converter reader wrapper**

For streaming, we need a reader that wraps `resp.Body` and converts on-the-fly:

```go
// sseConverterReader wraps an io.ReadCloser and converts Anthropic SSE to OpenAI SSE.
type sseConverterReader struct {
	source           io.ReadCloser
	output           *bytes.Buffer
	scanner          *bufio.Scanner
	model            string
	msgID            string
	created          int64
	usage            openAIUsage
	done             bool
	currentEventType string           // H1 fix: accumulated SSE event type
	currentData      strings.Builder  // H1 fix: accumulated SSE data lines
}

func newSSEConverterReader(source io.ReadCloser, model string) *sseConverterReader {
	sc := bufio.NewScanner(source)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return &sseConverterReader{
		source:  source,
		output:  &bytes.Buffer{},
		scanner: sc,
		model:   model,
		created: time.Now().Unix(),
		usage:   openAIUsage{},
	}
}

func (r *sseConverterReader) Read(p []byte) (int, error) {
	// If we have buffered output, return it first
	if r.output.Len() > 0 {
		return r.output.Read(p)
	}

	if r.done {
		return 0, io.EOF
	}

	// Process events until we have output or EOF
	for r.output.Len() == 0 {
		if !r.scanner.Scan() {
			r.done = true
			return 0, io.EOF
		}

		line := r.scanner.Text()

		if line == "" {
			// Process accumulated event
			if r.currentData.Len() > 0 {
				r.processEvent(r.currentEventType, json.RawMessage(r.currentData.String()))
				r.currentEventType = ""
				r.currentData.Reset()
			}
			continue
		}

		if strings.HasPrefix(line, "event:") {
			r.currentEventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			r.currentData.WriteString(strings.TrimPrefix(line, "data:"))
		}
	}

	return r.output.Read(p)
}

func (r *sseConverterReader) Close() error {
	return r.source.Close()
}

func (r *sseConverterReader) processEvent(eventType string, data json.RawMessage) {
	if len(data) == 0 {
		return
	}

	switch eventType {
	case "message_start":
		var event struct {
			Message struct {
				ID    string `json:"id"`
				Usage struct {
					InputTokens int `json:"input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal(data, &event); err == nil {
			r.msgID = "chatcmpl-" + strings.TrimPrefix(event.Message.ID, "msg_")
			r.usage.PromptTokens = event.Message.Usage.InputTokens
			r.writeChunk("", "assistant", nil, nil)
		}

	case "content_block_delta":
		var event struct {
			Index int `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		if err := json.Unmarshal(data, &event); err == nil {
			if event.Delta.Type == "text_delta" {
				r.writeChunk(event.Delta.Text, "", nil, nil)
			} else if event.Delta.Type == "input_json_delta" {
				r.writeChunk("", "", []openAIToolCall{{
					Index: event.Index,
					Function: openAIFunction{Arguments: event.Delta.PartialJSON},
				}}, nil)
			}
		}

	case "content_block_start":
		var event struct {
			Index        int                    `json:"index"`
			ContentBlock *anthropicContentBlock `json:"content_block"`
		}
		if err := json.Unmarshal(data, &event); err == nil && event.ContentBlock != nil {
			if event.ContentBlock.Type == "tool_use" {
				r.writeChunk("", "", []openAIToolCall{{
					Index: event.Index,
					ID:    event.ContentBlock.ID,
					Type:  "function",
					Function: openAIFunction{
						Name: event.ContentBlock.Name,
					},
				}}, nil)
			}
		}

	case "message_delta":
		var event struct {
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(data, &event); err == nil {
			r.usage.CompletionTokens = event.Usage.OutputTokens
			r.usage.TotalTokens = r.usage.PromptTokens + r.usage.CompletionTokens
			finishReason := mapStopReason(event.Delta.StopReason)
			usage := &openAIUsage{
				PromptTokens:     r.usage.PromptTokens,
				CompletionTokens: r.usage.CompletionTokens,
				TotalTokens:      r.usage.TotalTokens,
			}
			r.writeChunk("", "", nil, &finishReason, usage)
		}

	case "message_stop":
		fmt.Fprintf(r.output, "data: [DONE]\n\n")
	}
}

func (r *sseConverterReader) writeChunk(content string, role string, toolCalls []openAIToolCall, finishReason *string, extraUsage ...*openAIUsage) {
	delta := &openAIMessage{}
	if role != "" {
		delta.Role = role
		delta.Content = json.RawMessage(`null`)
	} else if content != "" {
		contentBytes, _ := json.Marshal(content)
		delta.Content = contentBytes
	}
	if len(toolCalls) > 0 {
		delta.ToolCalls = toolCalls
	}

	chunk := openAISSEChunk{
		ID:      r.msgID,
		Object:  "chat.completion.chunk",
		Created: r.created,
		Model:   r.model,
		Choices: []openAIChoice{
			{
				Index:        0,
				Delta:        delta,
				FinishReason: finishReason,
			},
		},
	}

	if len(extraUsage) > 0 && extraUsage[0] != nil {
		chunk.Usage = extraUsage[0]
	}

	data, err := json.Marshal(chunk)
	if err != nil {
		return
	}
	fmt.Fprintf(r.output, "data: %s\n\n", string(data))
}
```

**Note:** Add `currentEventType string` and `currentData strings.Builder` fields to `sseConverterReader` struct.

**Step 8: Compile to check for errors**

Run: `go build ./proxy/...`
Expected: Builds successfully.

**Step 9: Run all protocol conversion tests**

Run: `go test -v -run "TestConvert|TestSSEConverter" ./proxy/...`
Expected: All tests PASS.

**Step 10: Commit**

```bash
git add proxy/peerproxy_ext.go proxy/config/peer_ext.go
git commit -m "proxy: wire protocol conversion into peer proxy pipeline"
```

---

### Task 8: Write integration test

**Files:**
- Modify: `proxy/peerproxy_ext_test.go` (append integration test)

**Step 1: Write the integration test**

```go
func TestEnhancedPeerProxy_ProtocolConversion(t *testing.T) {
	// Mock Anthropic server that expects Anthropic format
	anthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/messages", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		body, _ := io.ReadAll(r.Body)
		var req anthropicRequest
		json.Unmarshal(body, &req)

		// Verify Anthropic format received
		assert.Equal(t, "user", req.Messages[0].Role)
		assert.Equal(t, 1024, req.MaxTokens)

		// Return Anthropic response
		resp := anthropicResponse{
			ID:   "msg_test",
			Type: "message",
			Role: "assistant",
			Content: []anthropicContentBlock{
				{Type: "text", Text: "Hello from Anthropic!"},
			},
			Model:      "test-model",
			StopReason: stringPtr("end_turn"),
			Usage: anthropicResponseUsage{
				InputTokens:  5,
				OutputTokens: 10,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer anthServer.Close()

	proxyURL, _ := url.Parse(anthServer.URL)
	peers := config.PeerDictionaryExtConfig{
		"anthropic-peer": config.ExtendedPeerConfig{
			Proxy:           anthServer.URL,
			ProxyURL:        proxyURL,
			Models:          []string{"test-model"},
			ConvertProtocol: true,
		},
	}

	pm, err := NewEnhancedPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	// Send OpenAI format request
	openAIReq := openAIChatRequest{
		Model:    "test-model",
		MaxTokens: 1024,
		Messages: []openAIMessage{
			{Role: "user", Content: json.RawMessage(`"Hi"`)},
		},
	}
	reqBody, _ := json.Marshal(openAIReq)

	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	captureStdout(func() {
		err = pm.ProxyRequest("test-model", w, req)
	})

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)

	// Verify OpenAI format response
	var resp openAIChatResponse
	err = json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "chat.completion", resp.Object)
	assert.Equal(t, "test-model", resp.Model)
	require.Len(t, resp.Choices, 1)
	assert.Equal(t, "assistant", resp.Choices[0].Message.Role)
	assert.JSONEq(t, `"Hello from Anthropic!"`, string(resp.Choices[0].Message.Content))
	require.NotNil(t, resp.Usage)
	assert.Equal(t, 5, resp.Usage.PromptTokens)
	assert.Equal(t, 10, resp.Usage.CompletionTokens)
}

func TestEnhancedPeerProxy_ProtocolConversion_SSE(t *testing.T) {
	// Mock Anthropic SSE server
	anthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/messages", r.URL.Path)

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)

		events := []string{
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_sse\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"test-model\",\"usage\":{\"input_tokens\":3,\"output_tokens\":0}}}",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}",
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}",
			"event: message_stop\ndata: {\"type\":\"message_stop\"}",
		}

		for _, e := range events {
			fmt.Fprintf(w, "%s\n\n", e)
			flusher.Flush()
		}
	}))
	defer anthServer.Close()

	proxyURL, _ := url.Parse(anthServer.URL)
	peers := config.PeerDictionaryExtConfig{
		"anthropic-peer": config.ExtendedPeerConfig{
			Proxy:           anthServer.URL,
			ProxyURL:        proxyURL,
			Models:          []string{"test-model"},
			ConvertProtocol: true,
		},
	}

	pm, err := NewEnhancedPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	openAIReq := openAIChatRequest{
		Model: "test-model",
		Messages: []openAIMessage{
			{Role: "user", Content: json.RawMessage(`"Hi"`)},
		},
		Stream: true,
	}
	reqBody, _ := json.Marshal(openAIReq)

	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	captureStdout(func() {
		err = pm.ProxyRequest("test-model", w, req)
	})

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)

	body := w.Body.String()
	assert.Contains(t, body, "chat.completion.chunk")
	assert.Contains(t, body, "Hi")
	assert.Contains(t, body, "[DONE]")
	assert.Equal(t, "no", w.Header().Get("X-Accel-Buffering"))
}

func TestEnhancedPeerProxy_ProtocolConversion_Disabled(t *testing.T) {
	// Verify conversion is skipped when ConvertProtocol is false
	called := false
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		// Should receive original path
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryExtConfig{
		"peer1": config.ExtendedPeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			Models:   []string{"test-model"},
			// ConvertProtocol not set (default false)
		},
	}

	pm, err := NewEnhancedPeerProxy(peers, false, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("test-model", w, req)
	require.NoError(t, err)
	assert.True(t, called)
}
```

**Step 2: Run the integration tests**

Run: `go test -v -run "TestEnhancedPeerProxy_ProtocolConversion" ./proxy/...`
Expected: PASS (may require fixing integration between the pieces from Tasks 3-7)

**Step 3: Commit**

```bash
git add proxy/peerproxy_ext_test.go
git commit -m "proxy: add integration tests for protocol conversion"
```

---

### Task 9: Full verification

**Step 1: Run all proxy tests**

Run: `go test -v ./proxy/...`
Expected: All tests PASS.

**Step 2: Run staticcheck**

Run: `make test-dev`
Expected: PASS (go test + staticcheck).

**Step 3: Run with race detection**

Run: `go test -race -v -run "TestConvert|TestSSEConverter|TestEnhancedPeerProxy_ProtocolConversion" ./proxy/...`
Expected: PASS, no race conditions.

**Step 4: Run full test suite**

Run: `make test-all`
Expected: PASS.

**Step 5: Final commit**

```bash
git add -A
git commit -m "proxy: complete protocol conversion feature with full verification"
```

---

## Summary

| Task | Description | Lines (est.) |
|------|-------------|-------------|
| 1 | Config field | ~5 |
| 2 | JSON structs | ~200 |
| 3 | Message conversion | ~150 |
| 4 | Request conversion | ~80 |
| 5 | Response conversion | ~80 |
| 6 | SSE streaming | ~200 |
| 7 | Wire into pipeline | ~150 |
| 8 | Integration tests | ~200 |
| 9 | Verification | 0 |
| **Total** | | **~1065** |

## Known Limitations (deferred)

- **Anthropic `thinking` blocks silently dropped** (L4) — extended thinking content blocks are not converted. Add `case "thinking"` to response conversion if needed.
- **Multiple system messages: last wins** (L3) — OpenAI allows interleaved system messages; only the last is extracted. Concatenation possible but low priority.
- **Anthropic-Version hardcoded** (L6) — `"2023-06-01"` works but newer features need `"2024-10-22"` or later. Make configurable if needed.
- **SSE test doesn't exercise true streaming** (L5) — `httptest.NewRecorder` buffers. Real streaming requires a custom ResponseWriter harness.
- **SSE deduplication** (M2) — `sseConverter` (test) and `sseConverterReader` (prod) duplicate event handling logic. Extract shared function in a future refactor.
