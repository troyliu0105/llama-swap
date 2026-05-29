package protocol

import "strings"

// Format represents a wire protocol for LLM APIs.
type Format string

const (
	FormatOpenAI    Format = "openai"    // /v1/chat/completions
	FormatResponses Format = "responses" // /v1/responses
	FormatAnthropic Format = "anthropic" // /v1/messages
	FormatUnknown   Format = ""
)

// ParseFormat parses a format string from config.
// Returns FormatOpenAI for empty string (backward compatible default).
func ParseFormat(s string) Format {
	switch s {
	case "openai", "":
		return FormatOpenAI
	case "responses":
		return FormatResponses
	case "anthropic":
		return FormatAnthropic
	default:
		return FormatUnknown
	}
}

// Converter handles request/response conversion between two formats.
type Converter struct {
	From Format
	To   Format

	// streamStarted tracks whether stream lifecycle start events have been
	// emitted. Used by OpenAI→Responses and OpenAI→Anthropic conversions to
	// avoid emitting duplicate start events when the upstream includes a
	// role field in every chunk.
	streamStarted bool

	currentBlockType  string
	currentBlockIndex int
	blockCount        int
	toolCallBuffers   map[int]*toolCallBuf
	streamFinished    bool
	pendingStopReason string // buffered stop reason, awaiting terminal emission
}

// toolCallBuf accumulates tool call data from OpenAI streaming chunks.
type toolCallBuf struct {
	id   string
	name string
	args strings.Builder
}

// Clone returns a shallow copy of the Converter with stream state reset.
// Each request must use its own clone so stream state is not shared across
// concurrent requests.
func (c *Converter) Clone() *Converter {
	return &Converter{From: c.From, To: c.To, currentBlockIndex: -1}
}

// NeedsConversion returns true when client and upstream formats differ.
func NeedsConversion(clientFormat, upstreamFormat Format) bool {
	return clientFormat != FormatUnknown && upstreamFormat != FormatUnknown && clientFormat != upstreamFormat
}
