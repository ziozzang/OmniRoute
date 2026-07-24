package translator

import "testing"

func TestIsAbortFinishReason(t *testing.T) {
	cases := []struct {
		in   any
		want bool
	}{
		{"malformed_function_call", true},
		{"MALFORMED_FUNCTION_CALL", true}, // case-insensitive
		{"unexpected_tool_call", true},
		{"no_image", true},
		{"stop", false},
		{"safety", false},
		{123, false},
		{nil, false},
	}
	for _, c := range cases {
		if got := IsAbortFinishReason(c.in); got != c.want {
			t.Errorf("IsAbortFinishReason(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestNormalizeOpenAICompatibleFinishReason(t *testing.T) {
	cases := []struct {
		in   any
		want any
	}{
		{"stop", "stop"},
		{"STOP", "stop"},          // lowercased
		{"length", "length"},
		{"tool_calls", "tool_calls"},
		{"max_tokens", "length"},  // mapped
		{"safety", "content_filter"},
		{"recitation", "content_filter"},
		{"policy_violation", "content_filter"},
		{"malformed_function_call", "malformed_function_call"}, // abort: passed through raw
		{"custom_reason", "custom_reason"}, // unknown: passed through
		{123, 123},            // non-string: unchanged
		{nil, nil},
	}
	for _, c := range cases {
		if got := NormalizeOpenAICompatibleFinishReason(c.in); got != c.want {
			t.Errorf("Normalize(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestNormalizeFinishReasonString(t *testing.T) {
	if got := NormalizeOpenAICompatibleFinishReasonString("stop", ""); got != "stop" {
		t.Fatalf("stop → %q", got)
	}
	if got := NormalizeOpenAICompatibleFinishReasonString(nil, ""); got != "stop" {
		t.Fatalf("nil → default stop, got %q", got)
	}
	if got := NormalizeOpenAICompatibleFinishReasonString(nil, "length"); got != "length" {
		t.Fatalf("nil → custom fallback, got %q", got)
	}
	if got := NormalizeOpenAICompatibleFinishReasonString(123, "stop"); got != "stop" {
		t.Fatalf("non-string → fallback, got %q", got)
	}
	if got := NormalizeOpenAICompatibleFinishReasonString("max_tokens", ""); got != "length" {
		t.Fatalf("max_tokens → length, got %q", got)
	}
}
