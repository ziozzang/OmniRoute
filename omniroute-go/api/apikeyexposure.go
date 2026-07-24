package api

import "strings"

// apikeyexposure.go — API key reveal/masking helpers.
// Port of src/lib/apiKeyExposure.ts
//
// Pure functions — no race concerns.

var apiKeyRevealEnabledValues = map[string]bool{
	"1": true, "true": true, "yes": true, "on": true,
}

// IsAPIKeyRevealEnabled checks the ALLOW_API_KEY_REVEAL env flag.
func IsAPIKeyRevealEnabled(env map[string]string) bool {
	raw := strings.ToLower(strings.TrimSpace(env["ALLOW_API_KEY_REVEAL"]))
	return apiKeyRevealEnabledValues[raw]
}

// MaskStoredAPIKey masks a stored API key: first 8 + "****" + last 4.
// Returns "" for non-string / too-short input (TS returns null).
func MaskStoredAPIKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 12 {
		return key[:1] + "****"
	}
	return key[:8] + "****" + key[len(key)-4:]
}
