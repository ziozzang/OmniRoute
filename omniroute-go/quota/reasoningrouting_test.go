package quota

import (
	"testing"
)

// ---------------------------------------------------------------------------
// Effort parsing
// ---------------------------------------------------------------------------

func TestParseEffort(t *testing.T) {
	cases := []struct {
		in   any
		want *ReasoningEffort
	}{
		{"high", ptr(EffortHigh)},
		{"HIGH", ptr(EffortHigh)},
		{" low ", ptr(EffortLow)},
		{"none", ptr(EffortNone)},
		{"ultra", ptr(EffortUltra)},
		{"bogus", nil},
		{123, nil},
		{nil, nil},
	}
	for _, c := range cases {
		got := parseEffort(c.in)
		if !effortPtrEqual(got, c.want) {
			t.Errorf("parseEffort(%v) = %v, want %v", c.in, effortStr(got), effortStr(c.want))
		}
	}
}

func TestThinkingLevelEffort(t *testing.T) {
	if e := thinkingLevelEffort(false); e == nil || *e != EffortNone {
		t.Errorf("false → none, got %v", effortStr(e))
	}
	if e := thinkingLevelEffort("disabled"); e == nil || *e != EffortNone {
		t.Errorf("disabled → none, got %v", effortStr(e))
	}
	if e := thinkingLevelEffort("high"); e == nil || *e != EffortHigh {
		t.Errorf("high → high, got %v", effortStr(e))
	}
	if e := thinkingLevelEffort(42); e != nil {
		t.Errorf("42 → nil, got %v", effortStr(e))
	}
}

// ---------------------------------------------------------------------------
// Model suffix splitting
// ---------------------------------------------------------------------------

func TestSplitClaudeEffortSuffix(t *testing.T) {
	b, e := splitClaudeEffortSuffix("claude-opus-4-8-high")
	if b != "claude-opus-4-8" || e == nil || *e != EffortHigh {
		t.Fatalf("got base=%q effort=%v", b, effortStr(e))
	}
	b, e = splitClaudeEffortSuffix("claude-sonnet-4")
	if b != "claude-sonnet-4" || e != nil {
		t.Fatalf("no suffix: got base=%q effort=%v", b, effortStr(e))
	}
	// xhigh must not collide with high
	b, e = splitClaudeEffortSuffix("claude-opus-xhigh")
	if b != "claude-opus" || e == nil || *e != EffortXHigh {
		t.Fatalf("xhigh: got base=%q effort=%v", b, effortStr(e))
	}
}

func TestSplitCodexEffortSuffix(t *testing.T) {
	b, e, ok := splitCodexEffortSuffix("gpt-5.6-sol-high")
	if !ok || b != "gpt-5.6-sol" || e == nil || *e != EffortHigh {
		t.Fatalf("got base=%q effort=%v ok=%v", b, effortStr(e), ok)
	}
	// max only for sol/terra/luna
	_, _, ok = splitCodexEffortSuffix("gpt-5.6-sol-max")
	if !ok {
		t.Fatal("gpt-5.6-sol-max should be valid")
	}
	_, _, ok = splitCodexEffortSuffix("gpt-4o-max")
	if ok {
		t.Fatal("gpt-4o-max should NOT be valid (max only for 5.6 sol/terra/luna)")
	}
	// ultra only for sol/terra
	_, _, ok = splitCodexEffortSuffix("gpt-5.6-luna-ultra")
	if ok {
		t.Fatal("luna-ultra should NOT be valid")
	}
	// codex/ prefix stripped
	b, e, ok = splitCodexEffortSuffix("codex/gpt-5.6-terra-low")
	if !ok || b != "codex/gpt-5.6-terra" || e == nil || *e != EffortLow {
		t.Fatalf("codex/ prefix: got base=%q effort=%v ok=%v", b, effortStr(e), ok)
	}
}

func TestSplitGenericEffortSuffix(t *testing.T) {
	b, e := splitGenericEffortSuffix("claude-opus-4-high")
	if b != "claude-opus-4" || e == nil || *e != EffortHigh {
		t.Fatalf("claude: got %q %v", b, effortStr(e))
	}
	b, e = splitGenericEffortSuffix("gpt-5.6-sol-medium")
	if b != "gpt-5.6-sol" || e == nil || *e != EffortMedium {
		t.Fatalf("codex: got %q %v", b, effortStr(e))
	}
	b, e = splitGenericEffortSuffix("plain-model")
	if b != "plain-model" || e != nil {
		t.Fatalf("plain: got %q %v", b, effortStr(e))
	}
}

// ---------------------------------------------------------------------------
// Intent extraction
// ---------------------------------------------------------------------------

func TestExtractReasoningIntent(t *testing.T) {
	// Explicit effort in body
	intent := ExtractReasoningIntent("gpt-4o", map[string]any{"reasoning_effort": "high"})
	if intent.Effort == nil || *intent.Effort != EffortHigh {
		t.Fatalf("explicit effort: got %v", effortStr(intent.Effort))
	}
	if intent.SourceEffort != "high" {
		t.Fatalf("sourceEffort = %q, want high", intent.SourceEffort)
	}
	if !intent.HasReasoningSignal {
		t.Fatal("should have reasoning signal")
	}

	// Suffix effort
	intent = ExtractReasoningIntent("claude-opus-4-high", map[string]any{})
	if intent.Model != "claude-opus-4" || intent.Effort == nil || *intent.Effort != EffortHigh {
		t.Fatalf("suffix: model=%q effort=%v", intent.Model, effortStr(intent.Effort))
	}

	// Signal without explicit effort (thinking key present)
	intent = ExtractReasoningIntent("gpt-4o", map[string]any{"thinking": map[string]any{"type": "enabled"}})
	if intent.Effort != nil {
		t.Fatalf("signal-only should have nil effort, got %v", effortStr(intent.Effort))
	}
	if intent.SourceEffort != "signal" {
		t.Fatalf("sourceEffort = %q, want signal", intent.SourceEffort)
	}

	// Missing
	intent = ExtractReasoningIntent("gpt-4o", map[string]any{})
	if intent.SourceEffort != "missing" {
		t.Fatalf("sourceEffort = %q, want missing", intent.SourceEffort)
	}

	// Thinking budget detection
	intent = ExtractReasoningIntent("gpt-4o", map[string]any{
		"thinking": map[string]any{"budget_tokens": 1000.0},
	})
	if !intent.HasThinkingBudget {
		t.Fatal("should detect thinking budget")
	}
}

// ---------------------------------------------------------------------------
// Glob matching
// ---------------------------------------------------------------------------

func TestGlobMatches(t *testing.T) {
	cases := []struct {
		pattern, value string
		want           bool
	}{
		{"gpt-*", "gpt-4o", true},
		{"gpt-*", "claude-3", false},
		{"*-high", "claude-opus-high", true},
		{"gpt-4?", "gpt-4o", true},
		{"gpt-4?", "gpt-4oo", false},
		{"exact", "exact", true},
		{"exact", "EXACT", true}, // case-insensitive
		{"a.b", "a.b", true},
		{"a.b", "axb", false}, // dot is literal
	}
	for _, c := range cases {
		if got := globMatches(c.pattern, c.value); got != c.want {
			t.Errorf("globMatches(%q, %q) = %v, want %v", c.pattern, c.value, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Tag normalization
// ---------------------------------------------------------------------------

func TestNormalizeRoutingTags(t *testing.T) {
	got := NormalizeRoutingTags([]any{"  Foo ", "bar", "foo", "BAR"})
	if len(got) != 2 || got[0] != "foo" || got[1] != "bar" {
		t.Fatalf("got %v, want [foo bar]", got)
	}
	got = NormalizeRoutingTags("a, b, a")
	if len(got) != 2 {
		t.Fatalf("string split: got %v", got)
	}
	got = NormalizeRoutingTags(nil)
	if len(got) != 0 {
		t.Fatalf("nil → empty, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// Rule matching + sorting
// ---------------------------------------------------------------------------

func strPtr(s string) *string { return &s }

func TestScopeMatches(t *testing.T) {
	apiKeyRule := ReasoningRoutingRule{Scope: ScopeAPIKey, APIKeyID: strPtr("k1"), SourceEffort: "any"}
	input := RoutingInput{SourceModel: "gpt-4o", APIKeyID: strPtr("k1"), SourceEffort: "missing"}
	if !scopeMatches(apiKeyRule, input) {
		t.Fatal("apiKey rule should match matching key")
	}
	input.APIKeyID = strPtr("k2")
	if scopeMatches(apiKeyRule, input) {
		t.Fatal("apiKey rule should NOT match different key")
	}

	// connection scope excluded when not connectionOnly
	connRule := ReasoningRoutingRule{Scope: ScopeConnection, ConnectionID: strPtr("c1"), SourceEffort: "any"}
	input2 := RoutingInput{SourceModel: "m", ConnectionID: strPtr("c1"), SourceEffort: "missing"}
	if scopeMatches(connRule, input2) {
		t.Fatal("connection rule should be excluded when connectionOnly=false")
	}
	input2.ConnectionOnly = true
	if !scopeMatches(connRule, input2) {
		t.Fatal("connection rule should match when connectionOnly=true")
	}
}

func TestRuleMatches(t *testing.T) {
	pat := "gpt-*"
	rule := ReasoningRoutingRule{
		Scope:        ScopeModel,
		ModelPattern: &pat,
		SourceEffort: "any",
		RequestTags:  []string{"prod"},
		TagMatchMode: TagAny,
	}
	input := RoutingInput{SourceModel: "gpt-4o", SourceEffort: "high", RequestTags: []string{"prod"}}
	if !ruleMatches(rule, input) {
		t.Fatal("should match")
	}
	input.RequestTags = []string{"dev"}
	if ruleMatches(rule, input) {
		t.Fatal("should NOT match (tag mismatch)")
	}
}

func TestSortRules(t *testing.T) {
	pat := "*"
	rules := []ReasoningRoutingRule{
		{ID: "global", Scope: ScopeGlobal, ModelPattern: &pat, SourceEffort: "any", Priority: 0},
		{ID: "apikey", Scope: ScopeAPIKey, ModelPattern: &pat, SourceEffort: "any", Priority: 0},
		{ID: "model", Scope: ScopeModel, ModelPattern: &pat, SourceEffort: "any", Priority: 10},
	}
	input := RoutingInput{SourceModel: "m", SourceEffort: "missing"}
	sortRules(rules, input)
	// apiKey (rank 4) should be first
	if rules[0].ID != "apikey" {
		t.Fatalf("first should be apiKey, got %q", rules[0].ID)
	}
}

// ---------------------------------------------------------------------------
// Target resolution
// ---------------------------------------------------------------------------

func TestResolveTargetEffort(t *testing.T) {
	// inherit + signal → nil
	rule := ReasoningRoutingRule{EffortMode: EffortInherit}
	input := RoutingInput{SourceEffort: "signal", HasReasoningSignal: true}
	if e := resolveTargetEffort(rule, input); e != nil {
		t.Fatalf("inherit+signal → nil, got %v", effortStr(e))
	}
	// inherit + explicit effort → that effort
	input.SourceEffort = "high"
	if e := resolveTargetEffort(rule, input); e == nil || *e != EffortHigh {
		t.Fatalf("inherit+high → high, got %v", effortStr(e))
	}
	// force → rule.TargetEffort
	te := EffortLow
	rule = ReasoningRoutingRule{EffortMode: EffortForce, TargetEffort: &te}
	if e := resolveTargetEffort(rule, input); e == nil || *e != EffortLow {
		t.Fatalf("force → low, got %v", effortStr(e))
	}
	// default + hasSignal → inherit
	rule = ReasoningRoutingRule{EffortMode: EffortDefault}
	input = RoutingInput{SourceEffort: "medium", HasReasoningSignal: true}
	if e := resolveTargetEffort(rule, input); e == nil || *e != EffortMedium {
		t.Fatalf("default+signal → medium, got %v", effortStr(e))
	}
}

// ---------------------------------------------------------------------------
// Main decision
// ---------------------------------------------------------------------------

func TestResolveReasoningRoutingRule(t *testing.T) {
	pat := "gpt-*"
	te := EffortHigh
	rules := []ReasoningRoutingRule{
		{
			ID: "r1", Name: "test", Scope: ScopeModel, ModelPattern: &pat,
			SourceEffort: "any", EffortMode: EffortForce, TargetEffort: &te,
			TargetKind: TargetKeep, BudgetAction: BudgetPreserve, Enabled: true,
		},
	}
	resolveCap := func(string) CapabilityInfo { s := true; return CapabilityInfo{SupportsThinking: &s} }

	input := RoutingInput{SourceModel: "gpt-4o", SourceEffort: "missing"}
	dec, err := ResolveReasoningRoutingRule(input, rules, nil, resolveCap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec == nil {
		t.Fatal("expected a decision")
	}
	if dec.TargetEffort == nil || *dec.TargetEffort != EffortHigh {
		t.Fatalf("targetEffort = %v, want high", effortStr(dec.TargetEffort))
	}
	if dec.TargetModel != "gpt-4o" {
		t.Fatalf("targetModel = %q, want gpt-4o (keep)", dec.TargetModel)
	}
	if dec.Capability != CapSupported {
		t.Fatalf("capability = %q, want supported", dec.Capability)
	}
}

func TestResolveReasoningRoutingRuleNoMatch(t *testing.T) {
	pat := "claude-*"
	rules := []ReasoningRoutingRule{
		{ID: "r1", Scope: ScopeModel, ModelPattern: &pat, SourceEffort: "any", Enabled: true},
	}
	input := RoutingInput{SourceModel: "gpt-4o", SourceEffort: "missing"}
	dec, err := ResolveReasoningRoutingRule(input, rules, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec != nil {
		t.Fatal("expected nil decision for non-matching rule")
	}
}

func TestResolveReasoningRoutingRuleComboTarget(t *testing.T) {
	te := EffortHigh
	comboID := "combo1"
	rules := []ReasoningRoutingRule{
		{
			ID: "r1", Scope: ScopeGlobal, SourceEffort: "any",
			EffortMode: EffortForce, TargetEffort: &te,
			TargetKind: TargetCombo, TargetComboID: &comboID,
			BudgetAction: BudgetPreserve, Enabled: true,
		},
	}
	resolveCombo := func(id string) (map[string]any, error) {
		return map[string]any{"name": "my-combo", "models": []any{"gpt-4o", "claude-3"}}, nil
	}
	resolveCap := func(string) CapabilityInfo { s := true; return CapabilityInfo{SupportsThinking: &s} }

	input := RoutingInput{SourceModel: "gpt-4o", SourceEffort: "missing"}
	dec, err := ResolveReasoningRoutingRule(input, rules, resolveCombo, resolveCap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.TargetModel != "my-combo" {
		t.Fatalf("targetModel = %q, want my-combo", dec.TargetModel)
	}
	if dec.TargetCombo == nil {
		t.Fatal("targetCombo should be set")
	}
}

// ---------------------------------------------------------------------------
// Body mutation
// ---------------------------------------------------------------------------

func TestAttachAndApplyDirective(t *testing.T) {
	te := EffortHigh
	dec := &ReasoningRuleDecision{
		Rule: ReasoningRoutingRule{
			ID: "r1", Name: "test", Scope: ScopeModel,
			EffortMode: EffortForce, BudgetAction: BudgetPreserve,
		},
		SourceModel:  "gpt-4o",
		TargetModel:  "gpt-4o-high",
		SourceEffort: "missing",
		TargetEffort: &te,
		Capability:   CapSupported,
	}

	body := map[string]any{"model": "gpt-4o", "messages": []any{}}
	stamped := AttachReasoningRuleDirective(body, dec)

	if stamped["model"] != "gpt-4o-high" {
		t.Fatalf("model should be rewritten, got %v", stamped["model"])
	}
	if _, ok := stamped["_omnirouteReasoningRule"]; !ok {
		t.Fatal("directive should be stamped")
	}

	// Apply
	applied := ApplyReasoningRuleDirective(stamped).(map[string]any)
	if applied["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %v, want high", applied["reasoning_effort"])
	}
	if _, ok := applied["_omnirouteReasoningRule"]; ok {
		t.Fatal("directive should be removed after apply")
	}
}

func TestApplyDirectiveForceNone(t *testing.T) {
	te := EffortNone
	dec := &ReasoningRuleDecision{
		Rule:         ReasoningRoutingRule{ID: "r1", Scope: ScopeModel, EffortMode: EffortForce, BudgetAction: BudgetPreserve},
		SourceModel:  "gpt-4o",
		TargetModel:  "gpt-4o",
		TargetEffort: &te,
		Capability:   CapSupported,
	}
	body := map[string]any{"model": "gpt-4o", "reasoning_effort": "high", "thinking": map[string]any{"type": "enabled"}}
	stamped := AttachReasoningRuleDirective(body, dec)
	applied := ApplyReasoningRuleDirective(stamped).(map[string]any)
	if _, ok := applied["reasoning_effort"]; ok {
		t.Fatal("force none should clear reasoning_effort")
	}
	if _, ok := applied["thinking"]; ok {
		t.Fatal("force none should clear thinking")
	}
}

func TestApplyDirectiveBudgetSet(t *testing.T) {
	te := EffortHigh
	bt := 5000
	dec := &ReasoningRuleDecision{
		Rule:         ReasoningRoutingRule{ID: "r1", Scope: ScopeModel, EffortMode: EffortForce, BudgetAction: BudgetSet, BudgetTokens: &bt},
		SourceModel:  "gpt-4o",
		TargetModel:  "gpt-4o",
		TargetEffort: &te,
		Capability:   CapSupported,
	}
	body := map[string]any{"model": "gpt-4o"}
	stamped := AttachReasoningRuleDirective(body, dec)
	applied := ApplyReasoningRuleDirective(stamped).(map[string]any)
	thinking, ok := applied["thinking"].(map[string]any)
	if !ok {
		t.Fatal("thinking should be set")
	}
	if thinking["budget_tokens"] != 5000 {
		t.Fatalf("budget_tokens = %v, want 5000", thinking["budget_tokens"])
	}
}

// ---------------------------------------------------------------------------
// Combo filtering
// ---------------------------------------------------------------------------

func TestFilterComboForReasoningDecision(t *testing.T) {
	te := EffortHigh
	dec := &ReasoningRuleDecision{
		TargetEffort:      &te,
		RequiresReasoning: true,
	}
	resolveCap := func(model string) CapabilityInfo {
		if model == "no-think" {
			f := false
			return CapabilityInfo{SupportsThinking: &f}
		}
		s := true
		return CapabilityInfo{SupportsThinking: &s}
	}
	combo := map[string]any{"models": []any{"gpt-4o", "no-think", "claude-3"}}
	filtered, removed := FilterComboForReasoningDecision(combo, dec, resolveCap)
	if filtered == nil {
		t.Fatal("combo should not be nil")
	}
	models := filtered["models"].([]any)
	if len(models) != 2 {
		t.Fatalf("should keep 2 models, got %d", len(models))
	}
	if len(removed) != 1 || removed[0] != "no-think" {
		t.Fatalf("removed = %v, want [no-think]", removed)
	}
}

func TestFilterComboAllUnsupported(t *testing.T) {
	te := EffortHigh
	dec := &ReasoningRuleDecision{TargetEffort: &te, RequiresReasoning: true}
	resolveCap := func(string) CapabilityInfo { f := false; return CapabilityInfo{SupportsThinking: &f} }
	combo := map[string]any{"models": []any{"no-think"}}
	filtered, removed := FilterComboForReasoningDecision(combo, dec, resolveCap)
	if filtered != nil {
		t.Fatal("all unsupported → nil combo")
	}
	if len(removed) != 1 {
		t.Fatalf("removed = %v", removed)
	}
}

// ---------------------------------------------------------------------------
// Codex helpers
// ---------------------------------------------------------------------------

func TestIsCodexTarget(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"gpt-4o", true},
		{"codex/gpt-5.6-sol", true},
		{"cx/gpt-5.6-terra", true},
		{"anthropic/claude-3", false},
		{"openai/gpt-4o", false},
	}
	for _, c := range cases {
		if got := IsCodexTarget(c.model); got != c.want {
			t.Errorf("IsCodexTarget(%q) = %v, want %v", c.model, got, c.want)
		}
	}
}

func TestValidateCodexWsDecision(t *testing.T) {
	// Non-codex model → error
	dec := &ReasoningRuleDecision{TargetModel: "anthropic/claude-3"}
	if msg := ValidateCodexWsDecision(dec); msg == "" {
		t.Fatal("non-codex model should fail validation")
	}
	// Codex model → ok
	dec = &ReasoningRuleDecision{TargetModel: "gpt-5.6-sol"}
	if msg := ValidateCodexWsDecision(dec); msg != "" {
		t.Fatalf("codex model should pass, got %q", msg)
	}
	// Combo → always error (WS can't execute combos)
	dec = &ReasoningRuleDecision{TargetModel: "gpt-5.6-sol", TargetCombo: map[string]any{"models": []any{"gpt-5.6-sol"}}}
	if msg := ValidateCodexWsDecision(dec); msg == "" {
		t.Fatal("combo target should fail WS validation")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func ptr(e ReasoningEffort) *ReasoningEffort { return &e }

func effortPtrEqual(a, b *ReasoningEffort) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func effortStr(e *ReasoningEffort) string {
	if e == nil {
		return "<nil>"
	}
	return string(*e)
}
