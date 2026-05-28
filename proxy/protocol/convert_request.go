package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// responses → openai (chat completions)
// ---------------------------------------------------------------------------

func responsesToOpenAIRequest(body []byte) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	out := make(map[string]any)

	// model
	if v, ok := req["model"]; ok {
		out["model"] = v
	}

	// Convert input → messages
	var messages []any
	if v, ok := req["instructions"]; ok && v != nil {
		if s, ok := v.(string); ok && s != "" {
			messages = append(messages, map[string]any{
				"role":    "system",
				"content": s,
			})
		}
	}

	if input, ok := req["input"]; ok {
		switch v := input.(type) {
		case string:
			messages = append(messages, map[string]any{
				"role":    "user",
				"content": v,
			})
		case []any:
			for _, item := range v {
				msg, ok := item.(map[string]any)
				if !ok {
					continue
				}
				itemType, _ := msg["type"].(string)
				switch itemType {
				case "function_call":
					messages = append(messages, map[string]any{
						"role":    "assistant",
						"content": nil,
						"tool_calls": []any{map[string]any{
							"id":   msg["call_id"],
							"type": "function",
							"function": map[string]any{
								"name":      msg["name"],
								"arguments": msg["arguments"],
							},
						}},
					})
				case "function_call_output":
					messages = append(messages, map[string]any{
						"role":         "tool",
						"tool_call_id": msg["call_id"],
						"content":      msg["output"],
					})
				default:
					role, _ := msg["role"].(string)
					content := msg["content"]
					converted, convErr := convertResponsesContentToOpenAI(content)
					if convErr != nil {
						return nil, convErr
					}
					outMsg := map[string]any{
						"role":    role,
						"content": converted,
					}
					messages = append(messages, outMsg)
				}
			}
		}
	}
	out["messages"] = messages

	// Simple field mappings
	if v, ok := req["temperature"]; ok {
		out["temperature"] = v
	}
	if v, ok := req["top_p"]; ok {
		out["top_p"] = v
	}
	if v, ok := req["max_output_tokens"]; ok {
		out["max_completion_tokens"] = v
	}
	if v, ok := req["stream"]; ok {
		out["stream"] = v
	}
	if v, ok := req["stop"]; ok {
		out["stop"] = v
	}
	if v, ok := req["presence_penalty"]; ok {
		out["presence_penalty"] = v
	}
	if v, ok := req["frequency_penalty"]; ok {
		out["frequency_penalty"] = v
	}
	if v, ok := req["seed"]; ok {
		out["seed"] = v
	}
	copyFields(out, req, "stream_options", "parallel_tool_calls", "service_tier", "metadata", "top_logprobs")
	if text, ok := req["text"].(map[string]any); ok {
		if format, ok := text["format"]; ok {
			out["response_format"] = format
		}
	}

	// Convert tools (function type only)
	if tools, ok := req["tools"].([]any); ok {
		var openaiTools []any
		for i, t := range tools {
			tool, ok := t.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid responses tool at index %d", i)
			}
			toolType, _ := tool["type"].(string)
			if toolType == "function" {
				openaiTools = append(openaiTools, map[string]any{
					"type": "function",
					"function": map[string]any{
						"name":        tool["name"],
						"description": tool["description"],
						"parameters":  tool["parameters"],
						"strict":      tool["strict"],
					},
				})
			} else {
				return nil, fmt.Errorf("unsupported tool type %q in responses→openai conversion", toolType)
			}
		}
		if len(openaiTools) > 0 {
			out["tools"] = openaiTools
		}
	}

	if v, ok := req["tool_choice"]; ok {
		out["tool_choice"] = convertResponsesToolChoiceToOpenAI(v)
	}

	return json.Marshal(out)
}

// convertResponsesContentToOpenAI converts Responses API content to OpenAI content.
func convertResponsesContentToOpenAI(content any) (any, error) {
	switch v := content.(type) {
	case string:
		return v, nil
	case []any:
		var parts []any
		for i, part := range v {
			p, ok := part.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid responses content part at index %d", i)
			}
			typ, _ := p["type"].(string)
			switch typ {
			case "input_text":
				part := map[string]any{"type": "text", "text": p["text"]}
				copyFields(part, p, "cache_control", "annotations", "citations")
				parts = append(parts, part)
			case "input_image":
				imgURL, _ := p["image_url"].(string)
				imgData, _ := p["image_data"].(string)
				if imgURL != "" {
					imageURL := map[string]any{"url": imgURL}
					copyFields(imageURL, p, "detail")
					parts = append(parts, map[string]any{"type": "image_url", "image_url": imageURL})
				} else if imgData != "" {
					mediaType, _ := p["media_type"].(string)
					if mediaType == "" {
						mediaType = "image/png"
					}
					imageURL := map[string]any{"url": "data:" + mediaType + ";base64," + imgData}
					copyFields(imageURL, p, "detail")
					parts = append(parts, map[string]any{"type": "image_url", "image_url": imageURL})
				}
			case "input_file":
				file := map[string]any{}
				copyFields(file, p, "file_url", "file_data", "file_id", "filename")
				if len(file) > 0 {
					parts = append(parts, map[string]any{"type": "file", "file": file})
				}
			default:
				return nil, fmt.Errorf("unsupported responses content type %q", typ)
			}
		}
		if len(parts) == 1 {
			if t, ok := parts[0].(map[string]any); ok {
				if t["type"] == "text" && len(t) == 2 {
					return t["text"], nil
				}
			}
		}
		return parts, nil
	default:
		return content, nil
	}
}

func convertResponsesToolChoiceToOpenAI(choice any) any {
	switch v := choice.(type) {
	case map[string]any:
		choiceType, _ := v["type"].(string)
		if choiceType == "function" {
			if _, ok := v["function"]; ok {
				return v
			}
			return map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": v["name"],
				},
			}
		}
	}
	return choice
}

func convertOpenAIToolChoiceToResponses(choice any) any {
	switch v := choice.(type) {
	case map[string]any:
		choiceType, _ := v["type"].(string)
		if choiceType == "function" {
			if fn, ok := v["function"].(map[string]any); ok {
				return map[string]any{
					"type": "function",
					"name": fn["name"],
				}
			}
			return v
		}
	}
	return choice
}

// ---------------------------------------------------------------------------
// openai (chat completions) → responses
// ---------------------------------------------------------------------------

func openAIToResponsesRequest(body []byte) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	out := make(map[string]any)

	// model
	if v, ok := req["model"]; ok {
		out["model"] = v
	}

	// Convert messages → input
	var instructions string
	var input []any

	if messages, ok := req["messages"].([]any); ok {
		for _, m := range messages {
			msg, ok := m.(map[string]any)
			if !ok {
				continue
			}
			role, _ := msg["role"].(string)

			// Extract system/developer as instructions
			if (role == "system" || role == "developer") && instructions == "" {
				if s, ok := msg["content"].(string); ok {
					instructions = s
				}
				continue
			}

			if role == "tool" {
				input = append(input, map[string]any{
					"type":    "function_call_output",
					"call_id": msg["tool_call_id"],
					"output":  openAIContentText(msg["content"]),
				})
				continue
			}

			outMsg := map[string]any{
				"role": role,
			}
			converted, convErr := convertOpenAIContentToResponses(msg["content"])
			if convErr != nil {
				return nil, convErr
			}
			outMsg["content"] = converted
			input = append(input, outMsg)

			// Handle assistant tool_calls after the assistant message so the
			// Responses input preserves chat message order.
			if role == "assistant" {
				if toolCalls, ok := msg["tool_calls"].([]any); ok && len(toolCalls) > 0 {
					for i, tc := range toolCalls {
						tcMap, ok := tc.(map[string]any)
						if !ok {
							return nil, fmt.Errorf("invalid openai tool_call at index %d", i)
						}
						fn, ok := tcMap["function"].(map[string]any)
						if !ok {
							return nil, fmt.Errorf("openai tool_call at index %d has non-object function field", i)
						}
						input = append(input, map[string]any{
							"type":      "function_call",
							"call_id":   tcMap["id"],
							"name":      fn["name"],
							"arguments": fn["arguments"],
						})
					}
				}
			}
		}
	}

	if instructions != "" {
		out["instructions"] = instructions
	}
	out["input"] = input

	// Simple field mappings
	if v, ok := req["temperature"]; ok {
		out["temperature"] = v
	}
	if v, ok := req["top_p"]; ok {
		out["top_p"] = v
	}
	if v, ok := req["max_completion_tokens"]; ok {
		out["max_output_tokens"] = v
	} else if v, ok := req["max_tokens"]; ok {
		out["max_output_tokens"] = v
	}
	if v, ok := req["stream"]; ok {
		out["stream"] = v
	}
	if v, ok := req["stop"]; ok {
		out["stop"] = v
	}
	copyFields(out, req, "stream_options", "parallel_tool_calls", "service_tier", "metadata", "top_logprobs", "prompt_cache_key")
	if responseFormat, ok := req["response_format"]; ok {
		out["text"] = map[string]any{"format": responseFormat}
	}
	if user, ok := req["user"]; ok {
		out["safety_identifier"] = user
	}

	// Convert tools
	if tools, ok := req["tools"].([]any); ok {
		var respTools []any
		for i, t := range tools {
			tool, ok := t.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid openai tool at index %d", i)
			}
			toolType, _ := tool["type"].(string)
			if toolType == "function" {
				fn, ok := tool["function"].(map[string]any)
				if !ok {
					return nil, fmt.Errorf("tool %q has non-object function field in openai→responses conversion", toolType)
				}
				respTools = append(respTools, map[string]any{
					"type":        "function",
					"name":        fn["name"],
					"description": fn["description"],
					"parameters":  fn["parameters"],
					"strict":      fn["strict"],
				})
			} else {
				return nil, fmt.Errorf("unsupported tool type %q in openai→responses conversion", toolType)
			}
		}
		if len(respTools) > 0 {
			out["tools"] = respTools
		}
	}

	if v, ok := req["tool_choice"]; ok {
		out["tool_choice"] = convertOpenAIToolChoiceToResponses(v)
	}

	return json.Marshal(out)
}

// convertOpenAIContentToResponses converts OpenAI message content to Responses API format.
func convertOpenAIContentToResponses(content any) (any, error) {
	switch v := content.(type) {
	case string:
		return v, nil
	case []any:
		var parts []any
		for i, part := range v {
			p, ok := part.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid openai content part at index %d", i)
			}
			typ, _ := p["type"].(string)
			switch typ {
			case "text":
				part := map[string]any{"type": "input_text", "text": p["text"]}
				copyFields(part, p, "cache_control", "annotations", "citations")
				parts = append(parts, part)
			case "image_url":
				imgMap, _ := p["image_url"].(map[string]any)
				if imgMap != nil {
					url, _ := imgMap["url"].(string)
					if strings.HasPrefix(url, "data:image/") {
						if idx := strings.Index(url, ","); idx >= 0 {
							part := map[string]any{"type": "input_image", "image_data": url[idx+1:]}
							mediaInfo := strings.TrimPrefix(url[:idx], "data:")
							part["media_type"] = strings.TrimSuffix(mediaInfo, ";base64")
							copyFields(part, imgMap, "detail")
							parts = append(parts, part)
						}
					} else {
						part := map[string]any{"type": "input_image", "image_url": url}
						copyFields(part, imgMap, "detail")
						parts = append(parts, part)
					}
				}
			case "file":
				if file, ok := p["file"].(map[string]any); ok {
					part := map[string]any{"type": "input_file"}
					copyFields(part, file, "file_url", "file_data", "file_id", "filename")
					parts = append(parts, part)
				}
			default:
				return nil, fmt.Errorf("unsupported openai content type %q", typ)
			}
		}
		if len(parts) == 1 {
			if t, ok := parts[0].(map[string]any); ok {
				if t["type"] == "input_text" && len(t) == 2 {
					return t["text"], nil
				}
			}
		}
		return parts, nil
	default:
		return content, nil
	}
}

// ---------------------------------------------------------------------------
// anthropic → openai (chat completions)
// ---------------------------------------------------------------------------

func anthropicToOpenAIRequest(body []byte) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	out := make(map[string]any)

	// model
	if v, ok := req["model"]; ok {
		out["model"] = v
	}

	// Convert system → system message, then messages
	var messages []any

	// system prompt
	if sys, ok := req["system"]; ok {
		sysContent, convErr := anthropicContentToOpenAI(sys)
		if convErr != nil {
			return nil, convErr
		}
		messages = append(messages, map[string]any{
			"role":    "system",
			"content": sysContent,
		})
	}

	// messages - anthropic only has user/assistant
	if msgs, ok := req["messages"].([]any); ok {
		for _, m := range msgs {
			msg, ok := m.(map[string]any)
			if !ok {
				continue
			}
			role, _ := msg["role"].(string)
			content := msg["content"]

			outMsg := map[string]any{
				"role": role,
			}

			// Handle assistant messages with tool_use
			if role == "assistant" {
				if contentArr, ok := content.([]any); ok {
					var textParts []any
					var toolCalls []any
					for _, block := range contentArr {
						blockMap, ok := block.(map[string]any)
						if !ok {
							continue
						}
						blockType, _ := blockMap["type"].(string)
						switch blockType {
						case "text":
							textParts = append(textParts, blockMap["text"])
						case "tool_use":
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
					if len(toolCalls) > 0 {
						outMsg["tool_calls"] = toolCalls
					}
					if len(textParts) > 0 {
						if len(textParts) == 1 {
							outMsg["content"] = textParts[0]
						} else {
							var sb strings.Builder
							for _, p := range textParts {
								sb.WriteString(fmt.Sprint(p))
							}
							outMsg["content"] = sb.String()
						}
					} else if len(toolCalls) > 0 {
						outMsg["content"] = nil
					}
				} else {
					converted, convErr := anthropicContentToOpenAI(content)
					if convErr != nil {
						return nil, convErr
					}
					outMsg["content"] = converted
				}
			} else if role == "user" {
				if contentArr, ok := content.([]any); ok {
					var userBlocks []any
					var flushErr error
					flushUserBlocks := func() {
						if len(userBlocks) == 0 || flushErr != nil {
							return
						}
						converted, convErr := anthropicContentToOpenAI(userBlocks)
						if convErr != nil {
							flushErr = convErr
							return
						}
						messages = append(messages, map[string]any{
							"role":    "user",
							"content": converted,
						})
						userBlocks = nil
					}
					for _, block := range contentArr {
						if flushErr != nil {
							return nil, flushErr
						}
						blockMap, ok := block.(map[string]any)
						if !ok {
							continue
						}
						if blockType, _ := blockMap["type"].(string); blockType == "tool_result" {
							flushUserBlocks()
							if flushErr != nil {
								return nil, flushErr
							}
							messages = append(messages, map[string]any{
								"role":         "tool",
								"content":      anthropicToolResultText(blockMap["content"]),
								"tool_call_id": blockMap["tool_use_id"],
							})
							continue
						}
						userBlocks = append(userBlocks, block)
					}
					flushUserBlocks()
					if flushErr != nil {
						return nil, flushErr
					}
					continue
				}
				converted, convErr := anthropicContentToOpenAI(content)
				if convErr != nil {
					return nil, convErr
				}
				outMsg["content"] = converted
			} else {
				converted, convErr := anthropicContentToOpenAI(content)
				if convErr != nil {
					return nil, convErr
				}
				outMsg["content"] = converted
			}

			messages = append(messages, outMsg)
		}
	}
	out["messages"] = messages

	// Simple field mappings
	if v, ok := req["max_tokens"]; ok {
		out["max_completion_tokens"] = v
	}
	if v, ok := req["temperature"]; ok {
		out["temperature"] = v
	}
	if v, ok := req["top_p"]; ok {
		out["top_p"] = v
	}
	if v, ok := req["stream"]; ok {
		out["stream"] = v
	}
	if v, ok := req["stop_sequences"]; ok {
		out["stop"] = v
	}
	copyFields(out, req, "metadata", "service_tier", "stream_options", "top_logprobs")
	if metadata, ok := req["metadata"].(map[string]any); ok {
		if userID, ok := metadata["user_id"]; ok {
			out["user"] = userID
		}
	}

	// Convert tools
	if tools, ok := req["tools"].([]any); ok {
		var openaiTools []any
		for i, t := range tools {
			tool, ok := t.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid anthropic tool at index %d", i)
			}
			toolType, _ := tool["type"].(string)
			if toolType == "custom" {
				openaiTools = append(openaiTools, map[string]any{
					"type": "function",
					"function": map[string]any{
						"name":        tool["name"],
						"description": tool["description"],
						"parameters":  tool["input_schema"],
						"strict":      tool["strict"],
					},
				})
			} else {
				return nil, fmt.Errorf("unsupported tool type %q in anthropic→openai conversion", toolType)
			}
		}
		if len(openaiTools) > 0 {
			out["tools"] = openaiTools
		}
	}
	if tc, ok := req["tool_choice"].(map[string]any); ok {
		tcType, _ := tc["type"].(string)
		switch tcType {
		case "auto":
			out["tool_choice"] = "auto"
		case "any":
			out["tool_choice"] = "required"
		case "tool":
			name, _ := tc["name"].(string)
			out["tool_choice"] = map[string]any{
				"type":     "function",
				"function": map[string]any{"name": name},
			}
		case "none":
			out["tool_choice"] = "none"
		}
	}

	return json.Marshal(out)
}

// anthropicContentToOpenAI converts Anthropic content blocks to OpenAI format.
func anthropicContentToOpenAI(content any) (any, error) {
	switch v := content.(type) {
	case string:
		return v, nil
	case []any:
		var parts []any
		for i, block := range v {
			b, ok := block.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid anthropic content block at index %d", i)
			}
			typ, _ := b["type"].(string)
			switch typ {
			case "text":
				text, _ := b["text"].(string)
				part := map[string]any{"type": "text", "text": text}
				copyFields(part, b, "cache_control", "citations")
				parts = append(parts, part)
			case "image":
				source, _ := b["source"].(map[string]any)
				if source != nil {
					srcType, _ := source["type"].(string)
					if srcType == "url" {
						parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": source["url"]}})
					} else if srcType == "base64" {
						mediaType, _ := source["media_type"].(string)
						data, _ := source["data"].(string)
						parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:" + mediaType + ";base64," + data}})
					}
				}
			case "tool_result":
				// Handled by the caller; should not appear here but skip gracefully.
			default:
				return nil, fmt.Errorf("unsupported anthropic content type %q", typ)
			}
		}
		if len(parts) == 1 {
			if t, ok := parts[0].(map[string]any); ok {
				if t["type"] == "text" && len(t) == 2 {
					return t["text"], nil
				}
			}
		}
		return parts, nil
	default:
		return content, nil
	}
}

// ---------------------------------------------------------------------------
// openai (chat completions) → anthropic
// ---------------------------------------------------------------------------

func openAIToAnthropicRequest(body []byte) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	out := make(map[string]any)

	// model
	if v, ok := req["model"]; ok {
		out["model"] = v
	}

	// max_tokens (required in Anthropic)
	if v, ok := req["max_completion_tokens"]; ok {
		out["max_tokens"] = v
	} else if v, ok := req["max_tokens"]; ok {
		out["max_tokens"] = v
	} else {
		out["max_tokens"] = 4096
	}

	// Extract system messages
	var systemContent any
	var messages []any

	if msgs, ok := req["messages"].([]any); ok {
		for _, m := range msgs {
			msg, ok := m.(map[string]any)
			if !ok {
				continue
			}
			role, _ := msg["role"].(string)

			if role == "system" || role == "developer" {
				if systemContent == nil {
					converted, convErr := openAIContentToAnthropic(msg["content"])
					if convErr != nil {
						return nil, convErr
					}
					systemContent = converted
				}
				continue
			}

			if role == "tool" {
				// Convert tool role to user message with tool_result
				toolCallID, _ := msg["tool_call_id"].(string)
				content := msg["content"]
				messages = append(messages, map[string]any{
					"role": "user",
					"content": []any{
						map[string]any{
							"type":        "tool_result",
							"tool_use_id": toolCallID,
							"content":     content,
						},
					},
				})
				continue
			}

			outMsg := map[string]any{
				"role": role,
			}

			if role == "assistant" {
				// Handle tool_calls
				if toolCalls, ok := msg["tool_calls"].([]any); ok && len(toolCalls) > 0 {
					var contentBlocks []any
					// Add text content if present
					if textContent := msg["content"]; textContent != nil {
						if s, ok := textContent.(string); ok && s != "" {
							contentBlocks = append(contentBlocks, map[string]any{
								"type": "text",
								"text": s,
							})
						}
					}
					for i, tc := range toolCalls {
						tcMap, ok := tc.(map[string]any)
						if !ok {
							return nil, fmt.Errorf("invalid openai tool_call at index %d", i)
						}
						fn, ok := tcMap["function"].(map[string]any)
						if !ok {
							return nil, fmt.Errorf("openai tool_call at index %d has non-object function field", i)
						}
						argsStr, _ := fn["arguments"].(string)
						var argsParsed any
						if err := json.Unmarshal([]byte(argsStr), &argsParsed); err != nil {
							argsParsed = map[string]any{}
						}
						contentBlocks = append(contentBlocks, map[string]any{
							"type":  "tool_use",
							"id":    tcMap["id"],
							"name":  fn["name"],
							"input": argsParsed,
						})
					}
					outMsg["content"] = contentBlocks
				} else {
					converted, convErr := openAIContentToAnthropic(msg["content"])
					if convErr != nil {
						return nil, convErr
					}
					outMsg["content"] = converted
				}
			} else {
				converted, convErr := openAIContentToAnthropic(msg["content"])
				if convErr != nil {
					return nil, convErr
				}
				outMsg["content"] = converted
			}

			messages = append(messages, outMsg)
		}
	}

	if systemContent != nil {
		out["system"] = systemContent
	}
	out["messages"] = messages

	// Simple field mappings
	if v, ok := req["temperature"]; ok {
		out["temperature"] = v
	}
	if v, ok := req["top_p"]; ok {
		out["top_p"] = v
	}
	if v, ok := req["stream"]; ok {
		out["stream"] = v
	}
	if v, ok := req["stop"]; ok {
		out["stop_sequences"] = v
	}
	copyFields(out, req, "metadata", "service_tier", "cache_control", "container")
	if user, ok := req["user"]; ok {
		out["metadata"] = map[string]any{"user_id": user}
	}

	// Convert tools
	if tools, ok := req["tools"].([]any); ok {
		var anthropicTools []any
		for i, t := range tools {
			tool, ok := t.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid openai tool at index %d", i)
			}
			toolType, _ := tool["type"].(string)
			if toolType == "function" {
				fn, ok := tool["function"].(map[string]any)
				if !ok {
					return nil, fmt.Errorf("tool %q has non-object function field in openai→anthropic conversion", toolType)
				}
				anthropicTools = append(anthropicTools, map[string]any{
					"type":         "custom",
					"name":         fn["name"],
					"description":  fn["description"],
					"input_schema": fn["parameters"],
					"strict":       fn["strict"],
				})
			} else {
				return nil, fmt.Errorf("unsupported tool type %q in openai→anthropic conversion", toolType)
			}
		}
		if len(anthropicTools) > 0 {
			out["tools"] = anthropicTools
		}
	}

	// Convert tool_choice
	if tc, ok := req["tool_choice"]; ok {
		switch v := tc.(type) {
		case string:
			switch v {
			case "auto":
				out["tool_choice"] = map[string]any{"type": "auto"}
			case "required":
				out["tool_choice"] = map[string]any{"type": "any"}
			case "none":
				out["tool_choice"] = map[string]any{"type": "none"}
			}
		case map[string]any:
			if fn, ok := v["function"].(map[string]any); ok {
				out["tool_choice"] = map[string]any{
					"type": "tool",
					"name": fn["name"],
				}
			}
		}
	}

	return json.Marshal(out)
}

// openAIContentToAnthropic converts OpenAI message content to Anthropic format.
func openAIContentToAnthropic(content any) (any, error) {
	switch v := content.(type) {
	case string:
		if v == "" {
			return []any{}, nil
		}
		return v, nil
	case []any:
		var blocks []any
		for i, part := range v {
			p, ok := part.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid openai content part at index %d", i)
			}
			typ, _ := p["type"].(string)
			switch typ {
			case "text":
				block := map[string]any{
					"type": "text",
					"text": p["text"],
				}
				copyFields(block, p, "cache_control", "citations")
				blocks = append(blocks, block)
			case "image_url":
				imgMap, _ := p["image_url"].(map[string]any)
				if imgMap != nil {
					url, _ := imgMap["url"].(string)
					if strings.HasPrefix(url, "data:image/") {
						parts := strings.SplitN(url, ",", 2)
						if len(parts) == 2 {
							mediaInfo := strings.TrimPrefix(parts[0], "data:")
							mediaInfo = strings.TrimSuffix(mediaInfo, ";base64")
							blocks = append(blocks, map[string]any{
								"type": "image",
								"source": map[string]any{
									"type":       "base64",
									"media_type": mediaInfo,
									"data":       parts[1],
								},
							})
						}
					} else {
						blocks = append(blocks, map[string]any{
							"type": "image",
							"source": map[string]any{
								"type": "url",
								"url":  url,
							},
						})
					}
				}
			default:
				return nil, fmt.Errorf("unsupported openai→anthropic content type %q", typ)
			}
		}
		return blocks, nil
	case nil:
		return []any{}, nil
	default:
		return content, nil
	}
}

// ---------------------------------------------------------------------------
// responses → anthropic
// ---------------------------------------------------------------------------

func responsesToAnthropicRequest(body []byte) ([]byte, error) {
	// First convert responses → openai, then openai → anthropic
	openaiBody, err := responsesToOpenAIRequest(body)
	if err != nil {
		return nil, err
	}
	return openAIToAnthropicRequest(openaiBody)
}

// ---------------------------------------------------------------------------
// anthropic → responses
// ---------------------------------------------------------------------------

func anthropicToResponsesRequest(body []byte) ([]byte, error) {
	// First convert anthropic → openai, then openai → responses
	openaiBody, err := anthropicToOpenAIRequest(body)
	if err != nil {
		return nil, err
	}
	return openAIToResponsesRequest(openaiBody)
}

func openAIContentText(content any) string {
	switch v := content.(type) {
	case nil:
		return ""
	case string:
		return v
	case []any:
		var sb strings.Builder
		for _, part := range v {
			p, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := p["text"].(string); ok {
				sb.WriteString(text)
			}
		}
		return sb.String()
	default:
		return fmt.Sprint(v)
	}
}

func anthropicToolResultText(content any) string {
	switch v := content.(type) {
	case nil:
		return ""
	case string:
		return v
	case []any:
		var sb strings.Builder
		for _, item := range v {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := block["text"].(string); ok {
				sb.WriteString(text)
			}
		}
		return sb.String()
	default:
		return fmt.Sprint(v)
	}
}
