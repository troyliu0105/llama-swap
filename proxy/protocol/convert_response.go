package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// openai (chat completions) response → responses
// ---------------------------------------------------------------------------

func convertOpenAIResponseMapToResponses(resp map[string]any) (map[string]any, error) {
	out := make(map[string]any, 8)
	out["id"] = prefixID(resp["id"], "resp_")
	out["object"] = "response"
	out["created_at"] = time.Now().Unix()
	out["model"] = resp["model"]

	// Convert choices → output
	var output []any
	if choices, ok := resp["choices"].([]any); ok && len(choices) > 0 {
		for _, ch := range choices {
			choice, ok := ch.(map[string]any)
			if !ok {
				continue
			}
			msg, _ := choice["message"].(map[string]any)
			if msg == nil {
				continue
			}

			// Text message
			content := msg["content"]
			contentStr, _ := content.(string)

			var contentParts []any
			if contentStr != "" {
				contentParts = append(contentParts, map[string]any{
					"type": "output_text",
					"text": contentStr,
				})
			}

			// Tool calls
			if toolCalls, ok := msg["tool_calls"].([]any); ok {
				for _, tc := range toolCalls {
					tcMap, ok := tc.(map[string]any)
					if !ok {
						continue
					}
					fn, _ := tcMap["function"].(map[string]any)
					output = append(output, map[string]any{
						"type":      "function_call",
						"id":        tcMap["id"],
						"call_id":   tcMap["id"],
						"name":      fn["name"],
						"arguments": fn["arguments"],
					})
				}
			}

			if len(contentParts) > 0 {
				output = append(output, map[string]any{
					"type":    "message",
					"id":      prefixID(nil, "msg_"),
					"role":    "assistant",
					"content": contentParts,
				})
			}
		}
	}

	if len(output) == 0 {
		output = append(output, map[string]any{
			"type":    "message",
			"id":      prefixID(nil, "msg_"),
			"role":    "assistant",
			"content": []any{},
		})
	}

	out["output"] = output
	out["status"] = "completed"

	if usage, ok := resp["usage"].(map[string]any); ok {
		out["usage"] = convertOpenAIUsageToResponses(usage)
	}
	copyFields(out, resp, "service_tier")

	return out, nil
}

// Legacy byte-based wrapper for direct tests.
func convertOpenAIResponseToResponses(body []byte) ([]byte, error) {
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	out, err := convertOpenAIResponseMapToResponses(resp)
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

// ---------------------------------------------------------------------------
// responses → openai (chat completions) response
// ---------------------------------------------------------------------------

func convertResponsesResponseMapToOpenAI(resp map[string]any) (map[string]any, error) {
	out := make(map[string]any, 8)
	out["id"] = prefixID(resp["id"], "chatcmpl-")
	out["object"] = "chat.completion"
	out["created"] = time.Now().Unix()
	out["model"] = resp["model"]

	var choices []any
	finishReason := responsesStatusToOpenAIFinishReason(resp)
	messageContent := ""
	var toolCalls []any

	if output, ok := resp["output"].([]any); ok {
		for _, item := range output {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			itemType, _ := itemMap["type"].(string)

			switch itemType {
			case "message":
				if content, ok := itemMap["content"].([]any); ok {
					for _, c := range content {
						cMap, ok := c.(map[string]any)
						if !ok {
							continue
						}
						if cMap["type"] == "output_text" {
							if text, ok := cMap["text"].(string); ok {
								messageContent += text
							}
						}
					}
				}
			case "function_call":
				finishReason = "tool_calls"
				toolCalls = append(toolCalls, map[string]any{
					"id":   itemMap["call_id"],
					"type": "function",
					"function": map[string]any{
						"name":      itemMap["name"],
						"arguments": itemMap["arguments"],
					},
				})
			}
		}
	}

	msg := map[string]any{
		"role":    "assistant",
		"content": messageContent,
	}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
		msg["content"] = nil
	}

	choices = append(choices, map[string]any{
		"index":         0,
		"message":       msg,
		"finish_reason": finishReason,
	})
	out["choices"] = choices

	if usage, ok := resp["usage"].(map[string]any); ok {
		out["usage"] = convertResponsesUsageToOpenAI(usage)
	}
	copyFields(out, resp, "service_tier")

	return out, nil
}

func convertResponsesResponseToOpenAI(body []byte) ([]byte, error) {
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	out, err := convertResponsesResponseMapToOpenAI(resp)
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

// ---------------------------------------------------------------------------
// openai (chat completions) response → anthropic
// ---------------------------------------------------------------------------

func convertOpenAIResponseMapToAnthropic(resp map[string]any) (map[string]any, error) {
	out := make(map[string]any, 8)
	out["id"] = prefixID(resp["id"], "msg_")
	out["type"] = "message"
	out["role"] = "assistant"
	out["model"] = resp["model"]

	var content []any
	stopReason := "end_turn"

	if choices, ok := resp["choices"].([]any); ok && len(choices) > 0 {
		choice, _ := choices[0].(map[string]any)
		if choice != nil {
			msg, _ := choice["message"].(map[string]any)
			if msg != nil {
				// Text content
				if text, ok := msg["content"].(string); ok && text != "" {
					content = append(content, map[string]any{
						"type": "text",
						"text": text,
					})
				}

				// Tool calls
				if toolCalls, ok := msg["tool_calls"].([]any); ok {
					for _, tc := range toolCalls {
						tcMap, ok := tc.(map[string]any)
						if !ok {
							continue
						}
						fn, _ := tcMap["function"].(map[string]any)
						argsStr, _ := fn["arguments"].(string)
						var argsParsed any
						if err := json.Unmarshal([]byte(argsStr), &argsParsed); err != nil {
							argsParsed = map[string]any{}
						}
						content = append(content, map[string]any{
							"type":  "tool_use",
							"id":    tcMap["id"],
							"name":  fn["name"],
							"input": argsParsed,
						})
					}
					stopReason = "tool_use"
				}
			}

			// Map finish_reason
			if fr, ok := choice["finish_reason"].(string); ok {
				stopReason = openAIFinishReasonToAnthropicStopReason(fr)
			}
		}
	}

	if len(content) == 0 {
		content = append(content, map[string]any{
			"type": "text",
			"text": "",
		})
	}

	out["content"] = content
	out["stop_reason"] = stopReason
	out["stop_sequence"] = nil

	if usage, ok := resp["usage"].(map[string]any); ok {
		out["usage"] = convertOpenAIUsageToAnthropic(usage)
	}

	return out, nil
}

func convertOpenAIResponseToAnthropic(body []byte) ([]byte, error) {
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	out, err := convertOpenAIResponseMapToAnthropic(resp)
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

// ---------------------------------------------------------------------------
// anthropic → openai (chat completions) response
// ---------------------------------------------------------------------------

func convertAnthropicResponseMapToOpenAI(resp map[string]any) (map[string]any, error) {
	out := make(map[string]any, 8)
	out["id"] = prefixID(resp["id"], "chatcmpl-")
	out["object"] = "chat.completion"
	out["created"] = time.Now().Unix()
	out["model"] = resp["model"]

	finishReason := "stop"
	messageContent := ""
	var toolCalls []any

	if content, ok := resp["content"].([]any); ok {
		for _, block := range content {
			blockMap, ok := block.(map[string]any)
			if !ok {
				continue
			}
			blockType, _ := blockMap["type"].(string)
			switch blockType {
			case "text":
				if text, ok := blockMap["text"].(string); ok {
					messageContent += text
				}
			case "tool_use":
				finishReason = "tool_calls"
				inputJSON, _ := json.Marshal(blockMap["input"])
				toolCalls = append(toolCalls, map[string]any{
					"id":   blockMap["id"],
					"type": "function",
					"function": map[string]any{
						"name":      blockMap["name"],
						"arguments": string(inputJSON),
					},
				})
			}
		}
	}

	msg := map[string]any{
		"role":    "assistant",
		"content": messageContent,
	}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
	}

	// Map stop_reason
	if sr, ok := resp["stop_reason"].(string); ok {
		finishReason = anthropicStopReasonToOpenAIFinishReason(sr)
	}

	out["choices"] = []any{
		map[string]any{
			"index":         0,
			"message":       msg,
			"finish_reason": finishReason,
		},
	}

	if usage, ok := resp["usage"].(map[string]any); ok {
		out["usage"] = convertAnthropicUsageToOpenAI(usage)
		copyFields(out, usage, "service_tier", "inference_geo")
	}

	return out, nil
}

func convertAnthropicResponseToOpenAI(body []byte) ([]byte, error) {
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	out, err := convertAnthropicResponseMapToOpenAI(resp)
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

// ---------------------------------------------------------------------------
// responses → anthropic response
// ---------------------------------------------------------------------------

func convertResponsesResponseMapToAnthropic(resp map[string]any) (map[string]any, error) {
	// Convert via openai as intermediate
	openaiMap, err := convertResponsesResponseMapToOpenAI(resp)
	if err != nil {
		return nil, err
	}
	return convertOpenAIResponseMapToAnthropic(openaiMap)
}

// ---------------------------------------------------------------------------
// anthropic → responses response
// ---------------------------------------------------------------------------

func convertAnthropicResponseMapToResponses(resp map[string]any) (map[string]any, error) {
	// Convert via openai as intermediate
	openaiMap, err := convertAnthropicResponseMapToOpenAI(resp)
	if err != nil {
		return nil, err
	}
	return convertOpenAIResponseMapToResponses(openaiMap)
}

// ---------------------------------------------------------------------------
func convertOpenAIUsageToResponses(usage map[string]any) map[string]any {
	out := make(map[string]any, 5)
	copyFields(out, usage, "total_tokens")
	if v, ok := usage["prompt_tokens"]; ok {
		out["input_tokens"] = v
	}
	if v, ok := usage["completion_tokens"]; ok {
		out["output_tokens"] = v
	}
	if details, ok := usage["prompt_tokens_details"].(map[string]any); ok {
		inputDetails := make(map[string]any, len(details))
		copyFields(inputDetails, details, "cached_tokens", "audio_tokens", "cache_creation_tokens", "cache_read_tokens")
		if len(inputDetails) > 0 {
			out["input_tokens_details"] = inputDetails
		}
	}
	if details, ok := usage["completion_tokens_details"].(map[string]any); ok {
		outputDetails := make(map[string]any, len(details))
		copyFields(outputDetails, details, "reasoning_tokens", "audio_tokens", "accepted_prediction_tokens", "rejected_prediction_tokens")
		if len(outputDetails) > 0 {
			out["output_tokens_details"] = outputDetails
		}
	}
	return out
}

func convertResponsesUsageToOpenAI(usage map[string]any) map[string]any {
	out := make(map[string]any, 4)
	inputTokens := toInt(usage["input_tokens"])
	outputTokens := toInt(usage["output_tokens"])
	out["prompt_tokens"] = inputTokens
	out["completion_tokens"] = outputTokens
	if total, ok := usage["total_tokens"]; ok {
		out["total_tokens"] = total
	} else {
		out["total_tokens"] = inputTokens + outputTokens
	}
	if details, ok := usage["input_tokens_details"].(map[string]any); ok {
		promptDetails := make(map[string]any, len(details))
		copyFields(promptDetails, details, "cached_tokens", "audio_tokens", "cache_creation_tokens", "cache_read_tokens")
		if len(promptDetails) > 0 {
			out["prompt_tokens_details"] = promptDetails
		}
	}
	if details, ok := usage["output_tokens_details"].(map[string]any); ok {
		completionDetails := make(map[string]any, len(details))
		copyFields(completionDetails, details, "reasoning_tokens", "audio_tokens", "accepted_prediction_tokens", "rejected_prediction_tokens")
		if len(completionDetails) > 0 {
			out["completion_tokens_details"] = completionDetails
		}
	}
	return out
}

func convertOpenAIUsageToAnthropic(usage map[string]any) map[string]any {
	out := make(map[string]any, 4)
	promptTokens := toInt(usage["prompt_tokens"])
	outputTokens := toInt(usage["completion_tokens"])
	cacheReadTokens := int64(0)
	cacheCreationTokens := int64(0)
	if details, ok := usage["prompt_tokens_details"].(map[string]any); ok {
		cacheReadTokens = toInt(details["cached_tokens"])
		cacheCreationTokens = toInt(details["cache_creation_tokens"])
	}
	inputTokens := promptTokens - cacheReadTokens - cacheCreationTokens
	if inputTokens < 0 {
		inputTokens = 0
	}
	out["input_tokens"] = inputTokens
	out["output_tokens"] = outputTokens
	if cacheCreationTokens > 0 {
		out["cache_creation_input_tokens"] = cacheCreationTokens
	}
	if cacheReadTokens > 0 {
		out["cache_read_input_tokens"] = cacheReadTokens
	}
	return out
}

func convertAnthropicUsageToOpenAI(usage map[string]any) map[string]any {
	out := make(map[string]any, 4)
	inputTokens := toInt(usage["input_tokens"])
	outputTokens := toInt(usage["output_tokens"])
	cacheCreationTokens := toInt(usage["cache_creation_input_tokens"])
	cacheReadTokens := toInt(usage["cache_read_input_tokens"])
	promptTokens := inputTokens + cacheCreationTokens + cacheReadTokens
	out["prompt_tokens"] = promptTokens
	out["completion_tokens"] = outputTokens
	out["total_tokens"] = promptTokens + outputTokens
	promptDetails := map[string]any{}
	if cacheReadTokens > 0 {
		promptDetails["cached_tokens"] = cacheReadTokens
		promptDetails["cache_read_tokens"] = cacheReadTokens
	}
	if cacheCreationTokens > 0 {
		promptDetails["cache_creation_tokens"] = cacheCreationTokens
	}
	if len(promptDetails) > 0 {
		out["prompt_tokens_details"] = promptDetails
	}
	return out
}

func copyFields(dst, src map[string]any, fields ...string) {
	for _, field := range fields {
		if value, ok := src[field]; ok {
			dst[field] = value
		}
	}
}

// helpers
// ---------------------------------------------------------------------------

// prefixID ensures an ID has the given prefix.
func prefixID(id any, prefix string) string {
	s, _ := id.(string)
	if s == "" {
		return prefix + fmt.Sprintf("%d", time.Now().UnixNano())
	}
	if strings.HasPrefix(s, prefix) {
		return s
	}
	return prefix + s
}

// toInt converts a numeric value to int.
func toInt(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	default:
		return 0
	}
}

func openAIFinishReasonToAnthropicStopReason(reason string) string {
	switch reason {
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	default:
		return "end_turn"
	}
}

func anthropicStopReasonToOpenAIFinishReason(reason string) string {
	switch reason {
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return "stop"
	}
}

func responsesStatusToOpenAIFinishReason(resp map[string]any) string {
	status, _ := resp["status"].(string)
	if status == "incomplete" {
		if details, ok := resp["incomplete_details"].(map[string]any); ok {
			reason, _ := details["reason"].(string)
			switch reason {
			case "max_output_tokens", "max_tokens":
				return "length"
			case "content_filter":
				return "content_filter"
			}
		}
		return "length"
	}
	return "stop"
}
