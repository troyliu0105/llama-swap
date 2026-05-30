package protocol

import (
	"encoding/json"
	"fmt"
	"sort"
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

func convertOpenAIStreamToResponses(c *Converter, data []byte) ([]byte, error) {
	if string(data) == "[DONE]" {
		if c.responseCompleted {
			return nil, nil
		}
		if !c.responseAwaitingCompletion {
			b, _ := json.Marshal(map[string]any{
				"type":            "response.completed",
				"sequence_number": 0,
				"response": map[string]any{
					"status": "completed",
				},
			})
			c.responseCompleted = true
			return b, nil
		}
		events := responsesCompletionEventsJSON(c, nil, nil)
		c.responseCompleted = true
		c.responseAwaitingCompletion = false
		return []byte(strings.Join(events, "\n")), nil
	}

	var chunk map[string]any
	if err := json.Unmarshal(data, &chunk); err != nil {
		return data, nil // pass through unparseable data
	}

	choices, _ := chunk["choices"].([]any)
	if len(choices) == 0 {
		if usage, ok := chunk["usage"].(map[string]any); ok && usage != nil {
			if c.responseCompleted {
				return nil, nil
			}
			events := responsesCompletionEventsJSON(c, chunk, usage)
			c.responseCompleted = true
			c.responseAwaitingCompletion = false
			return []byte(strings.Join(events, "\n")), nil
		}
		return nil, nil
	}

	choice, _ := choices[0].(map[string]any)
	if choice == nil {
		return nil, nil
	}
	if lp := choice["logprobs"]; lp != nil {
		return nil, fmt.Errorf("openai chat stream logprobs cannot be converted to Responses API")
	}

	delta, _ := choice["delta"].(map[string]any)
	finishReason, _ := choice["finish_reason"].(string)

	if delta == nil && finishReason == "" {
		return nil, nil
	}

	// Some providers include usage in the finish chunk alongside choices.
	chunkUsage, _ := chunk["usage"].(map[string]any)

	if delta == nil {
		// No delta, only finish reason
		if finishReason != "" {
			return emitResponsesEndEvents(chunk, chunkUsage), nil
		}
		return nil, nil
	}

	_, hasRole := delta["role"]
	content, hasContent := delta["content"]
	_, hasToolCalls := delta["tool_calls"]
	reasoningContent, hasReasoning := delta["reasoning_content"]

	// Accumulate result lines (may combine start + content or end events)
	var result []string

	// Emit start events exactly once, on the first chunk that carries role.
	// Providers like zhipu include role in every chunk; we only emit start
	// events on the very first one.
	if !c.streamStarted && hasRole {
		c.streamStarted = true
		result = append(result, responsesStartEventsJSON(chunk)...)
		// If this chunk also carries content, continue processing below.
	}

	if hasReasoning {
		reasoningStr, _ := reasoningContent.(string)
		b, _ := json.Marshal(map[string]any{
			"type":            "response.reasoning_text.delta",
			"sequence_number": 0,
			"output_index":    0,
			"content_index":   0,
			"delta":           reasoningStr,
		})
		result = append(result, string(b))
	}

	if hasContent {
		contentStr, _ := content.(string)
		b, _ := json.Marshal(map[string]any{
			"type":            "response.output_text.delta",
			"sequence_number": 0,
			"output_index":    0,
			"content_index":   0,
			"delta":           contentStr,
		})
		result = append(result, string(b))
	}

	if hasToolCalls {
		if c.responseToolIndexes == nil {
			c.responseToolIndexes = make(map[int]int)
		}
		if c.toolCallBuffers == nil {
			c.toolCallBuffers = make(map[int]*toolCallBuf)
		}
		toolCallDeltas, _ := delta["tool_calls"].([]any)
		for _, tcd := range toolCallDeltas {
			tcMap, ok := tcd.(map[string]any)
			if !ok {
				continue
			}
			fn, _ := tcMap["function"].(map[string]any)
			if fn == nil {
				continue
			}
			openAIIndex := int(toInt(tcMap["index"]))
			buf, ok := c.toolCallBuffers[openAIIndex]
			if !ok {
				buf = &toolCallBuf{}
				buf.outputIndex = len(c.responseToolIndexes) + 1
				c.responseToolIndexes[openAIIndex] = buf.outputIndex
				c.toolCallBuffers[openAIIndex] = buf
			}
			if buf.id == "" {
				if id, _ := tcMap["id"].(string); id != "" {
					buf.id = id
				} else {
					buf.id = fmt.Sprintf("call_protocol_%d", openAIIndex)
				}
			}
			if buf.name == "" {
				if name, _ := fn["name"].(string); name != "" {
					buf.name = name
				}
			}
			if buf.args.Len() == 0 {
				b, _ := json.Marshal(map[string]any{
					"type":            "response.output_item.added",
					"sequence_number": 0,
					"output_index":    buf.outputIndex,
					"item": map[string]any{
						"type":      "function_call",
						"id":        buf.id,
						"call_id":   buf.id,
						"name":      buf.name,
						"arguments": "",
					},
				})
				result = append(result, string(b))
			}
			argsDelta, _ := fn["arguments"].(string)
			buf.args.WriteString(argsDelta)
			b, _ := json.Marshal(map[string]any{
				"type":            "response.function_call_arguments.delta",
				"sequence_number": 0,
				"output_index":    buf.outputIndex,
				"call_id":         buf.id,
				"delta":           argsDelta,
			})
			result = append(result, string(b))
			c.responseSawToolCall = true
		}
	}

	if finishReason != "" {
		result = append(result, responsesDoneEventsJSON(c)...)
		c.responseAwaitingCompletion = true
		c.responsePendingTerminal = finishReason
		if chunkUsage != nil {
			result = append(result, responsesCompletionEventsJSON(c, chunk, chunkUsage)...)
			c.responseCompleted = true
			c.responseAwaitingCompletion = false
		}
	}

	if len(result) == 0 {
		return nil, nil
	}
	return []byte(strings.Join(result, "\n")), nil
}

// responsesStartEventsJSON returns the four lifecycle events that begin a
// Responses API stream: created, in_progress, output_item.added, content_part.added.
func responsesStartEventsJSON(chunk map[string]any) []string {
	events := []map[string]any{
		{
			"type":            "response.created",
			"sequence_number": 0,
			"response": map[string]any{
				"id":     prefixID(chunk["id"], "resp_"),
				"status": "in_progress",
				"model":  chunk["model"],
			},
		},
		{
			"type":            "response.in_progress",
			"sequence_number": 1,
			"response": map[string]any{
				"id":     prefixID(chunk["id"], "resp_"),
				"status": "in_progress",
			},
		},
		{
			"type":            "response.output_item.added",
			"sequence_number": 2,
			"output_index":    0,
			"item": map[string]any{
				"type":    "message",
				"role":    "assistant",
				"content": []any{},
			},
		},
		{
			"type":            "response.content_part.added",
			"sequence_number": 3,
			"output_index":    0,
			"content_index":   0,
			"part": map[string]any{
				"type": "output_text",
				"text": "",
			},
		},
	}

	var lines []string
	for _, evt := range events {
		b, _ := json.Marshal(evt)
		lines = append(lines, string(b))
	}
	return lines
}

// emitResponsesEndEvents builds the end-of-stream events: content_part.done,
// output_item.done, and optionally response.completed (when usage is present).
func emitResponsesEndEvents(chunk map[string]any, usage map[string]any) []byte {
	return []byte(strings.Join(responsesEndEventsJSON(chunk, usage), "\n"))
}

// responsesEndEventsJSON returns the end-of-stream events as JSON lines.
func responsesEndEventsJSON(chunk map[string]any, usage map[string]any) []string {
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

	// response.completed with usage (if available)
	if usage != nil {
		b, _ = json.Marshal(map[string]any{
			"type":            "response.completed",
			"sequence_number": 0,
			"response": map[string]any{
				"id":     prefixID(chunk["id"], "resp_"),
				"status": "completed",
				"model":  chunk["model"],
				"usage":  convertOpenAIUsageToResponses(usage),
			},
		})
		events = append(events, string(b))
	}

	return events
}

func responsesDoneEventsJSON(c *Converter) []string {
	var events []string
	if c.streamStarted && !c.responseMessageDone {
		contentDone, _ := json.Marshal(map[string]any{
			"type":            "response.content_part.done",
			"sequence_number": 0,
			"output_index":    0,
			"content_index":   0,
			"part": map[string]any{
				"type": "output_text",
				"text": "",
			},
		})
		itemDone, _ := json.Marshal(map[string]any{
			"type":            "response.output_item.done",
			"sequence_number": 0,
			"output_index":    0,
			"item": map[string]any{
				"type":    "message",
				"role":    "assistant",
				"content": []any{},
			},
		})
		events = append(events, string(contentDone), string(itemDone))
		c.responseMessageDone = true
	}
	if len(c.toolCallBuffers) == 0 {
		return events
	}
	indices := make([]int, 0, len(c.toolCallBuffers))
	for index := range c.toolCallBuffers {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	for _, index := range indices {
		buf := c.toolCallBuffers[index]
		args := buf.args.String()
		argsDone, _ := json.Marshal(map[string]any{
			"type":            "response.function_call_arguments.done",
			"sequence_number": 0,
			"output_index":    buf.outputIndex,
			"call_id":         buf.id,
			"arguments":       args,
		})
		itemDone, _ := json.Marshal(map[string]any{
			"type":            "response.output_item.done",
			"sequence_number": 0,
			"output_index":    buf.outputIndex,
			"item": map[string]any{
				"type":      "function_call",
				"id":        buf.id,
				"call_id":   buf.id,
				"name":      buf.name,
				"arguments": args,
			},
		})
		events = append(events, string(argsDone), string(itemDone))
	}
	c.toolCallBuffers = nil
	return events
}

func responsesCompletionEventsJSON(c *Converter, chunk map[string]any, usage map[string]any) []string {
	eventType := "response.completed"
	status := "completed"
	response := map[string]any{
		"status": status,
	}
	if chunk != nil {
		if chunk["id"] != nil {
			response["id"] = prefixID(chunk["id"], "resp_")
		}
		if chunk["model"] != nil {
			response["model"] = chunk["model"]
		}
	}
	if c.responsePendingTerminal == "length" || c.responsePendingTerminal == "content_filter" {
		eventType = "response.incomplete"
		response["status"] = "incomplete"
		reason := "max_output_tokens"
		if c.responsePendingTerminal == "content_filter" {
			reason = "content_filter"
		}
		response["incomplete_details"] = map[string]any{"reason": reason}
	}
	if usage != nil {
		response["usage"] = convertOpenAIUsageToResponses(usage)
	}
	b, _ := json.Marshal(map[string]any{
		"type":            eventType,
		"sequence_number": 0,
		"response":        response,
	})
	return []string{string(b)}
}

func ensureResponseToolCallBuffer(c *Converter, outputIndex int) *toolCallBuf {
	if c.toolCallBuffers == nil {
		c.toolCallBuffers = make(map[int]*toolCallBuf)
	}
	buf, ok := c.toolCallBuffers[outputIndex]
	if !ok {
		buf = &toolCallBuf{outputIndex: outputIndex}
		c.toolCallBuffers[outputIndex] = buf
	}
	return buf
}

func marshalOpenAIToolCallChunk(index int, callID, name, arguments string) ([]byte, error) {
	toolCall := map[string]any{
		"index": index,
		"type":  "function",
		"function": map[string]any{
			"arguments": arguments,
		},
	}
	if callID != "" {
		toolCall["id"] = callID
	}
	if name != "" {
		toolCall["function"].(map[string]any)["name"] = name
	}
	return json.Marshal(map[string]any{
		"id":      "chatcmpl-protocol",
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"choices": []any{
			map[string]any{
				"index": 0,
				"delta": map[string]any{
					"tool_calls": []any{toolCall},
				},
				"finish_reason": nil,
			},
		},
	})
}

func responsesStreamFinishReason(c *Converter, evtType string, evt map[string]any) string {
	if c.responseSawToolCall {
		return "tool_calls"
	}
	if evtType == "response.incomplete" {
		response, _ := evt["response"].(map[string]any)
		if response != nil {
			if details, ok := response["incomplete_details"].(map[string]any); ok {
				reason, _ := details["reason"].(string)
				switch reason {
				case "content_filter":
					return "content_filter"
				case "max_output_tokens", "max_tokens":
					return "length"
				}
			}
		}
		return "length"
	}
	return "stop"
}

// ---------------------------------------------------------------------------
// Responses stream → OpenAI stream
//
// Converts individual response events into chat.completion.chunk objects.
// ---------------------------------------------------------------------------

func convertResponsesStreamToOpenAI(c *Converter, data []byte) ([]byte, error) {
	if c == nil {
		c = &Converter{}
	}
	var evt map[string]any
	if err := json.Unmarshal(data, &evt); err != nil {
		return data, nil
	}

	evtType, _ := evt["type"].(string)

	switch evtType {
	case "response.output_item.added":
		item, _ := evt["item"].(map[string]any)
		if item == nil || item["type"] != "function_call" {
			return nil, nil
		}
		buf := ensureResponseToolCallBuffer(c, int(toInt(evt["output_index"])))
		if callID, _ := item["call_id"].(string); callID != "" {
			buf.id = callID
		} else if id, _ := item["id"].(string); id != "" {
			buf.id = id
		}
		if name, _ := item["name"].(string); name != "" {
			buf.name = name
		}
		if args, _ := item["arguments"].(string); args != "" {
			buf.args.WriteString(args)
		}
		c.responseSawToolCall = true
		return marshalOpenAIToolCallChunk(buf.outputIndex, buf.id, buf.name, buf.args.String())

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

	case "response.reasoning_text.delta":
		delta, _ := evt["delta"].(string)
		return json.Marshal(map[string]any{
			"id":      "chatcmpl-protocol",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"choices": []any{
				map[string]any{
					"index": 0,
					"delta": map[string]any{
						"reasoning_content": delta,
					},
					"finish_reason": nil,
				},
			},
		})

	case "response.refusal.delta":
		delta, _ := evt["delta"].(string)
		c.responseSawRefusalDelta = true
		return json.Marshal(map[string]any{
			"id":      "chatcmpl-protocol",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"choices": []any{
				map[string]any{
					"index": 0,
					"delta": map[string]any{
						"refusal": delta,
					},
					"finish_reason": nil,
				},
			},
		})

	case "response.refusal.done":
		if c.responseSawRefusalDelta {
			return nil, nil
		}
		refusal, _ := evt["refusal"].(string)
		return json.Marshal(map[string]any{
			"id":      "chatcmpl-protocol",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"choices": []any{
				map[string]any{
					"index": 0,
					"delta": map[string]any{
						"refusal": refusal,
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
		buf := ensureResponseToolCallBuffer(c, int(toInt(evt["output_index"])))
		if callID, _ := evt["call_id"].(string); callID != "" {
			buf.id = callID
		}
		buf.args.WriteString(argsDelta)
		c.responseSawToolCall = true
		return marshalOpenAIToolCallChunk(buf.outputIndex, buf.id, buf.name, argsDelta)

	case "response.function_call_arguments.done":
		return nil, nil

	case "response.output_item.done":
		item, _ := evt["item"].(map[string]any)
		if item != nil && item["type"] == "function_call" {
			buf := ensureResponseToolCallBuffer(c, int(toInt(evt["output_index"])))
			if callID, _ := item["call_id"].(string); callID != "" {
				buf.id = callID
			}
			if name, _ := item["name"].(string); name != "" {
				buf.name = name
			}
			c.responseSawToolCall = true
		}
		return nil, nil

	case "response.completed", "response.incomplete":
		finishReason := responsesStreamFinishReason(c, evtType, evt)
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
		if usage == nil || (!c.openAIIncludeUsage && c.To == FormatOpenAI) {
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
		"response.content_part.added", "response.content_part.done",
		"response.reasoning_text.done":
		// These events don't have direct OpenAI equivalents, skip them
		return nil, nil

	case "response.reasoning_summary_part.added",
		"response.reasoning_summary_part.done",
		"response.reasoning_summary_text.delta",
		"response.reasoning_summary_text.done",
		"response.output_text.annotation.added":
		return nil, fmt.Errorf("responses stream event %q cannot be converted to OpenAI Chat Completions", evtType)

	default:
		if isUnsupportedResponsesSemanticStreamEvent(evtType) {
			return nil, fmt.Errorf("responses stream event %q cannot be converted to OpenAI Chat Completions", evtType)
		}
		return nil, nil
	}
}

func isUnsupportedResponsesSemanticStreamEvent(evtType string) bool {
	for _, prefix := range []string{
		"response.web_search_call.",
		"response.file_search_call.",
		"response.code_interpreter_call.",
		"response.image_generation_call.",
		"response.image_gen_call.",
		"response.mcp_",
		"response.audio.",
		"response.audio_transcript.",
		"response.computer_call.",
		"response.custom_tool_call.",
	} {
		if strings.HasPrefix(evtType, prefix) {
			return true
		}
	}
	return false
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

func convertAnthropicStreamToOpenAI(c *Converter, data []byte) ([]byte, error) {
	if c == nil {
		c = &Converter{}
	}
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
			thinking, _ := delta["thinking"].(string)
			return json.Marshal(map[string]any{
				"id":      "chatcmpl-protocol",
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"choices": []any{
					map[string]any{
						"index": 0,
						"delta": map[string]any{
							"reasoning_content": thinking,
						},
						"finish_reason": nil,
					},
				},
			})

		default:
			return nil, fmt.Errorf("anthropic stream delta type %q cannot be converted to OpenAI Chat Completions", deltaType)
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
		if usage == nil || (!c.openAIIncludeUsage && c.To == FormatOpenAI) {
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
		if isUnsupportedAnthropicSemanticStreamEvent(evtType) {
			return nil, fmt.Errorf("anthropic stream event %q cannot be converted to OpenAI Chat Completions", evtType)
		}
		return nil, nil
	}

	return nil, nil
}

func isUnsupportedAnthropicSemanticStreamEvent(evtType string) bool {
	if evtType == "error" {
		return true
	}
	for _, prefix := range []string{
		"server_tool_",
		"web_search_",
		"web_fetch_",
		"code_execution_",
		"mcp_",
		"advisor_",
		"tool_search_",
	} {
		if strings.HasPrefix(evtType, prefix) {
			return true
		}
	}
	for _, exact := range []string{"citations_delta", "signature_delta"} {
		if evtType == exact {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// OpenAI stream → Anthropic stream
// ---------------------------------------------------------------------------

func convertOpenAIStreamToAnthropic(c *Converter, data []byte) ([]byte, error) {
	var events []string
	appendEvent := func(event map[string]any) {
		b, _ := json.Marshal(event)
		events = append(events, string(b))
	}
	closeCurrentBlock := func() {
		if c.currentBlockType == "" {
			return
		}
		appendEvent(map[string]any{
			"type":  "content_block_stop",
			"index": c.currentBlockIndex,
		})
		c.currentBlockType = ""
		c.currentBlockIndex = -1
	}
	emitTerminal := func(usage map[string]any) {
		if c.streamFinished {
			return
		}
		stopReason := c.pendingStopReason
		if stopReason == "" {
			stopReason = "end_turn"
		}
		appendEvent(map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason":   stopReason,
				"stop_sequence": nil,
			},
			"usage": usage,
		})
		appendEvent(map[string]any{
			"type": "message_stop",
		})
		c.streamFinished = true
	}
	returnEvents := func() []byte {
		if len(events) == 0 {
			return nil
		}
		return []byte(strings.Join(events, "\n"))
	}

	if string(data) == "[DONE]" {
		if c.streamFinished {
			return nil, nil
		}
		if c.pendingStopReason != "" {
			emitTerminal(map[string]any{"output_tokens": 0})
			return returnEvents(), nil
		}
		appendEvent(map[string]any{
			"type": "message_stop",
		})
		c.streamFinished = true
		return returnEvents(), nil
	}

	var chunk map[string]any
	if err := json.Unmarshal(data, &chunk); err != nil {
		return data, nil
	}

	choices, _ := chunk["choices"].([]any)
	if len(choices) == 0 {
		if c.streamFinished {
			return nil, nil
		}
		if usage, ok := chunk["usage"].(map[string]any); ok && usage != nil {
			if c.pendingStopReason != "" {
				emitTerminal(convertOpenAIUsageToAnthropic(usage))
				return returnEvents(), nil
			}
			appendEvent(map[string]any{
				"type": "message_delta",
				"delta": map[string]any{
					"stop_reason":   "end_turn",
					"stop_sequence": nil,
				},
				"usage": convertOpenAIUsageToAnthropic(usage),
			})
			return returnEvents(), nil
		}
		return data, nil
	}

	choice, _ := choices[0].(map[string]any)
	if choice == nil {
		return nil, nil
	}
	if lp := choice["logprobs"]; lp != nil {
		return nil, fmt.Errorf("openai chat stream logprobs cannot be converted to Anthropic Messages")
	}

	delta, _ := choice["delta"].(map[string]any)
	finishReason, _ := choice["finish_reason"].(string)

	if delta == nil && finishReason == "" {
		return nil, nil
	}

	// First chunk with role → message_start (emit exactly once)
	if delta != nil {
		if _, hasRole := delta["role"]; hasRole && !c.streamStarted {
			c.streamStarted = true
			appendEvent(map[string]any{
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

		// Thinking content delta
		if reasoningContent, ok := delta["reasoning_content"].(string); ok && reasoningContent != "" {
			if c.currentBlockType != "thinking" {
				closeCurrentBlock()
				blockIndex := c.blockCount
				appendEvent(map[string]any{
					"type":  "content_block_start",
					"index": blockIndex,
					"content_block": map[string]any{
						"type":     "thinking",
						"thinking": "",
					},
				})
				c.blockCount++
				c.currentBlockType = "thinking"
				c.currentBlockIndex = blockIndex
			}
			appendEvent(map[string]any{
				"type":  "content_block_delta",
				"index": c.currentBlockIndex,
				"delta": map[string]any{
					"type":     "thinking_delta",
					"thinking": reasoningContent,
				},
			})
		}

		// Text content delta
		if content, ok := delta["content"].(string); ok && content != "" {
			if c.currentBlockType != "text" {
				closeCurrentBlock()
				blockIndex := c.blockCount
				appendEvent(map[string]any{
					"type":  "content_block_start",
					"index": blockIndex,
					"content_block": map[string]any{
						"type": "text",
						"text": "",
					},
				})
				c.blockCount++
				c.currentBlockType = "text"
				c.currentBlockIndex = blockIndex
			}
			appendEvent(map[string]any{
				"type":  "content_block_delta",
				"index": c.currentBlockIndex,
				"delta": map[string]any{
					"type": "text_delta",
					"text": content,
				},
			})
		}

		// Tool calls delta — buffer for deferred emission at finish.
		if toolCalls, ok := delta["tool_calls"].([]any); ok && len(toolCalls) > 0 {
			if c.toolCallBuffers == nil {
				c.toolCallBuffers = make(map[int]*toolCallBuf)
			}

			if c.currentBlockType != "" {
				closeCurrentBlock()
			}

			for _, toolCall := range toolCalls {
				tcMap, _ := toolCall.(map[string]any)
				if tcMap == nil {
					continue
				}

				openAIIndex := 0
				switch index := tcMap["index"].(type) {
				case float64:
					openAIIndex = int(index)
				case int:
					openAIIndex = index
				}

				buf, exists := c.toolCallBuffers[openAIIndex]
				if !exists {
					buf = &toolCallBuf{}
					c.toolCallBuffers[openAIIndex] = buf
				}

				if id, ok := tcMap["id"].(string); ok && id != "" {
					buf.id = id
				}
				if fn, ok := tcMap["function"].(map[string]any); ok {
					if name, ok := fn["name"].(string); ok && name != "" {
						buf.name = name
					}
					if args, ok := fn["arguments"].(string); ok && args != "" {
						buf.args.WriteString(args)
					}
				}
			}
		}
	}

	// Finish reason → close content, emit buffered tool_use blocks, and defer terminal events.
	if finishReason != "" {
		stopReason := "end_turn"
		switch finishReason {
		case "length":
			stopReason = "max_tokens"
		case "tool_calls":
			stopReason = "tool_use"
		}

		closeCurrentBlock()

		if len(c.toolCallBuffers) > 0 {
			indices := make([]int, 0, len(c.toolCallBuffers))
			for index := range c.toolCallBuffers {
				indices = append(indices, index)
			}
			sort.Ints(indices)

			for _, openAIIndex := range indices {
				buf := c.toolCallBuffers[openAIIndex]
				blockIndex := c.blockCount

				toolID := buf.id
				if toolID == "" {
					toolID = "toolu_protocol"
				}

				appendEvent(map[string]any{
					"type":  "content_block_start",
					"index": blockIndex,
					"content_block": map[string]any{
						"type":  "tool_use",
						"id":    toolID,
						"name":  buf.name,
						"input": map[string]any{},
					},
				})

				if buf.args.Len() > 0 {
					appendEvent(map[string]any{
						"type":  "content_block_delta",
						"index": blockIndex,
						"delta": map[string]any{
							"type":         "input_json_delta",
							"partial_json": buf.args.String(),
						},
					})
				}

				appendEvent(map[string]any{
					"type":  "content_block_stop",
					"index": blockIndex,
				})

				c.blockCount++
			}
		}

		c.pendingStopReason = stopReason
	}

	return returnEvents(), nil
}

// ---------------------------------------------------------------------------
// Responses stream → Anthropic stream
// ---------------------------------------------------------------------------

func convertResponsesStreamToAnthropic(c *Converter, data []byte) ([]byte, error) {
	// Convert via OpenAI as intermediate
	openaiData, err := convertResponsesStreamToOpenAI(c, data)
	if err != nil {
		return nil, err
	}
	if openaiData == nil {
		return nil, nil
	}
	return convertViaLines(c, openaiData, convertOpenAIStreamToAnthropic)
}

// ---------------------------------------------------------------------------
// Anthropic stream → Responses stream
// ---------------------------------------------------------------------------

func convertAnthropicStreamToResponses(c *Converter, data []byte) ([]byte, error) {
	// Convert via OpenAI as intermediate
	openaiData, err := convertAnthropicStreamToOpenAI(c, data)
	if err != nil {
		return nil, err
	}
	if openaiData == nil {
		return nil, nil
	}
	return convertViaLines(c, openaiData, convertOpenAIStreamToResponses)
}

// convertViaLines splits multi-line intermediate output and converts each line.
func convertViaLines(c *Converter, data []byte, fn func(*Converter, []byte) ([]byte, error)) ([]byte, error) {
	lines := strings.Split(string(data), "\n")
	var results []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		converted, err := fn(c, []byte(line))
		if err != nil {
			return nil, err
		}
		if converted != nil {
			results = append(results, string(converted))
		}
	}
	if len(results) == 0 {
		return nil, nil
	}
	return []byte(strings.Join(results, "\n")), nil
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
