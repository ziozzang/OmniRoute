package shared

import "testing"

func TestNormalizeRequestBodyLimitMb(t *testing.T) {
	cases := []struct {
		in   any
		want int
	}{
		{10.0, 10}, {1, 1}, {"20", 20}, {500.0, 500}, {1.0, 1},
		{0.0, -1},      // below min
		{501.0, -1},    // above max
		{"abc", -1},    // non-numeric
		{nil, -1},      // nil
		{10.9, 10},     // floor
	}
	for _, c := range cases {
		if got := NormalizeRequestBodyLimitMb(c.in); got != c.want {
			t.Errorf("NormalizeRequestBodyLimitMb(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestRequestBodyLimitMbToBytes(t *testing.T) {
	if got := RequestBodyLimitMbToBytes(10); got != 10*1024*1024 {
		t.Fatalf("10 MB = %d", got)
	}
}

func TestParseRequestBodyLimitBytes(t *testing.T) {
	if got := ParseRequestBodyLimitBytes(nil); got != DefaultRequestBodyLimitBytes {
		t.Fatalf("nil → default, got %d", got)
	}
	if got := ParseRequestBodyLimitBytes(strp("")); got != DefaultRequestBodyLimitBytes {
		t.Fatalf("empty → default, got %d", got)
	}
	if got := ParseRequestBodyLimitBytes(strp("5000000")); got != 5000000 {
		t.Fatalf("valid → %d", got)
	}
	if got := ParseRequestBodyLimitBytes(strp("-5")); got != DefaultRequestBodyLimitBytes {
		t.Fatalf("negative → default, got %d", got)
	}
	if got := ParseRequestBodyLimitBytes(strp("abc")); got != DefaultRequestBodyLimitBytes {
		t.Fatalf("invalid → default, got %d", got)
	}
}

func TestRequestBodyLimitBytesToMb(t *testing.T) {
	if got := RequestBodyLimitBytesToMb(10 * 1024 * 1024); got != 10 {
		t.Fatalf("10MB bytes → %d", got)
	}
	if got := RequestBodyLimitBytesToMb(0); got != MinRequestBodyLimitMB {
		t.Fatalf("0 → min, got %d", got)
	}
	if got := RequestBodyLimitBytesToMb(1000 * 1024 * 1024); got != MaxRequestBodyLimitMB {
		t.Fatalf("huge → max, got %d", got)
	}
}

func TestRequestBodyLimitMbFromEnv(t *testing.T) {
	if got := RequestBodyLimitMbFromEnv(strp("20971520")); got != 20 { // 20 MB
		t.Fatalf("env 20MB → %d", got)
	}
	if got := RequestBodyLimitMbFromEnv(nil); got != DefaultRequestBodyLimitMB {
		t.Fatalf("nil → default, got %d", got)
	}
}

func TestGetBodySizeLimit(t *testing.T) {
	// Default path → configured (default 10MB)
	if got := GetBodySizeLimit("/api/other", nil); got != DefaultRequestBodyLimitBytes {
		t.Fatalf("default path → %d", got)
	}
	// LLM route → 50MB (max of 50MB route, 10MB configured)
	if got := GetBodySizeLimit("/api/v1/chat/completions", nil); got != MaxBodyBytesLLMAPI {
		t.Fatalf("chat route → %d, want %d", got, MaxBodyBytesLLMAPI)
	}
	// File route → 500MB
	if got := GetBodySizeLimit("/api/v1/files/upload", nil); got != MaxBodyBytesFile {
		t.Fatalf("files route → %d", got)
	}
	// Configured higher than route → configured wins (max)
	settings := map[string]any{"maxBodySizeMb": 200.0}
	if got := GetBodySizeLimit("/api/v1/chat/completions", settings); got != 200*1024*1024 {
		t.Fatalf("configured 200MB wins → %d", got)
	}
}

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{500, "500 bytes"},
		{2048, "2 KB"},
		{5 * 1024 * 1024, "5 MB"},
	}
	for _, c := range cases {
		if got := FormatBytes(c.in); got != c.want {
			t.Errorf("FormatBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
