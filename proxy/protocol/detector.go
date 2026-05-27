package protocol

import "strings"

// DetectClientFormat identifies the client's protocol from the request URL path.
func DetectClientFormat(path string) Format {
	// Normalize: strip /v1 or /v prefix
	p := path
	if strings.HasPrefix(p, "/v1") {
		p = p[3:]
	} else if strings.HasPrefix(p, "/v/") {
		p = p[2:]
	}

	switch {
	case strings.HasSuffix(p, "/chat/completions"):
		return FormatOpenAI
	case strings.HasSuffix(p, "/responses"):
		return FormatResponses
	case strings.HasSuffix(p, "/messages"):
		return FormatAnthropic
	default:
		return FormatUnknown
	}
}

// RewritePath changes the endpoint path from one format to another.
// It preserves any prefix (e.g. /v1, /v) before the endpoint.
func RewritePath(originalPath string, targetFormat Format) string {
	prefix := ""

	// Extract prefix
	if strings.HasPrefix(originalPath, "/v1/") {
		prefix = "/v1"
	} else if strings.HasPrefix(originalPath, "/v/") {
		prefix = "/v"
	}

	var suffix string
	switch targetFormat {
	case FormatOpenAI:
		suffix = "/chat/completions"
	case FormatResponses:
		suffix = "/responses"
	case FormatAnthropic:
		suffix = "/messages"
	default:
		return originalPath
	}

	return prefix + suffix
}
