package quota

import "testing"

// TestKnownPlanParity verifies the catalog matches the TS KNOWN_PLANS: provider
// slug, dimension count, and the exact float sentinel for "unknown" limits.
func TestKnownPlanParity(t *testing.T) {
	cases := []struct {
		provider string
		ndims    int
	}{
		{"codex", 2}, {"claude", 2}, {"glm", 2}, {"minimax", 2},
		{"deepseek", 1}, {"bailian", 3}, {"kimi", 1}, {"kimi-coding", 2},
		{"xiaomi-mimo", 1}, {"alibaba", 1}, {"grok-cli", 4},
	}
	for _, c := range cases {
		p := GetKnownPlan(c.provider)
		if p == nil {
			t.Fatalf("GetKnownPlan(%q) = nil, want plan", c.provider)
		}
		if p.Provider != c.provider {
			t.Errorf("%s: provider=%q, want %q", c.provider, p.Provider, c.provider)
		}
		if len(p.Dimensions) != c.ndims {
			t.Errorf("%s: %d dims, want %d", c.provider, len(p.Dimensions), c.ndims)
		}
	}
	if GetKnownPlan("does-not-exist") != nil {
		t.Fatal("unknown provider must return nil")
	}
}

// TestLimitUnknownSentinel pins the "unknown limit" sentinel to JS
// Number.EPSILON so float-parity comparisons against the TS source hold.
func TestLimitUnknownSentinel(t *testing.T) {
	glm := GetKnownPlan("glm")
	if glm.Dimensions[0].Limit != LimitUnknown {
		t.Fatalf("glm limit = %v, want LimitUnknown %v", glm.Dimensions[0].Limit, LimitUnknown)
	}
	// 2.220446049250313e-16 is the IEEE-754 double for 2^-52 (Number.EPSILON).
	if LimitUnknown != 2.220446049250313e-16 {
		t.Fatalf("LimitUnknown = %v, want 2.220446049250313e-16", LimitUnknown)
	}
}

// TestKnownProvidersOrder verifies insertion order matches TS Object.keys().
func TestKnownProvidersOrder(t *testing.T) {
	want := []string{"codex", "claude", "glm", "minimax", "deepseek", "bailian",
		"kimi", "kimi-coding", "xiaomi-mimo", "alibaba", "grok-cli"}
	got := KnownProviders()
	if len(got) != len(want) {
		t.Fatalf("len=%d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}

// TestGetKnownPlanDefensiveCopy ensures callers cannot mutate the shared catalog.
func TestGetKnownPlanDefensiveCopy(t *testing.T) {
	p := GetKnownPlan("codex")
	p.Dimensions[0].Limit = 999
	p.Provider = "hacked"
	fresh := GetKnownPlan("codex")
	if fresh.Provider != "codex" || fresh.Dimensions[0].Limit != 100 {
		t.Fatal("catalog was mutated through a returned copy")
	}
}

// TestResolvePlanCatalog verifies a known provider with no DB override resolves
// from the catalog with source=auto and null connectionId.
func TestResolvePlanCatalog(t *testing.T) {
	plan := ResolvePlan("conn-1", "claude", nil, nil)
	if plan.Source != SourceAuto {
		t.Fatalf("source=%q, want auto", plan.Source)
	}
	if plan.ConnectionID != "" {
		t.Fatalf("connectionId=%q, want empty (null)", plan.ConnectionID)
	}
	if plan.Provider != "claude" || len(plan.Dimensions) != 2 {
		t.Fatalf("unexpected catalog plan: %+v", plan)
	}
}

// TestResolvePlanEmptyManual verifies an unknown provider yields an empty plan
// with source=manual (manual configuration required).
func TestResolvePlanEmptyManual(t *testing.T) {
	plan := ResolvePlan("conn-1", "brand-new-provider", nil, nil)
	if plan.Source != SourceManual {
		t.Fatalf("source=%q, want manual", plan.Source)
	}
	if len(plan.Dimensions) != 0 {
		t.Fatalf("dimensions=%d, want 0", len(plan.Dimensions))
	}
	if plan.Provider != "brand-new-provider" {
		t.Fatalf("provider=%q not preserved", plan.Provider)
	}
}

// TestResolvePlanDBOverrideWins verifies a DB override with dimensions beats the
// catalog, and that an EMPTY-dimensions override falls through to the catalog.
func TestResolvePlanDBOverrideWins(t *testing.T) {
	override := &ProviderPlan{
		ConnectionID: "conn-9",
		Provider:     "claude",
		Dimensions:   []QuotaDimension{{Unit: UnitTokens, Window: Window5H, Limit: 5000}},
		Source:       SourceManual,
	}
	lookup := func(id string) (*ProviderPlan, bool) {
		if id == "conn-9" {
			return override, true
		}
		return nil, false
	}
	// Override present → wins.
	plan := ResolvePlan("conn-9", "claude", lookup, nil)
	if plan.Source != SourceManual || len(plan.Dimensions) != 1 || plan.Dimensions[0].Limit != 5000 {
		t.Fatalf("DB override should win: %+v", plan)
	}
	// Override with empty dimensions → falls through to catalog.
	emptyLookup := func(string) (*ProviderPlan, bool) {
		return &ProviderPlan{Provider: "claude", Dimensions: nil}, true
	}
	plan2 := ResolvePlan("conn-9", "claude", emptyLookup, nil)
	if plan2.Source != SourceAuto || len(plan2.Dimensions) != 2 {
		t.Fatalf("empty override should fall through to catalog: %+v", plan2)
	}
}

// TestResolvePlanFailOpen verifies a panicking lookup (DB unavailable) fails
// open to the catalog rather than crashing — TS try/catch parity.
func TestResolvePlanFailOpen(t *testing.T) {
	panicky := func(string) (*ProviderPlan, bool) {
		panic("db connection refused")
	}
	plan := ResolvePlan("conn-1", "kimi", panicky, nil)
	if plan.Source != SourceAuto || plan.Provider != "kimi" {
		t.Fatalf("panicking lookup should fail open to catalog: %+v", plan)
	}
}
