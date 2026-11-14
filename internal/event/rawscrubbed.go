package event

// RawDetails is the error detail a payload carries in RawScrubbed.
type RawDetails struct {
	Name    string
	Message string
	Source  string
}

// RawScrubbedDetails extracts error details from the RawScrubbed payload.
// Browser errors (promise rejections, cross-origin errors) often arrive with
// exception: {} but carry actual details in RawScrubbed. The title builder in
// storage and the fingerprint both use it, so they agree on the fallback.
func RawScrubbedDetails(rawScrubbed map[string]any) RawDetails {
	if rawScrubbed == nil {
		return RawDetails{}
	}
	name, _ := rawScrubbed["name"].(string)
	props, _ := rawScrubbed["properties"].(map[string]any)
	if props == nil {
		return RawDetails{Name: name}
	}
	message, _ := props["message"].(string)
	source, _ := props["source"].(string)
	if source == "" {
		source, _ = props["url"].(string)
	}
	return RawDetails{Name: name, Message: message, Source: source}
}
