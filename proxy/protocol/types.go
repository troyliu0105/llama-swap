package protocol

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
}

// NeedsConversion returns true when client and upstream formats differ.
func NeedsConversion(clientFormat, upstreamFormat Format) bool {
	return clientFormat != FormatUnknown && upstreamFormat != FormatUnknown && clientFormat != upstreamFormat
}
