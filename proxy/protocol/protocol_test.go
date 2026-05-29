package protocol

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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
		{"/api/v1/chat/completions", FormatResponses, "/api/v1/responses"},
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

	responsesBody, err := openAIToResponsesRequest(openaiBody)
	if err != nil {
		t.Fatalf("openai→responses: %v", err)
	}
	var responses map[string]any
	if err := json.Unmarshal(responsesBody, &responses); err != nil {
		t.Fatalf("invalid round-trip JSON: %v", err)
	}
	if responses["model"] != "gpt-4" {
		t.Errorf("model = %v, want gpt-4", responses["model"])
	}
	if responses["instructions"] != "Be helpful" {
		t.Errorf("instructions = %v, want Be helpful", responses["instructions"])
	}
	if responses["max_output_tokens"] != 100.0 {
		t.Errorf("max_output_tokens = %v, want 100", responses["max_output_tokens"])
	}
	input, ok := responses["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("input = %v, want one item", responses["input"])
	}
	msg := input[0].(map[string]any)
	if msg["role"] != "user" || msg["content"] != "Hello" {
		t.Errorf("input[0] = %v, want user Hello", msg)
	}
}

// ---------------------------------------------------------------------------
// Streaming conversion tests
// ---------------------------------------------------------------------------

func TestConvertOpenAIStreamToResponses(t *testing.T) {
	// Role chunk
	roleChunk := `{"id":"chatcmpl-123","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`
	result, err := convertOpenAIStreamToResponses(&Converter{}, []byte(roleChunk))
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
	result, err = convertOpenAIStreamToResponses(&Converter{}, []byte(contentChunk))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(result, "response.output_text.delta") {
		t.Errorf("expected response.output_text.delta in output: %s", string(result))
	}

	// [DONE] marker
	result, err = convertOpenAIStreamToResponses(&Converter{}, []byte("[DONE]"))
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
	result, err := convertAnthropicStreamToOpenAI(nil, []byte(startEvent))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(result, "chat.completion.chunk") {
		t.Errorf("expected chat.completion.chunk in output: %s", string(result))
	}

	// content_block_delta with text
	deltaEvent := `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`
	result, err = convertAnthropicStreamToOpenAI(nil, []byte(deltaEvent))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var contentChunk map[string]any
	if err := json.Unmarshal(result, &contentChunk); err != nil {
		t.Fatalf("failed to unmarshal content chunk: %v", err)
	}
	choices := contentChunk["choices"].([]any)
	delta := choices[0].(map[string]any)["delta"].(map[string]any)
	if delta["content"] != "Hi" {
		t.Errorf("content delta = %v, want Hi", delta["content"])
	}

	// message_stop → [DONE]
	stopEvent := `{"type":"message_stop"}`
	result, err = convertAnthropicStreamToOpenAI(nil, []byte(stopEvent))
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
	result, err := convertResponsesStreamToOpenAI(nil, []byte(deltaEvent))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(result, "chat.completion.chunk") {
		t.Errorf("expected chat.completion.chunk in output: %s", string(result))
	}

	// completed → final chunk
	completedEvent := `{"type":"response.completed","response":{"id":"resp_123","status":"completed"}}`
	result, err = convertResponsesStreamToOpenAI(nil, []byte(completedEvent))
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

func TestOpenAIToResponsesToolsPreservesToolResults(t *testing.T) {
	input := `{"model":"gpt-4","messages":[{"role":"user","content":"weather"},{"role":"assistant","content":null,"tool_calls":[{"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"Paris\"}"}}]},{"role":"tool","tool_call_id":"call_abc","content":"sunny"}]}`

	body, err := openAIToResponsesRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	items := req["input"].([]any)
	if len(items) != 4 {
		t.Fatalf("input length = %d, want 4: %v", len(items), items)
	}
	assistant := items[1].(map[string]any)
	if assistant["role"] != "assistant" {
		t.Fatalf("input[1] role = %v, want assistant", assistant["role"])
	}
	call := items[2].(map[string]any)
	if call["type"] != "function_call" || call["call_id"] != "call_abc" {
		t.Fatalf("input[2] = %v, want function_call call_abc", call)
	}
	output := items[3].(map[string]any)
	if output["type"] != "function_call_output" || output["call_id"] != "call_abc" || output["output"] != "sunny" {
		t.Fatalf("input[3] = %v, want function_call_output sunny", output)
	}
}

func TestToolChoiceObjectConversion(t *testing.T) {
	responsesInput := `{"model":"gpt-4","input":"Hi","tool_choice":{"type":"function","name":"lookup"}}`
	body, err := responsesToOpenAIRequest([]byte(responsesInput))
	if err != nil {
		t.Fatalf("responses→openai: %v", err)
	}
	var openai map[string]any
	if err := json.Unmarshal(body, &openai); err != nil {
		t.Fatalf("failed to unmarshal OpenAI request: %v", err)
	}
	choice := openai["tool_choice"].(map[string]any)
	fn := choice["function"].(map[string]any)
	if choice["type"] != "function" || fn["name"] != "lookup" {
		t.Fatalf("OpenAI tool_choice = %v, want function lookup", choice)
	}

	openAIInput := `{"model":"gpt-4","messages":[{"role":"user","content":"Hi"}],"tool_choice":{"type":"function","function":{"name":"lookup"}}}`
	body, err = openAIToResponsesRequest([]byte(openAIInput))
	if err != nil {
		t.Fatalf("openai→responses: %v", err)
	}
	var responses map[string]any
	if err := json.Unmarshal(body, &responses); err != nil {
		t.Fatalf("failed to unmarshal Responses request: %v", err)
	}
	choice = responses["tool_choice"].(map[string]any)
	if choice["type"] != "function" || choice["name"] != "lookup" {
		t.Fatalf("Responses tool_choice = %v, want function lookup", choice)
	}
}

func TestAnthropicToOpenAIMixedToolResultContent(t *testing.T) {
	input := `{"model":"claude-3","messages":[{"role":"user","content":[{"type":"text","text":"before"},{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"result"}]},{"type":"text","text":"after"}]}],"max_tokens":100}`
	body, err := anthropicToOpenAIRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	messages := req["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("messages length = %d, want 3: %v", len(messages), messages)
	}
	first := messages[0].(map[string]any)
	tool := messages[1].(map[string]any)
	last := messages[2].(map[string]any)
	if first["role"] != "user" || first["content"] != "before" {
		t.Fatalf("first message = %v, want user before", first)
	}
	if tool["role"] != "tool" || tool["tool_call_id"] != "toolu_1" || tool["content"] != "result" {
		t.Fatalf("tool message = %v, want tool result", tool)
	}
	if last["role"] != "user" || last["content"] != "after" {
		t.Fatalf("last message = %v, want user after", last)
	}
}

func TestTransformingWriterDelaysContentLengthUntilConverted(t *testing.T) {
	conv, err := NewConverter(FormatOpenAI, FormatResponses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := httptest.NewRecorder()
	w := NewTransformingWriter(r, conv)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", "999")
	w.WriteHeader(http.StatusOK)
	_, err = w.Write([]byte(`{"id":"resp_1","object":"response","model":"gpt-4","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}]}`))
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	w.Flush()

	if r.Header().Get("Content-Length") != strconv.Itoa(r.Body.Len()) {
		t.Fatalf("Content-Length = %q, want converted body length %d; body=%s", r.Header().Get("Content-Length"), r.Body.Len(), r.Body.String())
	}
	if !contains(r.Body.Bytes(), "chat.completion") || !contains(r.Body.Bytes(), "hello") {
		t.Fatalf("body was not converted to OpenAI format: %s", r.Body.String())
	}
}

func TestSchemaMappingPreservesRequestControls(t *testing.T) {
	responsesInput := `{"model":"gpt-4","input":"Hi","text":{"format":{"type":"json_object"}},"stream_options":{"include_usage":true},"parallel_tool_calls":false,"service_tier":"priority","metadata":{"trace":"abc"},"top_logprobs":3}`
	body, err := responsesToOpenAIRequest([]byte(responsesInput))
	if err != nil {
		t.Fatalf("responses→openai: %v", err)
	}
	var openai map[string]any
	if err := json.Unmarshal(body, &openai); err != nil {
		t.Fatalf("failed to unmarshal OpenAI request: %v", err)
	}
	if openai["response_format"].(map[string]any)["type"] != "json_object" {
		t.Fatalf("response_format = %v", openai["response_format"])
	}
	if openai["stream_options"].(map[string]any)["include_usage"] != true || openai["parallel_tool_calls"] != false || openai["service_tier"] != "priority" {
		t.Fatalf("request controls were not preserved: %v", openai)
	}

	openAIInput := `{"model":"gpt-4","messages":[{"role":"user","content":"Hi"}],"response_format":{"type":"json_schema","json_schema":{"name":"x"}},"user":"user-1","prompt_cache_key":"bucket"}`
	body, err = openAIToResponsesRequest([]byte(openAIInput))
	if err != nil {
		t.Fatalf("openai→responses: %v", err)
	}
	var responses map[string]any
	if err := json.Unmarshal(body, &responses); err != nil {
		t.Fatalf("failed to unmarshal Responses request: %v", err)
	}
	text := responses["text"].(map[string]any)
	format := text["format"].(map[string]any)
	if format["type"] != "json_schema" || responses["safety_identifier"] != "user-1" || responses["prompt_cache_key"] != "bucket" {
		t.Fatalf("Responses controls were not mapped: %v", responses)
	}
}

func TestSchemaMappingPreservesContentCacheAndMedia(t *testing.T) {
	responsesContent := []any{
		map[string]any{"type": "input_text", "text": "cache me", "cache_control": map[string]any{"type": "ephemeral"}},
		map[string]any{"type": "input_image", "image_data": "abc", "media_type": "image/webp", "detail": "low"},
	}
	openaiContentAny, err := convertResponsesContentToOpenAI(responsesContent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	openaiContent := openaiContentAny.([]any)
	text := openaiContent[0].(map[string]any)
	if text["cache_control"].(map[string]any)["type"] != "ephemeral" {
		t.Fatalf("cache_control was not preserved: %v", text)
	}
	image := openaiContent[1].(map[string]any)["image_url"].(map[string]any)
	if image["url"] != "data:image/webp;base64,abc" || image["detail"] != "low" {
		t.Fatalf("image metadata was not preserved: %v", image)
	}

	anthropicContent := []any{map[string]any{"type": "text", "text": "hello", "cache_control": map[string]any{"type": "ephemeral"}}}
	openaiContentAny, err = anthropicContentToOpenAI(anthropicContent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	openaiContent = openaiContentAny.([]any)
	text = openaiContent[0].(map[string]any)
	if text["cache_control"].(map[string]any)["type"] != "ephemeral" {
		t.Fatalf("Anthropic cache_control was not preserved: %v", text)
	}
}

func TestSchemaMappingPreservesUsageCacheDetails(t *testing.T) {
	openAIResp := `{"id":"chatcmpl-1","object":"chat.completion","model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":25,"total_tokens":125,"prompt_tokens_details":{"cached_tokens":40,"cache_creation_tokens":10,"audio_tokens":2},"completion_tokens_details":{"reasoning_tokens":7,"accepted_prediction_tokens":3}}}`
	body, err := convertOpenAIResponseToResponses([]byte(openAIResp))
	if err != nil {
		t.Fatalf("openai→responses response: %v", err)
	}
	var responses map[string]any
	if err := json.Unmarshal(body, &responses); err != nil {
		t.Fatalf("failed to unmarshal Responses response: %v", err)
	}
	usage := responses["usage"].(map[string]any)
	inputDetails := usage["input_tokens_details"].(map[string]any)
	outputDetails := usage["output_tokens_details"].(map[string]any)
	if usage["input_tokens"] != 100.0 || inputDetails["cached_tokens"] != 40.0 || inputDetails["cache_creation_tokens"] != 10.0 || outputDetails["reasoning_tokens"] != 7.0 {
		t.Fatalf("Responses usage did not preserve cache details: %v", usage)
	}

	body, err = convertOpenAIResponseToAnthropic([]byte(openAIResp))
	if err != nil {
		t.Fatalf("openai→anthropic response: %v", err)
	}
	var anthropic map[string]any
	if err := json.Unmarshal(body, &anthropic); err != nil {
		t.Fatalf("failed to unmarshal Anthropic response: %v", err)
	}
	usage = anthropic["usage"].(map[string]any)
	if usage["input_tokens"] != float64(50) || usage["cache_read_input_tokens"] != float64(40) || usage["cache_creation_input_tokens"] != float64(10) {
		t.Fatalf("Anthropic usage did not preserve cache split: %v", usage)
	}

	anthropicResp := `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"claude","stop_reason":"end_turn","usage":{"input_tokens":50,"output_tokens":25,"cache_creation_input_tokens":10,"cache_read_input_tokens":40,"service_tier":"standard"}}`
	body, err = convertAnthropicResponseToOpenAI([]byte(anthropicResp))
	if err != nil {
		t.Fatalf("anthropic→openai response: %v", err)
	}
	var backToOpenAI map[string]any
	if err := json.Unmarshal(body, &backToOpenAI); err != nil {
		t.Fatalf("failed to unmarshal OpenAI response: %v", err)
	}
	usage = backToOpenAI["usage"].(map[string]any)
	promptDetails := usage["prompt_tokens_details"].(map[string]any)
	if usage["prompt_tokens"] != float64(100) || promptDetails["cached_tokens"] != float64(40) || promptDetails["cache_creation_tokens"] != float64(10) || backToOpenAI["service_tier"] != "standard" {
		t.Fatalf("OpenAI usage did not preserve Anthropic cache details: response=%v usage=%v", backToOpenAI, usage)
	}
}

func TestSchemaMappingPreservesStreamingUsage(t *testing.T) {
	openAIChunk := `{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4","choices":[],"usage":{"prompt_tokens":100,"completion_tokens":25,"total_tokens":125,"prompt_tokens_details":{"cached_tokens":40},"completion_tokens_details":{"reasoning_tokens":7}}}`
	converted, err := convertOpenAIStreamToResponses(&Converter{}, []byte(openAIChunk))
	if err != nil {
		t.Fatalf("openai stream→responses: %v", err)
	}
	var evt map[string]any
	if err := json.Unmarshal(converted, &evt); err != nil {
		t.Fatalf("failed to unmarshal Responses event: %v", err)
	}
	response := evt["response"].(map[string]any)
	usage := response["usage"].(map[string]any)
	if usage["input_tokens_details"].(map[string]any)["cached_tokens"] != 40.0 {
		t.Fatalf("streaming usage cache details lost: %v", usage)
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

func mustMarshal(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("<marshal error: %v>", err)
	}
	return string(b)
}

func appendAnthropicStreamEvents(t *testing.T, events *[]map[string]any, label string, data []byte) {
	t.Helper()
	if data == nil {
		t.Fatalf("%s: expected non-nil result", label)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("%s: failed to unmarshal event %q: %v", label, line, err)
		}
		*events = append(*events, event)
	}
}

// ---------------------------------------------------------------------------
// Hardening regression tests
// ---------------------------------------------------------------------------

func TestConvertResponsesStreamToOpenAI_PassesThroughUnknownEvent(t *testing.T) {
	unknownEvt := `{"type":"custom.vendor_event","data":"something","sequence_number":42}`
	result, err := convertResponsesStreamToOpenAI(nil, []byte(unknownEvt))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("unknown Responses event was dropped, expected pass-through")
	}
	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("pass-through data is not valid JSON: %v", err)
	}
	if parsed["type"] != "custom.vendor_event" {
		t.Errorf("pass-through modified the event: %s", string(result))
	}
}

func TestConvertResponsesStreamToOpenAI_SkipsKnownNoOps(t *testing.T) {
	for _, evtType := range []string{
		"response.output_text.done",
		"response.function_call_arguments.done",
		"response.created",
		"response.in_progress",
		"response.output_item.added",
		"response.content_part.added",
		"response.content_part.done",
		"response.output_item.done",
		"response.refusal.delta",
		"response.refusal.done",
		"response.reasoning_text.delta",
		"response.reasoning_text.done",
		"response.queued",
	} {
		t.Run(evtType, func(t *testing.T) {
			evt := `{"type":"` + evtType + `"}`
			result, err := convertResponsesStreamToOpenAI(nil, []byte(evt))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != nil {
				t.Errorf("expected nil for known no-op event %s, got: %s", evtType, string(result))
			}
		})
	}
}

func TestConvertAnthropicStreamToOpenAI_PassesThroughUnknownEvent(t *testing.T) {
	unknownEvt := `{"type":"custom_event","data":"payload"}`
	result, err := convertAnthropicStreamToOpenAI(nil, []byte(unknownEvt))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("unknown Anthropic event was dropped, expected pass-through")
	}
	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("pass-through data is not valid JSON: %v", err)
	}
	if parsed["type"] != "custom_event" {
		t.Errorf("pass-through modified the event: %s", string(result))
	}
}

func TestConvertAnthropicStreamToOpenAI_SkipsKnownNoOps(t *testing.T) {
	for _, evtType := range []string{"ping", "content_block_stop", "thinking_delta"} {
		t.Run(evtType, func(t *testing.T) {
			evt := `{"type":"` + evtType + `"}`
			if evtType == "thinking_delta" {
				evt = `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}`
			}
			result, err := convertAnthropicStreamToOpenAI(nil, []byte(evt))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != nil {
				t.Errorf("expected nil for known no-op event %s, got: %s", evtType, string(result))
			}
		})
	}
}

func TestConvertOpenAIStreamToResponses_PassesThroughNoChoicesChunk(t *testing.T) {
	chunk := `{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"gpt-4","choices":[]}`
	result, err := convertOpenAIStreamToResponses(&Converter{}, []byte(chunk))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("parseable chunk with empty choices was dropped, expected pass-through")
	}
	if string(result) != chunk {
		t.Errorf("pass-through modified the chunk: %s", string(result))
	}
}

func TestConvertOpenAIStreamToAnthropic_PassesThroughNoChoicesChunk(t *testing.T) {
	chunk := `{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"gpt-4","choices":[]}`
	result, err := convertOpenAIStreamToAnthropic(&Converter{}, []byte(chunk))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("parseable chunk with empty choices was dropped, expected pass-through")
	}
	if string(result) != chunk {
		t.Errorf("pass-through modified the chunk: %s", string(result))
	}
}

func TestConvertOpenAIStreamToAnthropic_ReasoningContent(t *testing.T) {
	c := &Converter{From: FormatOpenAI, To: FormatAnthropic}
	var events []map[string]any

	appendEvents := func(label string, data []byte) {
		t.Helper()
		if data == nil {
			t.Fatalf("%s: expected non-nil result", label)
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var event map[string]any
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				t.Fatalf("%s: failed to unmarshal event %q: %v", label, line, err)
			}
			events = append(events, event)
		}
	}

	chunk1 := `{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"Let me think"},"finish_reason":null}]}`
	result, err := convertOpenAIStreamToAnthropic(c, []byte(chunk1))
	if err != nil {
		t.Fatalf("chunk1: unexpected error: %v", err)
	}
	appendEvents("chunk1", result)

	chunk2 := `{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":" about this"},"finish_reason":null}]}`
	result, err = convertOpenAIStreamToAnthropic(c, []byte(chunk2))
	if err != nil {
		t.Fatalf("chunk2: unexpected error: %v", err)
	}
	appendEvents("chunk2", result)

	chunk3 := `{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello!"},"finish_reason":null}]}`
	result, err = convertOpenAIStreamToAnthropic(c, []byte(chunk3))
	if err != nil {
		t.Fatalf("chunk3: unexpected error: %v", err)
	}
	appendEvents("chunk3", result)

	chunk4 := `{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`
	result, err = convertOpenAIStreamToAnthropic(c, []byte(chunk4))
	if err != nil {
		t.Fatalf("chunk4: unexpected error: %v", err)
	}
	appendEvents("chunk4", result)

	doneResult, err := convertOpenAIStreamToAnthropic(c, []byte(`[DONE]`))
	if err != nil {
		t.Fatalf("done: unexpected error: %v", err)
	}
	appendEvents("done", doneResult)

	wantTypes := []string{
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_delta",
		"content_block_stop",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
	}
	if len(events) != len(wantTypes) {
		t.Fatalf("expected %d events, got %d: %#v", len(wantTypes), len(events), events)
	}
	for i, wantType := range wantTypes {
		if events[i]["type"] != wantType {
			t.Fatalf("event %d type = %v, want %s: %#v", i, events[i]["type"], wantType, events[i])
		}
	}
	if count := strings.Count(string(result), "message_start"); count != 0 {
		t.Fatalf("finish chunk unexpectedly repeated message_start: %s", string(result))
	}

	thinkingStart, _ := events[1]["content_block"].(map[string]any)
	if events[1]["index"] != float64(0) || thinkingStart["type"] != "thinking" || thinkingStart["thinking"] != "" {
		t.Errorf("thinking start event mismatch: %#v", events[1])
	}
	firstThinkingDelta, _ := events[2]["delta"].(map[string]any)
	if events[2]["index"] != float64(0) || firstThinkingDelta["type"] != "thinking_delta" || firstThinkingDelta["thinking"] != "Let me think" {
		t.Errorf("first thinking delta mismatch: %#v", events[2])
	}
	secondThinkingDelta, _ := events[3]["delta"].(map[string]any)
	if events[3]["index"] != float64(0) || secondThinkingDelta["type"] != "thinking_delta" || secondThinkingDelta["thinking"] != " about this" {
		t.Errorf("second thinking delta mismatch: %#v", events[3])
	}
	if events[4]["index"] != float64(0) {
		t.Errorf("thinking stop event mismatch: %#v", events[4])
	}

	textStart, _ := events[5]["content_block"].(map[string]any)
	if events[5]["index"] != float64(1) || textStart["type"] != "text" || textStart["text"] != "" {
		t.Errorf("text start event mismatch: %#v", events[5])
	}
	textDelta, _ := events[6]["delta"].(map[string]any)
	if events[6]["index"] != float64(1) || textDelta["type"] != "text_delta" || textDelta["text"] != "Hello!" {
		t.Errorf("text delta mismatch: %#v", events[6])
	}
	if events[7]["index"] != float64(1) {
		t.Errorf("text stop event mismatch: %#v", events[7])
	}
	messageDelta, _ := events[8]["delta"].(map[string]any)
	if messageDelta["stop_reason"] != "end_turn" {
		t.Errorf("message_delta stop_reason = %v, want end_turn: %#v", messageDelta["stop_reason"], events[8])
	}

	allEventsJSON, _ := json.Marshal(events)
	if count := strings.Count(string(allEventsJSON), "message_start"); count != 1 {
		t.Errorf("expected exactly 1 message_start, got %d: %s", count, string(allEventsJSON))
	}
	if count := strings.Count(string(allEventsJSON), "thinking_delta"); count != 2 {
		t.Errorf("expected exactly 2 thinking_delta events, got %d: %s", count, string(allEventsJSON))
	}
}

func TestTransformingWriterPreservesSSEMetadataForConvertedData(t *testing.T) {
	// Client wants Responses format, upstream is OpenAI.
	conv, err := NewConverter(FormatResponses, FormatOpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := httptest.NewRecorder()
	w := NewTransformingWriter(r, conv)
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)

	// Upstream sends OpenAI SSE with event type + data lines
	sse := "event: message_start\n" +
		"data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n"
	w.Write([]byte(sse))
	w.Flush()

	body := r.Body.String()
	// The data line should produce converted output with response.created
	if !containsSubstring(body, "response.created") {
		t.Fatalf("expected converted content in output, got: %q", body)
	}
	// The event: metadata line should be preserved
	if !containsSubstring(body, "event:") {
		t.Fatalf("expected SSE event metadata preserved, got: %q", body)
	}
}

func TestTransformingWriterAcceptsDataLineWithoutSpace(t *testing.T) {
	// Client wants Responses format, upstream is OpenAI.
	conv, err := NewConverter(FormatResponses, FormatOpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := httptest.NewRecorder()
	w := NewTransformingWriter(r, conv)
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)

	// "data:" without trailing space (valid per SSE spec)
	sse := "data:{\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hi\"},\"finish_reason\":null}]}\n\n"
	w.Write([]byte(sse))
	w.Flush()

	body := r.Body.String()
	if !containsSubstring(body, "response.output_text.delta") {
		t.Fatalf("expected conversion of data: without space, got: %q", body)
	}
}

func TestTransformingWriterReturnsBadGatewayOnConversionError(t *testing.T) {
	conv := &Converter{From: FormatOpenAI, To: FormatUnknown}
	r := httptest.NewRecorder()
	w := NewTransformingWriter(r, conv)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	_, err := w.Write([]byte(`{"id":"resp_1","object":"response","output":[]}`))
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	w.Flush()

	if r.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d with body %q", r.Code, r.Body.String())
	}
	if !containsSubstring(r.Body.String(), "response conversion failed") {
		t.Fatalf("expected conversion error body, got %q", r.Body.String())
	}
	if containsSubstring(r.Body.String(), "resp_1") {
		t.Fatalf("should not return original upstream body on conversion error: %q", r.Body.String())
	}
}

func TestTransformingWriterBadGatewayWroteBody(t *testing.T) {
	// Verify that writeHeadlineBadGateway sets wroteBody and prevents
	// Flush from double-writing, and that Content-Length is correct.
	conv, err := NewConverter(FormatOpenAI, FormatResponses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := httptest.NewRecorder()
	w := NewTransformingWriter(r, conv)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// Buffer some body
	w.buf.WriteString(`{"id":"x"}`)
	// Now force bad gateway
	w.writeHeadlineBadGateway("conversion failed: test error")
	// Flush should be a no-op since wroteBody is set
	w.Flush()

	if r.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", r.Code)
	}
	if r.Body.String() != "conversion failed: test error" {
		t.Errorf("body = %q, want %q", r.Body.String(), "conversion failed: test error")
	}
	cl := r.Header().Get("Content-Length")
	if cl != strconv.Itoa(len("conversion failed: test error")) {
		t.Errorf("Content-Length = %q, want %d", cl, len("conversion failed: test error"))
	}
}

func TestSSEMetadataPreservedWhenDataEmits(t *testing.T) {
	// Client wants Responses, upstream is OpenAI.
	conv, err := NewConverter(FormatResponses, FormatOpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := httptest.NewRecorder()
	w := NewTransformingWriter(r, conv)
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)

	// event: + data: where data converts to non-nil output
	sse := "event: chunk\n" +
		"data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hi\"},\"finish_reason\":null}]}\n\n"
	w.Write([]byte(sse))
	w.Flush()

	body := r.Body.String()
	if !containsSubstring(body, "response.output_text.delta") {
		t.Fatalf("expected converted data, got: %q", body)
	}
	if !containsSubstring(body, "event: chunk") {
		t.Fatalf("metadata should be preserved when data emits, got: %q", body)
	}
}

func TestSSEMetadataNotEmittedWhenDataSkipped(t *testing.T) {
	// Client wants Responses, upstream is OpenAI.
	// Send an event: + data: where the data line produces nil (skipped event).
	conv, err := NewConverter(FormatResponses, FormatOpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := httptest.NewRecorder()
	w := NewTransformingWriter(r, conv)
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)

	// Send a chunk with no choices and no usage — the converter returns the
	// raw data (pass-through, not nil). We need a chunk that actually
	// produces nil. Use an OpenAI chunk where delta is empty and no
	// finish_reason — the converter returns nil for these.
	// Actually, with our fix, no-choices no-usage passes through.
	// To get nil, send a chunk where choices[0] has delta=null.
	sse := "event: skip_me\n" +
		"data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":null,\"finish_reason\":null}]}\n\n"
	w.Write([]byte(sse))
	w.Flush()

	body := r.Body.String()
	if containsSubstring(body, "skip_me") {
		t.Fatalf("metadata should NOT be emitted when data is skipped, got: %q", body)
	}
}

func TestSSEAcceptsMetadataWithoutTrailingSpace(t *testing.T) {
	conv, err := NewConverter(FormatResponses, FormatOpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := httptest.NewRecorder()
	w := NewTransformingWriter(r, conv)
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)

	// "event:" without trailing space, then data line
	sse := "event:chunk\n" +
		"data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"X\"},\"finish_reason\":null}]}\n\n"
	w.Write([]byte(sse))
	w.Flush()

	body := r.Body.String()
	if !containsSubstring(body, "response.output_text.delta") {
		t.Fatalf("expected converted data, got: %q", body)
	}
	if !containsSubstring(body, "event:chunk") {
		t.Fatalf("metadata without space should be preserved, got: %q", body)
	}
}

func TestSSECommentPreservedWhenDataEmits(t *testing.T) {
	conv, err := NewConverter(FormatResponses, FormatOpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := httptest.NewRecorder()
	w := NewTransformingWriter(r, conv)
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)

	sse := ": keep this comment\n" +
		"data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Y\"},\"finish_reason\":null}]}\n\n"
	w.Write([]byte(sse))
	w.Flush()

	body := r.Body.String()
	if !containsSubstring(body, "response.output_text.delta") {
		t.Fatalf("expected converted data, got: %q", body)
	}
	if !containsSubstring(body, ": keep this comment") {
		t.Fatalf("comment should be preserved when data emits, got: %q", body)
	}
}

func TestSSECommentNotEmittedWhenDataSkipped(t *testing.T) {
	conv, err := NewConverter(FormatResponses, FormatOpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := httptest.NewRecorder()
	w := NewTransformingWriter(r, conv)
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)

	sse := ": drop this comment\n" +
		"data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":null,\"finish_reason\":null}]}\n\n"
	w.Write([]byte(sse))
	w.Flush()

	body := r.Body.String()
	if containsSubstring(body, "drop this comment") {
		t.Fatalf("comment should NOT be emitted when data is skipped, got: %q", body)
	}
}

func TestSSEMetadataOnlyFrameIsNotEmitted(t *testing.T) {
	conv, err := NewConverter(FormatResponses, FormatOpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := httptest.NewRecorder()
	w := NewTransformingWriter(r, conv)
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)

	sse := "event: metadata_only\n" +
		"id: evt_1\n\n"
	w.Write([]byte(sse))
	w.Flush()

	body := r.Body.String()
	if containsSubstring(body, "metadata_only") || containsSubstring(body, "evt_1") {
		t.Fatalf("metadata-only frame should not be emitted, got: %q", body)
	}
}

func TestSSEDataLineWithoutSpace(t *testing.T) {
	conv, err := NewConverter(FormatResponses, FormatOpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := httptest.NewRecorder()
	w := NewTransformingWriter(r, conv)
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)

	// "data:" without space after colon (valid per SSE spec)
	sse := "data:{\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Z\"},\"finish_reason\":null}]}\n\n"
	w.Write([]byte(sse))
	w.Flush()

	body := r.Body.String()
	if !containsSubstring(body, "response.output_text.delta") {
		t.Fatalf("expected conversion of data: without space, got: %q", body)
	}
}

func TestUnsupportedContentTypeResponsesToOpenAI(t *testing.T) {
	input := `{"model":"gpt-4","input":[{"type":"message","role":"user","content":[{"type":"audio","data":"..."}]}]}`
	_, err := responsesToOpenAIRequest([]byte(input))
	if err == nil {
		t.Fatal("expected error for unsupported content type, got nil")
	}
	if !containsSubstring(err.Error(), "unsupported") {
		t.Errorf("error = %q, want mention of unsupported", err.Error())
	}
}

func TestUnsupportedContentTypeOpenAIToAnthropic(t *testing.T) {
	input := `{"model":"gpt-4","messages":[{"role":"user","content":[{"type":"video","url":"..."}]}],"max_completion_tokens":100}`
	_, err := openAIToAnthropicRequest([]byte(input))
	if err == nil {
		t.Fatal("expected error for unsupported content type, got nil")
	}
	if !containsSubstring(err.Error(), "unsupported") {
		t.Errorf("error = %q, want mention of unsupported", err.Error())
	}
}

func TestUnsupportedContentTypeAnthropicToOpenAI(t *testing.T) {
	input := `{"model":"claude-3","messages":[{"role":"user","content":[{"type":"audio","data":"..."}]}],"max_tokens":100}`
	_, err := anthropicToOpenAIRequest([]byte(input))
	if err == nil {
		t.Fatal("expected error for unsupported content type, got nil")
	}
	if !containsSubstring(err.Error(), "unsupported") {
		t.Errorf("error = %q, want mention of unsupported", err.Error())
	}
}

func TestUnsupportedContentTypeOpenAIToResponses(t *testing.T) {
	input := `{"model":"gpt-4","messages":[{"role":"user","content":[{"type":"video_file","url":"..."}]}]}`
	_, err := openAIToResponsesRequest([]byte(input))
	if err == nil {
		t.Fatal("expected error for unsupported content type, got nil")
	}
	if !containsSubstring(err.Error(), "unsupported") {
		t.Errorf("error = %q, want mention of unsupported", err.Error())
	}
}

func TestMalformedToolMissingFunction(t *testing.T) {
	// OpenAI → Responses with a function tool missing the function object
	input := `{"model":"gpt-4","messages":[{"role":"user","content":"test"}],"tools":[{"type":"function","name":"search"}]}`
	_, err := openAIToResponsesRequest([]byte(input))
	if err == nil {
		t.Fatal("expected error for malformed tool, got nil")
	}
	if !containsSubstring(err.Error(), "non-object function") {
		t.Errorf("error = %q, want mention of non-object function", err.Error())
	}

	// OpenAI → Anthropic with same issue
	input2 := `{"model":"gpt-4","messages":[{"role":"user","content":"test"}],"max_completion_tokens":100,"tools":[{"type":"function","name":"search"}]}`
	_, err = openAIToAnthropicRequest([]byte(input2))
	if err == nil {
		t.Fatal("expected error for malformed tool, got nil")
	}
	if !containsSubstring(err.Error(), "non-object function") {
		t.Errorf("error = %q, want mention of non-object function", err.Error())
	}
}

func TestConvertOpenAIStreamToResponses_ReasoningContent(t *testing.T) {
	// Simulate zhipu-style stream where every chunk includes role: "assistant"
	// along with reasoning_content. Start events must be emitted exactly once.
	c := &Converter{From: FormatOpenAI, To: FormatResponses}

	// Chunk 1: role + reasoning_content (first chunk)
	chunk1 := `{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"Let me think"},"finish_reason":null}]}`
	result, err := convertOpenAIStreamToResponses(c, []byte(chunk1))
	if err != nil {
		t.Fatalf("chunk1: unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("chunk1: expected non-nil result")
	}
	// Must contain start events AND reasoning delta
	if !contains(result, "response.created") {
		t.Errorf("chunk1: missing response.created: %s", string(result))
	}
	if !contains(result, "response.output_text.delta") {
		t.Errorf("chunk1: missing output_text.delta for reasoning: %s", string(result))
	}
	if !contains(result, "Let me think") {
		t.Errorf("chunk1: missing reasoning content: %s", string(result))
	}
	// Count start events — must be exactly one of each
	if count := strings.Count(string(result), "response.created"); count != 1 {
		t.Errorf("chunk1: expected 1 response.created, got %d", count)
	}

	// Chunk 2: role + reasoning_content (subsequent — no start events)
	chunk2 := `{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":" about this"},"finish_reason":null}]}`
	result, err = convertOpenAIStreamToResponses(c, []byte(chunk2))
	if err != nil {
		t.Fatalf("chunk2: unexpected error: %v", err)
	}
	if contains(result, "response.created") {
		t.Errorf("chunk2: unexpected response.created (should not repeat): %s", string(result))
	}
	if !contains(result, "response.output_text.delta") {
		t.Errorf("chunk2: missing output_text.delta: %s", string(result))
	}
	if !contains(result, " about this") {
		t.Errorf("chunk2: missing reasoning content: %s", string(result))
	}

	// Chunk 3: role + content (actual output starts)
	chunk3 := `{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello!"},"finish_reason":null}]}`
	result, err = convertOpenAIStreamToResponses(c, []byte(chunk3))
	if err != nil {
		t.Fatalf("chunk3: unexpected error: %v", err)
	}
	if contains(result, "response.created") {
		t.Errorf("chunk3: unexpected response.created: %s", string(result))
	}
	if !contains(result, "Hello!") {
		t.Errorf("chunk3: missing content: %s", string(result))
	}

	// Chunk 4: finish with usage
	chunk4 := `{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`
	result, err = convertOpenAIStreamToResponses(c, []byte(chunk4))
	if err != nil {
		t.Fatalf("chunk4: unexpected error: %v", err)
	}
	if !contains(result, "response.content_part.done") {
		t.Errorf("chunk4: missing content_part.done: %s", string(result))
	}
	if !contains(result, "response.output_item.done") {
		t.Errorf("chunk4: missing output_item.done: %s", string(result))
	}
	if !contains(result, "response.completed") {
		t.Errorf("chunk4: missing response.completed (usage should trigger it): %s", string(result))
	}
	if !contains(result, "input_tokens") {
		t.Errorf("chunk4: missing usage data in response.completed: %s", string(result))
	}
}

func TestConvertOpenAIStreamToAnthropic_ToolCalls(t *testing.T) {
	// Scenario: thinking + text + tool_use
	c := &Converter{From: FormatOpenAI, To: FormatAnthropic}
	var events []map[string]any

	chunks := []string{
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"Let me think"},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","content":"I'll search for you."},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_abc123","type":"function","function":{"name":"web_search","arguments":"{\"query\":"}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"function":{"arguments":"\"latest news\"}"}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`,
		`[DONE]`,
	}
	for i, chunk := range chunks {
		result, err := convertOpenAIStreamToAnthropic(c, []byte(chunk))
		if err != nil {
			t.Fatalf("chunk%d: unexpected error: %v", i+1, err)
		}
		appendAnthropicStreamEventsIfAny(t, &events, fmt.Sprintf("chunk%d", i+1), result)
	}

	wantTypes := []string{
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
	}
	assertEventTypes(t, events, wantTypes)

	toolStart, _ := events[7]["content_block"].(map[string]any)
	if events[7]["index"] != float64(2) || toolStart["type"] != "tool_use" {
		t.Errorf("tool_use start mismatch: %s", mustMarshal(events[7]))
	}
	if toolStart["id"] != "call_abc123" || toolStart["name"] != "web_search" {
		t.Errorf("tool_use id/name mismatch: %s", mustMarshal(events[7]))
	}
	toolInput, _ := toolStart["input"].(map[string]any)
	if len(toolInput) != 0 {
		t.Errorf("tool_use block input = %v, want empty object: %s", toolInput, mustMarshal(events[7]))
	}
	toolDelta, _ := events[8]["delta"].(map[string]any)
	if events[8]["index"] != float64(2) || toolDelta["type"] != "input_json_delta" || toolDelta["partial_json"] != `{"query":"latest news"}` {
		t.Errorf("tool delta mismatch: %s", mustMarshal(events[8]))
	}
	messageDelta, _ := events[10]["delta"].(map[string]any)
	if messageDelta["stop_reason"] != "tool_use" {
		t.Errorf("stop_reason = %v, want tool_use: %s", messageDelta["stop_reason"], mustMarshal(events[10]))
	}
}

func TestConvertOpenAIStreamToAnthropic_ToolCallsTextOnly(t *testing.T) {
	// Scenario: text + tool_use (no thinking)
	c := &Converter{From: FormatOpenAI, To: FormatAnthropic}
	var events []map[string]any

	chunks := []string{
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","content":"Searching now."},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_xyz","type":"function","function":{"name":"search","arguments":"{\"q\":"}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	for i, chunk := range chunks {
		result, err := convertOpenAIStreamToAnthropic(c, []byte(chunk))
		if err != nil {
			t.Fatalf("chunk%d: unexpected error: %v", i+1, err)
		}
		appendAnthropicStreamEventsIfAny(t, &events, fmt.Sprintf("chunk%d", i+1), result)
	}

	wantTypes := []string{
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
	}
	assertEventTypes(t, events, wantTypes)

	toolStart, _ := events[4]["content_block"].(map[string]any)
	if events[4]["index"] != float64(1) || toolStart["type"] != "tool_use" {
		t.Errorf("tool_use start mismatch: %s", mustMarshal(events[4]))
	}
	toolDelta, _ := events[5]["delta"].(map[string]any)
	if events[5]["index"] != float64(1) || toolDelta["type"] != "input_json_delta" || toolDelta["partial_json"] != `{"q":` {
		t.Errorf("tool delta mismatch: %s", mustMarshal(events[5]))
	}
}

func TestConvertOpenAIStreamToAnthropic_ToolCallsOnly(t *testing.T) {
	// Scenario: tool_use only (no thinking, no text)
	c := &Converter{From: FormatOpenAI, To: FormatAnthropic}
	var events []map[string]any

	chunks := []string{
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_direct","type":"function","function":{"name":"lookup","arguments":"{\"key\":"}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	for i, chunk := range chunks {
		result, err := convertOpenAIStreamToAnthropic(c, []byte(chunk))
		if err != nil {
			t.Fatalf("chunk%d: unexpected error: %v", i+1, err)
		}
		appendAnthropicStreamEventsIfAny(t, &events, fmt.Sprintf("chunk%d", i+1), result)
	}

	wantTypes := []string{
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
	}
	assertEventTypes(t, events, wantTypes)

	toolStart, _ := events[1]["content_block"].(map[string]any)
	if events[1]["index"] != float64(0) || toolStart["type"] != "tool_use" {
		t.Errorf("tool_use start mismatch: %s", mustMarshal(events[1]))
	}
	if toolStart["name"] != "lookup" || toolStart["id"] != "call_direct" {
		t.Errorf("tool_use id/name mismatch: %s", mustMarshal(events[1]))
	}
}

func TestConvertOpenAIStreamToAnthropic_ToolCallsMissingID(t *testing.T) {
	// Verify missing tool call id gets a fallback
	c := &Converter{From: FormatOpenAI, To: FormatAnthropic}
	var events []map[string]any

	chunks := []string{
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"type":"function","function":{"name":"search","arguments":"{}"}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	for i, chunk := range chunks {
		result, err := convertOpenAIStreamToAnthropic(c, []byte(chunk))
		if err != nil {
			t.Fatalf("chunk%d: unexpected error: %v", i+1, err)
		}
		appendAnthropicStreamEventsIfAny(t, &events, fmt.Sprintf("chunk%d", i+1), result)
	}

	for _, evt := range events {
		if evt["type"] == "content_block_start" {
			block, _ := evt["content_block"].(map[string]any)
			if block["type"] == "tool_use" {
				if block["id"] != "toolu_protocol" {
					t.Errorf("missing id fallback: got %v, want toolu_protocol: %s", block["id"], mustMarshal(evt))
				}
				return
			}
		}
	}
	t.Fatal("no tool_use content_block_start found in events")
}

func TestConvertOpenAIStreamToAnthropic_MultipleToolCalls(t *testing.T) {
	c := &Converter{From: FormatOpenAI, To: FormatAnthropic}
	var events []map[string]any

	chunks := []string{
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_weather","type":"function","function":{"name":"get_weather","arguments":"{\"city\":"}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"function":{"arguments":"\"Paris\"}"}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":1,"id":"call_time","type":"function","function":{"name":"get_time","arguments":"{\"zone\":\"UTC\"}"}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	for i, chunk := range chunks {
		result, err := convertOpenAIStreamToAnthropic(c, []byte(chunk))
		if err != nil {
			t.Fatalf("chunk%d: unexpected error: %v", i+1, err)
		}
		appendAnthropicStreamEventsIfAny(t, &events, fmt.Sprintf("chunk%d", i+1), result)
	}

	wantTypes := []string{
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
	}
	assertEventTypes(t, events, wantTypes)

	firstToolStart, _ := events[1]["content_block"].(map[string]any)
	secondToolStart, _ := events[4]["content_block"].(map[string]any)
	if events[1]["index"] != float64(0) || firstToolStart["type"] != "tool_use" || firstToolStart["name"] != "get_weather" {
		t.Errorf("first tool start mismatch: %s", mustMarshal(events[1]))
	}
	if events[4]["index"] != float64(1) || secondToolStart["type"] != "tool_use" || secondToolStart["name"] != "get_time" {
		t.Errorf("second tool start mismatch: %s", mustMarshal(events[4]))
	}
	firstDelta, _ := events[2]["delta"].(map[string]any)
	secondDelta, _ := events[5]["delta"].(map[string]any)
	if firstDelta["partial_json"] != `{"city":"Paris"}` {
		t.Errorf("first tool args mismatch: %s", mustMarshal(events[2]))
	}
	if secondDelta["partial_json"] != `{"zone":"UTC"}` {
		t.Errorf("second tool args mismatch: %s", mustMarshal(events[5]))
	}
	messageDelta, _ := events[7]["delta"].(map[string]any)
	if messageDelta["stop_reason"] != "tool_use" {
		t.Errorf("stop_reason = %v, want tool_use: %s", messageDelta["stop_reason"], mustMarshal(events[7]))
	}
}

func TestConvertOpenAIStreamToAnthropic_TextAfterToolCalls(t *testing.T) {
	c := &Converter{From: FormatOpenAI, To: FormatAnthropic}
	var events []map[string]any

	chunks := []string{
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_search","type":"function","function":{"name":"search","arguments":"{}"}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","content":"Found it."},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	for i, chunk := range chunks {
		result, err := convertOpenAIStreamToAnthropic(c, []byte(chunk))
		if err != nil {
			t.Fatalf("chunk%d: unexpected error: %v", i+1, err)
		}
		appendAnthropicStreamEventsIfAny(t, &events, fmt.Sprintf("chunk%d", i+1), result)
	}

	wantTypes := []string{
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
	}
	assertEventTypes(t, events, wantTypes)

	textStart, _ := events[1]["content_block"].(map[string]any)
	textDelta, _ := events[2]["delta"].(map[string]any)
	toolStart, _ := events[4]["content_block"].(map[string]any)
	if events[1]["index"] != float64(0) || textStart["type"] != "text" {
		t.Errorf("text start mismatch: %s", mustMarshal(events[1]))
	}
	if events[2]["index"] != float64(0) || textDelta["type"] != "text_delta" || textDelta["text"] != "Found it." {
		t.Errorf("text delta mismatch: %s", mustMarshal(events[2]))
	}
	if events[3]["index"] != float64(0) {
		t.Errorf("text stop should close index 0 before buffered tool starts: %s", mustMarshal(events[3]))
	}
	if events[4]["index"] != float64(1) || toolStart["type"] != "tool_use" {
		t.Errorf("tool start mismatch: %s", mustMarshal(events[4]))
	}
	messageDelta, _ := events[7]["delta"].(map[string]any)
	if messageDelta["stop_reason"] != "end_turn" {
		t.Errorf("stop_reason = %v, want end_turn: %s", messageDelta["stop_reason"], mustMarshal(events[7]))
	}
}

func TestConvertOpenAIStreamToAnthropic_ThinkingTextMultipleTools(t *testing.T) {
	c := &Converter{From: FormatOpenAI, To: FormatAnthropic}
	var events []map[string]any

	chunks := []string{
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"I should plan."},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","content":"I will use tools."},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_weather","type":"function","function":{"name":"get_weather","arguments":"{}"}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":1,"id":"call_time","type":"function","function":{"name":"get_time","arguments":"{}"}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	for i, chunk := range chunks {
		result, err := convertOpenAIStreamToAnthropic(c, []byte(chunk))
		if err != nil {
			t.Fatalf("chunk%d: unexpected error: %v", i+1, err)
		}
		appendAnthropicStreamEventsIfAny(t, &events, fmt.Sprintf("chunk%d", i+1), result)
	}

	wantTypes := []string{
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
	}
	assertEventTypes(t, events, wantTypes)

	blockStarts := []struct {
		eventIndex int
		blockIndex float64
		blockType  string
	}{
		{eventIndex: 1, blockIndex: 0, blockType: "thinking"},
		{eventIndex: 4, blockIndex: 1, blockType: "text"},
		{eventIndex: 7, blockIndex: 2, blockType: "tool_use"},
		{eventIndex: 10, blockIndex: 3, blockType: "tool_use"},
	}
	for _, want := range blockStarts {
		block, _ := events[want.eventIndex]["content_block"].(map[string]any)
		if events[want.eventIndex]["index"] != want.blockIndex || block["type"] != want.blockType {
			t.Errorf("block start at event %d mismatch: %s", want.eventIndex, mustMarshal(events[want.eventIndex]))
		}
	}

	for _, want := range []struct {
		eventIndex int
		blockIndex float64
	}{
		{eventIndex: 3, blockIndex: 0},
		{eventIndex: 6, blockIndex: 1},
		{eventIndex: 9, blockIndex: 2},
		{eventIndex: 12, blockIndex: 3},
	} {
		if events[want.eventIndex]["index"] != want.blockIndex {
			t.Errorf("block stop at event %d mismatch: %s", want.eventIndex, mustMarshal(events[want.eventIndex]))
		}
	}
}

func TestConvertOpenAIStreamToAnthropic_InterleavedToolCalls(t *testing.T) {
	c := &Converter{From: FormatOpenAI, To: FormatAnthropic}
	var events []map[string]any

	chunks := []string{
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_weather","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\""}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":1,"id":"call_time","type":"function","function":{"name":"get_time","arguments":"{\"zone\":\"UTC\"}"}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"function":{"arguments":"Paris\"}"}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	for i, chunk := range chunks {
		result, err := convertOpenAIStreamToAnthropic(c, []byte(chunk))
		if err != nil {
			t.Fatalf("chunk%d: unexpected error: %v", i+1, err)
		}
		appendAnthropicStreamEventsIfAny(t, &events, fmt.Sprintf("chunk%d", i+1), result)
	}

	wantTypes := []string{
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
	}
	assertEventTypes(t, events, wantTypes)
	assertToolUseBlock(t, events, 1, 0, "call_weather", "get_weather", `{"city":"Paris"}`)
	assertToolUseBlock(t, events, 4, 1, "call_time", "get_time", `{"zone":"UTC"}`)
	messageDelta, _ := events[7]["delta"].(map[string]any)
	if messageDelta["stop_reason"] != "tool_use" {
		t.Errorf("stop_reason = %v, want tool_use: %s", messageDelta["stop_reason"], mustMarshal(events[7]))
	}
}

func TestConvertOpenAIStreamToAnthropic_MultiEntryToolChunk(t *testing.T) {
	c := &Converter{From: FormatOpenAI, To: FormatAnthropic}
	var events []map[string]any

	chunks := []string{
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_weather","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}},{"index":1,"id":"call_time","type":"function","function":{"name":"get_time","arguments":"{\"zone\":\"UTC\"}"}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	for i, chunk := range chunks {
		result, err := convertOpenAIStreamToAnthropic(c, []byte(chunk))
		if err != nil {
			t.Fatalf("chunk%d: unexpected error: %v", i+1, err)
		}
		appendAnthropicStreamEventsIfAny(t, &events, fmt.Sprintf("chunk%d", i+1), result)
	}

	wantTypes := []string{
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
	}
	assertEventTypes(t, events, wantTypes)
	assertToolUseBlock(t, events, 1, 0, "call_weather", "get_weather", `{"city":"Paris"}`)
	assertToolUseBlock(t, events, 4, 1, "call_time", "get_time", `{"zone":"UTC"}`)
}

func TestConvertOpenAIStreamToAnthropic_StreamTermination(t *testing.T) {
	c := &Converter{From: FormatOpenAI, To: FormatAnthropic}
	var events []map[string]any

	chunks := []string{
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello."},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	for i, chunk := range chunks {
		result, err := convertOpenAIStreamToAnthropic(c, []byte(chunk))
		if err != nil {
			t.Fatalf("chunk%d: unexpected error: %v", i+1, err)
		}
		appendAnthropicStreamEventsIfAny(t, &events, fmt.Sprintf("chunk%d", i+1), result)
	}

	if countEvents(events, "message_stop") != 1 {
		t.Fatalf("expected exactly one message_stop, got %d: %s", countEvents(events, "message_stop"), mustMarshal(events))
	}
}

func TestConvertOpenAIStreamToAnthropic_StreamTerminationWithUsage(t *testing.T) {
	c := &Converter{From: FormatOpenAI, To: FormatAnthropic}
	var events []map[string]any

	chunks := []string{
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello."},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`,
		`[DONE]`,
	}
	for i, chunk := range chunks {
		result, err := convertOpenAIStreamToAnthropic(c, []byte(chunk))
		if err != nil {
			t.Fatalf("chunk%d: unexpected error: %v", i+1, err)
		}
		appendAnthropicStreamEventsIfAny(t, &events, fmt.Sprintf("chunk%d", i+1), result)
	}

	if countEvents(events, "message_delta") != 1 {
		t.Fatalf("expected exactly one message_delta, got %d: %s", countEvents(events, "message_delta"), mustMarshal(events))
	}
	if countEvents(events, "message_stop") != 1 {
		t.Fatalf("expected exactly one message_stop, got %d: %s", countEvents(events, "message_stop"), mustMarshal(events))
	}
	var messageDelta map[string]any
	for _, event := range events {
		if event["type"] == "message_delta" {
			messageDelta = event
			break
		}
	}
	if messageDelta == nil {
		t.Fatalf("message_delta not found: %s", mustMarshal(events))
	}
	usage, _ := messageDelta["usage"].(map[string]any)
	if usage["input_tokens"] != float64(10) || usage["output_tokens"] != float64(20) {
		t.Fatalf("message_delta usage = %s, want input_tokens=10 output_tokens=20", mustMarshal(usage))
	}
}

func TestConvertResponsesStreamToAnthropic_MultiLineIntermediateUsage(t *testing.T) {
	c := &Converter{From: FormatResponses, To: FormatAnthropic}
	input := `{"type":"response.completed","response":{"id":"resp_123","status":"completed","model":"gpt-4","usage":{"input_tokens":10,"output_tokens":5}}}`
	result, err := convertResponsesStreamToAnthropic(c, []byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var events []map[string]any
	appendAnthropicStreamEvents(t, &events, "responses_to_anthropic", result)
	assertEventTypes(t, events, []string{"message_delta", "message_stop"})
	usage, _ := events[0]["usage"].(map[string]any)
	if usage["input_tokens"] != float64(10) || usage["output_tokens"] != float64(5) {
		t.Fatalf("message_delta usage = %s, want input_tokens=10 output_tokens=5", mustMarshal(usage))
	}
}

func TestConvertAnthropicStreamToResponses_MultiLineIntermediateUsage(t *testing.T) {
	c := &Converter{From: FormatAnthropic, To: FormatResponses}
	input := `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":7,"output_tokens":3}}`
	result, err := convertAnthropicStreamToResponses(c, []byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(result, "response.content_part.done") {
		t.Fatalf("missing response.content_part.done: %s", string(result))
	}
	if !contains(result, "response.completed") {
		t.Fatalf("missing response.completed from usage line: %s", string(result))
	}
	if !contains(result, "input_tokens") {
		t.Fatalf("missing converted usage: %s", string(result))
	}
}

func appendAnthropicStreamEventsIfAny(t *testing.T, events *[]map[string]any, label string, data []byte) {
	t.Helper()
	if data == nil {
		return
	}
	appendAnthropicStreamEvents(t, events, label, data)
}

func assertEventTypes(t *testing.T, events []map[string]any, wantTypes []string) {
	t.Helper()
	if len(events) != len(wantTypes) {
		t.Fatalf("expected %d events, got %d:\n%s", len(wantTypes), len(events), mustMarshal(events))
	}
	for i, wantType := range wantTypes {
		if events[i]["type"] != wantType {
			t.Fatalf("event %d type = %v, want %s: %s", i, events[i]["type"], wantType, mustMarshal(events[i]))
		}
	}
}

func assertToolUseBlock(t *testing.T, events []map[string]any, startEventIndex int, blockIndex float64, id, name, args string) {
	t.Helper()
	start := events[startEventIndex]
	delta := events[startEventIndex+1]
	stop := events[startEventIndex+2]
	block, _ := start["content_block"].(map[string]any)
	if start["index"] != blockIndex || block["type"] != "tool_use" || block["id"] != id || block["name"] != name {
		t.Errorf("tool_use start mismatch: %s", mustMarshal(start))
	}
	deltaData, _ := delta["delta"].(map[string]any)
	if delta["index"] != blockIndex || deltaData["type"] != "input_json_delta" || deltaData["partial_json"] != args {
		t.Errorf("tool_use delta mismatch: %s", mustMarshal(delta))
	}
	if stop["index"] != blockIndex {
		t.Errorf("tool_use stop mismatch: %s", mustMarshal(stop))
	}
}

func countEvents(events []map[string]any, eventType string) int {
	count := 0
	for _, event := range events {
		if event["type"] == eventType {
			count++
		}
	}
	return count
}
