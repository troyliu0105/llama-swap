package audit

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

func ComputeFingerprint(reqPath string, body []byte) string {
	if isResponsesEndpoint(reqPath) {
		previousResponseID := gjson.GetBytes(body, "previous_response_id").String()
		if previousResponseID != "" {
			return hashString(previousResponseID)
		}
	}

	var systemPart string
	var userPart string
	switch {
	case isChatCompletionsEndpoint(reqPath):
		systemPart = extractSystemFromMessages(body)
		userPart = extractFirstUserFromMessages(body)
	case isResponsesEndpoint(reqPath):
		systemPart = extractInstructions(body)
		userPart = extractFirstUserFromInput(body)
	case isMessagesEndpoint(reqPath):
		systemPart = extractSystemTopLevel(body)
		userPart = extractFirstUserFromMessages(body)
	case isCompletionsEndpoint(reqPath):
		userPart = gjson.GetBytes(body, "prompt").String()
	}

	if systemPart == "" && userPart == "" {
		return randomFingerprint()
	}

	return hashString(fmt.Sprintf("sys:%d:%s\x00usr:%d:%s", len(systemPart), systemPart, len(userPart), userPart))
}

func IsChatEndpoint(reqPath string) bool {
	return isChatCompletionsEndpoint(reqPath) || isResponsesEndpoint(reqPath) || isMessagesEndpoint(reqPath)
}

func hashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func randomFingerprint() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return hashString("audit-random-fallback" + string(buf))
	}
	return hex.EncodeToString(buf)
}

func extractSystemFromMessages(body []byte) string {
	parts := make([]string, 0)
	for _, message := range gjson.GetBytes(body, "messages").Array() {
		if message.Get("role").String() == "system" {
			parts = append(parts, contentString(message.Get("content")))
		}
	}
	return strings.Join(parts, "\x00")
}

func extractFirstUserFromMessages(body []byte) string {
	parts := make([]string, 0)
	for _, message := range gjson.GetBytes(body, "messages").Array() {
		if message.Get("role").String() == "user" {
			parts = append(parts, contentString(message.Get("content")))
		}
	}
	return strings.Join(parts, "\x00")
}

func extractInstructions(body []byte) string {
	return contentString(gjson.GetBytes(body, "instructions"))
}

func extractFirstUserFromInput(body []byte) string {
	input := gjson.GetBytes(body, "input")
	if input.IsArray() {
		parts := make([]string, 0)
		for _, item := range input.Array() {
			if item.Get("role").String() == "user" {
				parts = append(parts, contentString(item.Get("content")))
			}
		}
		return strings.Join(parts, "\x00")
	}
	return contentString(input)
}

func extractSystemTopLevel(body []byte) string {
	system := gjson.GetBytes(body, "system")
	if system.IsArray() {
		parts := make([]string, 0)
		for _, item := range system.Array() {
			if item.Get("type").String() == "text" {
				text := item.Get("text").String()
				if text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\x00")
	}
	return contentString(system)
}

func isChatCompletionsEndpoint(path string) bool {
	return normalizePath(path) == "/v1/chat/completions"
}

func isResponsesEndpoint(path string) bool {
	return normalizePath(path) == "/v1/responses"
}

func isMessagesEndpoint(path string) bool {
	return normalizePath(path) == "/v1/messages"
}

func isCompletionsEndpoint(path string) bool {
	return normalizePath(path) == "/v1/completions"
}

func normalizePath(path string) string {
	path = strings.TrimSpace(path)
	if idx := strings.IndexByte(path, '?'); idx >= 0 {
		path = path[:idx]
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return strings.TrimRight(path, "/")
}

func contentString(value gjson.Result) string {
	if value.IsArray() {
		parts := make([]string, 0)
		for _, item := range value.Array() {
			text := item.Get("text").String()
			if text != "" {
				parts = append(parts, text)
				continue
			}
			if item.Type == gjson.String {
				parts = append(parts, item.String())
			}
		}
		return strings.Join(parts, "\x00")
	}
	return value.String()
}
