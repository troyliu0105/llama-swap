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
				role, _ := msg["role"].(string)
				content := msg["content"]
				outMsg := map[string]any{
					"role":    role,
					"content": convertResponsesContentToOpenAI(content),
				}
				messages = append(messages, outMsg)
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

	// Convert tools (function type only)
	if tools, ok := req["tools"].([]any); ok {
		var openaiTools []any
		for _, t := range tools {
			tool, ok := t.(map[string]any)
			if !ok {
				continue
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
			}
		}
		if len(openaiTools) > 0 {
			out["tools"] = openaiTools
		}
	}

	if v, ok := req["tool_choice"]; ok {
		out["tool_choice"] = v
	}

	return json.Marshal(out)
}

// convertResponsesContentToOpenAI converts Responses API content to OpenAI content.
func convertResponsesContentToOpenAI(content any) any {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []any
		for _, part := range v {
			p, ok := part.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := p["type"].(string)
			switch typ {
			case "input_text":
				parts = append(parts, map[string]any{
					"type": "text",
					"text": p["text"],
				})
			case "input_image":
				imgURL, _ := p["image_url"].(string)
				imgData, _ := p["image_data"].(string)
				if imgURL != "" {
					parts = append(parts, map[string]any{
						"type":      "image_url",
						"image_url": map[string]any{"url": imgURL},
					})
				} else if imgData != "" {
					parts = append(parts, map[string]any{
						"type":      "image_url",
						"image_url": map[string]any{"url": "data:image/png;base64," + imgData},
					})
				}
			default:
				// Pass through as text if has text field
				if text, ok := p["text"].(string); ok {
					parts = append(parts, map[string]any{
						"type": "text",
						"text": text,
					})
				}
			}
		}
		if len(parts) == 1 {
			if t, ok := parts[0].(map[string]any); ok {
				if t["type"] == "text" {
					return t["text"]
				}
			}
		}
		return parts
	default:
		return content
	}
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

			// Skip tool role messages for now (complex conversion)
			if role == "tool" {
				continue
			}

			outMsg := map[string]any{
				"role":    role,
				"content": convertOpenAIContentToResponses(msg["content"]),
			}

			// Handle assistant tool_calls
			if role == "assistant" {
				if toolCalls, ok := msg["tool_calls"].([]any); ok && len(toolCalls) > 0 {
					for _, tc := range toolCalls {
						tcMap, ok := tc.(map[string]any)
						if !ok {
							continue
						}
						fn, _ := tcMap["function"].(map[string]any)
						input = append(input, map[string]any{
							"type":      "function_call",
							"call_id":   tcMap["id"],
							"name":      fn["name"],
							"arguments": fn["arguments"],
						})
					}
				}
			}

			input = append(input, outMsg)
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

	// Convert tools
	if tools, ok := req["tools"].([]any); ok {
		var respTools []any
		for _, t := range tools {
			tool, ok := t.(map[string]any)
			if !ok {
				continue
			}
			toolType, _ := tool["type"].(string)
			if toolType == "function" {
				fn, _ := tool["function"].(map[string]any)
				respTools = append(respTools, map[string]any{
					"type":        "function",
					"name":        fn["name"],
					"description": fn["description"],
					"parameters":  fn["parameters"],
					"strict":      fn["strict"],
				})
			}
		}
		if len(respTools) > 0 {
			out["tools"] = respTools
		}
	}

	if v, ok := req["tool_choice"]; ok {
		out["tool_choice"] = v
	}

	return json.Marshal(out)
}

// convertOpenAIContentToResponses converts OpenAI message content to Responses API format.
func convertOpenAIContentToResponses(content any) any {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []any
		for _, part := range v {
			p, ok := part.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := p["type"].(string)
			switch typ {
			case "text":
				parts = append(parts, map[string]any{
					"type": "input_text",
					"text": p["text"],
				})
			case "image_url":
				imgMap, _ := p["image_url"].(map[string]any)
				if imgMap != nil {
					url, _ := imgMap["url"].(string)
					if strings.HasPrefix(url, "data:image/") {
						// Extract base64 data
						if idx := strings.Index(url, ","); idx >= 0 {
							parts = append(parts, map[string]any{
								"type":       "input_image",
								"image_data": url[idx+1:],
							})
						}
					} else {
						parts = append(parts, map[string]any{
							"type":      "input_image",
							"image_url": url,
						})
					}
				}
			default:
				if text, ok := p["text"].(string); ok {
					parts = append(parts, map[string]any{
						"type": "input_text",
						"text": text,
					})
				}
			}
		}
		if len(parts) == 1 {
			if t, ok := parts[0].(map[string]any); ok {
				if t["type"] == "input_text" {
					return t["text"]
				}
			}
		}
		return parts
	default:
		return content
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
		sysContent := anthropicContentToOpenAI(sys)
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
							// concatenate
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
					outMsg["content"] = anthropicContentToOpenAI(content)
				}
			} else if role == "user" {
				// Check for tool_result blocks
				if contentArr, ok := content.([]any); ok {
					hasToolResult := false
					for _, block := range contentArr {
						blockMap, ok := block.(map[string]any)
						if !ok {
							continue
						}
						if blockType, _ := blockMap["type"].(string); blockType == "tool_result" {
							hasToolResult = true
							toolUseID, _ := blockMap["tool_use_id"].(string)
							resultContent := blockMap["content"]
							resultStr := fmt.Sprint(resultContent)
							if arr, ok := resultContent.([]any); ok && len(arr) > 0 {
								if first, ok := arr[0].(map[string]any); ok {
									resultStr, _ = first["text"].(string)
								}
							}
							messages = append(messages, map[string]any{
								"role":         "tool",
								"content":      resultStr,
								"tool_call_id": toolUseID,
							})
						}
					}
					if hasToolResult {
						continue
					}
				}
				outMsg["content"] = anthropicContentToOpenAI(content)
			} else {
				outMsg["content"] = anthropicContentToOpenAI(content)
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

	// Convert tools
	if tools, ok := req["tools"].([]any); ok {
		var openaiTools []any
		for _, t := range tools {
			tool, ok := t.(map[string]any)
			if !ok {
				continue
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
			}
		}
		if len(openaiTools) > 0 {
			out["tools"] = openaiTools
		}
	}

	// Convert tool_choice
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
func anthropicContentToOpenAI(content any) any {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []any
		for _, block := range v {
			b, ok := block.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := b["type"].(string)
			switch typ {
			case "text":
				text, _ := b["text"].(string)
				parts = append(parts, map[string]any{
					"type": "text",
					"text": text,
				})
			case "image":
				source, _ := b["source"].(map[string]any)
				if source != nil {
					srcType, _ := source["type"].(string)
					if srcType == "url" {
						parts = append(parts, map[string]any{
							"type":      "image_url",
							"image_url": map[string]any{"url": source["url"]},
						})
					} else if srcType == "base64" {
						mediaType, _ := source["media_type"].(string)
						data, _ := source["data"].(string)
						parts = append(parts, map[string]any{
							"type":      "image_url",
							"image_url": map[string]any{"url": "data:" + mediaType + ";base64," + data},
						})
					}
				}
			// tool_use and tool_result are handled at the message level
			default:
				if text, ok := b["text"].(string); ok {
					parts = append(parts, map[string]any{
						"type": "text",
						"text": text,
					})
				}
			}
		}
		if len(parts) == 1 {
			if t, ok := parts[0].(map[string]any); ok {
				if t["type"] == "text" {
					return t["text"]
				}
			}
		}
		return parts
	default:
		return content
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
					systemContent = openAIContentToAnthropic(msg["content"])
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
						contentBlocks = append(contentBlocks, map[string]any{
							"type":  "tool_use",
							"id":    tcMap["id"],
							"name":  fn["name"],
							"input": argsParsed,
						})
					}
					outMsg["content"] = contentBlocks
				} else {
					outMsg["content"] = openAIContentToAnthropic(msg["content"])
				}
			} else {
				outMsg["content"] = openAIContentToAnthropic(msg["content"])
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

	// Convert tools
	if tools, ok := req["tools"].([]any); ok {
		var anthropicTools []any
		for _, t := range tools {
			tool, ok := t.(map[string]any)
			if !ok {
				continue
			}
			toolType, _ := tool["type"].(string)
			if toolType == "function" {
				fn, _ := tool["function"].(map[string]any)
				anthropicTools = append(anthropicTools, map[string]any{
					"type":         "custom",
					"name":         fn["name"],
					"description":  fn["description"],
					"input_schema": fn["parameters"],
					"strict":       fn["strict"],
				})
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
func openAIContentToAnthropic(content any) any {
	switch v := content.(type) {
	case string:
		if v == "" {
			return []any{}
		}
		return v
	case []any:
		var blocks []any
		for _, part := range v {
			p, ok := part.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := p["type"].(string)
			switch typ {
			case "text":
				blocks = append(blocks, map[string]any{
					"type": "text",
					"text": p["text"],
				})
			case "image_url":
				imgMap, _ := p["image_url"].(map[string]any)
				if imgMap != nil {
					url, _ := imgMap["url"].(string)
					if strings.HasPrefix(url, "data:image/") {
						// Parse data URI: data:image/png;base64,xxxx
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
				if text, ok := p["text"].(string); ok {
					blocks = append(blocks, map[string]any{
						"type": "text",
						"text": text,
					})
				}
			}
		}
		return blocks
	case nil:
		return []any{}
	default:
		return content
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
