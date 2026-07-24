package shared

import "testing"

func strp(s string) *string { return &s }

func TestParseEnvBoolean(t *testing.T) {
	cases := []struct {
		val      *string
		fallback bool
		want     bool
	}{
		{strp("true"), false, true},
		{strp("TRUE"), false, true},
		{strp(" 1 "), false, true},
		{strp("yes"), false, true},
		{strp("on"), false, true},
		{strp("false"), true, false},
		{strp("0"), true, false},
		{strp("no"), true, false},
		{strp("off"), true, false},
		{strp(""), true, true},        // empty → fallback
		{strp(""), false, false},      // empty → fallback
		{strp("maybe"), true, true},   // unknown → fallback
		{strp("maybe"), false, false}, // unknown → fallback
		{nil, true, true},             // unset → fallback
		{nil, false, false},           // unset → fallback
	}
	for _, c := range cases {
		if got := ParseEnvBoolean(c.val, c.fallback); got != c.want {
			t.Errorf("ParseEnvBoolean(%v, %v) = %v, want %v", c.val, c.fallback, got, c.want)
		}
	}
}

func TestShouldBlockDetections(t *testing.T) {
	// Empty detections → false
	if ShouldBlockDetections(nil, SeverityHigh) {
		t.Fatal("empty → false")
	}
	// high detection, high threshold → true
	if !ShouldBlockDetections([]DetectionLike{{Severity: "high"}}, SeverityHigh) {
		t.Fatal("high/high → true")
	}
	// medium detection, high threshold → false (observe-only)
	if ShouldBlockDetections([]DetectionLike{{Severity: "medium"}}, SeverityHigh) {
		t.Fatal("medium/high → false")
	}
	// medium detection, medium threshold → true
	if !ShouldBlockDetections([]DetectionLike{{Severity: "medium"}}, SeverityMedium) {
		t.Fatal("medium/medium → true")
	}
	// low detection, low threshold → true
	if !ShouldBlockDetections([]DetectionLike{{Severity: "low"}}, SeverityLow) {
		t.Fatal("low/low → true")
	}
	// empty severity defaults to high
	if !ShouldBlockDetections([]DetectionLike{{Severity: ""}}, SeverityHigh) {
		t.Fatal("empty severity → high → true")
	}
	// unknown severity → score 0 → false
	if ShouldBlockDetections([]DetectionLike{{Severity: "bogus"}}, SeverityLow) {
		t.Fatal("bogus severity → score 0 → false")
	}
	// invalid threshold defaults to high
	if ShouldBlockDetections([]DetectionLike{{Severity: "medium"}}, InjectionSeverity("bogus")) {
		t.Fatal("bogus threshold → high → medium not blocked")
	}
	// mixed: one high among lows → true at high threshold
	if !ShouldBlockDetections([]DetectionLike{{Severity: "low"}, {Severity: "high"}}, SeverityHigh) {
		t.Fatal("mixed with high → true")
	}
}

func TestResolveBlockThreshold(t *testing.T) {
	noEnv := func(string) string { return "" }

	// explicit wins
	if got := ResolveBlockThreshold(strp("low"), noEnv); got != SeverityLow {
		t.Fatalf("explicit low → %v", got)
	}
	// explicit case-insensitive + trimmed
	if got := ResolveBlockThreshold(strp("  MEDIUM "), noEnv); got != SeverityMedium {
		t.Fatalf("explicit MEDIUM → %v", got)
	}
	// invalid explicit → high
	if got := ResolveBlockThreshold(strp("bogus"), noEnv); got != SeverityHigh {
		t.Fatalf("bogus → %v", got)
	}
	// nil explicit, no env → high
	if got := ResolveBlockThreshold(nil, noEnv); got != SeverityHigh {
		t.Fatalf("nil/noenv → %v", got)
	}
	// env INPUT_SANITIZER_BLOCK_THRESHOLD
	env1 := func(k string) string {
		if k == "INPUT_SANITIZER_BLOCK_THRESHOLD" {
			return "medium"
		}
		return ""
	}
	if got := ResolveBlockThreshold(nil, env1); got != SeverityMedium {
		t.Fatalf("env INPUT_SANITIZER → %v", got)
	}
	// env INJECTION_GUARD_BLOCK_THRESHOLD (fallback)
	env2 := func(k string) string {
		if k == "INJECTION_GUARD_BLOCK_THRESHOLD" {
			return "low"
		}
		return ""
	}
	if got := ResolveBlockThreshold(nil, env2); got != SeverityLow {
		t.Fatalf("env INJECTION_GUARD → %v", got)
	}
	// INPUT_SANITIZER takes precedence over INJECTION_GUARD
	env3 := func(k string) string {
		if k == "INPUT_SANITIZER_BLOCK_THRESHOLD" {
			return "high"
		}
		if k == "INJECTION_GUARD_BLOCK_THRESHOLD" {
			return "low"
		}
		return ""
	}
	if got := ResolveBlockThreshold(nil, env3); got != SeverityHigh {
		t.Fatalf("precedence → %v, want high", got)
	}
}
