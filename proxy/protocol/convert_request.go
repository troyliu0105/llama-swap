package protocol

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"path"
	"path/filepath"
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
	if err := rejectResponsesStatefulRequestFields(req, "OpenAI Chat Completions"); err != nil {
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
			if err := validateResponsesTextFormatForOpenAI(format); err != nil {
				return nil, err
			}
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
				if isResponsesNativeToolType(toolType) {
					return nil, fmt.Errorf("responses-native tool type %q cannot be converted to OpenAI Chat Completions", toolType)
				}
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
				hasURL := false
				hasData := false
				if raw, ok := p["image_url"]; ok {
					s, ok := raw.(string)
					if !ok || strings.TrimSpace(s) == "" {
						return nil, fmt.Errorf("responses input_image content block cannot be converted to OpenAI/Anthropic image content with non-string or empty image_url")
					}
					hasURL = true
				}
				if raw, ok := p["image_data"]; ok {
					s, ok := raw.(string)
					if !ok || strings.TrimSpace(s) == "" {
						return nil, fmt.Errorf("responses input_image content block cannot be converted to OpenAI/Anthropic image content with non-string or empty image_data")
					}
					hasData = true
				}
				if hasURL && hasData {
					return nil, fmt.Errorf("responses input_image content block cannot be converted to OpenAI/Anthropic image content with both image_url and image_data set")
				}
				if !hasURL && !hasData {
					return nil, fmt.Errorf("responses input_image content block cannot be converted to OpenAI/Anthropic image content without image_url or image_data")
				}
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
				if !hasNonEmptyStringField(p, "file_url") && !hasNonEmptyStringField(p, "file_data") && !hasNonEmptyStringField(p, "file_id") {
					return nil, fmt.Errorf("responses input_file content block cannot be converted to OpenAI file content without file_url, file_data, or file_id")
				}
				if len(file) == 0 {
					return nil, fmt.Errorf("responses input_file content block cannot be converted to OpenAI file content without file_url, file_data, file_id, or filename")
				}
				parts = append(parts, map[string]any{"type": "file", "file": file})
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
	if err := rejectOpenAIUnsupportedRequestFields(req, "Responses"); err != nil {
		return nil, err
	}

	out := make(map[string]any)

	// model
	if v, ok := req["model"]; ok {
		out["model"] = v
	}

	// Convert messages → input
	var instructionParts []string
	var input []any

	if messages, ok := req["messages"].([]any); ok {
		for _, m := range messages {
			msg, ok := m.(map[string]any)
			if !ok {
				continue
			}
			role, _ := msg["role"].(string)

			// Extract system/developer as ordered instructions
			if role == "system" || role == "developer" {
				if s := openAIInstructionText(msg["content"]); s != "" {
					instructionParts = append(instructionParts, s)
				}
				continue
			}

			if role == "tool" {
				outputText, outputErr := openAIToolMessageText(msg["content"])
				if outputErr != nil {
					return nil, outputErr
				}
				input = append(input, map[string]any{
					"type":    "function_call_output",
					"call_id": msg["tool_call_id"],
					"output":  outputText,
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

	if len(instructionParts) > 0 {
		out["instructions"] = strings.Join(instructionParts, "\n\n")
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
	copyFields(out, req, "stream_options", "parallel_tool_calls", "service_tier", "metadata", "top_logprobs", "prompt_cache_key", "presence_penalty", "frequency_penalty", "seed", "logprobs")
	if responseFormat, ok := req["response_format"]; ok {
		if err := validateOpenAIResponseFormatForResponses(responseFormat); err != nil {
			return nil, err
		}
		out["text"] = map[string]any{"format": responseFormat}
	}
	if user, ok := req["user"]; ok {
		out["safety_identifier"] = user
	}

	// Convert tools
	toolsValue := req["tools"]
	if toolsValue == nil {
		if functions, ok := req["functions"].([]any); ok {
			var converted []any
			for _, fn := range functions {
				fnMap, ok := fn.(map[string]any)
				if !ok {
					continue
				}
				converted = append(converted, map[string]any{
					"type": "function",
					"function": map[string]any{
						"name":        fnMap["name"],
						"description": fnMap["description"],
						"parameters":  fnMap["parameters"],
					},
				})
			}
			toolsValue = converted
		}
	}
	if tools, ok := toolsValue.([]any); ok {
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
				if isOpenAINonPortableToolType(toolType) {
					return nil, fmt.Errorf("openai non-portable tool type %q cannot be converted to Responses", toolType)
				}
				return nil, fmt.Errorf("unsupported tool type %q in openai→responses conversion", toolType)
			}
		}
		if len(respTools) > 0 {
			out["tools"] = respTools
		}
	}

	toolChoiceValue := req["tool_choice"]
	if toolChoiceValue == nil {
		if functionCall, ok := req["function_call"]; ok {
			switch v := functionCall.(type) {
			case string:
				switch v {
				case "none":
					toolChoiceValue = "none"
				case "auto":
					toolChoiceValue = "auto"
				default:
					toolChoiceValue = "required"
				}
			case map[string]any:
				if name, _ := v["name"].(string); name != "" {
					toolChoiceValue = map[string]any{
						"type":     "function",
						"function": map[string]any{"name": name},
					}
				}
			}
		}
	}
	if v := toolChoiceValue; v != nil {
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
				if imgMap == nil {
					return nil, fmt.Errorf("openai image_url content block cannot be converted to Responses input_image without an object image_url payload")
				}
				url, ok := imgMap["url"].(string)
				if !ok || strings.TrimSpace(url) == "" {
					return nil, fmt.Errorf("openai image_url content block cannot be converted to Responses input_image without a non-empty url")
				}
				if strings.HasPrefix(url, "data:image/") {
					idx := strings.Index(url, ",")
					if idx < 0 {
						return nil, fmt.Errorf("openai image_url content block cannot be converted to Responses input_image from malformed data URL")
					}
					if idx+1 >= len(url) || strings.TrimSpace(url[idx+1:]) == "" {
						return nil, fmt.Errorf("openai image_url content block cannot be converted to Responses input_image without non-empty base64 data")
					}
					part := map[string]any{"type": "input_image", "image_data": url[idx+1:]}
					mediaInfo := strings.TrimPrefix(url[:idx], "data:")
					part["media_type"] = strings.TrimSuffix(mediaInfo, ";base64")
					copyFields(part, imgMap, "detail")
					parts = append(parts, part)
				} else {
					part := map[string]any{"type": "input_image", "image_url": url}
					copyFields(part, imgMap, "detail")
					parts = append(parts, part)
				}
			case "file":
				file, ok := p["file"].(map[string]any)
				if !ok || file == nil {
					return nil, fmt.Errorf("openai file content block cannot be converted to Responses input_file without an object file payload")
				}
				part := map[string]any{"type": "input_file"}
				copyFields(part, file, "file_url", "file_data", "file_id", "filename")
				if !hasNonEmptyStringField(file, "file_url") && !hasNonEmptyStringField(file, "file_data") && !hasNonEmptyStringField(file, "file_id") {
					return nil, fmt.Errorf("openai file content block cannot be converted to Responses input_file without file_url, file_data, or file_id")
				}
				if len(part) == 1 {
					return nil, fmt.Errorf("openai file content block cannot be converted to Responses input_file without file_url, file_data, file_id, or filename")
				}
				parts = append(parts, part)
			default:
				if isOpenAINonPortableContentType(typ) {
					return nil, fmt.Errorf("unsupported openai non-portable content block %q for Responses/Anthropic conversion", typ)
				}
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
	if err := rejectAnthropicUnsupportedRequestFields(req, "OpenAI Chat Completions"); err != nil {
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
					for blockIndex, block := range contentArr {
						blockMap, ok := block.(map[string]any)
						if !ok {
							return nil, fmt.Errorf("invalid anthropic assistant content block at index %d", blockIndex)
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
						default:
							if isAnthropicNonPortableContentType(blockType) {
								return nil, fmt.Errorf("anthropic non-portable content block %q cannot be converted to OpenAI/Responses chat content", blockType)
							}
							return nil, fmt.Errorf("anthropic assistant content block type %q cannot be converted to OpenAI/Responses chat content", blockType)
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
							toolText, toolErr := anthropicToolResultText(blockMap["content"])
							if toolErr != nil {
								return nil, toolErr
							}
							messages = append(messages, map[string]any{
								"role":         "tool",
								"content":      toolText,
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
			if toolType == "" || toolType == "custom" {
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
				if isAnthropicBuiltInToolType(toolType) {
					return nil, fmt.Errorf("anthropic built-in tool type %q cannot be converted to OpenAI Chat Completions", toolType)
				}
				return nil, fmt.Errorf("unsupported tool type %q in anthropic→openai conversion", toolType)
			}
		}
		if len(openaiTools) > 0 {
			out["tools"] = openaiTools
		}
	}
	if tc, ok := req["tool_choice"].(map[string]any); ok {
		tcType, _ := tc["type"].(string)
		if disabled, ok := tc["disable_parallel_tool_use"].(bool); ok && disabled {
			out["parallel_tool_calls"] = false
		}
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
				if source == nil {
					return nil, fmt.Errorf("anthropic image block cannot be converted to OpenAI/Responses image content without an object source payload")
				}
				srcType, _ := source["type"].(string)
				if srcType == "url" {
					url, ok := source["url"].(string)
					if !ok || strings.TrimSpace(url) == "" {
						return nil, fmt.Errorf("anthropic image block cannot be converted to OpenAI/Responses image content without a non-empty source.url")
					}
					parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
				} else if srcType == "base64" {
					mediaType, ok := source["media_type"].(string)
					if !ok || strings.TrimSpace(mediaType) == "" {
						return nil, fmt.Errorf("anthropic image block cannot be converted to OpenAI/Responses image content without a non-empty source.media_type")
					}
					data, ok := source["data"].(string)
					if !ok || strings.TrimSpace(data) == "" {
						return nil, fmt.Errorf("anthropic image block cannot be converted to OpenAI/Responses image content without non-empty source.data")
					}
					parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:" + mediaType + ";base64," + data}})
				} else {
					return nil, fmt.Errorf("anthropic image block cannot be converted to OpenAI/Responses image content from unsupported source type %q", srcType)
				}
			case "document":
				if _, hasSource := b["source"]; !hasSource {
					return nil, fmt.Errorf("anthropic document block cannot be converted to OpenAI/Responses file content without a supported source payload")
				}
				source, _ := b["source"].(map[string]any)
				if source == nil {
					return nil, fmt.Errorf("anthropic document block cannot be converted to OpenAI/Responses file content without an object source payload")
				}
				srcType, _ := source["type"].(string)
				file := map[string]any{}
				switch srcType {
				case "url":
					if url, _ := source["url"].(string); url != "" {
						file["file_url"] = url
					} else {
						return nil, fmt.Errorf("anthropic document block cannot be converted to OpenAI/Responses file content without a non-empty url source")
					}
				case "base64":
					if data, _ := source["data"].(string); data != "" {
						file["file_data"] = data
					} else {
						return nil, fmt.Errorf("anthropic document block cannot be converted to OpenAI/Responses file content without non-empty base64 data")
					}
				default:
					return nil, fmt.Errorf("anthropic document block cannot be converted to OpenAI/Responses file content from unsupported source type %q", srcType)
				}
				if title := inferAnthropicDocumentFilename(b, source); title != "" {
					file["filename"] = title
				}
				parts = append(parts, map[string]any{"type": "file", "file": file})
			case "tool_result":
				// Handled by the caller; should not appear here but skip gracefully.
			default:
				if isAnthropicNonPortableContentType(typ) {
					return nil, fmt.Errorf("anthropic non-portable content block %q cannot be converted to OpenAI/Responses chat content", typ)
				}
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
	if err := rejectOpenAIUnsupportedRequestFields(req, "Anthropic Messages"); err != nil {
		return nil, err
	}
	if err := rejectOpenAIToAnthropicUnsupportedRequestFields(req); err != nil {
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
	var systemParts []string
	var messages []any

	if msgs, ok := req["messages"].([]any); ok {
		for _, m := range msgs {
			msg, ok := m.(map[string]any)
			if !ok {
				continue
			}
			role, _ := msg["role"].(string)

			if role == "system" || role == "developer" {
				if s := openAIInstructionText(msg["content"]); s != "" {
					systemParts = append(systemParts, s)
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

	if len(systemParts) > 0 {
		out["system"] = strings.Join(systemParts, "\n\n")
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
	toolsValue := req["tools"]
	if toolsValue == nil {
		if functions, ok := req["functions"].([]any); ok {
			var converted []any
			for _, fn := range functions {
				fnMap, ok := fn.(map[string]any)
				if !ok {
					continue
				}
				converted = append(converted, map[string]any{
					"type": "function",
					"function": map[string]any{
						"name":        fnMap["name"],
						"description": fnMap["description"],
						"parameters":  fnMap["parameters"],
					},
				})
			}
			toolsValue = converted
		}
	}
	if tools, ok := toolsValue.([]any); ok {
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
					"name":         fn["name"],
					"description":  fn["description"],
					"input_schema": fn["parameters"],
					"strict":       fn["strict"],
				})
			} else {
				if isOpenAINonPortableToolType(toolType) {
					return nil, fmt.Errorf("openai non-portable tool type %q cannot be converted to Anthropic Messages", toolType)
				}
				return nil, fmt.Errorf("unsupported tool type %q in openai→anthropic conversion", toolType)
			}
		}
		if len(anthropicTools) > 0 {
			out["tools"] = anthropicTools
		}
	}

	// Convert tool_choice
	toolChoiceValue := req["tool_choice"]
	if toolChoiceValue == nil {
		if functionCall, ok := req["function_call"]; ok {
			switch v := functionCall.(type) {
			case string:
				switch v {
				case "none":
					toolChoiceValue = "none"
				case "auto":
					toolChoiceValue = "auto"
				default:
					toolChoiceValue = "required"
				}
			case map[string]any:
				if name, _ := v["name"].(string); name != "" {
					toolChoiceValue = map[string]any{
						"type":     "function",
						"function": map[string]any{"name": name},
					}
				}
			}
		}
	}
	if tc := toolChoiceValue; tc != nil {
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
				if imgMap == nil {
					return nil, fmt.Errorf("openai image_url block cannot be converted to Anthropic image content without an object image_url payload")
				}
				url, ok := imgMap["url"].(string)
				if !ok || strings.TrimSpace(url) == "" {
					return nil, fmt.Errorf("openai image_url block cannot be converted to Anthropic image content without a non-empty url")
				}
				if strings.HasPrefix(url, "data:image/") {
					parts := strings.SplitN(url, ",", 2)
					if len(parts) != 2 {
						return nil, fmt.Errorf("openai image_url block cannot be converted to Anthropic image content from malformed data URL")
					}
					if strings.TrimSpace(parts[1]) == "" {
						return nil, fmt.Errorf("openai image_url block cannot be converted to Anthropic image content without non-empty base64 data")
					}
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
				} else {
					blocks = append(blocks, map[string]any{
						"type": "image",
						"source": map[string]any{
							"type": "url",
							"url":  url,
						},
					})
				}
			case "file":
				fileMap, ok := p["file"].(map[string]any)
				if !ok || fileMap == nil {
					return nil, fmt.Errorf("openai file block cannot be converted to Anthropic document content without an object file payload")
				}
				doc := map[string]any{"type": "document"}
				source := map[string]any{}
				if fileURL, _ := fileMap["file_url"].(string); fileURL != "" {
					source["type"] = "url"
					source["url"] = fileURL
				} else if fileData, _ := fileMap["file_data"].(string); fileData != "" {
					source["type"] = "base64"
					source["media_type"] = inferMediaTypeFromFilename(fileMap["filename"])
					source["data"] = fileData
				} else if fileID, _ := fileMap["file_id"].(string); fileID != "" {
					return nil, fmt.Errorf("openai file block with file_id %q cannot be converted to Anthropic document content", fileID)
				} else {
					return nil, fmt.Errorf("openai file block cannot be converted to Anthropic document content without file_url or file_data")
				}
				doc["source"] = source
				if title := inferOpenAIFileTitle(fileMap); title != "" {
					doc["title"] = title
				}
				blocks = append(blocks, doc)
			default:
				if isOpenAINonPortableContentType(typ) {
					return nil, fmt.Errorf("unsupported openai non-portable content block %q for Responses/Anthropic conversion", typ)
				}
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

func rejectResponsesStatefulRequestFields(req map[string]any, target string) error {
	if hasNonEmptyStringField(req, "previous_response_id") {
		return fmt.Errorf("responses stateful field previous_response_id cannot be converted to %s", target)
	}
	if hasNonEmptyStringField(req, "conversation") {
		return fmt.Errorf("responses stateful field conversation cannot be converted to %s", target)
	}
	if _, ok := req["conversation"].(map[string]any); ok {
		return fmt.Errorf("responses stateful field conversation cannot be converted to %s", target)
	}
	if _, ok := req["context_management"]; ok && req["context_management"] != nil {
		return fmt.Errorf("responses stateful field context_management cannot be converted to %s", target)
	}
	if _, ok := req["prompt"]; ok && req["prompt"] != nil {
		return fmt.Errorf("responses stateful field prompt cannot be converted to %s", target)
	}
	if include, ok := req["include"].([]any); ok && len(include) > 0 {
		return fmt.Errorf("responses stateful field include cannot be converted to %s", target)
	}
	if _, ok := req["reasoning"]; ok && req["reasoning"] != nil {
		return fmt.Errorf("responses stateful field reasoning cannot be converted to %s", target)
	}
	if text, ok := req["text"].(map[string]any); ok {
		if _, ok := text["verbosity"]; ok && text["verbosity"] != nil {
			return fmt.Errorf("responses field text.verbosity cannot be converted to %s", target)
		}
	}
	if _, ok := req["truncation"]; ok && req["truncation"] != nil {
		return fmt.Errorf("responses stateful field truncation cannot be converted to %s", target)
	}
	if _, ok := req["max_tool_calls"]; ok && req["max_tool_calls"] != nil {
		return fmt.Errorf("responses stateful field max_tool_calls cannot be converted to %s", target)
	}
	if store, ok := req["store"].(bool); ok && store {
		return fmt.Errorf("responses stateful field store cannot be converted to %s", target)
	}
	if background, ok := req["background"].(bool); ok && background {
		return fmt.Errorf("responses stateful field background cannot be converted to %s", target)
	}
	return nil
}

func rejectAnthropicUnsupportedRequestFields(req map[string]any, target string) error {
	for _, field := range []string{"thinking", "output_config", "container", "inference_geo", "top_k", "cache_control", "mcp_servers", "user_profile_id", "speed"} {
		if _, ok := req[field]; ok && req[field] != nil {
			return fmt.Errorf("anthropic field %s cannot be converted to %s", field, target)
		}
	}
	return nil
}

func rejectOpenAIUnsupportedRequestFields(req map[string]any, target string) error {
	if n, ok := req["n"]; ok && toInt(n) > 1 {
		return fmt.Errorf("openai field n cannot be converted to %s when greater than 1", target)
	}
	if _, ok := req["logit_bias"]; ok && req["logit_bias"] != nil {
		return fmt.Errorf("openai field logit_bias cannot be converted to %s", target)
	}
	if _, ok := req["prediction"]; ok && req["prediction"] != nil {
		return fmt.Errorf("openai field prediction cannot be converted to %s", target)
	}
	if _, ok := req["audio"]; ok && req["audio"] != nil {
		return fmt.Errorf("openai field audio cannot be converted to %s", target)
	}
	return nil
}

func rejectOpenAIToAnthropicUnsupportedRequestFields(req map[string]any) error {
	for _, field := range []string{"response_format", "stream_options", "top_logprobs", "presence_penalty", "frequency_penalty", "seed", "logprobs"} {
		if _, ok := req[field]; ok && req[field] != nil {
			return fmt.Errorf("openai field %s cannot be converted to Anthropic Messages", field)
		}
	}
	return nil
}

func validateOpenAIResponseFormatForResponses(responseFormat any) error {
	format, ok := responseFormat.(map[string]any)
	if !ok || format == nil {
		return fmt.Errorf("openai field response_format cannot be converted to Responses without an object payload")
	}
	typeValue, _ := format["type"].(string)
	switch typeValue {
	case "text", "json_object", "json_schema":
		return nil
	default:
		return fmt.Errorf("openai field response_format cannot be converted to Responses for unsupported format type %q", typeValue)
	}
}

func validateResponsesTextFormatForOpenAI(textFormat any) error {
	format, ok := textFormat.(map[string]any)
	if !ok || format == nil {
		return fmt.Errorf("responses field text.format cannot be converted to OpenAI Chat Completions without an object payload")
	}
	typeValue, _ := format["type"].(string)
	switch typeValue {
	case "text", "json_object", "json_schema":
		return nil
	default:
		return fmt.Errorf("responses field text.format cannot be converted to OpenAI Chat Completions for unsupported format type %q", typeValue)
	}
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

func openAIInstructionText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		return openAIContentText(v)
	default:
		return ""
	}
}

func inferMediaTypeFromFilename(filename any) string {
	name, _ := filename.(string)
	if name == "" {
		return "application/octet-stream"
	}
	ext := strings.ToLower(filepath.Ext(name))
	if ext == "" {
		return "application/octet-stream"
	}
	if mediaType := mime.TypeByExtension(ext); mediaType != "" {
		return mediaType
	}
	return "application/octet-stream"
}

func inferAnthropicDocumentFilename(block map[string]any, source map[string]any) string {
	if title, _ := block["title"].(string); title != "" {
		return title
	}
	sourceURL, _ := source["url"].(string)
	if sourceURL != "" {
		parsed, err := url.Parse(sourceURL)
		if err == nil {
			base := path.Base(parsed.Path)
			if base != "." && base != "/" && base != "" {
				return base
			}
		}
	}
	mediaType, _ := source["media_type"].(string)
	if mediaType == "" {
		return ""
	}
	if exts, err := mime.ExtensionsByType(mediaType); err == nil && len(exts) > 0 {
		ext := exts[0]
		if ext != "" {
			return "document" + ext
		}
	}
	return ""
}

func hasNonEmptyStringField(m map[string]any, key string) bool {
	v, ok := m[key]
	if !ok {
		return false
	}
	s, ok := v.(string)
	return ok && s != ""
}

func inferOpenAIFileTitle(fileMap map[string]any) string {
	if filename, _ := fileMap["filename"].(string); filename != "" {
		return filename
	}
	fileURL, _ := fileMap["file_url"].(string)
	if fileURL == "" {
		return ""
	}
	parsed, err := url.Parse(fileURL)
	if err != nil {
		return ""
	}
	base := path.Base(parsed.Path)
	if base == "." || base == "/" || base == "" {
		return ""
	}
	return base
}

func isResponsesNativeToolType(toolType string) bool {
	switch toolType {
	case "web_search", "file_search", "code_interpreter", "computer_use", "image_generation", "mcp", "local_shell", "tool_search":
		return true
	default:
		return false
	}
}

func isOpenAINonPortableToolType(toolType string) bool {
	return toolType != "" && toolType != "function"
}

func isOpenAINonPortableContentType(contentType string) bool {
	switch contentType {
	case "input_audio", "audio", "video", "video_file":
		return true
	default:
		return false
	}
}

func isAnthropicBuiltInToolType(toolType string) bool {
	return toolType != "" && toolType != "custom"
}

func isAnthropicNonPortableContentType(contentType string) bool {
	switch contentType {
	case "search_result", "thinking", "redacted_thinking", "server_tool_use", "server_tool_result":
		return true
	default:
		return false
	}
}

func anthropicToolResultText(content any) (string, error) {
	switch v := content.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	case []any:
		var sb strings.Builder
		for _, item := range v {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			blockType, _ := block["type"].(string)
			if blockType != "text" {
				return "", fmt.Errorf("anthropic tool_result content block %q cannot be converted to OpenAI/Responses tool output", blockType)
			}
			if text, ok := block["text"].(string); ok {
				sb.WriteString(text)
			}
		}
		return sb.String(), nil
	default:
		return fmt.Sprint(v), nil
	}
}

func openAIToolMessageText(content any) (string, error) {
	switch v := content.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	case []any:
		var sb strings.Builder
		for _, item := range v {
			part, ok := item.(map[string]any)
			if !ok {
				continue
			}
			partType, _ := part["type"].(string)
			if partType != "text" {
				return "", fmt.Errorf("openai tool message content part %q cannot be converted to Responses tool output", partType)
			}
			if text, ok := part["text"].(string); ok {
				sb.WriteString(text)
			}
		}
		return sb.String(), nil
	default:
		return fmt.Sprint(v), nil
	}
}

func detectOpenAIStreamIncludeUsage(body []byte) bool {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return false
	}
	streamOptions, _ := req["stream_options"].(map[string]any)
	includeUsage, _ := streamOptions["include_usage"].(bool)
	return includeUsage
}
