package shared

import "strings"

// envboolean.go — Shared env/flag boolean parsing for security guards.
// Port of src/shared/utils/envBoolean.ts
//
// Truthy: true / 1 / yes / on (case-insensitive, trimmed)
// Falsy:  false / 0 / no / off
// Unset/empty/unknown → fallback
//
// Pure function — no race concerns.

// ParseEnvBoolean parses a boolean from an env/flag string with a fallback.
// A nil pointer represents an unset value (→ fallback).
func ParseEnvBoolean(value *string, fallback bool) bool {
	if value == nil {
		return fallback
	}
	normalized := strings.ToLower(strings.TrimSpace(*value))
	if normalized == "" {
		return fallback
	}
	switch normalized {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
