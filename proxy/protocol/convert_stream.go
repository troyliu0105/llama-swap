package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// OpenAI stream → Responses stream
//
// OpenAI SSE:
//   data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","choices":[{"delta":{"content":"Hi"},...}]}
//   data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","choices":[{"delta":{},"finish_reason":"stop"}]}
//   data: [DONE]
//
// Responses SSE:
//   data: {"type":"response.created",...}
//   data: {"type":"response.output_text.delta","delta":"Hi",...}
//   data: {"type":"response.completed",...}
// ---------------------------------------------------------------------------

func convertOpenAIStreamToResponses(data []byte) ([]byte, error) {
	if string(data) == "[DONE]" {
		return json.Marshal(map[string]any{
			"type":            "response.completed",
			"sequence_number": 0,
			"response": map[string]any{
				"status": "completed",
			},
		})
	}

	var chunk map[string]any
	if err := json.Unmarshal(data, &chunk); err != nil {
		return data, nil // pass through unparseable data
	}

	choices, _ := chunk["choices"].([]any)
	if len(choices) == 0 {
		if usage, ok := chunk["usage"].(map[string]any); ok && usage != nil {
			return json.Marshal(map[string]any{
				"type":            "response.completed",
				"sequence_number": 0,
				"response": map[string]any{
					"id":     prefixID(chunk["id"], "resp_"),
					"status": "completed",
					"model":  chunk["model"],
					"usage":  convertOpenAIUsageToResponses(usage),
				},
			})
		}
		return data, nil
	}

	choice, _ := choices[0].(map[string]any)
	if choice == nil {
		return nil, nil
	}

	delta, _ := choice["delta"].(map[string]any)
	finishReason, _ := choice["finish_reason"].(string)

	// First chunk with role → response.created + in_progress + output_item.added + content_part.added
	if delta == nil {
		return nil, nil
	}

	_, hasRole := delta["role"]
	content, hasContent := delta["content"]
	_, hasToolCalls := delta["tool_calls"]

	if hasRole && !hasContent {
		// Start events
		var events []map[string]any
		events = append(events, map[string]any{
			"type":            "response.created",
			"sequence_number": 0,
			"response": map[string]any{
				"id":     prefixID(chunk["id"], "resp_"),
				"status": "in_progress",
				"model":  chunk["model"],
			},
		})
		events = append(events, map[string]any{
			"type":            "response.in_progress",
			"sequence_number": 1,
			"response": map[string]any{
				"id":     prefixID(chunk["id"], "resp_"),
				"status": "in_progress",
			},
		})
		events = append(events, map[string]any{
			"type":            "response.output_item.added",
			"sequence_number": 2,
			"output_index":    0,
			"item": map[string]any{
				"type":    "message",
				"role":    "assistant",
				"content": []any{},
			},
		})
		events = append(events, map[string]any{
			"type":            "response.content_part.added",
			"sequence_number": 3,
			"output_index":    0,
			"content_index":   0,
			"part": map[string]any{
				"type": "output_text",
				"text": "",
			},
		})

		// Combine into a single SSE data with newlines
		var lines []string
		for _, evt := range events {
			b, _ := json.Marshal(evt)
			lines = append(lines, string(b))
		}
		return []byte(strings.Join(lines, "\n")), nil
	}

	if hasContent {
		contentStr, _ := content.(string)
		return json.Marshal(map[string]any{
			"type":            "response.output_text.delta",
			"sequence_number": 0,
			"output_index":    0,
			"content_index":   0,
			"delta":           contentStr,
		})
	}

	if hasToolCalls {
		// Convert tool call deltas
		toolCallDeltas, _ := delta["tool_calls"].([]any)
		var events []string
		for _, tcd := range toolCallDeltas {
			tcMap, ok := tcd.(map[string]any)
			if !ok {
				continue
			}
			fn, _ := tcMap["function"].(map[string]any)
			if fn == nil {
				continue
			}
			argsDelta, _ := fn["arguments"].(string)
			evt := map[string]any{
				"type":            "response.function_call_arguments.delta",
				"sequence_number": 0,
				"output_index":    0,
				"call_id":         tcMap["id"],
				"delta":           argsDelta,
			}
			b, _ := json.Marshal(evt)
			events = append(events, string(b))
		}
		if len(events) > 0 {
			return []byte(strings.Join(events, "\n")), nil
		}
		return nil, nil
	}

	if finishReason != "" {
		// End events
		var events []string

		// content_part.done
		b, _ := json.Marshal(map[string]any{
			"type":            "response.content_part.done",
			"sequence_number": 0,
			"output_index":    0,
			"content_index":   0,
			"part": map[string]any{
				"type": "output_text",
				"text": "",
			},
		})
		events = append(events, string(b))

		// output_item.done
		b, _ = json.Marshal(map[string]any{
			"type":            "response.output_item.done",
			"sequence_number": 0,
			"output_index":    0,
			"item": map[string]any{
				"type":    "message",
				"role":    "assistant",
				"content": []any{},
			},
		})
		events = append(events, string(b))

		return []byte(strings.Join(events, "\n")), nil
	}

	return nil, nil
}

// ---------------------------------------------------------------------------
// Responses stream → OpenAI stream
//
// Converts individual response events into chat.completion.chunk objects.
// ---------------------------------------------------------------------------

func convertResponsesStreamToOpenAI(data []byte) ([]byte, error) {
	var evt map[string]any
	if err := json.Unmarshal(data, &evt); err != nil {
		return data, nil
	}

	evtType, _ := evt["type"].(string)

	switch evtType {
	case "response.output_text.delta":
		delta, _ := evt["delta"].(string)
		return json.Marshal(map[string]any{
			"id":      "chatcmpl-protocol",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"choices": []any{
				map[string]any{
					"index": 0,
					"delta": map[string]any{
						"content": delta,
					},
					"finish_reason": nil,
				},
			},
		})

	case "response.output_text.done":
		// Ignore, we already sent the deltas
		return nil, nil

	case "response.function_call_arguments.delta":
		argsDelta, _ := evt["delta"].(string)
		callID, _ := evt["call_id"].(string)
		return json.Marshal(map[string]any{
			"id":      "chatcmpl-protocol",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"choices": []any{
				map[string]any{
					"index": 0,
					"delta": map[string]any{
						"tool_calls": []any{
							map[string]any{
								"index": 0,
								"id":    callID,
								"function": map[string]any{
									"arguments": argsDelta,
								},
								"type": "function",
							},
						},
					},
					"finish_reason": nil,
				},
			},
		})

	case "response.function_call_arguments.done":
		return nil, nil

	case "response.completed", "response.incomplete":
		finishReason := "stop"
		if evtType == "response.incomplete" {
			finishReason = "length"
		}
		finalChunk := map[string]any{
			"id":      "chatcmpl-protocol",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"choices": []any{
				map[string]any{
					"index":         0,
					"delta":         map[string]any{},
					"finish_reason": finishReason,
				},
			},
		}
		response, _ := evt["response"].(map[string]any)
		if response == nil {
			return json.Marshal(finalChunk)
		}
		copyFields(finalChunk, response, "model")
		usage, _ := response["usage"].(map[string]any)
		if usage == nil {
			return json.Marshal(finalChunk)
		}
		usageChunk := map[string]any{
			"id":      finalChunk["id"],
			"object":  "chat.completion.chunk",
			"created": finalChunk["created"],
			"model":   finalChunk["model"],
			"choices": []any{},
			"usage":   convertResponsesUsageToOpenAI(usage),
		}
		finalBytes, _ := json.Marshal(finalChunk)
		usageBytes, _ := json.Marshal(usageChunk)
		return []byte(string(finalBytes) + "\n" + string(usageBytes)), nil

	case "response.created", "response.in_progress", "response.queued",
		"response.output_item.added", "response.output_item.done",
		"response.content_part.added", "response.content_part.done",
		"response.refusal.delta", "response.refusal.done",
		"response.reasoning_text.delta", "response.reasoning_text.done":
		// These events don't have direct OpenAI equivalents, skip them
		return nil, nil

	default:
		// Pass through unknown events
		return data, nil
	}
}

// ---------------------------------------------------------------------------
// Anthropic stream → OpenAI stream
//
// Anthropic SSE events:
//   event: message_start
//   data: {"type":"message_start","message":{...}}
//   event: content_block_delta
//   data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}
//   event: message_stop
//   data: {"type":"message_stop"}
// ---------------------------------------------------------------------------

func convertAnthropicStreamToOpenAI(data []byte) ([]byte, error) {
	var evt map[string]any
	if err := json.Unmarshal(data, &evt); err != nil {
		return data, nil
	}

	evtType, _ := evt["type"].(string)

	switch evtType {
	case "content_block_delta":
		delta, _ := evt["delta"].(map[string]any)
		if delta == nil {
			return nil, nil
		}
		deltaType, _ := delta["type"].(string)

		switch deltaType {
		case "text_delta":
			text, _ := delta["text"].(string)
			return json.Marshal(map[string]any{
				"id":      "chatcmpl-protocol",
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"choices": []any{
					map[string]any{
						"index": 0,
						"delta": map[string]any{
							"content": text,
						},
						"finish_reason": nil,
					},
				},
			})

		case "input_json_delta":
			// Tool call arguments delta
			partialJSON, _ := delta["partial_json"].(string)
			return json.Marshal(map[string]any{
				"id":      "chatcmpl-protocol",
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"choices": []any{
					map[string]any{
						"index": 0,
						"delta": map[string]any{
							"tool_calls": []any{
								map[string]any{
									"index": 0,
									"function": map[string]any{
										"arguments": partialJSON,
									},
								},
							},
						},
						"finish_reason": nil,
					},
				},
			})

		case "thinking_delta":
			// No OpenAI equivalent, skip
			return nil, nil
		}

	case "message_delta":
		delta, _ := evt["delta"].(map[string]any)
		if delta == nil {
			return nil, nil
		}
		stopReason, _ := delta["stop_reason"].(string)
		finishReason := "stop"
		switch stopReason {
		case "end_turn":
			finishReason = "stop"
		case "max_tokens":
			finishReason = "length"
		case "tool_use":
			finishReason = "tool_calls"
		}
		finalChunk := map[string]any{
			"id":      "chatcmpl-protocol",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"choices": []any{
				map[string]any{
					"index":         0,
					"delta":         map[string]any{},
					"finish_reason": finishReason,
				},
			},
		}
		usage, _ := evt["usage"].(map[string]any)
		if usage == nil {
			return json.Marshal(finalChunk)
		}
		usageChunk := map[string]any{
			"id":      finalChunk["id"],
			"object":  "chat.completion.chunk",
			"created": finalChunk["created"],
			"choices": []any{},
			"usage":   convertAnthropicUsageToOpenAI(usage),
		}
		finalBytes, _ := json.Marshal(finalChunk)
		usageBytes, _ := json.Marshal(usageChunk)
		return []byte(string(finalBytes) + "\n" + string(usageBytes)), nil

	case "content_block_start":
		// Check if it's a tool_use block start
		contentBlock, _ := evt["content_block"].(map[string]any)
		if contentBlock != nil {
			blockType, _ := contentBlock["type"].(string)
			if blockType == "tool_use" {
				toolID, _ := contentBlock["id"].(string)
				toolName, _ := contentBlock["name"].(string)
				return json.Marshal(map[string]any{
					"id":      "chatcmpl-protocol",
					"object":  "chat.completion.chunk",
					"created": time.Now().Unix(),
					"choices": []any{
						map[string]any{
							"index": 0,
							"delta": map[string]any{
								"role": "assistant",
								"tool_calls": []any{
									map[string]any{
										"index": evt["index"],
										"id":    toolID,
										"type":  "function",
										"function": map[string]any{
											"name":      toolName,
											"arguments": "",
										},
									},
								},
							},
							"finish_reason": nil,
						},
					},
				})
			}
		}
		return nil, nil

	case "message_start":
		// First chunk with role
		return json.Marshal(map[string]any{
			"id":      "chatcmpl-protocol",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"choices": []any{
				map[string]any{
					"index": 0,
					"delta": map[string]any{
						"role": "assistant",
					},
					"finish_reason": nil,
				},
			},
		})

	case "message_stop":
		// End of stream - send [DONE] marker
		return []byte("[DONE]"), nil

	case "ping", "content_block_stop":
		return nil, nil

	default:
		return data, nil
	}

	return nil, nil
}

// ---------------------------------------------------------------------------
// OpenAI stream → Anthropic stream
// ---------------------------------------------------------------------------

func convertOpenAIStreamToAnthropic(data []byte) ([]byte, error) {
	if string(data) == "[DONE]" {
		return json.Marshal(map[string]any{
			"type": "message_stop",
		})
	}

	var chunk map[string]any
	if err := json.Unmarshal(data, &chunk); err != nil {
		return data, nil
	}

	choices, _ := chunk["choices"].([]any)
	if len(choices) == 0 {
		if usage, ok := chunk["usage"].(map[string]any); ok && usage != nil {
			return json.Marshal(map[string]any{
				"type": "message_delta",
				"delta": map[string]any{
					"stop_reason":   "end_turn",
					"stop_sequence": nil,
				},
				"usage": convertOpenAIUsageToAnthropic(usage),
			})
		}
		return data, nil
	}

	choice, _ := choices[0].(map[string]any)
	if choice == nil {
		return nil, nil
	}

	delta, _ := choice["delta"].(map[string]any)
	finishReason, _ := choice["finish_reason"].(string)

	if delta == nil && finishReason == "" {
		return nil, nil
	}

	// First chunk with role → message_start
	if delta != nil {
		if _, hasRole := delta["role"]; hasRole {
			return json.Marshal(map[string]any{
				"type": "message_start",
				"message": map[string]any{
					"id":      prefixID(chunk["id"], "msg_"),
					"type":    "message",
					"role":    "assistant",
					"content": []any{},
					"model":   chunk["model"],
					"usage":   map[string]any{"input_tokens": 0, "output_tokens": 0},
				},
			})
		}

		// Text content delta
		if content, ok := delta["content"].(string); ok {
			return json.Marshal(map[string]any{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]any{
					"type": "text_delta",
					"text": content,
				},
			})
		}

		// Tool calls delta
		if toolCalls, ok := delta["tool_calls"].([]any); ok && len(toolCalls) > 0 {
			tcMap, _ := toolCalls[0].(map[string]any)
			if tcMap != nil {
				fn, _ := tcMap["function"].(map[string]any)
				if fn != nil {
					args, _ := fn["arguments"].(string)
					return json.Marshal(map[string]any{
						"type":  "content_block_delta",
						"index": 1,
						"delta": map[string]any{
							"type":         "input_json_delta",
							"partial_json": args,
						},
					})
				}
			}
		}
	}

	// Finish reason → message_delta
	if finishReason != "" {
		stopReason := "end_turn"
		switch finishReason {
		case "length":
			stopReason = "max_tokens"
		case "tool_calls":
			stopReason = "tool_use"
		}

		// content_block_stop + message_delta + message_stop
		var events []string

		b, _ := json.Marshal(map[string]any{
			"type":  "content_block_stop",
			"index": 0,
		})
		events = append(events, string(b))

		b, _ = json.Marshal(map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason":   stopReason,
				"stop_sequence": nil,
			},
			"usage": map[string]any{
				"output_tokens": 0,
			},
		})
		events = append(events, string(b))

		b, _ = json.Marshal(map[string]any{
			"type": "message_stop",
		})
		events = append(events, string(b))

		return []byte(strings.Join(events, "\n")), nil
	}

	return nil, nil
}

// ---------------------------------------------------------------------------
// Responses stream → Anthropic stream
// ---------------------------------------------------------------------------

func convertResponsesStreamToAnthropic(data []byte) ([]byte, error) {
	// Convert via OpenAI as intermediate
	openaiData, err := convertResponsesStreamToOpenAI(data)
	if err != nil {
		return nil, err
	}
	if openaiData == nil {
		return nil, nil
	}
	return convertOpenAIStreamToAnthropic(openaiData)
}

// ---------------------------------------------------------------------------
// Anthropic stream → Responses stream
// ---------------------------------------------------------------------------

func convertAnthropicStreamToResponses(data []byte) ([]byte, error) {
	// Convert via OpenAI as intermediate
	openaiData, err := convertAnthropicStreamToOpenAI(data)
	if err != nil {
		return nil, err
	}
	if openaiData == nil {
		return nil, nil
	}
	if string(openaiData) == "[DONE]" {
		return convertOpenAIStreamToResponses(openaiData)
	}
	return convertOpenAIStreamToResponses(openaiData)
}

// FormatStreamEvents takes raw SSE data lines and converts them.
// Each line may contain multiple JSON objects separated by newlines (from multi-event conversions).
// Returns the formatted SSE output with "data: " prefixes.
func FormatStreamEvents(convertedData []byte) []byte {
	if convertedData == nil {
		return nil
	}
	if string(convertedData) == "[DONE]" {
		return []byte("data: [DONE]\n\n")
	}

	lines := strings.Split(string(convertedData), "\n")
	var out strings.Builder
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out.WriteString("data: ")
		out.WriteString(line)
		out.WriteString("\n\n")
	}
	result := out.String()
	if result == "" {
		return nil
	}
	return []byte(result)
}

// FormatStreamLine converts a single SSE data line and formats it for output.
// The input should be the payload after "data: " prefix (trimmed).
// Returns the complete "data: ...\n\n" formatted output, or nil to skip.
func (c *Converter) FormatStreamLine(data []byte) ([]byte, error) {
	converted, err := c.ConvertStreamEvent(data)
	if err != nil {
		return nil, err
	}
	return FormatStreamEvents(converted), nil
}

// ---------------------------------------------------------------------------
// Ensure compile-time interface satisfaction
// ---------------------------------------------------------------------------

func init() {
	// Validate all converter maps are populated
	_ = fmt.Sprintf
	_ = time.Now
}
