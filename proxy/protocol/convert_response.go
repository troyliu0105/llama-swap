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

	// Convert usage
	if usage, ok := resp["usage"].(map[string]any); ok {
		out["usage"] = map[string]any{
			"input_tokens":  usage["prompt_tokens"],
			"output_tokens": usage["completion_tokens"],
			"total_tokens":  usage["total_tokens"],
		}
	}

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
	finishReason := "stop"
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

	// Convert usage
	if usage, ok := resp["usage"].(map[string]any); ok {
		inputTokens, _ := usage["input_tokens"].(float64)
		outputTokens, _ := usage["output_tokens"].(float64)
		out["usage"] = map[string]any{
			"prompt_tokens":     int64(inputTokens),
			"completion_tokens": int64(outputTokens),
			"total_tokens":      int64(inputTokens + outputTokens),
		}
	}

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
				switch fr {
				case "stop":
					stopReason = "end_turn"
				case "length":
					stopReason = "max_tokens"
				case "tool_calls":
					stopReason = "tool_use"
				}
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

	// Convert usage
	if usage, ok := resp["usage"].(map[string]any); ok {
		out["usage"] = map[string]any{
			"input_tokens":  toInt(usage["prompt_tokens"]),
			"output_tokens": toInt(usage["completion_tokens"]),
		}
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
		switch sr {
		case "end_turn":
			finishReason = "stop"
		case "max_tokens":
			finishReason = "length"
		case "tool_use":
			finishReason = "tool_calls"
		case "stop_sequence":
			finishReason = "stop"
		}
	}

	out["choices"] = []any{
		map[string]any{
			"index":         0,
			"message":       msg,
			"finish_reason": finishReason,
		},
	}

	// Convert usage
	if usage, ok := resp["usage"].(map[string]any); ok {
		inputTokens := toInt(usage["input_tokens"])
		outputTokens := toInt(usage["output_tokens"])
		out["usage"] = map[string]any{
			"prompt_tokens":     inputTokens,
			"completion_tokens": outputTokens,
			"total_tokens":      inputTokens + outputTokens,
		}
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
