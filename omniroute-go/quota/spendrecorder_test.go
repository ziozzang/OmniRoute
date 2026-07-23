package quota

import "testing"

// TestBuildConsumptionCost verifies token coercion and USD clamping.
func TestBuildConsumptionCost(t *testing.T) {
	// Normal usage.
	c := BuildConsumptionCost(map[string]any{"prompt_tokens": 100.0, "completion_tokens": 50.0}, 0.25)
	if c.Tokens != 150 || c.USD != 0.25 || c.Requests != 1 {
		t.Fatalf("got %+v, want {150 0.25 1}", c)
	}
	// String tokens coerced.
	c = BuildConsumptionCost(map[string]any{"prompt_tokens": "10", "completion_tokens": "5"}, 0)
	if c.Tokens != 15 {
		t.Fatalf("string tokens: got %v, want 15", c.Tokens)
	}
	// Negative cost clamped to 0.
	c = BuildConsumptionCost(nil, -5)
	if c.USD != 0 {
		t.Fatalf("negative cost should clamp to 0, got %v", c.USD)
	}
	// Nil usage → 0 tokens.
	c = BuildConsumptionCost(nil, 1.0)
	if c.Tokens != 0 || c.USD != 1.0 {
		t.Fatalf("nil usage: got %+v, want {0 1 1}", c)
	}
	// Non-numeric token → 0.
	c = BuildConsumptionCost(map[string]any{"prompt_tokens": "abc"}, 0)
	if c.Tokens != 0 {
		t.Fatalf("non-numeric token should be 0, got %v", c.Tokens)
	}
}

// TestToFloat64 verifies the coercion helper edge cases.
func TestToFloat64(t *testing.T) {
	cases := []struct {
		in   any
		want float64
	}{
		{100.0, 100}, {int(5), 5}, {int64(7), 7}, {"12.5", 12.5},
		{"abc", 0}, {nil, 0}, {[]int{1}, 0}, {true, 0},
	}
	for _, c := range cases {
		if got := toFloat64(c.in); got != c.want {
			t.Errorf("toFloat64(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
