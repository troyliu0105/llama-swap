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

func TestOpenAIToResponsesRequest_MultipleInstructionMessagesCombined(t *testing.T) {
	input := `{"model":"gpt-4","messages":[{"role":"system","content":"First instruction"},{"role":"developer","content":"Second instruction"},{"role":"user","content":"Hello"}]}`
	body, err := openAIToResponsesRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}
	if req["instructions"] != "First instruction\n\nSecond instruction" {
		t.Fatalf("instructions = %v, want combined ordered instructions", req["instructions"])
	}
}

func TestOpenAIToAnthropicRequest_MultipleInstructionMessagesCombined(t *testing.T) {
	input := `{"model":"gpt-4","messages":[{"role":"system","content":"First instruction"},{"role":"developer","content":"Second instruction"},{"role":"user","content":"Hello"}],"max_completion_tokens":100}`
	body, err := openAIToAnthropicRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}
	if req["system"] != "First instruction\n\nSecond instruction" {
		t.Fatalf("system = %v, want combined ordered instructions", req["system"])
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

func TestConvertOpenAIResponseToResponses_PreservesRefusal(t *testing.T) {
	input := `{"id":"chatcmpl-123","object":"chat.completion","model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"","refusal":"I can't help with that."},"finish_reason":"stop"}]}`
	body, err := convertOpenAIResponseToResponses([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	output := resp["output"].([]any)
	message := output[0].(map[string]any)
	content := message["content"].([]any)
	refusal := content[0].(map[string]any)
	if refusal["type"] != "refusal" || refusal["refusal"] != "I can't help with that." {
		t.Fatalf("refusal content = %v, want refusal block", refusal)
	}
}

func TestConvertOpenAIResponseToResponses_IncompleteMappings(t *testing.T) {
	tests := []struct {
		name       string
		finish     string
		wantStatus string
		wantReason string
	}{
		{name: "length", finish: "length", wantStatus: "incomplete", wantReason: "max_output_tokens"},
		{name: "content_filter", finish: "content_filter", wantStatus: "incomplete", wantReason: "content_filter"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := fmt.Sprintf(`{"id":"chatcmpl-123","object":"chat.completion","model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"partial"},"finish_reason":%q}]}`, tt.finish)
			body, err := convertOpenAIResponseToResponses([]byte(input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var resp map[string]any
			if err := json.Unmarshal(body, &resp); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}
			if resp["status"] != tt.wantStatus {
				t.Fatalf("status = %v, want %s", resp["status"], tt.wantStatus)
			}
			details := resp["incomplete_details"].(map[string]any)
			if details["reason"] != tt.wantReason {
				t.Fatalf("incomplete_details = %v, want reason %s", details, tt.wantReason)
			}
		})
	}
}

func TestConvertOpenAIResponseToResponses_MultiChoiceFailsExplicitly(t *testing.T) {
	input := `{"id":"chatcmpl-123","object":"chat.completion","model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"first"},"finish_reason":"stop"},{"index":1,"message":{"role":"assistant","content":"second"},"finish_reason":"stop"}]}`
	_, err := convertOpenAIResponseToResponses([]byte(input))
	if err == nil {
		t.Fatal("expected explicit error for multi-choice chat response, got nil")
	}
	if !containsSubstring(err.Error(), "choices") {
		t.Fatalf("error = %q, want mention of choices", err.Error())
	}
	if !containsSubstring(err.Error(), "cannot be converted") {
		t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
	}
}

func TestConvertOpenAIResponseToResponses_LogprobsFailExplicitly(t *testing.T) {
	input := `{"id":"chatcmpl-123","object":"chat.completion","model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"Hello!"},"finish_reason":"stop","logprobs":{"content":[{"token":"Hello"}]}}]}`
	_, err := convertOpenAIResponseToResponses([]byte(input))
	if err == nil {
		t.Fatal("expected explicit error for chat response logprobs, got nil")
	}
	if !containsSubstring(err.Error(), "logprobs") {
		t.Fatalf("error = %q, want mention of logprobs", err.Error())
	}
	if !containsSubstring(err.Error(), "cannot be converted") {
		t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
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

func TestConvertResponsesResponseToOpenAI_PreservesRefusal(t *testing.T) {
	input := `{"id":"resp_123","object":"response","model":"gpt-4","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"refusal","refusal":"I can't help with that."}]}]}`
	body, err := convertResponsesResponseToOpenAI([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	choice := resp["choices"].([]any)[0].(map[string]any)
	msg := choice["message"].(map[string]any)
	if msg["refusal"] != "I can't help with that." {
		t.Fatalf("refusal = %v, want refusal preserved", msg["refusal"])
	}
	if msg["content"] != "" {
		t.Fatalf("content = %v, want empty string for refusal-only message", msg["content"])
	}
}

func TestConvertResponsesResponseToOpenAI_UnsupportedOutputItemFailsExplicitly(t *testing.T) {
	input := `{"id":"resp_123","object":"response","model":"gpt-4","status":"completed","output":[{"type":"reasoning","id":"rs_1","summary":[]}]}`
	_, err := convertResponsesResponseToOpenAI([]byte(input))
	if err == nil {
		t.Fatal("expected explicit error for unsupported Responses output item, got nil")
	}
	if !containsSubstring(err.Error(), "output item") {
		t.Fatalf("error = %q, want mention of output item", err.Error())
	}
	if !containsSubstring(err.Error(), "cannot be converted") {
		t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
	}
}

func TestConvertResponsesResponseToOpenAI_RepeatedMessageItemsFailExplicitly(t *testing.T) {
	input := `{"id":"resp_123","object":"response","model":"gpt-4","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"first"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"second"}]}]}`
	_, err := convertResponsesResponseToOpenAI([]byte(input))
	if err == nil {
		t.Fatal("expected explicit error for repeated Responses message items, got nil")
	}
	if !containsSubstring(err.Error(), "multiple message output items") {
		t.Fatalf("error = %q, want mention of multiple message output items", err.Error())
	}
	if !containsSubstring(err.Error(), "cannot be converted") {
		t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
	}
}

func TestConvertResponsesResponseToOpenAI_UnsupportedMessageContentPartFailsExplicitly(t *testing.T) {
	input := `{"id":"resp_123","object":"response","model":"gpt-4","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"reasoning_text","text":"internal"}]}]}`
	_, err := convertResponsesResponseToOpenAI([]byte(input))
	if err == nil {
		t.Fatal("expected explicit error for unsupported Responses message content part, got nil")
	}
	if !containsSubstring(err.Error(), "message content part") {
		t.Fatalf("error = %q, want mention of message content part", err.Error())
	}
	if !containsSubstring(err.Error(), "cannot be converted") {
		t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
	}
}

func TestConvertResponsesResponseToOpenAI_OutputTextAnnotationsFailExplicitly(t *testing.T) {
	input := `{"id":"resp_123","object":"response","model":"gpt-4","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello","annotations":[{"type":"url_citation","url":"https://example.com"}]}]}]}`
	_, err := convertResponsesResponseToOpenAI([]byte(input))
	if err == nil {
		t.Fatal("expected explicit error for output_text annotations, got nil")
	}
	if !containsSubstring(err.Error(), "annotations") {
		t.Fatalf("error = %q, want mention of annotations", err.Error())
	}
	if !containsSubstring(err.Error(), "cannot be converted") {
		t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
	}
}

func TestConvertResponsesResponseToOpenAI_OutputTextLogprobsFailExplicitly(t *testing.T) {
	input := `{"id":"resp_123","object":"response","model":"gpt-4","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello","logprobs":[{"token":"hello"}]}]}]}`
	_, err := convertResponsesResponseToOpenAI([]byte(input))
	if err == nil {
		t.Fatal("expected explicit error for output_text logprobs, got nil")
	}
	if !containsSubstring(err.Error(), "logprobs") {
		t.Fatalf("error = %q, want mention of logprobs", err.Error())
	}
	if !containsSubstring(err.Error(), "cannot be converted") {
		t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
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

func TestAnthropicToOpenAITools_AcceptsStandardToolWithoutType(t *testing.T) {
	input := `{"model":"claude-3","messages":[{"role":"user","content":"test"}],"max_tokens":100,"tools":[{"name":"search","description":"Search","input_schema":{"type":"object","properties":{"q":{"type":"string"}}}}]}`

	body, err := anthropicToOpenAIRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	tools := req["tools"].([]any)
	tool := tools[0].(map[string]any)
	fn := tool["function"].(map[string]any)
	if tool["type"] != "function" || fn["name"] != "search" {
		t.Fatalf("converted tool = %v, want OpenAI function search", tool)
	}
}

func TestOpenAIToAnthropicTools_EmitsCompatibleSchema(t *testing.T) {
	input := `{"model":"gpt-4","messages":[{"role":"user","content":"test"}],"max_completion_tokens":100,"tools":[{"type":"function","function":{"name":"search","description":"Search","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}}]}`

	body, err := openAIToAnthropicRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	tools := req["tools"].([]any)
	tool := tools[0].(map[string]any)
	if tool["name"] != "search" || tool["input_schema"] == nil {
		t.Fatalf("converted tool = %v, want Anthropiс-compatible schema", tool)
	}
}

func TestOpenAIToResponsesRequest_DeprecatedFunctionsCompatibility(t *testing.T) {
	input := `{"model":"gpt-4","messages":[{"role":"user","content":"test"}],"functions":[{"name":"search","description":"Search","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}],"function_call":{"name":"search"}}`
	body, err := openAIToResponsesRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	tools, ok := req["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v, want one converted function tool", req["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" || tool["name"] != "search" {
		t.Fatalf("converted tool = %v, want function/search", tool)
	}
	choice, ok := req["tool_choice"].(map[string]any)
	if !ok || choice["type"] != "function" || choice["name"] != "search" {
		t.Fatalf("tool_choice = %v, want function search", req["tool_choice"])
	}
}

func TestOpenAIToAnthropicRequest_DeprecatedFunctionsCompatibility(t *testing.T) {
	input := `{"model":"gpt-4","messages":[{"role":"user","content":"test"}],"max_completion_tokens":100,"functions":[{"name":"search","description":"Search","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}],"function_call":{"name":"search"}}`
	body, err := openAIToAnthropicRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	tools, ok := req["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v, want one converted tool", req["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "search" || tool["input_schema"] == nil {
		t.Fatalf("converted tool = %v, want anthropic-compatible search tool", tool)
	}
	choice, ok := req["tool_choice"].(map[string]any)
	if !ok || choice["type"] != "tool" || choice["name"] != "search" {
		t.Fatalf("tool_choice = %v, want tool search", req["tool_choice"])
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

func TestOpenAIToAnthropicRequest_FileURLCompatibility(t *testing.T) {
	input := `{"model":"gpt-4","messages":[{"role":"user","content":[{"type":"file","file":{"file_url":"https://example.com/doc.pdf","filename":"doc.pdf"}},{"type":"text","text":"summarize this"}]}],"max_completion_tokens":100}`
	body, err := openAIToAnthropicRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	messages := req["messages"].([]any)
	content := messages[0].(map[string]any)["content"].([]any)
	doc := content[0].(map[string]any)
	if doc["type"] != "document" {
		t.Fatalf("document block = %v, want document", doc)
	}
	source := doc["source"].(map[string]any)
	if source["type"] != "url" || source["url"] != "https://example.com/doc.pdf" {
		t.Fatalf("document source = %v, want url doc.pdf", source)
	}
	if doc["title"] != "doc.pdf" {
		t.Fatalf("document title = %v, want doc.pdf", doc["title"])
	}
	text := content[1].(map[string]any)
	if text["type"] != "text" || text["text"] != "summarize this" {
		t.Fatalf("text block = %v, want summarize text", text)
	}
}

func TestResponsesToAnthropicRequest_InputFileURLCompatibility(t *testing.T) {
	input := `{"model":"gpt-4","input":[{"role":"user","content":[{"type":"input_file","file_url":"https://example.com/doc.pdf","filename":"doc.pdf"},{"type":"input_text","text":"summarize this"}]}]}`
	body, err := responsesToAnthropicRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	messages := req["messages"].([]any)
	content := messages[0].(map[string]any)["content"].([]any)
	doc := content[0].(map[string]any)
	if doc["type"] != "document" {
		t.Fatalf("document block = %v, want document", doc)
	}
	source := doc["source"].(map[string]any)
	if source["type"] != "url" || source["url"] != "https://example.com/doc.pdf" {
		t.Fatalf("document source = %v, want url doc.pdf", source)
	}
	if doc["title"] != "doc.pdf" {
		t.Fatalf("document title = %v, want doc.pdf", doc["title"])
	}
	text := content[1].(map[string]any)
	if text["type"] != "text" || text["text"] != "summarize this" {
		t.Fatalf("text block = %v, want summarize text", text)
	}
}

func TestOpenAIToAnthropicRequest_FileURLDerivesTitle(t *testing.T) {
	input := `{"model":"gpt-4","messages":[{"role":"user","content":[{"type":"file","file":{"file_url":"https://example.com/files/report.pdf"}}]}],"max_completion_tokens":100}`
	body, err := openAIToAnthropicRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	content := req["messages"].([]any)[0].(map[string]any)["content"].([]any)
	doc := content[0].(map[string]any)
	if doc["title"] != "report.pdf" {
		t.Fatalf("title = %v, want report.pdf", doc["title"])
	}
}

func TestResponsesToAnthropicRequest_InputFileURLDerivesTitle(t *testing.T) {
	input := `{"model":"gpt-4","input":[{"role":"user","content":[{"type":"input_file","file_url":"https://example.com/files/report.pdf"}]}]}`
	body, err := responsesToAnthropicRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	content := req["messages"].([]any)[0].(map[string]any)["content"].([]any)
	doc := content[0].(map[string]any)
	if doc["title"] != "report.pdf" {
		t.Fatalf("title = %v, want report.pdf", doc["title"])
	}
}

func TestOpenAIToAnthropicRequest_FileDataCompatibility(t *testing.T) {
	input := `{"model":"gpt-4","messages":[{"role":"user","content":[{"type":"file","file":{"file_data":"cGRmLWRhdGE=","filename":"doc.pdf"}},{"type":"text","text":"summarize this"}]}],"max_completion_tokens":100}`
	body, err := openAIToAnthropicRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	messages := req["messages"].([]any)
	content := messages[0].(map[string]any)["content"].([]any)
	doc := content[0].(map[string]any)
	source := doc["source"].(map[string]any)
	if source["type"] != "base64" || source["media_type"] != "application/pdf" || source["data"] != "cGRmLWRhdGE=" {
		t.Fatalf("document source = %v, want base64 application/pdf", source)
	}
	if doc["title"] != "doc.pdf" {
		t.Fatalf("document title = %v, want doc.pdf", doc["title"])
	}
}

func TestResponsesToAnthropicRequest_InputFileDataCompatibility(t *testing.T) {
	input := `{"model":"gpt-4","input":[{"role":"user","content":[{"type":"input_file","file_data":"cGRmLWRhdGE=","filename":"doc.pdf"},{"type":"input_text","text":"summarize this"}]}]}`
	body, err := responsesToAnthropicRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	messages := req["messages"].([]any)
	content := messages[0].(map[string]any)["content"].([]any)
	doc := content[0].(map[string]any)
	source := doc["source"].(map[string]any)
	if source["type"] != "base64" || source["media_type"] != "application/pdf" || source["data"] != "cGRmLWRhdGE=" {
		t.Fatalf("document source = %v, want base64 application/pdf", source)
	}
	if doc["title"] != "doc.pdf" {
		t.Fatalf("document title = %v, want doc.pdf", doc["title"])
	}
}

func TestOpenAIToAnthropicRequest_FileIDFailsExplicitly(t *testing.T) {
	input := `{"model":"gpt-4","messages":[{"role":"user","content":[{"type":"file","file":{"file_id":"file_123"}}]}],"max_completion_tokens":100}`
	_, err := openAIToAnthropicRequest([]byte(input))
	if err == nil {
		t.Fatal("expected error for file_id-only conversion, got nil")
	}
	if !containsSubstring(err.Error(), "cannot be converted to Anthropic document") {
		t.Fatalf("error = %q, want explicit document conversion failure", err.Error())
	}
}

func TestOpenAIToAnthropicRequest_FileWithoutPayloadFailsExplicitly(t *testing.T) {
	input := `{"model":"gpt-4","messages":[{"role":"user","content":[{"type":"file","file":{"filename":"doc.pdf"}}]}],"max_completion_tokens":100}`
	_, err := openAIToAnthropicRequest([]byte(input))
	if err == nil {
		t.Fatal("expected error for file without payload, got nil")
	}
	if !containsSubstring(err.Error(), "cannot be converted to Anthropic document") {
		t.Fatalf("error = %q, want explicit document conversion failure", err.Error())
	}
}

func TestOpenAIToAnthropicRequest_FileObjectMissingFailsExplicitly(t *testing.T) {
	input := `{"model":"gpt-4","messages":[{"role":"user","content":[{"type":"file"}]}],"max_completion_tokens":100}`
	_, err := openAIToAnthropicRequest([]byte(input))
	if err == nil {
		t.Fatal("expected error for missing file object, got nil")
	}
	if !containsSubstring(err.Error(), "cannot be converted to Anthropic document") {
		t.Fatalf("error = %q, want explicit document conversion failure", err.Error())
	}
}

func TestResponsesToAnthropicRequest_InputFileIDFailsExplicitly(t *testing.T) {
	input := `{"model":"gpt-4","input":[{"role":"user","content":[{"type":"input_file","file_id":"file_123"}]}]}`
	_, err := responsesToAnthropicRequest([]byte(input))
	if err == nil {
		t.Fatal("expected error for input_file file_id-only conversion, got nil")
	}
	if !containsSubstring(err.Error(), "cannot be converted to Anthropic document") {
		t.Fatalf("error = %q, want explicit document conversion failure", err.Error())
	}
}

func TestOpenAIToResponsesRequest_FileWithoutPayloadFailsExplicitly(t *testing.T) {
	input := `{"model":"gpt-4","messages":[{"role":"user","content":[{"type":"file","file":{"filename":"doc.pdf"}}]}]}`
	_, err := openAIToResponsesRequest([]byte(input))
	if err == nil {
		t.Fatal("expected error for file without payload, got nil")
	}
	if !containsSubstring(err.Error(), "cannot be converted to Responses input_file") {
		t.Fatalf("error = %q, want explicit input_file conversion failure", err.Error())
	}
}

func TestOpenAIToResponsesRequest_FileEmptyReferenceFailsExplicitly(t *testing.T) {
	input := `{"model":"gpt-4","messages":[{"role":"user","content":[{"type":"file","file":{"file_url":""}}]}]}`
	_, err := openAIToResponsesRequest([]byte(input))
	if err == nil {
		t.Fatal("expected error for empty file reference, got nil")
	}
	if !containsSubstring(err.Error(), "cannot be converted to Responses input_file") {
		t.Fatalf("error = %q, want explicit input_file conversion failure", err.Error())
	}
}

func TestOpenAIToResponsesRequest_FileNonStringReferenceFailsExplicitly(t *testing.T) {
	input := `{"model":"gpt-4","messages":[{"role":"user","content":[{"type":"file","file":{"file_url":123}}]}]}`
	_, err := openAIToResponsesRequest([]byte(input))
	if err == nil {
		t.Fatal("expected error for non-string file reference, got nil")
	}
	if !containsSubstring(err.Error(), "cannot be converted to Responses input_file") {
		t.Fatalf("error = %q, want explicit input_file conversion failure", err.Error())
	}
}

func TestOpenAIToResponsesRequest_FileObjectMissingFailsExplicitly(t *testing.T) {
	input := `{"model":"gpt-4","messages":[{"role":"user","content":[{"type":"file"}]}]}`
	_, err := openAIToResponsesRequest([]byte(input))
	if err == nil {
		t.Fatal("expected error for missing file object, got nil")
	}
	if !containsSubstring(err.Error(), "cannot be converted to Responses input_file") {
		t.Fatalf("error = %q, want explicit input_file conversion failure", err.Error())
	}
}

func TestResponsesContentToOpenAI_EmptyInputFileFailsExplicitly(t *testing.T) {
	_, err := convertResponsesContentToOpenAI([]any{map[string]any{"type": "input_file"}})
	if err == nil {
		t.Fatal("expected error for empty input_file, got nil")
	}
	if !containsSubstring(err.Error(), "cannot be converted to OpenAI file content") {
		t.Fatalf("error = %q, want explicit file conversion failure", err.Error())
	}
}

func TestResponsesContentToOpenAI_FilenameOnlyInputFileFailsExplicitly(t *testing.T) {
	_, err := convertResponsesContentToOpenAI([]any{map[string]any{"type": "input_file", "filename": "doc.pdf"}})
	if err == nil {
		t.Fatal("expected error for filename-only input_file, got nil")
	}
	if !containsSubstring(err.Error(), "cannot be converted to OpenAI file content") {
		t.Fatalf("error = %q, want explicit file conversion failure", err.Error())
	}
}

func TestResponsesContentToOpenAI_EmptyReferenceFailsExplicitly(t *testing.T) {
	_, err := convertResponsesContentToOpenAI([]any{map[string]any{"type": "input_file", "file_url": ""}})
	if err == nil {
		t.Fatal("expected error for empty input_file reference, got nil")
	}
	if !containsSubstring(err.Error(), "cannot be converted to OpenAI file content") {
		t.Fatalf("error = %q, want explicit file conversion failure", err.Error())
	}
}

func TestResponsesContentToOpenAI_NonStringReferenceFailsExplicitly(t *testing.T) {
	_, err := convertResponsesContentToOpenAI([]any{map[string]any{"type": "input_file", "file_url": 123}})
	if err == nil {
		t.Fatal("expected error for non-string input_file reference, got nil")
	}
	if !containsSubstring(err.Error(), "cannot be converted to OpenAI file content") {
		t.Fatalf("error = %q, want explicit file conversion failure", err.Error())
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

func TestAnthropicToOpenAIRequest_ToolResultNonTextContentFailsExplicitly(t *testing.T) {
	input := `{"model":"claude-3","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"image","source":{"type":"url","url":"https://example.com/image.png"}}]}]}],"max_tokens":100}`
	_, err := anthropicToOpenAIRequest([]byte(input))
	if err == nil {
		t.Fatal("expected explicit error for non-text tool_result content, got nil")
	}
	if !containsSubstring(err.Error(), "tool_result") {
		t.Fatalf("error = %q, want mention of tool_result", err.Error())
	}
	if !containsSubstring(err.Error(), "cannot be converted") {
		t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
	}
}

func TestAnthropicToResponsesRequest_ToolResultNonTextContentFailsExplicitly(t *testing.T) {
	input := `{"model":"claude-3","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"cGRm"}}]}]}],"max_tokens":100}`
	_, err := anthropicToResponsesRequest([]byte(input))
	if err == nil {
		t.Fatal("expected explicit error for non-text tool_result content, got nil")
	}
	if !containsSubstring(err.Error(), "tool_result") {
		t.Fatalf("error = %q, want mention of tool_result", err.Error())
	}
	if !containsSubstring(err.Error(), "cannot be converted") {
		t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
	}
}

func TestOpenAIToResponsesRequest_ToolMessageNonTextContentFailsExplicitly(t *testing.T) {
	input := `{"model":"gpt-4","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_abc","content":[{"type":"image_url","image_url":{"url":"https://example.com/image.png"}}]}]}`
	_, err := openAIToResponsesRequest([]byte(input))
	if err == nil {
		t.Fatal("expected explicit error for non-text tool message content, got nil")
	}
	if !containsSubstring(err.Error(), "tool") {
		t.Fatalf("error = %q, want mention of tool content", err.Error())
	}
	if !containsSubstring(err.Error(), "cannot be converted") {
		t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
	}
}

func TestAnthropicToOpenAIRequest_DocumentBlockCompatibility(t *testing.T) {
	input := `{"model":"claude-3","messages":[{"role":"user","content":[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"cGRmLWRhdGE="}},{"type":"text","text":"summarize this"}]}],"max_tokens":100}`
	body, err := anthropicToOpenAIRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	messages := req["messages"].([]any)
	content := messages[0].(map[string]any)["content"].([]any)
	filePart := content[0].(map[string]any)
	if filePart["type"] != "file" {
		t.Fatalf("file part type = %v, want file", filePart["type"])
	}
	file := filePart["file"].(map[string]any)
	if file["file_data"] != "cGRmLWRhdGE=" {
		t.Fatalf("file_data = %v, want anthropic base64 payload", file["file_data"])
	}
	textPart := content[1].(map[string]any)
	if textPart["type"] != "text" || textPart["text"] != "summarize this" {
		t.Fatalf("text part = %v, want summarize text", textPart)
	}
}

func TestAnthropicToOpenAIRequest_DocumentMissingSourceFailsExplicitly(t *testing.T) {
	input := `{"model":"claude-3","messages":[{"role":"user","content":[{"type":"document"}]}],"max_tokens":100}`
	_, err := anthropicToOpenAIRequest([]byte(input))
	if err == nil {
		t.Fatal("expected error for missing document source, got nil")
	}
	if !containsSubstring(err.Error(), "cannot be converted to OpenAI/Responses file content") {
		t.Fatalf("error = %q, want explicit document conversion failure", err.Error())
	}
}

func TestAnthropicToOpenAIRequest_DocumentUnsupportedSourceFailsExplicitly(t *testing.T) {
	input := `{"model":"claude-3","messages":[{"role":"user","content":[{"type":"document","source":{"type":"file_id","id":"doc_123"}}]}],"max_tokens":100}`
	_, err := anthropicToOpenAIRequest([]byte(input))
	if err == nil {
		t.Fatal("expected error for unsupported document source, got nil")
	}
	if !containsSubstring(err.Error(), "cannot be converted to OpenAI/Responses file content") {
		t.Fatalf("error = %q, want explicit document conversion failure", err.Error())
	}
}

func TestAnthropicToResponsesRequest_DocumentBlockCompatibility(t *testing.T) {
	input := `{"model":"claude-3","messages":[{"role":"user","content":[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"cGRmLWRhdGE="}},{"type":"text","text":"summarize this"}]}],"max_tokens":100}`
	body, err := anthropicToResponsesRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	inputItems := req["input"].([]any)
	content := inputItems[0].(map[string]any)["content"].([]any)
	filePart := content[0].(map[string]any)
	if filePart["type"] != "input_file" {
		t.Fatalf("file part type = %v, want input_file", filePart["type"])
	}
	if filePart["file_data"] != "cGRmLWRhdGE=" {
		t.Fatalf("file_data = %v, want anthropic base64 payload", filePart["file_data"])
	}
	textPart := content[1].(map[string]any)
	if textPart["type"] != "input_text" || textPart["text"] != "summarize this" {
		t.Fatalf("text part = %v, want summarize text", textPart)
	}
}

func TestAnthropicToOpenAIRequest_DocumentURLDerivesFilename(t *testing.T) {
	input := `{"model":"claude-3","messages":[{"role":"user","content":[{"type":"document","source":{"type":"url","url":"https://example.com/files/report.pdf"}}]}],"max_tokens":100}`
	body, err := anthropicToOpenAIRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	content := req["messages"].([]any)[0].(map[string]any)["content"].([]any)
	file := content[0].(map[string]any)["file"].(map[string]any)
	if file["file_url"] != "https://example.com/files/report.pdf" {
		t.Fatalf("file = %v, want file_url preserved", file)
	}
	if file["filename"] != "report.pdf" {
		t.Fatalf("filename = %v, want report.pdf", file["filename"])
	}
}

func TestAnthropicToOpenAIRequest_DocumentBase64DerivesFilename(t *testing.T) {
	input := `{"model":"claude-3","messages":[{"role":"user","content":[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"cGRmLWRhdGE="}}]}],"max_tokens":100}`
	body, err := anthropicToOpenAIRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	content := req["messages"].([]any)[0].(map[string]any)["content"].([]any)
	file := content[0].(map[string]any)["file"].(map[string]any)
	if file["filename"] != "document.pdf" {
		t.Fatalf("filename = %v, want document.pdf", file["filename"])
	}
}

func TestAnthropicToResponsesRequest_DocumentURLDerivesFilename(t *testing.T) {
	input := `{"model":"claude-3","messages":[{"role":"user","content":[{"type":"document","source":{"type":"url","url":"https://example.com/files/report.pdf"}}]}],"max_tokens":100}`
	body, err := anthropicToResponsesRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	content := req["input"].([]any)[0].(map[string]any)["content"].([]any)
	file := content[0].(map[string]any)
	if file["file_url"] != "https://example.com/files/report.pdf" {
		t.Fatalf("file = %v, want file_url preserved", file)
	}
	if file["filename"] != "report.pdf" {
		t.Fatalf("filename = %v, want report.pdf", file["filename"])
	}
}

func TestAnthropicToResponsesRequest_DocumentBase64DerivesFilename(t *testing.T) {
	input := `{"model":"claude-3","messages":[{"role":"user","content":[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"cGRmLWRhdGE="}}]}],"max_tokens":100}`
	body, err := anthropicToResponsesRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	content := req["input"].([]any)[0].(map[string]any)["content"].([]any)
	file := content[0].(map[string]any)
	if file["filename"] != "document.pdf" {
		t.Fatalf("filename = %v, want document.pdf", file["filename"])
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

func TestResponsesToOpenAIRequest_UnsupportedTextFormatFailsExplicitly(t *testing.T) {
	input := `{"model":"gpt-4","input":"Hi","text":{"format":{"type":"xml_schema","schema":{"name":"x"}}}}`
	for name, convert := range map[string]func([]byte) ([]byte, error){
		"openai":    responsesToOpenAIRequest,
		"anthropic": responsesToAnthropicRequest,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := convert([]byte(input))
			if err == nil {
				t.Fatal("expected explicit error for unsupported text.format, got nil")
			}
			if !containsSubstring(err.Error(), "text.format") {
				t.Fatalf("error = %q, want mention of text.format", err.Error())
			}
			if !containsSubstring(err.Error(), "cannot be converted") {
				t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
			}
		})
	}
}

func TestOpenAIToResponsesRequest_UnsupportedResponseFormatFailsExplicitly(t *testing.T) {
	input := `{"model":"gpt-4","messages":[{"role":"user","content":"Hi"}],"response_format":{"type":"xml_schema","schema":{"name":"x"}}}`
	_, err := openAIToResponsesRequest([]byte(input))
	if err == nil {
		t.Fatal("expected explicit error for unsupported response_format, got nil")
	}
	if !containsSubstring(err.Error(), "response_format") {
		t.Fatalf("error = %q, want mention of response_format", err.Error())
	}
	if !containsSubstring(err.Error(), "cannot be converted") {
		t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
	}
}

func TestResponsesStatefulRequestFieldsFailExplicitly(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		convert   func([]byte) ([]byte, error)
		wantField string
	}{
		{
			name:      "previous_response_id to openai",
			input:     `{"model":"gpt-4","input":"Hi","previous_response_id":"resp_123"}`,
			convert:   responsesToOpenAIRequest,
			wantField: "previous_response_id",
		},
		{
			name:      "conversation to anthropic",
			input:     `{"model":"gpt-4","input":"Hi","conversation":"conv_123"}`,
			convert:   responsesToAnthropicRequest,
			wantField: "conversation",
		},
		{
			name:      "store true to openai",
			input:     `{"model":"gpt-4","input":"Hi","store":true}`,
			convert:   responsesToOpenAIRequest,
			wantField: "store",
		},
		{
			name:      "background true to anthropic",
			input:     `{"model":"gpt-4","input":"Hi","background":true}`,
			convert:   responsesToAnthropicRequest,
			wantField: "background",
		},
		{
			name:      "include to openai",
			input:     `{"model":"gpt-4","input":"Hi","include":["message.output_text.logprobs"]}`,
			convert:   responsesToOpenAIRequest,
			wantField: "include",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.convert([]byte(tt.input))
			if err == nil {
				t.Fatalf("expected explicit error for %s, got nil", tt.wantField)
			}
			if !containsSubstring(err.Error(), tt.wantField) {
				t.Fatalf("error = %q, want mention of %s", err.Error(), tt.wantField)
			}
			if !containsSubstring(err.Error(), "cannot be converted") {
				t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
			}
		})
	}
}

func TestOpenAIToResponsesRequest_PreservesPortableChatControls(t *testing.T) {
	input := `{"model":"gpt-4","messages":[{"role":"user","content":"Hi"}],"presence_penalty":0.2,"frequency_penalty":0.4,"seed":42,"logprobs":true,"top_logprobs":3}`
	body, err := openAIToResponsesRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal converted request: %v", err)
	}

	if req["presence_penalty"] != 0.2 {
		t.Fatalf("presence_penalty = %v, want 0.2", req["presence_penalty"])
	}
	if req["frequency_penalty"] != 0.4 {
		t.Fatalf("frequency_penalty = %v, want 0.4", req["frequency_penalty"])
	}
	if req["seed"] != 42.0 {
		t.Fatalf("seed = %v, want 42", req["seed"])
	}
	if req["logprobs"] != true {
		t.Fatalf("logprobs = %v, want true", req["logprobs"])
	}
	if req["top_logprobs"] != 3.0 {
		t.Fatalf("top_logprobs = %v, want 3", req["top_logprobs"])
	}
}

func TestAnthropicToolChoiceDisableParallelToolUseMapsToParallelToolCalls(t *testing.T) {
	input := `{"model":"claude-3","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"tool_choice":{"type":"auto","disable_parallel_tool_use":true}}`

	openAIBody, err := anthropicToOpenAIRequest([]byte(input))
	if err != nil {
		t.Fatalf("anthropic→openai: unexpected error: %v", err)
	}
	var openAIReq map[string]any
	if err := json.Unmarshal(openAIBody, &openAIReq); err != nil {
		t.Fatalf("failed to unmarshal OpenAI request: %v", err)
	}
	if openAIReq["tool_choice"] != "auto" {
		t.Fatalf("tool_choice = %v, want auto", openAIReq["tool_choice"])
	}
	if openAIReq["parallel_tool_calls"] != false {
		t.Fatalf("parallel_tool_calls = %v, want false", openAIReq["parallel_tool_calls"])
	}

	responsesBody, err := anthropicToResponsesRequest([]byte(input))
	if err != nil {
		t.Fatalf("anthropic→responses: unexpected error: %v", err)
	}
	var responsesReq map[string]any
	if err := json.Unmarshal(responsesBody, &responsesReq); err != nil {
		t.Fatalf("failed to unmarshal Responses request: %v", err)
	}
	if responsesReq["tool_choice"] != "auto" {
		t.Fatalf("responses tool_choice = %v, want auto", responsesReq["tool_choice"])
	}
	if responsesReq["parallel_tool_calls"] != false {
		t.Fatalf("responses parallel_tool_calls = %v, want false", responsesReq["parallel_tool_calls"])
	}
}

func TestAnthropicToolChoiceFormsMapToResponses(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		wantToolChoice any
	}{
		{
			name:           "auto",
			input:          `{"model":"claude-3","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"tool_choice":{"type":"auto"}}`,
			wantToolChoice: "auto",
		},
		{
			name:           "any",
			input:          `{"model":"claude-3","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"tool_choice":{"type":"any"}}`,
			wantToolChoice: "required",
		},
		{
			name:  "named tool",
			input: `{"model":"claude-3","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"tool_choice":{"type":"tool","name":"lookup"}}`,
			wantToolChoice: map[string]any{
				"type": "function",
				"name": "lookup",
			},
		},
		{
			name:           "none",
			input:          `{"model":"claude-3","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"tool_choice":{"type":"none"}}`,
			wantToolChoice: "none",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := anthropicToResponsesRequest([]byte(tt.input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var req map[string]any
			if err := json.Unmarshal(body, &req); err != nil {
				t.Fatalf("failed to unmarshal Responses request: %v", err)
			}
			got := req["tool_choice"]
			switch want := tt.wantToolChoice.(type) {
			case string:
				if got != want {
					t.Fatalf("tool_choice = %v, want %v", got, want)
				}
			case map[string]any:
				choice, ok := got.(map[string]any)
				if !ok {
					t.Fatalf("tool_choice = %T, want map[string]any", got)
				}
				if choice["type"] != want["type"] || choice["name"] != want["name"] {
					t.Fatalf("tool_choice = %v, want %v", choice, want)
				}
			}
		})
	}
}

func TestAnthropicThinkingRequestFailsExplicitlyOnStatelessTargets(t *testing.T) {
	input := `{"model":"claude-3","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"thinking":{"type":"enabled","budget_tokens":1024}}`

	for name, convert := range map[string]func([]byte) ([]byte, error){
		"openai":    anthropicToOpenAIRequest,
		"responses": anthropicToResponsesRequest,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := convert([]byte(input))
			if err == nil {
				t.Fatal("expected explicit thinking conversion error, got nil")
			}
			if !containsSubstring(err.Error(), "thinking") {
				t.Fatalf("error = %q, want mention of thinking", err.Error())
			}
			if !containsSubstring(err.Error(), "cannot be converted") {
				t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
			}
		})
	}
}

func TestResponsesUnsupportedRequestFieldsFailExplicitly(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		convert   func([]byte) ([]byte, error)
		wantField string
	}{
		{
			name:      "reasoning to openai",
			input:     `{"model":"gpt-4","input":"Hi","reasoning":{"effort":"high"}}`,
			convert:   responsesToOpenAIRequest,
			wantField: "reasoning",
		},
		{
			name:      "truncation to anthropic",
			input:     `{"model":"gpt-4","input":"Hi","truncation":"auto"}`,
			convert:   responsesToAnthropicRequest,
			wantField: "truncation",
		},
		{
			name:      "max_tool_calls to openai",
			input:     `{"model":"gpt-4","input":"Hi","max_tool_calls":2}`,
			convert:   responsesToOpenAIRequest,
			wantField: "max_tool_calls",
		},
		{
			name:      "prompt to anthropic",
			input:     `{"model":"gpt-4","input":"Hi","prompt":{"id":"pmpt_123"}}`,
			convert:   responsesToAnthropicRequest,
			wantField: "prompt",
		},
		{
			name:      "context_management to openai",
			input:     `{"model":"gpt-4","input":"Hi","context_management":{"type":"auto"}}`,
			convert:   responsesToOpenAIRequest,
			wantField: "context_management",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.convert([]byte(tt.input))
			if err == nil {
				t.Fatalf("expected explicit error for %s, got nil", tt.wantField)
			}
			if !containsSubstring(err.Error(), tt.wantField) {
				t.Fatalf("error = %q, want mention of %s", err.Error(), tt.wantField)
			}
			if !containsSubstring(err.Error(), "cannot be converted") {
				t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
			}
		})
	}
}

func TestAnthropicUnsupportedRequestFieldsFailExplicitly(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		convert   func([]byte) ([]byte, error)
		wantField string
	}{
		{
			name:      "output_config to openai",
			input:     `{"model":"claude-3","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"output_config":{"format":{"type":"json_schema","name":"x"}}}`,
			convert:   anthropicToOpenAIRequest,
			wantField: "output_config",
		},
		{
			name:      "container to responses",
			input:     `{"model":"claude-3","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"container":"ctr_123"}`,
			convert:   anthropicToResponsesRequest,
			wantField: "container",
		},
		{
			name:      "inference_geo to openai",
			input:     `{"model":"claude-3","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"inference_geo":"us"}`,
			convert:   anthropicToOpenAIRequest,
			wantField: "inference_geo",
		},
		{
			name:      "top_k to responses",
			input:     `{"model":"claude-3","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"top_k":5}`,
			convert:   anthropicToResponsesRequest,
			wantField: "top_k",
		},
		{
			name:      "cache_control to openai",
			input:     `{"model":"claude-3","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"cache_control":{"type":"ephemeral"}}`,
			convert:   anthropicToOpenAIRequest,
			wantField: "cache_control",
		},
		{
			name:      "mcp_servers to responses",
			input:     `{"model":"claude-3","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"mcp_servers":[{"url":"https://example.com/mcp"}]}`,
			convert:   anthropicToResponsesRequest,
			wantField: "mcp_servers",
		},
		{
			name:      "user_profile_id to openai",
			input:     `{"model":"claude-3","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"user_profile_id":"profile_123"}`,
			convert:   anthropicToOpenAIRequest,
			wantField: "user_profile_id",
		},
		{
			name:      "speed to responses",
			input:     `{"model":"claude-3","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"speed":"default"}`,
			convert:   anthropicToResponsesRequest,
			wantField: "speed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.convert([]byte(tt.input))
			if err == nil {
				t.Fatalf("expected explicit error for %s, got nil", tt.wantField)
			}
			if !containsSubstring(err.Error(), tt.wantField) {
				t.Fatalf("error = %q, want mention of %s", err.Error(), tt.wantField)
			}
			if !containsSubstring(err.Error(), "cannot be converted") {
				t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
			}
		})
	}
}

func TestOpenAIUnsupportedRequestFieldsFailExplicitly(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		convert   func([]byte) ([]byte, error)
		wantField string
	}{
		{
			name:      "n greater than one to responses",
			input:     `{"model":"gpt-4","messages":[{"role":"user","content":"Hi"}],"n":2}`,
			convert:   openAIToResponsesRequest,
			wantField: "n",
		},
		{
			name:      "n greater than one to anthropic",
			input:     `{"model":"gpt-4","messages":[{"role":"user","content":"Hi"}],"n":2}`,
			convert:   openAIToAnthropicRequest,
			wantField: "n",
		},
		{
			name:      "logit_bias to responses",
			input:     `{"model":"gpt-4","messages":[{"role":"user","content":"Hi"}],"logit_bias":{"42":5}}`,
			convert:   openAIToResponsesRequest,
			wantField: "logit_bias",
		},
		{
			name:      "logit_bias to anthropic",
			input:     `{"model":"gpt-4","messages":[{"role":"user","content":"Hi"}],"logit_bias":{"42":5}}`,
			convert:   openAIToAnthropicRequest,
			wantField: "logit_bias",
		},
		{
			name:      "prediction to responses",
			input:     `{"model":"gpt-4","messages":[{"role":"user","content":"Hi"}],"prediction":{"type":"content","content":"Hello"}}`,
			convert:   openAIToResponsesRequest,
			wantField: "prediction",
		},
		{
			name:      "prediction to anthropic",
			input:     `{"model":"gpt-4","messages":[{"role":"user","content":"Hi"}],"prediction":{"type":"content","content":"Hello"}}`,
			convert:   openAIToAnthropicRequest,
			wantField: "prediction",
		},
		{
			name:      "audio to responses",
			input:     `{"model":"gpt-4","messages":[{"role":"user","content":"Hi"}],"audio":{"voice":"alloy","format":"wav"}}`,
			convert:   openAIToResponsesRequest,
			wantField: "audio",
		},
		{
			name:      "audio to anthropic",
			input:     `{"model":"gpt-4","messages":[{"role":"user","content":"Hi"}],"audio":{"voice":"alloy","format":"wav"}}`,
			convert:   openAIToAnthropicRequest,
			wantField: "audio",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.convert([]byte(tt.input))
			if err == nil {
				t.Fatalf("expected explicit error for %s, got nil", tt.wantField)
			}
			if !containsSubstring(err.Error(), tt.wantField) {
				t.Fatalf("error = %q, want mention of %s", err.Error(), tt.wantField)
			}
			if !containsSubstring(err.Error(), "cannot be converted") {
				t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
			}
		})
	}
}

func TestOpenAIUnsupportedRequestFieldsAllowBoundaryValues(t *testing.T) {
	t.Run("n equals one to responses passes", func(t *testing.T) {
		input := `{"model":"gpt-4","messages":[{"role":"user","content":"Hi"}],"n":1}`
		body, err := openAIToResponsesRequest([]byte(input))
		if err != nil {
			t.Fatalf("expected nil error for n=1, got: %v", err)
		}
		var req map[string]any
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("failed to unmarshal converted request: %v", err)
		}
		if req["model"] != "gpt-4" {
			t.Fatalf("model = %v, want gpt-4", req["model"])
		}
	})

	t.Run("explicit null fields do not reject", func(t *testing.T) {
		input := `{"model":"gpt-4","messages":[{"role":"user","content":"Hi"}],"logit_bias":null,"prediction":null,"audio":null}`
		for name, convert := range map[string]func([]byte) ([]byte, error){
			"responses": openAIToResponsesRequest,
			"anthropic": openAIToAnthropicRequest,
		} {
			t.Run(name, func(t *testing.T) {
				_, err := convert([]byte(input))
				if err != nil {
					t.Fatalf("expected nil error for explicit-null fields, got: %v", err)
				}
			})
		}
	})
}

func TestResponsesTextVerbosityFailsExplicitly(t *testing.T) {
	input := `{"model":"gpt-4","input":"Hi","text":{"format":{"type":"json_object"},"verbosity":"high"}}`
	for name, convert := range map[string]func([]byte) ([]byte, error){
		"openai":    responsesToOpenAIRequest,
		"anthropic": responsesToAnthropicRequest,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := convert([]byte(input))
			if err == nil {
				t.Fatal("expected explicit verbosity conversion error, got nil")
			}
			if !containsSubstring(err.Error(), "text.verbosity") {
				t.Fatalf("error = %q, want mention of text.verbosity", err.Error())
			}
			if !containsSubstring(err.Error(), "cannot be converted") {
				t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
			}
		})
	}
}

func TestAnthropicRequestPreservesServiceTier(t *testing.T) {
	input := `{"model":"claude-3","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"service_tier":"standard_only"}`

	openAIBody, err := anthropicToOpenAIRequest([]byte(input))
	if err != nil {
		t.Fatalf("anthropic→openai: unexpected error: %v", err)
	}
	var openAIReq map[string]any
	if err := json.Unmarshal(openAIBody, &openAIReq); err != nil {
		t.Fatalf("failed to unmarshal OpenAI request: %v", err)
	}
	if openAIReq["service_tier"] != "standard_only" {
		t.Fatalf("openai service_tier = %v, want standard_only", openAIReq["service_tier"])
	}

	responsesBody, err := anthropicToResponsesRequest([]byte(input))
	if err != nil {
		t.Fatalf("anthropic→responses: unexpected error: %v", err)
	}
	var responsesReq map[string]any
	if err := json.Unmarshal(responsesBody, &responsesReq); err != nil {
		t.Fatalf("failed to unmarshal Responses request: %v", err)
	}
	if responsesReq["service_tier"] != "standard_only" {
		t.Fatalf("responses service_tier = %v, want standard_only", responsesReq["service_tier"])
	}
}

func TestOpenAIToAnthropicUnsupportedRequestFieldsFailExplicitly(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantField string
	}{
		{
			name:      "response_format to anthropic",
			input:     `{"model":"gpt-4","messages":[{"role":"user","content":"Hi"}],"response_format":{"type":"json_schema","json_schema":{"name":"x"}}}`,
			wantField: "response_format",
		},
		{
			name:      "stream_options to anthropic",
			input:     `{"model":"gpt-4","messages":[{"role":"user","content":"Hi"}],"stream":true,"stream_options":{"include_usage":true}}`,
			wantField: "stream_options",
		},
		{
			name:      "top_logprobs to anthropic",
			input:     `{"model":"gpt-4","messages":[{"role":"user","content":"Hi"}],"top_logprobs":3}`,
			wantField: "top_logprobs",
		},
		{
			name:      "presence_penalty to anthropic",
			input:     `{"model":"gpt-4","messages":[{"role":"user","content":"Hi"}],"presence_penalty":0.2}`,
			wantField: "presence_penalty",
		},
		{
			name:      "frequency_penalty to anthropic",
			input:     `{"model":"gpt-4","messages":[{"role":"user","content":"Hi"}],"frequency_penalty":0.4}`,
			wantField: "frequency_penalty",
		},
		{
			name:      "seed to anthropic",
			input:     `{"model":"gpt-4","messages":[{"role":"user","content":"Hi"}],"seed":42}`,
			wantField: "seed",
		},
		{
			name:      "logprobs to anthropic",
			input:     `{"model":"gpt-4","messages":[{"role":"user","content":"Hi"}],"logprobs":true}`,
			wantField: "logprobs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := openAIToAnthropicRequest([]byte(tt.input))
			if err == nil {
				t.Fatalf("expected explicit error for %s, got nil", tt.wantField)
			}
			if !containsSubstring(err.Error(), tt.wantField) {
				t.Fatalf("error = %q, want mention of %s", err.Error(), tt.wantField)
			}
			if !containsSubstring(err.Error(), "cannot be converted") {
				t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
			}
		})
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

func TestConvertOpenAIResponseToAnthropic_MultiChoiceFailsExplicitly(t *testing.T) {
	input := `{"id":"chatcmpl-123","object":"chat.completion","model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"first"},"finish_reason":"stop"},{"index":1,"message":{"role":"assistant","content":"second"},"finish_reason":"stop"}]}`
	_, err := convertOpenAIResponseToAnthropic([]byte(input))
	if err == nil {
		t.Fatal("expected explicit error for multi-choice chat response, got nil")
	}
	if !containsSubstring(err.Error(), "choices") {
		t.Fatalf("error = %q, want mention of choices", err.Error())
	}
	if !containsSubstring(err.Error(), "cannot be converted") {
		t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
	}
}

func TestConvertOpenAIResponseToAnthropic_LogprobsFailExplicitly(t *testing.T) {
	input := `{"id":"chatcmpl-123","object":"chat.completion","model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"Hello!"},"finish_reason":"stop","logprobs":{"content":[{"token":"Hello"}]}}]}`
	_, err := convertOpenAIResponseToAnthropic([]byte(input))
	if err == nil {
		t.Fatal("expected explicit error for chat response logprobs, got nil")
	}
	if !containsSubstring(err.Error(), "logprobs") {
		t.Fatalf("error = %q, want mention of logprobs", err.Error())
	}
	if !containsSubstring(err.Error(), "cannot be converted") {
		t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
	}
}

func TestConvertOpenAIResponseToAnthropic_PreservesRefusal(t *testing.T) {
	input := `{"id":"chatcmpl-123","object":"chat.completion","model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"","refusal":"I can't help with that."},"finish_reason":"content_filter"}]}`
	body, err := convertOpenAIResponseToAnthropic([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if resp["stop_reason"] != "refusal" {
		t.Fatalf("stop_reason = %v, want refusal", resp["stop_reason"])
	}
	content := resp["content"].([]any)
	text := content[0].(map[string]any)
	if text["type"] != "text" || text["text"] != "I can't help with that." {
		t.Fatalf("content = %v, want refusal text block", text)
	}
}

func TestConvertAnthropicResponseToOpenAI_PreservesRefusal(t *testing.T) {
	input := `{"id":"msg_01","type":"message","role":"assistant","content":[{"type":"text","text":"I can't help with that."}],"model":"claude-3","stop_reason":"refusal"}`
	body, err := convertAnthropicResponseToOpenAI([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	choice := resp["choices"].([]any)[0].(map[string]any)
	msg := choice["message"].(map[string]any)
	if msg["refusal"] != "I can't help with that." {
		t.Fatalf("refusal = %v, want refusal preserved", msg["refusal"])
	}
	if choice["finish_reason"] != "content_filter" {
		t.Fatalf("finish_reason = %v, want content_filter", choice["finish_reason"])
	}
	if msg["content"] != "" {
		t.Fatalf("content = %v, want empty string for refusal-only output", msg["content"])
	}
}

func TestConvertAnthropicResponseToOpenAI_UnsupportedContentBlockFailsExplicitly(t *testing.T) {
	input := `{"id":"msg_01","type":"message","role":"assistant","content":[{"type":"thinking","thinking":"internal reasoning","signature":"sig_123"}],"model":"claude-3","stop_reason":"end_turn"}`
	_, err := convertAnthropicResponseToOpenAI([]byte(input))
	if err == nil {
		t.Fatal("expected explicit error for unsupported Anthropic content block, got nil")
	}
	if !containsSubstring(err.Error(), "content block") {
		t.Fatalf("error = %q, want mention of content block", err.Error())
	}
	if !containsSubstring(err.Error(), "cannot be converted") {
		t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
	}
}

func TestConvertResponsesResponseToAnthropic_PreservesRefusal(t *testing.T) {
	input := `{"id":"resp_123","object":"response","model":"gpt-4","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"refusal","refusal":"I can't help with that."}]}]}`
	body, err := convertResponsesResponseMapToAnthropic(mustUnmarshalMap(t, input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body["stop_reason"] != "refusal" {
		t.Fatalf("stop_reason = %v, want refusal", body["stop_reason"])
	}
	content := body["content"].([]any)
	text := content[0].(map[string]any)
	if text["type"] != "text" || text["text"] != "I can't help with that." {
		t.Fatalf("content = %v, want refusal text block", text)
	}
}

func TestConvertResponsesResponseToAnthropic_UnsupportedOutputItemFailsExplicitly(t *testing.T) {
	input := `{"id":"resp_123","object":"response","model":"gpt-4","status":"completed","output":[{"type":"web_search_call","id":"ws_1","status":"completed"}]}`
	_, err := convertResponsesResponseMapToAnthropic(mustUnmarshalMap(t, input))
	if err == nil {
		t.Fatal("expected explicit error for unsupported Responses output item, got nil")
	}
	if !containsSubstring(err.Error(), "output item") {
		t.Fatalf("error = %q, want mention of output item", err.Error())
	}
	if !containsSubstring(err.Error(), "cannot be converted") {
		t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
	}
}

func TestConvertResponsesResponseToAnthropic_RepeatedMessageItemsFailExplicitly(t *testing.T) {
	input := `{"id":"resp_123","object":"response","model":"gpt-4","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"first"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"second"}]}]}`
	_, err := convertResponsesResponseMapToAnthropic(mustUnmarshalMap(t, input))
	if err == nil {
		t.Fatal("expected explicit error for repeated Responses message items, got nil")
	}
	if !containsSubstring(err.Error(), "multiple message output items") {
		t.Fatalf("error = %q, want mention of multiple message output items", err.Error())
	}
	if !containsSubstring(err.Error(), "cannot be converted") {
		t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
	}
}

func TestConvertResponsesResponseToAnthropic_UnsupportedMessageContentPartFailsExplicitly(t *testing.T) {
	input := `{"id":"resp_123","object":"response","model":"gpt-4","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"reasoning_text","text":"internal"}]}]}`
	_, err := convertResponsesResponseMapToAnthropic(mustUnmarshalMap(t, input))
	if err == nil {
		t.Fatal("expected explicit error for unsupported Responses message content part, got nil")
	}
	if !containsSubstring(err.Error(), "message content part") {
		t.Fatalf("error = %q, want mention of message content part", err.Error())
	}
	if !containsSubstring(err.Error(), "cannot be converted") {
		t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
	}
}

func TestConvertResponsesResponseToAnthropic_OutputTextAnnotationsFailExplicitly(t *testing.T) {
	input := `{"id":"resp_123","object":"response","model":"gpt-4","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello","annotations":[{"type":"url_citation","url":"https://example.com"}]}]}]}`
	_, err := convertResponsesResponseMapToAnthropic(mustUnmarshalMap(t, input))
	if err == nil {
		t.Fatal("expected explicit error for output_text annotations, got nil")
	}
	if !containsSubstring(err.Error(), "annotations") {
		t.Fatalf("error = %q, want mention of annotations", err.Error())
	}
	if !containsSubstring(err.Error(), "cannot be converted") {
		t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
	}
}

func TestConvertResponsesResponseToAnthropic_OutputTextLogprobsFailExplicitly(t *testing.T) {
	input := `{"id":"resp_123","object":"response","model":"gpt-4","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello","logprobs":[{"token":"hello"}]}]}]}`
	_, err := convertResponsesResponseMapToAnthropic(mustUnmarshalMap(t, input))
	if err == nil {
		t.Fatal("expected explicit error for output_text logprobs, got nil")
	}
	if !containsSubstring(err.Error(), "logprobs") {
		t.Fatalf("error = %q, want mention of logprobs", err.Error())
	}
	if !containsSubstring(err.Error(), "cannot be converted") {
		t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
	}
}

func TestConvertResponsesResponseToAnthropic_UnsupportedStatusFailsExplicitly(t *testing.T) {
	tests := []struct {
		name   string
		status string
	}{
		{name: "queued", status: "queued"},
		{name: "in_progress", status: "in_progress"},
		{name: "failed", status: "failed"},
		{name: "cancelled", status: "cancelled"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := fmt.Sprintf(`{"id":"resp_123","object":"response","model":"gpt-4","status":%q,"output":[]}`, tt.status)
			_, err := convertResponsesResponseMapToAnthropic(mustUnmarshalMap(t, input))
			if err == nil {
				t.Fatalf("expected explicit error for status %s, got nil", tt.status)
			}
			if !containsSubstring(err.Error(), "status") {
				t.Fatalf("error = %q, want mention of status", err.Error())
			}
			if !containsSubstring(err.Error(), "cannot be converted") {
				t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
			}
		})
	}
}

func TestConvertAnthropicResponseToResponses_PreservesRefusal(t *testing.T) {
	input := `{"id":"msg_01","type":"message","role":"assistant","content":[{"type":"text","text":"I can't help with that."}],"model":"claude-3","stop_reason":"refusal"}`
	body, err := convertAnthropicResponseMapToResponses(mustUnmarshalMap(t, input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := body["output"].([]any)
	message := output[0].(map[string]any)
	content := message["content"].([]any)
	refusal := content[0].(map[string]any)
	if refusal["type"] != "refusal" || refusal["refusal"] != "I can't help with that." {
		t.Fatalf("content = %v, want refusal block", refusal)
	}
}

func TestConvertAnthropicResponseToResponses_PreservesMultipleTextBlocks(t *testing.T) {
	input := `{"id":"msg_01","type":"message","role":"assistant","content":[{"type":"text","text":"first"},{"type":"text","text":"second"}],"model":"claude-3","stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":2}}`
	body, err := convertAnthropicResponseMapToResponses(mustUnmarshalMap(t, input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := body["output"].([]any)
	if len(output) != 1 {
		t.Fatalf("output length = %d, want 1: %s", len(output), mustMarshal(output))
	}
	message := output[0].(map[string]any)
	content := message["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content length = %d, want 2: %s", len(content), mustMarshal(content))
	}
	if content[0].(map[string]any)["text"] != "first" || content[1].(map[string]any)["text"] != "second" {
		t.Fatalf("content = %s, want ordered text blocks", mustMarshal(content))
	}
	if body["status"] != "completed" {
		t.Fatalf("status = %v, want completed", body["status"])
	}
	usage := body["usage"].(map[string]any)
	if toInt(usage["input_tokens"]) != 5 || toInt(usage["output_tokens"]) != 2 {
		t.Fatalf("usage = %v, want preserved anthropic usage", usage)
	}
}

func TestConvertAnthropicResponseToResponses_PreservesOrderedTextAndToolUse(t *testing.T) {
	input := `{"id":"msg_01","type":"message","role":"assistant","content":[{"type":"text","text":"before"},{"type":"tool_use","id":"toolu_1","name":"lookup","input":{"city":"Paris"}},{"type":"text","text":"after"}],"model":"claude-3","stop_reason":"tool_use","usage":{"input_tokens":5,"output_tokens":2,"service_tier":"standard","inference_geo":"us"}}`
	body, err := convertAnthropicResponseMapToResponses(mustUnmarshalMap(t, input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := body["output"].([]any)
	if len(output) != 3 {
		t.Fatalf("output length = %d, want 3: %s", len(output), mustMarshal(output))
	}
	firstMsg := output[0].(map[string]any)
	toolCall := output[1].(map[string]any)
	secondMsg := output[2].(map[string]any)
	if firstMsg["type"] != "message" || firstMsg["role"] != "assistant" {
		t.Fatalf("first output = %s, want assistant message", mustMarshal(firstMsg))
	}
	firstContent := firstMsg["content"].([]any)
	if len(firstContent) != 1 || firstContent[0].(map[string]any)["text"] != "before" {
		t.Fatalf("first message content = %s, want first text block", mustMarshal(firstContent))
	}
	if toolCall["type"] != "function_call" || toolCall["call_id"] != "toolu_1" || toolCall["name"] != "lookup" {
		t.Fatalf("tool call = %s, want preserved tool_use", mustMarshal(toolCall))
	}
	if toolCall["arguments"] != `{"city":"Paris"}` {
		t.Fatalf("arguments = %v, want encoded tool input", toolCall["arguments"])
	}
	secondContent := secondMsg["content"].([]any)
	if len(secondContent) != 1 || secondContent[0].(map[string]any)["text"] != "after" {
		t.Fatalf("second message content = %s, want trailing text block", mustMarshal(secondContent))
	}
	if body["service_tier"] != "standard" || body["inference_geo"] != "us" {
		t.Fatalf("body = %s, want top-level service_tier/inference_geo preserved", mustMarshal(body))
	}
}

func TestConvertAnthropicResponseToResponses_UnsupportedContentBlockFailsExplicitly(t *testing.T) {
	input := `{"id":"msg_01","type":"message","role":"assistant","content":[{"type":"redacted_thinking","data":"opaque"}],"model":"claude-3","stop_reason":"end_turn"}`
	_, err := convertAnthropicResponseMapToResponses(mustUnmarshalMap(t, input))
	if err == nil {
		t.Fatal("expected explicit error for unsupported Anthropic content block, got nil")
	}
	if !containsSubstring(err.Error(), "content block") {
		t.Fatalf("error = %q, want mention of content block", err.Error())
	}
	if !containsSubstring(err.Error(), "cannot be converted") {
		t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
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

func appendJSONLineEvents(t *testing.T, events *[]map[string]any, label string, data []byte) {
	t.Helper()
	if data == nil {
		return
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

func findEventByType(events []map[string]any, eventType string, predicate func(map[string]any) bool) map[string]any {
	for _, event := range events {
		if event["type"] != eventType {
			continue
		}
		if predicate == nil || predicate(event) {
			return event
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Hardening regression tests
// ---------------------------------------------------------------------------

func TestConvertResponsesStreamToOpenAI_DropsUnknownEvent(t *testing.T) {
	unknownEvt := `{"type":"custom.vendor_event","data":"something","sequence_number":42}`
	result, err := convertResponsesStreamToOpenAI(nil, []byte(unknownEvt))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected unknown Responses event to be dropped, got: %s", string(result))
	}
}

func TestConvertResponsesStreamToOpenAI_SkipsKnownNoOps(t *testing.T) {
	for _, evtType := range []string{
		"response.output_text.done",
		"response.function_call_arguments.done",
		"response.created",
		"response.in_progress",
		"response.content_part.added",
		"response.content_part.done",
		"response.output_item.done",
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

func TestConvertResponsesStreamToOpenAI_UnsupportedSemanticEventsFailExplicitly(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "reasoning summary part added",
			payload: `{"type":"response.reasoning_summary_part.added","part":{"type":"summary_text","text":""}}`,
			want:    "response.reasoning_summary_part.added",
		},
		{
			name:    "reasoning summary text delta",
			payload: `{"type":"response.reasoning_summary_text.delta","delta":"The problem"}`,
			want:    "response.reasoning_summary_text.delta",
		},
		{
			name:    "output text annotation added",
			payload: `{"type":"response.output_text.annotation.added","annotation":{"type":"url_citation","url":"https://example.com"}}`,
			want:    "response.output_text.annotation.added",
		},
		{
			name:    "hosted web search event",
			payload: `{"type":"response.web_search_call.in_progress","item_id":"ws_1"}`,
			want:    "response.web_search_call.in_progress",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := convertResponsesStreamToOpenAI(nil, []byte(tt.payload))
			if err == nil {
				t.Fatal("expected explicit error for unsupported semantic event, got nil")
			}
			if !containsSubstring(err.Error(), tt.want) {
				t.Fatalf("error = %q, want mention of %s", err.Error(), tt.want)
			}
			if !containsSubstring(err.Error(), "cannot be converted") {
				t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
			}
		})
	}
}

func TestConvertResponsesStreamToAnthropic_UnsupportedSemanticEventsFailExplicitly(t *testing.T) {
	payload := `{"type":"response.reasoning_summary_part.added","part":{"type":"summary_text","text":""}}`
	_, err := convertResponsesStreamToAnthropic(&Converter{}, []byte(payload))
	if err == nil {
		t.Fatal("expected explicit error for unsupported semantic event, got nil")
	}
	if !containsSubstring(err.Error(), "response.reasoning_summary_part.added") {
		t.Fatalf("error = %q, want mention of semantic event type", err.Error())
	}
	if !containsSubstring(err.Error(), "cannot be converted") {
		t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
	}
}

func TestConvertAnthropicStreamToOpenAI_DropsUnknownEvent(t *testing.T) {
	unknownEvt := `{"type":"custom_event","data":"payload"}`
	result, err := convertAnthropicStreamToOpenAI(nil, []byte(unknownEvt))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected unknown Anthropic event to be dropped, got: %s", string(result))
	}
}

func TestConvertAnthropicStreamToOpenAI_UnsupportedSemanticEventsFailExplicitly(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "citations delta",
			payload: `{"type":"citations_delta","index":0,"citation":{"type":"char_location"}}`,
			want:    "citations_delta",
		},
		{
			name:    "signature delta",
			payload: `{"type":"signature_delta","index":0,"signature":"abc"}`,
			want:    "signature_delta",
		},
		{
			name:    "error event",
			payload: `{"type":"error","error":{"type":"invalid_request_error","message":"bad"}}`,
			want:    "error",
		},
		{
			name:    "server tool event",
			payload: `{"type":"server_tool_use.delta","id":"st_1"}`,
			want:    "server_tool_use.delta",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := convertAnthropicStreamToOpenAI(nil, []byte(tt.payload))
			if err == nil {
				t.Fatal("expected explicit error for unsupported anthropic semantic event, got nil")
			}
			if !containsSubstring(err.Error(), tt.want) {
				t.Fatalf("error = %q, want mention of %s", err.Error(), tt.want)
			}
			if !containsSubstring(err.Error(), "cannot be converted") {
				t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
			}
		})
	}
}

func TestConvertAnthropicStreamToOpenAI_UnsupportedNestedDeltaTypesFailExplicitly(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "nested citations delta",
			payload: `{"type":"content_block_delta","index":0,"delta":{"type":"citations_delta","citation":{"type":"char_location"}}}`,
			want:    "citations_delta",
		},
		{
			name:    "nested signature delta",
			payload: `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"abc"}}`,
			want:    "signature_delta",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := convertAnthropicStreamToOpenAI(nil, []byte(tt.payload))
			if err == nil {
				t.Fatal("expected explicit error for unsupported nested anthropic delta, got nil")
			}
			if !containsSubstring(err.Error(), tt.want) {
				t.Fatalf("error = %q, want mention of %s", err.Error(), tt.want)
			}
			if !containsSubstring(err.Error(), "cannot be converted") {
				t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
			}
		})
	}
}

func TestConvertAnthropicStreamToOpenAI_SkipsKnownNoOps(t *testing.T) {
	for _, evtType := range []string{"ping", "content_block_stop"} {
		t.Run(evtType, func(t *testing.T) {
			evt := `{"type":"` + evtType + `"}`
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

func TestConvertOpenAIStreamToResponses_DropsNoChoicesChunkWithoutUsage(t *testing.T) {
	chunk := `{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"gpt-4","choices":[]}`
	result, err := convertOpenAIStreamToResponses(&Converter{}, []byte(chunk))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected empty-choices chunk without usage to be dropped, got: %s", string(result))
	}
}

func TestConvertOpenAIStreamToResponses_LogprobsFailExplicitly(t *testing.T) {
	chunk := `{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"gpt-4","choices":[{"index":0,"delta":{"content":"hi"},"logprobs":{"content":[{"token":"hi"}]},"finish_reason":null}]}`
	_, err := convertOpenAIStreamToResponses(&Converter{}, []byte(chunk))
	if err == nil {
		t.Fatal("expected explicit error for stream logprobs, got nil")
	}
	if !containsSubstring(err.Error(), "logprobs") {
		t.Fatalf("error = %q, want mention of logprobs", err.Error())
	}
	if !containsSubstring(err.Error(), "cannot be converted") {
		t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
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

func TestConvertOpenAIStreamToAnthropic_LogprobsFailExplicitly(t *testing.T) {
	chunk := `{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"gpt-4","choices":[{"index":0,"delta":{"content":"hi"},"logprobs":{"content":[{"token":"hi"}]},"finish_reason":null}]}`
	_, err := convertOpenAIStreamToAnthropic(&Converter{}, []byte(chunk))
	if err == nil {
		t.Fatal("expected explicit error for stream logprobs, got nil")
	}
	if !containsSubstring(err.Error(), "logprobs") {
		t.Fatalf("error = %q, want mention of logprobs", err.Error())
	}
	if !containsSubstring(err.Error(), "cannot be converted") {
		t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
	}
}

func TestConvertOpenAIStreamToResponses_ToolCallLifecycle(t *testing.T) {
	c := &Converter{From: FormatOpenAI, To: FormatResponses}
	var events []map[string]any

	chunks := []string{
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"gpt-4","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_weather","type":"function","function":{"name":"get_weather","arguments":"{\"city\":"}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"gpt-4","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Paris\"}"}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}`,
		`[DONE]`,
	}

	for i, chunk := range chunks {
		result, err := convertOpenAIStreamToResponses(c, []byte(chunk))
		if err != nil {
			t.Fatalf("chunk%d: unexpected error: %v", i+1, err)
		}
		appendJSONLineEvents(t, &events, fmt.Sprintf("chunk%d", i+1), result)
	}

	if countEvents(events, "response.output_item.added") != 2 {
		t.Fatalf("expected message and function_call output_item.added events: %s", mustMarshal(events))
	}
	toolAdded := findEventByType(events, "response.output_item.added", func(event map[string]any) bool {
		item, _ := event["item"].(map[string]any)
		return item["type"] == "function_call"
	})
	if toolAdded == nil {
		t.Fatalf("missing function_call output_item.added: %s", mustMarshal(events))
	}
	item := toolAdded["item"].(map[string]any)
	if item["call_id"] != "call_weather" || item["name"] != "get_weather" {
		t.Fatalf("tool added item = %s, want call_weather/get_weather", mustMarshal(toolAdded))
	}
	if countEvents(events, "response.function_call_arguments.delta") != 2 {
		t.Fatalf("expected two function_call_arguments.delta events: %s", mustMarshal(events))
	}
	if countEvents(events, "response.function_call_arguments.done") != 1 {
		t.Fatalf("expected one function_call_arguments.done event: %s", mustMarshal(events))
	}
	toolDone := findEventByType(events, "response.output_item.done", func(event map[string]any) bool {
		item, _ := event["item"].(map[string]any)
		return item["type"] == "function_call"
	})
	if toolDone == nil {
		t.Fatalf("missing function_call output_item.done: %s", mustMarshal(events))
	}
	if countEvents(events, "response.completed") != 1 {
		t.Fatalf("expected exactly one response.completed: %s", mustMarshal(events))
	}
}

func TestConvertOpenAIStreamToResponses_TerminalEventsOnlyOnce(t *testing.T) {
	c := &Converter{From: FormatOpenAI, To: FormatResponses}
	var events []map[string]any
	chunks := []string{
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"gpt-4","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`,
		`[DONE]`,
	}
	for i, chunk := range chunks {
		result, err := convertOpenAIStreamToResponses(c, []byte(chunk))
		if err != nil {
			t.Fatalf("chunk%d: unexpected error: %v", i+1, err)
		}
		appendJSONLineEvents(t, &events, fmt.Sprintf("chunk%d", i+1), result)
	}
	if countEvents(events, "response.content_part.done") != 1 || countEvents(events, "response.output_item.done") != 1 || countEvents(events, "response.completed") != 1 {
		t.Fatalf("expected terminal events once: %s", mustMarshal(events))
	}
}

func TestConvertResponsesStreamToOpenAI_FunctionCallLifecycle(t *testing.T) {
	c := &Converter{From: FormatResponses, To: FormatOpenAI, openAIIncludeUsage: true}
	var chunks []map[string]any
	events := []string{
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_weather","name":"get_weather","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","output_index":1,"call_id":"call_weather","delta":"{\"city\":"}`,
		`{"type":"response.function_call_arguments.delta","output_index":1,"call_id":"call_weather","delta":"\"Paris\"}"}`,
		`{"type":"response.function_call_arguments.done","output_index":1,"call_id":"call_weather","arguments":"{\"city\":\"Paris\"}"}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_weather","name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}`,
		`{"type":"response.completed","response":{"id":"resp_1","status":"completed","model":"gpt-4","usage":{"input_tokens":10,"output_tokens":2}}}`,
	}
	for i, event := range events {
		result, err := convertResponsesStreamToOpenAI(c, []byte(event))
		if err != nil {
			t.Fatalf("event%d: unexpected error: %v", i+1, err)
		}
		appendJSONLineEvents(t, &chunks, fmt.Sprintf("event%d", i+1), result)
	}
	if len(chunks) < 3 {
		t.Fatalf("expected multiple OpenAI chunks, got %d: %s", len(chunks), mustMarshal(chunks))
	}
	firstToolCall := chunks[0]["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	if firstToolCall["id"] != "call_weather" {
		t.Fatalf("first tool call id = %v, want call_weather: %s", firstToolCall["id"], mustMarshal(chunks[0]))
	}
	fn := firstToolCall["function"].(map[string]any)
	if fn["name"] != "get_weather" {
		t.Fatalf("first tool call name = %v, want get_weather: %s", fn["name"], mustMarshal(chunks[0]))
	}
	finalChunk := chunks[len(chunks)-2]
	finishReason := finalChunk["choices"].([]any)[0].(map[string]any)["finish_reason"]
	if finishReason != "tool_calls" {
		t.Fatalf("finish_reason = %v, want tool_calls: %s", finishReason, mustMarshal(finalChunk))
	}
}

func TestConvertResponsesStreamToOpenAI_RefusalLifecycle(t *testing.T) {
	c := &Converter{}
	var chunks []map[string]any
	events := []string{
		`{"type":"response.refusal.delta","delta":"I can't","output_index":0,"content_index":0}`,
		`{"type":"response.refusal.done","refusal":"I can't assist with that.","output_index":0,"content_index":0}`,
		`{"type":"response.incomplete","response":{"id":"resp_1","status":"incomplete","incomplete_details":{"reason":"content_filter"}}}`,
	}
	for i, event := range events {
		result, err := convertResponsesStreamToOpenAI(c, []byte(event))
		if err != nil {
			t.Fatalf("event%d: unexpected error: %v", i+1, err)
		}
		appendJSONLineEvents(t, &chunks, fmt.Sprintf("event%d", i+1), result)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected refusal chunk and terminal chunk, got %d: %s", len(chunks), mustMarshal(chunks))
	}
	if len(chunks) != 2 {
		t.Fatalf("expected exactly one refusal chunk plus one terminal chunk, got %d: %s", len(chunks), mustMarshal(chunks))
	}
	refusalDelta := chunks[0]["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)
	if refusalDelta["refusal"] != "I can't" {
		t.Fatalf("delta = %v, want refusal text", refusalDelta)
	}
	if _, ok := chunks[1]["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)["refusal"]; ok {
		t.Fatalf("terminal chunk must not repeat refusal payload after refusal.delta: %s", mustMarshal(chunks[1]))
	}
	finishReason := chunks[len(chunks)-1]["choices"].([]any)[0].(map[string]any)["finish_reason"]
	if finishReason != "content_filter" {
		t.Fatalf("finish_reason = %v, want content_filter", finishReason)
	}
}

func TestConvertResponsesStreamToOpenAI_RefusalDoneWithoutDelta(t *testing.T) {
	result, err := convertResponsesStreamToOpenAI(&Converter{}, []byte(`{"type":"response.refusal.done","refusal":"I can't assist with that.","output_index":0,"content_index":0}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected refusal chunk from refusal.done event")
	}
	var chunk map[string]any
	if err := json.Unmarshal(result, &chunk); err != nil {
		t.Fatalf("failed to unmarshal refusal chunk: %v", err)
	}
	delta := chunk["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)
	if delta["refusal"] != "I can't assist with that." {
		t.Fatalf("delta = %v, want full refusal text", delta)
	}
}

func TestConvertResponsesResponseToOpenAI_IncompleteUsesLength(t *testing.T) {
	input := `{"id":"resp_123","object":"response","model":"gpt-4","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial"}]}]}`
	body, err := convertResponsesResponseToOpenAI([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	finishReason := resp["choices"].([]any)[0].(map[string]any)["finish_reason"]
	if finishReason != "length" {
		t.Fatalf("finish_reason = %v, want length", finishReason)
	}
}

func TestConvertResponsesResponseToOpenAI_IncompleteUsesContentFilter(t *testing.T) {
	input := `{"id":"resp_123","object":"response","model":"gpt-4","status":"incomplete","incomplete_details":{"reason":"content_filter"},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"filtered"}]}]}`
	body, err := convertResponsesResponseToOpenAI([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	finishReason := resp["choices"].([]any)[0].(map[string]any)["finish_reason"]
	if finishReason != "content_filter" {
		t.Fatalf("finish_reason = %v, want content_filter", finishReason)
	}
}

func TestConvertResponsesResponseToOpenAI_UnsupportedStatusFailsExplicitly(t *testing.T) {
	tests := []struct {
		name   string
		status string
	}{
		{name: "queued", status: "queued"},
		{name: "in_progress", status: "in_progress"},
		{name: "failed", status: "failed"},
		{name: "cancelled", status: "cancelled"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := fmt.Sprintf(`{"id":"resp_123","object":"response","model":"gpt-4","status":%q,"output":[]}`, tt.status)
			_, err := convertResponsesResponseToOpenAI([]byte(input))
			if err == nil {
				t.Fatalf("expected explicit error for status %s, got nil", tt.status)
			}
			if !containsSubstring(err.Error(), "status") {
				t.Fatalf("error = %q, want mention of status", err.Error())
			}
			if !containsSubstring(err.Error(), "cannot be converted") {
				t.Fatalf("error = %q, want explicit cannot-be-converted policy", err.Error())
			}
		})
	}
}

func TestConvertOpenAIStreamToResponses_ReasoningUsesReasoningEvents(t *testing.T) {
	c := &Converter{From: FormatOpenAI, To: FormatResponses}
	chunk := `{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"Let me think"},"finish_reason":null}]}`
	result, err := convertOpenAIStreamToResponses(c, []byte(chunk))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(result, "response.reasoning_text.delta") {
		t.Fatalf("expected reasoning event, got: %s", string(result))
	}
	if contains(result, `"type":"response.output_text.delta"`) {
		t.Fatalf("reasoning must not be emitted as output_text.delta: %s", string(result))
	}
}

func TestConvertResponsesStreamToOpenAI_ReasoningPreserved(t *testing.T) {
	event := `{"type":"response.reasoning_text.delta","delta":"reason step"}`
	result, err := convertResponsesStreamToOpenAI(&Converter{}, []byte(event))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected reasoning chunk")
	}
	var chunk map[string]any
	if err := json.Unmarshal(result, &chunk); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	delta := chunk["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)
	if delta["reasoning_content"] != "reason step" {
		t.Fatalf("delta = %v, want reasoning_content", delta)
	}
}

func TestConvertAnthropicStreamToResponses_ThinkingBecomesReasoning(t *testing.T) {
	c := &Converter{From: FormatAnthropic, To: FormatResponses}
	event := `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"ponder"}}`
	result, err := convertAnthropicStreamToResponses(c, []byte(event))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || !contains(result, "response.reasoning_text.delta") {
		t.Fatalf("expected reasoning event, got: %s", string(result))
	}
}

func TestDetectClientFormat_CountTokensIsNotMessages(t *testing.T) {
	for _, path := range []string{"/v1/messages/count_tokens", "/v/messages/count_tokens", "/messages/count_tokens"} {
		if got := DetectClientFormat(path); got != FormatUnknown {
			t.Fatalf("DetectClientFormat(%q) = %q, want unknown", path, got)
		}
	}
}

func TestRewritePath_DoesNotRewriteCountTokens(t *testing.T) {
	for _, format := range []Format{FormatOpenAI, FormatResponses, FormatAnthropic} {
		got := RewritePath("/v1/messages/count_tokens", format)
		if got != "/v1/messages/count_tokens" {
			t.Fatalf("RewritePath count_tokens = %q, want unchanged", got)
		}
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

func TestOpenAINonPortableContentFailsExplicitly(t *testing.T) {
	input := `{"model":"gpt-4","messages":[{"role":"user","content":[{"type":"input_audio","input_audio":{"data":"c29tZS1hdWRpby1kYXRh","format":"wav"}}]}]}`
	_, err := openAIToResponsesRequest([]byte(input))
	if err == nil {
		t.Fatal("expected error for OpenAI non-portable content, got nil")
	}
	if !containsSubstring(err.Error(), "openai non-portable content block") {
		t.Fatalf("responses error = %q, want explicit non-portable content message", err.Error())
	}

	_, err = openAIToAnthropicRequest([]byte(input))
	if err == nil {
		t.Fatal("expected error for OpenAI non-portable content via anthropic conversion, got nil")
	}
	if !containsSubstring(err.Error(), "openai non-portable content block") {
		t.Fatalf("anthropic error = %q, want explicit non-portable content message", err.Error())
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

func TestAnthropicNonPortableContentFailsExplicitly(t *testing.T) {
	input := `{"model":"claude-3","messages":[{"role":"user","content":[{"type":"search_result","source":"web","text":"example"}]}],"max_tokens":100}`
	_, err := anthropicToOpenAIRequest([]byte(input))
	if err == nil {
		t.Fatal("expected error for Anthropic non-portable content block, got nil")
	}
	if !containsSubstring(err.Error(), "anthropic non-portable content block") {
		t.Fatalf("openai error = %q, want explicit non-portable content message", err.Error())
	}

	_, err = anthropicToResponsesRequest([]byte(input))
	if err == nil {
		t.Fatal("expected error for Anthropic non-portable content block via responses bridge, got nil")
	}
	if !containsSubstring(err.Error(), "anthropic non-portable content block") {
		t.Fatalf("responses error = %q, want explicit non-portable content message", err.Error())
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

func TestMalformedResponsesInputImageFailsExplicitly(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "missing image payload",
			input: `{"model":"gpt-4","input":[{"type":"message","role":"user","content":[{"type":"input_image"}]}]}`,
		},
		{
			name:  "empty image_url",
			input: `{"model":"gpt-4","input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":""}]}]}`,
		},
		{
			name:  "non-string image_data",
			input: `{"model":"gpt-4","input":[{"type":"message","role":"user","content":[{"type":"input_image","image_data":123}]}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := responsesToOpenAIRequest([]byte(tt.input))
			if err == nil {
				t.Fatal("expected explicit error for malformed responses input_image, got nil")
			}
			if !containsSubstring(err.Error(), "cannot be converted") {
				t.Fatalf("openai error = %q, want explicit cannot-be-converted policy", err.Error())
			}

			_, err = responsesToAnthropicRequest([]byte(tt.input))
			if err == nil {
				t.Fatal("expected explicit error for malformed responses input_image via anthropic bridge, got nil")
			}
			if !containsSubstring(err.Error(), "cannot be converted") {
				t.Fatalf("anthropic error = %q, want explicit cannot-be-converted policy", err.Error())
			}
		})
	}
}

func TestMalformedOpenAIImageURLFailsExplicitly(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "missing image_url object",
			input: `{"model":"gpt-4","messages":[{"role":"user","content":[{"type":"image_url"}]}]}`,
		},
		{
			name:  "image_url missing url",
			input: `{"model":"gpt-4","messages":[{"role":"user","content":[{"type":"image_url","image_url":{}}]}]}`,
		},
		{
			name:  "malformed data url",
			input: `{"model":"gpt-4","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64"}}]}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := openAIToResponsesRequest([]byte(tt.input))
			if err == nil {
				t.Fatal("expected explicit error for malformed openai image_url to responses, got nil")
			}
			if !containsSubstring(err.Error(), "cannot be converted") {
				t.Fatalf("responses error = %q, want explicit cannot-be-converted policy", err.Error())
			}

			inputAnthropic := strings.TrimSuffix(tt.input, "}") + `,"max_completion_tokens":64}`
			_, err = openAIToAnthropicRequest([]byte(inputAnthropic))
			if err == nil {
				t.Fatal("expected explicit error for malformed openai image_url to anthropic, got nil")
			}
			if !containsSubstring(err.Error(), "cannot be converted") {
				t.Fatalf("anthropic error = %q, want explicit cannot-be-converted policy", err.Error())
			}
		})
	}
}

func TestMalformedAnthropicImageSourceFailsExplicitly(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "missing source",
			input: `{"model":"claude-3","max_tokens":100,"messages":[{"role":"user","content":[{"type":"image"}]}]}`,
		},
		{
			name:  "unsupported source type",
			input: `{"model":"claude-3","max_tokens":100,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"file_id","id":"f_1"}}]}]}`,
		},
		{
			name:  "empty url source",
			input: `{"model":"claude-3","max_tokens":100,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":""}}]}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := anthropicToOpenAIRequest([]byte(tt.input))
			if err == nil {
				t.Fatal("expected explicit error for malformed anthropic image source to openai, got nil")
			}
			if !containsSubstring(err.Error(), "cannot be converted") {
				t.Fatalf("openai error = %q, want explicit cannot-be-converted policy", err.Error())
			}

			_, err = anthropicToResponsesRequest([]byte(tt.input))
			if err == nil {
				t.Fatal("expected explicit error for malformed anthropic image source to responses, got nil")
			}
			if !containsSubstring(err.Error(), "cannot be converted") {
				t.Fatalf("responses error = %q, want explicit cannot-be-converted policy", err.Error())
			}
		})
	}
}

func TestMalformedAnthropicAssistantImageSourceFailsExplicitly(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "assistant unsupported source type",
			input: `{"model":"claude-3","max_tokens":100,"messages":[{"role":"assistant","content":[{"type":"image","source":{"type":"file_id","id":"f_1"}}]}]}`,
		},
		{
			name:  "assistant missing source",
			input: `{"model":"claude-3","max_tokens":100,"messages":[{"role":"assistant","content":[{"type":"image"}]}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := anthropicToOpenAIRequest([]byte(tt.input))
			if err == nil {
				t.Fatal("expected explicit error for malformed anthropic assistant image source to openai, got nil")
			}
			if !containsSubstring(err.Error(), "cannot be converted") {
				t.Fatalf("openai error = %q, want explicit cannot-be-converted policy", err.Error())
			}

			_, err = anthropicToResponsesRequest([]byte(tt.input))
			if err == nil {
				t.Fatal("expected explicit error for malformed anthropic assistant image source to responses, got nil")
			}
			if !containsSubstring(err.Error(), "cannot be converted") {
				t.Fatalf("responses error = %q, want explicit cannot-be-converted policy", err.Error())
			}
		})
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

func TestResponsesHostedToolsFailExplicitly(t *testing.T) {
	input := `{"model":"gpt-4","input":"search","tools":[{"type":"web_search"}]}`
	_, err := responsesToOpenAIRequest([]byte(input))
	if err == nil {
		t.Fatal("expected error for Responses hosted tool, got nil")
	}
	if !containsSubstring(err.Error(), "responses-native tool") {
		t.Fatalf("error = %q, want explicit Responses-native tool message", err.Error())
	}

	_, err = responsesToAnthropicRequest([]byte(input))
	if err == nil {
		t.Fatal("expected error for Responses hosted tool via anthropic bridge, got nil")
	}
	if !containsSubstring(err.Error(), "responses-native tool") {
		t.Fatalf("bridge error = %q, want explicit Responses-native tool message", err.Error())
	}
}

func TestAnthropicBuiltInToolsFailExplicitly(t *testing.T) {
	input := `{"model":"claude-3","messages":[{"role":"user","content":"test"}],"max_tokens":100,"tools":[{"type":"web_search_20250305"}]}`
	_, err := anthropicToOpenAIRequest([]byte(input))
	if err == nil {
		t.Fatal("expected error for Anthropic built-in tool, got nil")
	}
	if !containsSubstring(err.Error(), "anthropic built-in tool") {
		t.Fatalf("error = %q, want explicit built-in tool message", err.Error())
	}
}

func TestOpenAINonPortableToolsFailExplicitly(t *testing.T) {
	input := `{"model":"gpt-4","messages":[{"role":"user","content":"test"}],"tools":[{"type":"custom","name":"grammar_tool","format":{"type":"grammar","syntax":"lark","definition":"start: WORD"}}]}`
	_, err := openAIToResponsesRequest([]byte(input))
	if err == nil {
		t.Fatal("expected error for OpenAI non-portable tool, got nil")
	}
	if !containsSubstring(err.Error(), "openai non-portable tool") {
		t.Fatalf("responses error = %q, want explicit OpenAI non-portable tool message", err.Error())
	}

	_, err = openAIToAnthropicRequest([]byte(input))
	if err == nil {
		t.Fatal("expected error for OpenAI non-portable tool via anthropic conversion, got nil")
	}
	if !containsSubstring(err.Error(), "openai non-portable tool") {
		t.Fatalf("anthropic error = %q, want explicit OpenAI non-portable tool message", err.Error())
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
	if !contains(result, "response.reasoning_text.delta") {
		t.Errorf("chunk1: missing reasoning_text.delta for reasoning: %s", string(result))
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
	if !contains(result, "response.reasoning_text.delta") {
		t.Errorf("chunk2: missing reasoning_text.delta: %s", string(result))
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

func mustUnmarshalMap(t *testing.T, input string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(input), &out); err != nil {
		t.Fatalf("failed to unmarshal fixture: %v", err)
	}
	return out
}
