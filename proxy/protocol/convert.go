package protocol

import (
	"encoding/json"
	"fmt"
)

// NewConverter creates a converter for the given direction.
func NewConverter(from, to Format) (*Converter, error) {
	if from == to {
		return nil, fmt.Errorf("no conversion needed: both formats are %s", from)
	}
	if from == FormatUnknown || to == FormatUnknown {
		return nil, fmt.Errorf("cannot convert from/to unknown format")
	}
	return &Converter{From: from, To: to}, nil
}

// ConvertRequest transforms a request body from the client format to the upstream format.
// Returns the new request body bytes and the target path.
func (c *Converter) ConvertRequest(body []byte, requestPath string) (newBody []byte, newPath string, err error) {
	convertFn, ok := requestConverters[convertKey{c.From, c.To}]
	if !ok {
		return nil, "", fmt.Errorf("unsupported request conversion: %s → %s", c.From, c.To)
	}

	newBody, err = convertFn(body)
	if err != nil {
		return nil, "", fmt.Errorf("converting request %s → %s: %w", c.From, c.To, err)
	}

	newPath = RewritePath(requestPath, c.To)
	return newBody, newPath, nil
}

// ConvertResponseMap transforms a pre-parsed response from upstream format to client format.
// This avoids redundant JSON parsing when the caller has already decoded the body.
func (c *Converter) ConvertResponseMap(parsed map[string]any) (map[string]any, error) {
	convertFn, ok := responseMapConverters[convertKey{c.To, c.From}]
	if !ok {
		return nil, fmt.Errorf("unsupported response conversion: %s → %s", c.To, c.From)
	}
	return convertFn(parsed)
}

// ConvertResponse transforms a non-streaming response body from upstream format back to client format.
func (c *Converter) ConvertResponse(body []byte) ([]byte, error) {
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parsing response JSON: %w", err)
	}
	result, err := c.ConvertResponseMap(parsed)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

// ConvertStreamEvent transforms a single SSE data line from upstream format to client format.
// The input is the JSON payload after the "data: " prefix (without trailing newline).
// Returns the converted JSON payload, or the original if no conversion is needed for this event type.
func (c *Converter) ConvertStreamEvent(data []byte) ([]byte, error) {
	// Stream events flow upstream→client, so swap the direction.
	convertFn, ok := streamConverters[convertKey{c.To, c.From}]
	if !ok {
		return data, nil
	}
	return convertFn(data)
}

// convertKey identifies a conversion direction.
type convertKey struct{ from, to Format }

// requestConverters maps (from, to) format pairs to request body conversion functions.
var requestConverters = map[convertKey]func([]byte) ([]byte, error){
	{FormatResponses, FormatOpenAI}:    responsesToOpenAIRequest,
	{FormatOpenAI, FormatResponses}:    openAIToResponsesRequest,
	{FormatResponses, FormatAnthropic}: responsesToAnthropicRequest,
	{FormatAnthropic, FormatResponses}: anthropicToResponsesRequest,
	{FormatAnthropic, FormatOpenAI}:    anthropicToOpenAIRequest,
	{FormatOpenAI, FormatAnthropic}:    openAIToAnthropicRequest,
}

// responseMapConverters maps (upstream, client) format pairs to response map conversion functions.
// "from" is the upstream format (what we received), "to" is the client format (what we send back).
var responseMapConverters = map[convertKey]func(map[string]any) (map[string]any, error){
	{FormatOpenAI, FormatResponses}:    convertOpenAIResponseMapToResponses,
	{FormatResponses, FormatOpenAI}:    convertResponsesResponseMapToOpenAI,
	{FormatOpenAI, FormatAnthropic}:    convertOpenAIResponseMapToAnthropic,
	{FormatAnthropic, FormatOpenAI}:    convertAnthropicResponseMapToOpenAI,
	{FormatResponses, FormatAnthropic}: convertResponsesResponseMapToAnthropic,
	{FormatAnthropic, FormatResponses}: convertAnthropicResponseMapToResponses,
}

// streamConverters maps conversion directions to streaming event converters.
var streamConverters = map[convertKey]func([]byte) ([]byte, error){
	{FormatOpenAI, FormatResponses}:    convertOpenAIStreamToResponses,
	{FormatResponses, FormatOpenAI}:    convertResponsesStreamToOpenAI,
	{FormatOpenAI, FormatAnthropic}:    convertOpenAIStreamToAnthropic,
	{FormatAnthropic, FormatOpenAI}:    convertAnthropicStreamToOpenAI,
	{FormatResponses, FormatAnthropic}: convertResponsesStreamToAnthropic,
	{FormatAnthropic, FormatResponses}: convertAnthropicStreamToResponses,
}
