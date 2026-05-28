package protocol

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
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
	streamMeta  *bytes.Buffer // accumulates SSE metadata lines between blank frames
	wroteHeader bool
	wroteBody   bool
	statusCode  int
	isStreaming bool
}

// NewTransformingWriter creates a response writer that converts from upstream to client format.
func NewTransformingWriter(w http.ResponseWriter, converter *Converter) *TransformingWriter {
	return &TransformingWriter{
		ResponseWriter: w,
		converter:      converter,
		buf:            &bytes.Buffer{},
		streamBuf:      &bytes.Buffer{},
		streamMeta:     &bytes.Buffer{},
	}
}

// WriteHeader intercepts the status code and checks if this is a streaming response.
func (w *TransformingWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.statusCode = statusCode

	// Detect streaming from Content-Type. Streaming responses must send headers
	// immediately so clients can start consuming the event stream. Non-streaming
	// responses are buffered and converted in Flush, so their headers must not be
	// committed until the final Content-Length is known.
	contentType := w.Header().Get("Content-Type")
	w.isStreaming = strings.Contains(contentType, "text/event-stream")
	if w.isStreaming {
		w.ResponseWriter.WriteHeader(statusCode)
	}
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
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	if w.isStreaming {
		if f, ok := w.ResponseWriter.(http.Flusher); ok {
			f.Flush()
		}
		return
	}

	if w.wroteBody {
		return
	}
	w.wroteBody = true

	body := w.buf.Bytes()
	writeBody := body

	// Check if it looks like JSON.
	contentType := w.Header().Get("Content-Type")
	if len(body) > 0 && (strings.Contains(contentType, "application/json") || json.Valid(body)) {
		var parsed map[string]any
		if err := json.Unmarshal(body, &parsed); err == nil {
			// Pass through error responses as-is — converting them via the success
			// path would produce a nonsensical response shape.
			if _, hasError := parsed["error"]; !hasError {
				converted, convErr := w.converter.ConvertResponseMap(parsed)
				if convErr != nil {
					w.writeHeadlineBadGateway("response conversion failed: " + convErr.Error())
					return
				}
				result, marshalErr := json.Marshal(converted)
				if marshalErr != nil {
					w.writeHeadlineBadGateway("response conversion failed: " + marshalErr.Error())
					return
				}
				writeBody = result
			}
		}
	}

	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(writeBody)))
	w.ResponseWriter.WriteHeader(w.statusCode)
	if len(writeBody) > 0 {
		_, _ = w.ResponseWriter.Write(writeBody)
	}
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

	// isSSEMeta returns true for SSE metadata lines (event:, id:, retry:, comments).
	isSSEMeta := func(line []byte) bool {
		if bytes.HasPrefix(line, []byte(":")) {
			return true
		}
		// Accept "event:" with or without a trailing space.
		if bytes.HasPrefix(line, []byte("event:")) {
			return true
		}
		if bytes.HasPrefix(line, []byte("id:")) {
			return true
		}
		if bytes.HasPrefix(line, []byte("retry:")) {
			return true
		}
		return false
	}

	// Find complete lines (terminated by \n)
	for {
		idx := bytes.IndexByte(buf, '\n')
		if idx < 0 {
			break
		}

		line := buf[:idx]
		buf = buf[idx+1:]

		// Trim \r for \r\n line endings
		line = bytes.TrimRight(line, "\r")

		// Blank line = end of SSE frame. Flush buffered metadata if the
		// frame had metadata without emitted data, then emit the boundary.
		if len(line) == 0 {
			w.streamMeta.Reset()
			outBuf.WriteByte('\n')
			continue
		}

		// Accumulate metadata lines — they are emitted only when the
		// associated data line produces converted output.
		if isSSEMeta(line) {
			w.streamMeta.Write(line)
			w.streamMeta.WriteByte('\n')
			continue
		}

		// Accept "data:" with or without a single space after the colon.
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}

		var payload []byte
		if bytes.HasPrefix(line, []byte("data: ")) {
			payload = bytes.TrimPrefix(line, []byte("data: "))
		} else {
			payload = bytes.TrimPrefix(line, []byte("data:"))
		}

		if string(payload) == "[DONE]" {
			converted, err := w.converter.FormatStreamLine([]byte("[DONE]"))
			if err == nil && converted != nil {
				if w.streamMeta.Len() > 0 {
					outBuf.Write(w.streamMeta.Bytes())
				}
				outBuf.Write(converted)
			}
			w.streamMeta.Reset()
			continue
		}

		converted, err := w.converter.FormatStreamLine(payload)
		if err == nil && converted != nil {
			if w.streamMeta.Len() > 0 {
				outBuf.Write(w.streamMeta.Bytes())
			}
			outBuf.Write(converted)
			w.streamMeta.Reset()
		} else {
			// Data line was skipped — discard any buffered metadata to
			// avoid orphan event:/id:/retry: lines with no data.
			w.streamMeta.Reset()
		}
	}

	// Keep any remaining partial line in streamBuf
	w.streamBuf.Reset()
	if len(buf) > 0 {
		w.streamBuf.Write(buf)
	}

	if outBuf.Len() > 0 {
		if _, err := w.ResponseWriter.Write(outBuf.Bytes()); err != nil {
			return len(data), err
		}
		if f, ok := w.ResponseWriter.(http.Flusher); ok {
			f.Flush()
		}
	}

	return len(data), nil
}

// writeHeadlineBadGateway writes a 502 Bad Gateway response with a text/plain body.
// Used when non-streaming response conversion fails. Sets wroteBody so Flush
// does not attempt a second write.
func (w *TransformingWriter) writeHeadlineBadGateway(msg string) {
	w.wroteBody = true
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(msg)))
	w.ResponseWriter.WriteHeader(http.StatusBadGateway)
	_, _ = w.ResponseWriter.Write([]byte(msg))
}

// Hijack implements the http.Hijacker interface for WebSocket support.
func (w *TransformingWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
}
