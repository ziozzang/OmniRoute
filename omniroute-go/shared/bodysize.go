package shared

import (
	"math"
	"strconv"
	"strings"
)

// bodysize.go — Request body size limits.
// Port of src/shared/constants/bodySize.ts + pure parts of bodySizeGuard.ts
//
// Pure functions — no race concerns.

const (
	RequestBodyBytesPerMB      = 1024 * 1024
	DefaultRequestBodyLimitMB  = 10
	MinRequestBodyLimitMB      = 1
	MaxRequestBodyLimitMB      = 500
	DefaultRequestBodyLimitBytes = DefaultRequestBodyLimitMB * RequestBodyBytesPerMB
)

// Route-specific body limits (from bodySizeGuard.ts).
const (
	MaxBodyBytesImport = 100 * 1024 * 1024 // backup/import: 100 MB
	MaxBodyBytesAudio  = 100 * 1024 * 1024 // audio transcription: 100 MB
	MaxBodyBytesFile   = 500 * 1024 * 1024 // file uploads: 500 MB
	MaxBodyBytesLLMAPI = 50 * 1024 * 1024  // LLM payloads: 50 MB
)

type bodySizeRule struct {
	prefix string
	limit  int
}

var routeLimits = []bodySizeRule{
	{"/api/db-backups/import", MaxBodyBytesImport},
	{"/api/v1/chat/completions", MaxBodyBytesLLMAPI},
	{"/api/v1/responses", MaxBodyBytesLLMAPI},
	{"/api/v1/audio/transcriptions", MaxBodyBytesAudio},
	{"/api/v1/files", MaxBodyBytesFile},
}

// NormalizeRequestBodyLimitMb normalizes a configured MB limit. Returns -1 for
// invalid/out-of-range values (TS returns null).
func NormalizeRequestBodyLimitMb(value any) int {
	var parsed float64
	switch v := value.(type) {
	case float64:
		parsed = v
	case int:
		parsed = float64(v)
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return -1
		}
		parsed = f
	default:
		return -1
	}
	if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return -1
	}
	normalized := int(math.Floor(parsed))
	if normalized < MinRequestBodyLimitMB || normalized > MaxRequestBodyLimitMB {
		return -1
	}
	return normalized
}

// RequestBodyLimitMbToBytes converts MB to bytes.
func RequestBodyLimitMbToBytes(mb int) int {
	return mb * RequestBodyBytesPerMB
}

// ParseRequestBodyLimitBytes parses a byte limit from an env string. A nil/empty
// or invalid value falls back to the default (10 MB).
func ParseRequestBodyLimitBytes(value *string) int {
	if value == nil || *value == "" {
		return DefaultRequestBodyLimitBytes
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(*value))
	if err != nil || parsed <= 0 {
		return DefaultRequestBodyLimitBytes
	}
	return parsed
}

// RequestBodyLimitBytesToMb converts bytes to MB, clamped to [1, 500].
func RequestBodyLimitBytesToMb(bytes int) int {
	configuredMb := int(math.Round(float64(bytes) / float64(RequestBodyBytesPerMB)))
	if configuredMb < MinRequestBodyLimitMB {
		return MinRequestBodyLimitMB
	}
	if configuredMb > MaxRequestBodyLimitMB {
		return MaxRequestBodyLimitMB
	}
	return configuredMb
}

// RequestBodyLimitMbFromEnv parses an env byte string into a clamped MB value.
func RequestBodyLimitMbFromEnv(value *string) int {
	return RequestBodyLimitBytesToMb(ParseRequestBodyLimitBytes(value))
}

// GetConfiguredBodySizeLimitBytes resolves the configured limit from settings,
// falling back to the default (10 MB). settings may be nil.
func GetConfiguredBodySizeLimitBytes(settings map[string]any) int {
	var raw any
	if settings != nil {
		raw = settings["maxBodySizeMb"]
	}
	configuredMb := NormalizeRequestBodyLimitMb(raw)
	if configuredMb == -1 {
		return DefaultRequestBodyLimitBytes
	}
	return RequestBodyLimitMbToBytes(configuredMb)
}

// GetBodySizeLimit resolves the body size limit for a request path, applying
// route-specific overrides (taking the max of route limit and configured limit).
func GetBodySizeLimit(pathname string, settings map[string]any) int {
	configuredLimit := GetConfiguredBodySizeLimitBytes(settings)
	for _, rule := range routeLimits {
		if strings.HasPrefix(pathname, rule.prefix) {
			if rule.limit > configuredLimit {
				return rule.limit
			}
			return configuredLimit
		}
	}
	return configuredLimit
}

// FormatBytes formats bytes as a human-readable string (matches TS formatBytes).
func FormatBytes(bytes int) string {
	if bytes >= 1024*1024 {
		return strconv.Itoa(bytes/(1024*1024)) + " MB"
	}
	if bytes >= 1024 {
		return strconv.Itoa(bytes/1024) + " KB"
	}
	return strconv.Itoa(bytes) + " bytes"
}
