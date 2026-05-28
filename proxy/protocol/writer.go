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
	streamBuf   *bytes.Buffer // accumulates partial SSE lines across Write calls
	wroteHeader bool
	isStreaming bool
}

// NewTransformingWriter creates a response writer that converts from upstream to client format.
func NewTransformingWriter(w http.ResponseWriter, converter *Converter) *TransformingWriter {
	return &TransformingWriter{
		ResponseWriter: w,
		converter:      converter,
		buf:            &bytes.Buffer{},
		streamBuf:      &bytes.Buffer{},
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

	// Check if it looks like JSON
	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/json") && !json.Valid(body) {
		w.ResponseWriter.Write(body)
		return
	}

	// Single parse for the entire response
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		w.ResponseWriter.Write(body)
		return
	}
	// Pass through error responses as-is — converting them via the success
	// path would produce a nonsensical response shape.
	if _, hasError := parsed["error"]; hasError {
		w.ResponseWriter.Write(body)
		return
	}

	// Convert using the pre-parsed map (no redundant parse)
	converted, err := w.converter.ConvertResponseMap(parsed)
	if err != nil {
		w.ResponseWriter.Write(body)
		return
	}

	result, err := json.Marshal(converted)
	if err != nil {
		w.ResponseWriter.Write(body)
		return
	}

	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(result)))
	w.ResponseWriter.Write(result)
}

// writeStream processes SSE data for streaming conversion.
// It accumulates data in streamBuf and scans for complete lines,
// reusing the buffer across Write calls to handle partial lines.
func (w *TransformingWriter) writeStream(data []byte) (int, error) {
	// Safety limit: if streamBuf has grown beyond 10MB without a newline,
	// the upstream is sending garbage — flush and reset to prevent OOM.
	const maxStreamBufSize = 10 << 20
	if w.streamBuf.Len()+len(data) > maxStreamBufSize {
		w.streamBuf.Reset()
	}
	w.streamBuf.Write(data)

	var outBuf bytes.Buffer
	buf := w.streamBuf.Bytes()

	// Find complete lines (terminated by \n)
	for {
		idx := bytes.IndexByte(buf, '\n')
		if idx < 0 {
			break // No complete line, wait for more data
		}

		line := buf[:idx]
		buf = buf[idx+1:]

		// Trim \r for \r\n line endings
		line = bytes.TrimRight(line, "\r")

		if !bytes.HasPrefix(line, []byte("data: ")) {
			// Non-"data: " lines (empty lines, event type lines, etc.) are skipped.
			continue
		}

		payload := bytes.TrimPrefix(line, []byte("data: "))

		if string(payload) == "[DONE]" {
			converted, err := w.converter.FormatStreamLine([]byte("[DONE]"))
			if err == nil && converted != nil {
				outBuf.Write(converted)
			}
			continue
		}

		converted, err := w.converter.FormatStreamLine(payload)
		if err == nil && converted != nil {
			outBuf.Write(converted)
		}
	}

	// Keep any remaining partial line in streamBuf
	w.streamBuf.Reset()
	if len(buf) > 0 {
		w.streamBuf.Write(buf)
	}

	if outBuf.Len() > 0 {
		w.ResponseWriter.Write(outBuf.Bytes())
		if f, ok := w.ResponseWriter.(http.Flusher); ok {
			f.Flush()
		}
	}

	return len(data), nil
}




// Hijack implements the http.Hijacker interface for WebSocket support.
func (w *TransformingWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
}
