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
// It preserves any path prefix before the protocol endpoint.
func RewritePath(originalPath string, targetFormat Format) string {
	var targetSuffix string
	switch targetFormat {
	case FormatOpenAI:
		targetSuffix = "/chat/completions"
	case FormatResponses:
		targetSuffix = "/responses"
	case FormatAnthropic:
		targetSuffix = "/messages"
	default:
		return originalPath
	}

	for _, currentSuffix := range []string{"/chat/completions", "/responses", "/messages"} {
		if strings.HasSuffix(originalPath, currentSuffix) {
			return strings.TrimSuffix(originalPath, currentSuffix) + targetSuffix
		}
	}
	return originalPath
}
