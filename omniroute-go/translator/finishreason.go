package translator

import "strings"

// finishreason.go — OpenAI-compatible finish reason normalization.
// Port of open-sse/utils/finishReason.ts
//
// Pure functions — no race concerns.

var openAIFinishReasons = map[string]bool{
	"stop":           true,
	"length":         true,
	"tool_calls":     true,
	"content_filter": true,
	"function_call":  true,
}

var safetyFinishReasons = map[string]bool{
	"safety":             true,
	"recitation":         true,
	"blocklist":          true,
	"prohibited_content": true,
	"content_filtered":   true,
	"policy_violation":   true,
	"malformed_response": true,
}

// abortFinishReasons are Gemini/Antigravity reasons meaning the model ABORTED
// the turn mid tool-call (distinct from deliberate safety blocks). Left
// un-mapped so clients see an honest non-standard value rather than "stop".
var abortFinishReasons = map[string]bool{
	"malformed_function_call":  true,
	"unexpected_tool_call":     true,
	"finish_reason_unspecified": true,
	"other":                    true,
	"language":                 true,
	"no_image":                 true,
}

// IsAbortFinishReason returns whether a finish reason indicates an aborted turn.
func IsAbortFinishReason(value any) bool {
	s, ok := value.(string)
	if !ok {
		return false
	}
	return abortFinishReasons[strings.ToLower(s)]
}

// NormalizeOpenAICompatibleFinishReason normalizes a finish reason to an
// OpenAI-compatible value. Returns the input unchanged for non-strings.
func NormalizeOpenAICompatibleFinishReason(value any) any {
	s, ok := value.(string)
	if !ok {
		return value
	}
	normalized := strings.ToLower(s)
	if openAIFinishReasons[normalized] {
		return normalized
	}
	if normalized == "max_tokens" {
		return "length"
	}
	if safetyFinishReasons[normalized] {
		return "content_filter"
	}
	return normalized
}

// NormalizeOpenAICompatibleFinishReasonString normalizes and guarantees a
// non-empty string, falling back to "stop" (or the provided fallback).
func NormalizeOpenAICompatibleFinishReasonString(value any, fallback string) string {
	if fallback == "" {
		fallback = "stop"
	}
	normalized := NormalizeOpenAICompatibleFinishReason(value)
	if s, ok := normalized.(string); ok && s != "" {
		return s
	}
	return fallback
}
