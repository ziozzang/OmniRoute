package quota

import "testing"

// TestQuotaGroupSlug mirrors the TS quotaGroupSlug examples.
func TestQuotaGroupSlug(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Pool Principal", "poolprincipal"},
		{"My-Pool_01", "mypool01"},
		{"!!!", "pool"}, // empty after strip → fallback
		{"", "pool"},
		{"ABC123", "abc123"},
		{"café-pool", "cafpool"}, // non-ascii stripped
	}
	for _, c := range cases {
		if got := QuotaGroupSlug(c.in); got != c.want {
			t.Errorf("QuotaGroupSlug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestQuotaModelNameRoundTrip verifies build → parse round-trips, including a
// namespaced model id containing "/".
func TestQuotaModelNameRoundTrip(t *testing.T) {
	cases := []struct{ group, provider, model string }{
		{"Pool Principal", "codex", "gpt-5.5"},
		{"Team A", "claude", "claude-sonnet-4"},
		{"ns", "openai", "org/model/v2"}, // namespaced model with "/"
	}
	for _, c := range cases {
		name := QuotaModelName(c.group, c.provider, c.model)
		if !IsQuotaModelName(name) {
			t.Fatalf("IsQuotaModelName(%q) = false", name)
		}
		p := ParseQuotaModelName(name)
		if p == nil {
			t.Fatalf("ParseQuotaModelName(%q) = nil", name)
		}
		if p.GroupSlug != QuotaGroupSlug(c.group) || p.Provider != c.provider || p.Model != c.model {
			t.Errorf("round-trip %q → %+v, want group=%q provider=%q model=%q",
				name, p, QuotaGroupSlug(c.group), c.provider, c.model)
		}
	}
}

// TestQuotaModelNameExample checks the documented canonical example.
func TestQuotaModelNameExample(t *testing.T) {
	got := QuotaModelName("Pool Principal", "codex", "gpt-5.5")
	want := "qtSd/poolprincipal/codex/gpt-5.5"
	if got != want {
		t.Errorf("QuotaModelName = %q, want %q", got, want)
	}
}

// TestParseQuotaModelNameInvalid verifies rejection of malformed names.
func TestParseQuotaModelNameInvalid(t *testing.T) {
	bad := []string{
		"",
		"gpt-4",
		"qtSd/",
		"qtSd/group",
		"qtSd/group/provider",      // missing model
		"qtSd//provider/model",     // empty group
		"qtSd/group//model",        // empty provider
		"qtSd/group/provider/",     // empty model
		"other/group/provider/model",
	}
	for _, name := range bad {
		if p := ParseQuotaModelName(name); p != nil {
			t.Errorf("ParseQuotaModelName(%q) = %+v, want nil", name, p)
		}
	}
}
