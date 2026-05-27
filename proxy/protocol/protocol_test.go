package protocol

import (
	"encoding/json"
	"testing"
)

// ---------------------------------------------------------------------------
// types_test.go
// ---------------------------------------------------------------------------

func TestParseFormat(t *testing.T) {
	tests := []struct {
		input string
		want  Format
	}{
		{"openai", FormatOpenAI},
		{"", FormatOpenAI},
		{"responses", FormatResponses},
		{"anthropic", FormatAnthropic},
		{"unknown", FormatUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := ParseFormat(tt.input); got != tt.want {
				t.Errorf("ParseFormat(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDetectClientFormat(t *testing.T) {
	tests := []struct {
		path string
		want Format
	}{
		{"/v1/chat/completions", FormatOpenAI},
		{"/v1/responses", FormatResponses},
		{"/v1/messages", FormatAnthropic},
		{"/v/chat/completions", FormatOpenAI},
		{"/v/responses", FormatResponses},
		{"/v/messages", FormatAnthropic},
		{"/v1/models", FormatUnknown},
		{"/chat/completions", FormatOpenAI},
		{"/something/else", FormatUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := DetectClientFormat(tt.path); got != tt.want {
				t.Errorf("DetectClientFormat(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestRewritePath(t *testing.T) {
	tests := []struct {
		path   string
		target Format
		want   string
	}{
		{"/v1/chat/completions", FormatResponses, "/v1/responses"},
		{"/v1/chat/completions", FormatAnthropic, "/v1/messages"},
		{"/v1/responses", FormatOpenAI, "/v1/chat/completions"},
		{"/v1/messages", FormatOpenAI, "/v1/chat/completions"},
		{"/v/responses", FormatOpenAI, "/v/chat/completions"},
		{"/v1/chat/completions", FormatUnknown, "/v1/chat/completions"},
	}
	for _, tt := range tests {
		t.Run(tt.path+"→"+string(tt.target), func(t *testing.T) {
			if got := RewritePath(tt.path, tt.target); got != tt.want {
				t.Errorf("RewritePath(%q, %q) = %q, want %q", tt.path, tt.target, got, tt.want)
			}
		})
	}
}

func TestNeedsConversion(t *testing.T) {
	if NeedsConversion(FormatOpenAI, FormatOpenAI) {
		t.Error("same format should not need conversion")
	}
	if NeedsConversion(FormatUnknown, FormatOpenAI) {
		t.Error("unknown client format should not need conversion")
	}
	if !NeedsConversion(FormatOpenAI, FormatAnthropic) {
		t.Error("different formats should need conversion")
	}
}

// ---------------------------------------------------------------------------
// Request conversion tests
// ---------------------------------------------------------------------------

func TestResponsesToOpenAIRequest(t *testing.T) {
	input := `{"model":"gpt-4","input":"Hello","instructions":"Be helpful","temperature":0.7,"max_output_tokens":100,"stream":true}`
	body, err := responsesToOpenAIRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}

	// Check model
	if req["model"] != "gpt-4" {
		t.Errorf("model = %v, want gpt-4", req["model"])
	}

	// Check messages contain system + user
	messages, ok := req["messages"].([]any)
	if !ok {
		t.Fatalf("messages is not an array")
	}
	if len(messages) != 2 {
		t.Fatalf("messages length = %d, want 2", len(messages))
	}

	sysMsg := messages[0].(map[string]any)
	if sysMsg["role"] != "system" {
		t.Errorf("first message role = %v, want system", sysMsg["role"])
	}
	if sysMsg["content"] != "Be helpful" {
		t.Errorf("first message content = %v, want 'Be helpful'", sysMsg["content"])
	}

	userMsg := messages[1].(map[string]any)
	if userMsg["role"] != "user" {
		t.Errorf("second message role = %v, want user", userMsg["role"])
	}
	if userMsg["content"] != "Hello" {
		t.Errorf("second message content = %v, want 'Hello'", userMsg["content"])
	}

	if req["temperature"] != 0.7 {
		t.Errorf("temperature = %v, want 0.7", req["temperature"])
	}
	if req["stream"] != true {
		t.Errorf("stream = %v, want true", req["stream"])
	}
	if req["max_completion_tokens"] != 100.0 {
		t.Errorf("max_completion_tokens = %v, want 100", req["max_completion_tokens"])
	}
}

func TestOpenAIToResponsesRequest(t *testing.T) {
	input := `{"model":"gpt-4","messages":[{"role":"system","content":"Be helpful"},{"role":"user","content":"Hello"}],"temperature":0.7,"max_completion_tokens":100,"stream":true}`
	body, err := openAIToResponsesRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}

	if req["model"] != "gpt-4" {
		t.Errorf("model = %v, want gpt-4", req["model"])
	}
	if req["instructions"] != "Be helpful" {
		t.Errorf("instructions = %v, want 'Be helpful'", req["instructions"])
	}
	if req["max_output_tokens"] != 100.0 {
		t.Errorf("max_output_tokens = %v, want 100", req["max_output_tokens"])
	}

	inputArr, ok := req["input"].([]any)
	if !ok {
		t.Fatalf("input is not an array")
	}
	if len(inputArr) != 1 {
		t.Fatalf("input length = %d, want 1", len(inputArr))
	}
	userMsg := inputArr[0].(map[string]any)
	if userMsg["role"] != "user" {
		t.Errorf("input[0] role = %v, want user", userMsg["role"])
	}
}

func TestAnthropicToOpenAIRequest(t *testing.T) {
	input := `{"model":"claude-3","system":"You are helpful","messages":[{"role":"user","content":"Hi"},{"role":"assistant","content":"Hello"},{"role":"user","content":"How are you?"}],"max_tokens":100,"temperature":0.5,"stream":false}`
	body, err := anthropicToOpenAIRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}

	messages, ok := req["messages"].([]any)
	if !ok {
		t.Fatalf("messages is not an array")
	}

	// system + user + assistant + user = 4
	if len(messages) != 4 {
		t.Fatalf("messages length = %d, want 4", len(messages))
	}

	sysMsg := messages[0].(map[string]any)
	if sysMsg["role"] != "system" {
		t.Errorf("first message role = %v, want system", sysMsg["role"])
	}

	if req["max_completion_tokens"] != 100.0 {
		t.Errorf("max_completion_tokens = %v, want 100", req["max_completion_tokens"])
	}
}

func TestOpenAIToAnthropicRequest(t *testing.T) {
	input := `{"model":"gpt-4","messages":[{"role":"system","content":"Be helpful"},{"role":"user","content":"Hello"}],"max_completion_tokens":100,"temperature":0.5}`
	body, err := openAIToAnthropicRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}

	if req["system"] != "Be helpful" {
		t.Errorf("system = %v, want 'Be helpful'", req["system"])
	}
	if req["max_tokens"] != 100.0 {
		t.Errorf("max_tokens = %v, want 100", req["max_tokens"])
	}

	messages, ok := req["messages"].([]any)
	if !ok {
		t.Fatalf("messages is not an array")
	}
	// Only user message, system was extracted
	if len(messages) != 1 {
		t.Fatalf("messages length = %d, want 1", len(messages))
	}
	userMsg := messages[0].(map[string]any)
	if userMsg["role"] != "user" {
		t.Errorf("message role = %v, want user", userMsg["role"])
	}
}

// ---------------------------------------------------------------------------
// Response conversion tests
// ---------------------------------------------------------------------------

func TestConvertOpenAIResponseToResponses(t *testing.T) {
	input := `{"id":"chatcmpl-123","object":"chat.completion","model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"Hello!"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`

	body, err := convertOpenAIResponseToResponses([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if resp["object"] != "response" {
		t.Errorf("object = %v, want 'response'", resp["object"])
	}
	if resp["model"] != "gpt-4" {
		t.Errorf("model = %v, want gpt-4", resp["model"])
	}
	if resp["status"] != "completed" {
		t.Errorf("status = %v, want completed", resp["status"])
	}

	output, ok := resp["output"].([]any)
	if !ok || len(output) == 0 {
		t.Fatalf("output should be a non-empty array")
	}
}

func TestConvertAnthropicResponseToOpenAI(t *testing.T) {
	input := `{"id":"msg_01","type":"message","role":"assistant","content":[{"type":"text","text":"Hello!"}],"model":"claude-3","stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`

	body, err := convertAnthropicResponseToOpenAI([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if resp["object"] != "chat.completion" {
		t.Errorf("object = %v, want 'chat.completion'", resp["object"])
	}

	choices, ok := resp["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatalf("choices should be a non-empty array")
	}

	choice := choices[0].(map[string]any)
	msg := choice["message"].(map[string]any)
	if msg["content"] != "Hello!" {
		t.Errorf("content = %v, want 'Hello!'", msg["content"])
	}
	if choice["finish_reason"] != "stop" {
		t.Errorf("finish_reason = %v, want stop", choice["finish_reason"])
	}
}

func TestConvertResponsesResponseToOpenAI(t *testing.T) {
	input := `{"id":"resp_123","object":"response","model":"gpt-4","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hi there!"}]}],"usage":{"input_tokens":10,"output_tokens":5}}`

	body, err := convertResponsesResponseToOpenAI([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if resp["object"] != "chat.completion" {
		t.Errorf("object = %v, want 'chat.completion'", resp["object"])
	}

	choices, ok := resp["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatalf("choices should be a non-empty array")
	}

	choice := choices[0].(map[string]any)
	msg := choice["message"].(map[string]any)
	if msg["content"] != "Hi there!" {
		t.Errorf("content = %v, want 'Hi there!'", msg["content"])
	}
}

// ---------------------------------------------------------------------------
// Converter integration tests
// ---------------------------------------------------------------------------

func TestConverterConvertRequest(t *testing.T) {
	conv, err := NewConverter(FormatResponses, FormatOpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	body := []byte(`{"model":"gpt-4","input":"Hello"}`)
	newBody, newPath, err := conv.ConvertRequest(body, "/v1/responses")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if newPath != "/v1/chat/completions" {
		t.Errorf("newPath = %q, want /v1/chat/completions", newPath)
	}

	var req map[string]any
	if err := json.Unmarshal(newBody, &req); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if req["model"] != "gpt-4" {
		t.Errorf("model = %v, want gpt-4", req["model"])
	}
}

func TestConverterSameFormatError(t *testing.T) {
	_, err := NewConverter(FormatOpenAI, FormatOpenAI)
	if err == nil {
		t.Error("expected error for same format conversion")
	}
}

func TestRoundTripResponsesOpenAI(t *testing.T) {
	// Responses → OpenAI → Responses
	original := `{"model":"gpt-4","input":"Hello","instructions":"Be helpful","max_output_tokens":100}`

	openaiBody, err := responsesToOpenAIRequest([]byte(original))
	if err != nil {
		t.Fatalf("responses→openai: %v", err)
	}

	// Verify it's valid OpenAI format
	var openai map[string]any
	if err := json.Unmarshal(openaiBody, &openai); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := openai["messages"]; !ok {
		t.Fatal("missing 'messages' field in OpenAI request")
	}
}

// ---------------------------------------------------------------------------
// Streaming conversion tests
// ---------------------------------------------------------------------------

func TestConvertOpenAIStreamToResponses(t *testing.T) {
	// Role chunk
	roleChunk := `{"id":"chatcmpl-123","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`
	result, err := convertOpenAIStreamToResponses([]byte(roleChunk))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result for role chunk")
	}
	// Should contain response.created
	if !contains(result, "response.created") {
		t.Errorf("expected response.created event in output: %s", string(result))
	}

	// Content chunk
	contentChunk := `{"id":"chatcmpl-123","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`
	result, err = convertOpenAIStreamToResponses([]byte(contentChunk))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(result, "response.output_text.delta") {
		t.Errorf("expected response.output_text.delta in output: %s", string(result))
	}

	// [DONE] marker
	result, err = convertOpenAIStreamToResponses([]byte("[DONE]"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(result, "response.completed") {
		t.Errorf("expected response.completed in output: %s", string(result))
	}
}

func TestConvertAnthropicStreamToOpenAI(t *testing.T) {
	// message_start
	startEvent := `{"type":"message_start","message":{"id":"msg_01","type":"message","role":"assistant","content":[],"model":"claude-3","usage":{"input_tokens":10,"output_tokens":0}}}`
	result, err := convertAnthropicStreamToOpenAI([]byte(startEvent))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(result, "chat.completion.chunk") {
		t.Errorf("expected chat.completion.chunk in output: %s", string(result))
	}

	// content_block_delta with text
	deltaEvent := `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`
	result, err = convertAnthropicStreamToOpenAI([]byte(deltaEvent))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(result, "content") {
		t.Errorf("expected content in output: %s", string(result))
	}

	// message_stop → [DONE]
	stopEvent := `{"type":"message_stop"}`
	result, err = convertAnthropicStreamToOpenAI([]byte(stopEvent))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(result) != "[DONE]" {
		t.Errorf("expected [DONE], got: %s", string(result))
	}
}

func TestConvertResponsesStreamToOpenAI(t *testing.T) {
	// text delta
	deltaEvent := `{"type":"response.output_text.delta","delta":"Hello","item_id":"msg_001","output_index":0,"content_index":0}`
	result, err := convertResponsesStreamToOpenAI([]byte(deltaEvent))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(result, "chat.completion.chunk") {
		t.Errorf("expected chat.completion.chunk in output: %s", string(result))
	}

	// completed → final chunk
	completedEvent := `{"type":"response.completed","response":{"id":"resp_123","status":"completed"}}`
	result, err = convertResponsesStreamToOpenAI([]byte(completedEvent))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(result, "finish_reason") {
		t.Errorf("expected finish_reason in output: %s", string(result))
	}
}

// ---------------------------------------------------------------------------
// Tool conversion tests
// ---------------------------------------------------------------------------

func TestResponsesToOpenAITools(t *testing.T) {
	input := `{"model":"gpt-4","input":"What's the weather?","tools":[{"type":"function","name":"get_weather","description":"Get weather","parameters":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}],"tool_choice":"auto"}`

	body, err := responsesToOpenAIRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	tools, ok := req["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %v", req["tools"])
	}

	tool := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Errorf("tool type = %v, want function", tool["type"])
	}
	fn, _ := tool["function"].(map[string]any)
	if fn["name"] != "get_weather" {
		t.Errorf("function name = %v, want get_weather", fn["name"])
	}
}

func TestAnthropicToOpenAITools(t *testing.T) {
	input := `{"model":"claude-3","messages":[{"role":"user","content":"test"}],"max_tokens":100,"tools":[{"type":"custom","name":"search","description":"Search","input_schema":{"type":"object","properties":{"q":{"type":"string"}}}}],"tool_choice":{"type":"auto"}}`

	body, err := anthropicToOpenAIRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	tools, ok := req["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %v", req["tools"])
	}

	tool := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Errorf("tool type = %v, want function", tool["type"])
	}
	fn, _ := tool["function"].(map[string]any)
	if fn["name"] != "search" {
		t.Errorf("function name = %v, want search", fn["name"])
	}

	if req["tool_choice"] != "auto" {
		t.Errorf("tool_choice = %v, want auto", req["tool_choice"])
	}
}

func TestOpenAIToAnthropicToolChoice(t *testing.T) {
	input := `{"model":"gpt-4","messages":[{"role":"user","content":"test"}],"max_completion_tokens":100,"tool_choice":"required"}`

	body, err := openAIToAnthropicRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	tc, ok := req["tool_choice"].(map[string]any)
	if !ok {
		t.Fatalf("tool_choice should be an object")
	}
	if tc["type"] != "any" {
		t.Errorf("tool_choice type = %v, want any", tc["type"])
	}
}

// ---------------------------------------------------------------------------
// Response conversion with tool calls
// ---------------------------------------------------------------------------

func TestConvertOpenAIResponseToAnthropic_WithToolUse(t *testing.T) {
	input := `{"id":"chatcmpl-123","object":"chat.completion","model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"Paris\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`

	body, err := convertOpenAIResponseToAnthropic([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if resp["stop_reason"] != "tool_use" {
		t.Errorf("stop_reason = %v, want tool_use", resp["stop_reason"])
	}

	content, ok := resp["content"].([]any)
	if !ok {
		t.Fatalf("content should be an array")
	}

	// Find tool_use block
	found := false
	for _, block := range content {
		b, _ := block.(map[string]any)
		if b["type"] == "tool_use" {
			found = true
			if b["name"] != "get_weather" {
				t.Errorf("tool name = %v, want get_weather", b["name"])
			}
		}
	}
	if !found {
		t.Error("expected tool_use block in content")
	}
}

// ---------------------------------------------------------------------------
// Converter response/stream direction tests
// ---------------------------------------------------------------------------

func TestConverterConvertResponse_Direction(t *testing.T) {
	// Client uses Responses API, upstream expects OpenAI.
	// So converter is built: From=responses, To=openai.
	// Upstream returns OpenAI format; response must convert back to Responses.
	conv, err := NewConverter(FormatResponses, FormatOpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Simulate an OpenAI response from the upstream
	openAIResp := `{"id":"chatcmpl-1","object":"chat.completion","model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`

	converted, err := conv.ConvertResponse([]byte(openAIResp))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(converted, &resp); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	// Should be in Responses format, not OpenAI
	if resp["object"] == "chat.completion" {
		t.Error("response should be in Responses format, not OpenAI")
	}
	if resp["object"] != "response" {
		t.Errorf("object = %v, want 'response'", resp["object"])
	}
}

func TestConverterConvertStreamEvent_Direction(t *testing.T) {
	// Client uses Responses API, upstream expects OpenAI.
	conv, err := NewConverter(FormatResponses, FormatOpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Simulate an OpenAI streaming chunk from the upstream
	openAIChunk := `{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4","choices":[{"index":0,"delta":{"content":"Hi"},"finish_reason":null}]}`

	converted, err := conv.ConvertStreamEvent([]byte(openAIChunk))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var chunk map[string]any
	if err := json.Unmarshal(converted, &chunk); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	// Should be in Responses format
	if chunk["type"] == nil {
		t.Error("expected 'type' field in Responses stream event")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func contains(data []byte, substr string) bool {
	return jsonStrContains(string(data), substr)
}

func jsonStrContains(s, substr string) bool {
	return len(s) >= len(substr) && containsSubstring(s, substr)
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
