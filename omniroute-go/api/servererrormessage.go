package api

// servererrormessage.go — Extract human-readable message from an API error body.
// Port of src/lib/api/serverErrorMessage.ts
//
// Pure function — no race concerns.

// ResolveServerErrorMessage extracts the most specific message from a parsed
// API error body { error: { message, details?: [{message}] } }, falling back
// to the caller-supplied default. Never panics on nil/malformed input.
func ResolveServerErrorMessage(body any, fallback string) string {
	m, ok := body.(map[string]any)
	if !ok {
		return fallback
	}
	errObj, ok := m["error"].(map[string]any)
	if !ok {
		return fallback
	}
	// Prefer details[0].message (most specific, e.g. COMBO_002 field errors).
	if details, ok := errObj["details"].([]any); ok && len(details) > 0 {
		if first, ok := details[0].(map[string]any); ok {
			if dm, ok := first["message"].(string); ok && dm != "" {
				return dm
			}
		}
	}
	if msg, ok := errObj["message"].(string); ok && msg != "" {
		return msg
	}
	return fallback
}
