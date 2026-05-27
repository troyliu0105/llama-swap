package protocol

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// TransformingWriter wraps an http.ResponseWriter to convert the response
// body from the upstream format to the client format.
//
// For non-streaming responses: buffers the entire body, converts, then writes.
// For streaming responses: intercepts SSE data lines and converts each event.
type TransformingWriter struct {
	http.ResponseWriter
	converter   *Converter
	buf         *bytes.Buffer
	wroteHeader bool
	isStreaming bool
}

// NewTransformingWriter creates a response writer that converts from upstream to client format.
func NewTransformingWriter(w http.ResponseWriter, converter *Converter) *TransformingWriter {
	return &TransformingWriter{
		ResponseWriter: w,
		converter:      converter,
		buf:            &bytes.Buffer{},
	}
}

// WriteHeader intercepts the status code and checks if this is a streaming response.
func (w *TransformingWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true

	// Detect streaming from Content-Type
	contentType := w.Header().Get("Content-Type")
	w.isStreaming = strings.Contains(contentType, "text/event-stream")

	w.ResponseWriter.WriteHeader(statusCode)
}

// Write intercepts response data and either buffers (non-streaming) or
// converts SSE events on-the-fly (streaming).
func (w *TransformingWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	if w.isStreaming {
		return w.writeStream(data)
	}
	// Buffer for non-streaming conversion
	return w.buf.Write(data)
}

// Flush sends any buffered data to the underlying writer.
// For streaming responses, this is a pass-through.
// For non-streaming responses, this triggers the final conversion and write.
func (w *TransformingWriter) Flush() {
	if w.isStreaming {
		if f, ok := w.ResponseWriter.(http.Flusher); ok {
			f.Flush()
		}
		return
	}

	// Non-streaming: convert the buffered response
	if w.buf.Len() == 0 {
		return
	}

	body := w.buf.Bytes()

	// Check if response is JSON
	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/json") && !json.Valid(body) {
		// Not JSON, pass through as-is
		w.ResponseWriter.Write(body)
		return
	}

	// Check if it's an error response
	var peek map[string]any
	if err := json.Unmarshal(body, &peek); err != nil {
		w.ResponseWriter.Write(body)
		return
	}

	// Check for error objects and convert them too
	if _, hasError := peek["error"]; hasError {
		convertedErr := w.convertError(body)
		if convertedErr != nil {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(convertedErr)))
			w.ResponseWriter.Write(convertedErr)
			return
		}
	}

	converted, err := w.converter.ConvertResponse(body)
	if err != nil {
		// Conversion failed, write original
		w.ResponseWriter.Write(body)
		return
	}

	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(converted)))
	w.ResponseWriter.Write(converted)
}

// writeStream processes SSE data for streaming conversion.
func (w *TransformingWriter) writeStream(data []byte) (int, error) {
	// SSE format: lines of "data: <json>\n" or "event: <type>\n"
	// We need to parse the "data: " lines and convert them.
	// We must handle partial lines (data may arrive in chunks).

	scanner := bufio.NewScanner(bytes.NewReader(data))
	var outBuf bytes.Buffer

	for scanner.Scan() {
		line := scanner.Text()

		// Handle "data: " lines
		if strings.HasPrefix(line, "data: ") {
			payload := strings.TrimPrefix(line, "data: ")

			if payload == "[DONE]" {
				converted, err := w.converter.FormatStreamLine([]byte("[DONE]"))
				if err == nil && converted != nil {
					outBuf.Write(converted)
				}
				continue
			}

			converted, err := w.converter.FormatStreamLine([]byte(payload))
			if err == nil && converted != nil {
				outBuf.Write(converted)
			}
			continue
		}

		// Non-"data: " lines (empty lines, event type lines, etc.) are skipped.
	}

	if outBuf.Len() > 0 {
		w.ResponseWriter.Write(outBuf.Bytes())
		if f, ok := w.ResponseWriter.(http.Flusher); ok {
			f.Flush()
		}
	}

	return len(data), nil
}

// convertError attempts to convert an error response between formats.
func (w *TransformingWriter) convertError(body []byte) []byte {
	var errResp map[string]any
	if err := json.Unmarshal(body, &errResp); err != nil {
		return nil
	}

	// Anthropic error: {"type":"error","error":{"type":"...","message":"..."}}
	if errObj, ok := errResp["error"].(map[string]any); ok {
		if errResp["type"] == "error" {
			// Convert Anthropic error to OpenAI format
			msg, _ := errObj["message"].(string)
			errType, _ := errObj["type"].(string)
			result, err := json.Marshal(map[string]any{
				"error": map[string]any{
					"message": msg,
					"type":    errType,
				},
			})
			if err != nil {
				return nil
			}
			return result
		}
	}

	return nil
}

// Hijack implements the http.Hijacker interface for WebSocket support.
func (w *TransformingWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("ResponseWriter does not implement http.Hijacker")
}
