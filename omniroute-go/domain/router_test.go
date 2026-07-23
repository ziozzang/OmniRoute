package domain

import "testing"

func TestNormalizeRoutingTags(t *testing.T) {
	got := NormalizeRoutingTags([]any{"  Foo ", "bar", "foo"})
	if len(got) != 2 || got[0] != "foo" || got[1] != "bar" {
		t.Fatalf("got %v", got)
	}
	got = NormalizeRoutingTags("a, b, a")
	if len(got) != 2 {
		t.Fatalf("string split: got %v", got)
	}
	if got := NormalizeRoutingTags(nil); len(got) != 0 {
		t.Fatalf("nil → empty, got %v", got)
	}
}

func TestNormalizeRoutingTagMatchMode(t *testing.T) {
	if NormalizeRoutingTagMatchMode("all") != TagMatchAll {
		t.Fatal("all → all")
	}
	if NormalizeRoutingTagMatchMode("ALL") != TagMatchAll {
		t.Fatal("ALL → all")
	}
	if NormalizeRoutingTagMatchMode("any") != TagMatchAny {
		t.Fatal("any → any")
	}
	if NormalizeRoutingTagMatchMode(nil) != TagMatchAny {
		t.Fatal("nil → any")
	}
}

func TestMatchesRoutingTags(t *testing.T) {
	conn := []string{"prod", "us"}
	if !MatchesRoutingTags(conn, nil, TagMatchAny) {
		t.Fatal("empty request → true")
	}
	if MatchesRoutingTags(nil, []string{"prod"}, TagMatchAny) {
		t.Fatal("empty conn → false")
	}
	if !MatchesRoutingTags(conn, []string{"prod"}, TagMatchAny) {
		t.Fatal("any: prod in conn")
	}
	if MatchesRoutingTags(conn, []string{"dev"}, TagMatchAny) {
		t.Fatal("any: dev not in conn")
	}
	if !MatchesRoutingTags(conn, []string{"prod", "us"}, TagMatchAll) {
		t.Fatal("all: both in conn")
	}
	if MatchesRoutingTags(conn, []string{"prod", "eu"}, TagMatchAll) {
		t.Fatal("all: eu not in conn")
	}
}

func TestResolveRequestRoutingTags(t *testing.T) {
	body := map[string]any{"metadata": map[string]any{"tags": []any{"A", "b"}, "tag_match_mode": "all"}}
	got := ResolveRequestRoutingTags(body)
	if len(got.Tags) != 2 || got.MatchMode != TagMatchAll {
		t.Fatalf("got %+v", got)
	}
	got = ResolveRequestRoutingTags(nil)
	if len(got.Tags) != 0 || got.MatchMode != TagMatchAny {
		t.Fatalf("nil body: got %+v", got)
	}
}

func TestWildcardMatch(t *testing.T) {
	cases := []struct{ model, pattern string; want bool }{
		{"gpt-4o", "*", true},
		{"gpt-4o", "gpt-4o", true},
		{"gpt-4o", "gpt-*", true},
		{"gpt-4o", "claude-*", false},
		{"gpt-4o", "GPT-4O", true}, // case-insensitive
		{"gpt-4o", "gpt-4?", true},
		{"gpt-4oo", "gpt-4?", false},
		{"", "*", false},
		{"gpt-4o", "", false},
	}
	for _, c := range cases {
		if got := WildcardMatch(c.model, c.pattern); got != c.want {
			t.Errorf("WildcardMatch(%q,%q)=%v want %v", c.model, c.pattern, got, c.want)
		}
	}
}

func TestIsModelExcludedByConnection(t *testing.T) {
	data := map[string]any{"excludedModels": []any{"gpt-*", "claude-3"}}
	if !IsModelExcludedByConnection("gpt-4o", data) {
		t.Fatal("gpt-4o should be excluded by gpt-*")
	}
	if IsModelExcludedByConnection("llama-3", data) {
		t.Fatal("llama-3 should NOT be excluded")
	}
	if IsModelExcludedByConnection("", data) {
		t.Fatal("empty model → false")
	}
	// namespaced model: raw model part also checked
	data2 := map[string]any{"excluded_models": "gpt-4o"}
	if !IsModelExcludedByConnection("openai/gpt-4o", data2) {
		t.Fatal("openai/gpt-4o should match excluded gpt-4o via raw candidate")
	}
}

func TestHasEligibleConnectionForModel(t *testing.T) {
	conns := []ConnectionLike{
		{ProviderSpecificData: map[string]any{"excludedModels": []any{"gpt-*"}}},
		{ProviderSpecificData: map[string]any{}},
	}
	if !HasEligibleConnectionForModel(conns, "gpt-4o") {
		t.Fatal("second conn has no exclusion → eligible")
	}
	conns2 := []ConnectionLike{
		{ProviderSpecificData: map[string]any{"excludedModels": []any{"gpt-*"}}},
	}
	if HasEligibleConnectionForModel(conns2, "gpt-4o") {
		t.Fatal("all conns exclude gpt-* → not eligible")
	}
	if HasEligibleConnectionForModel(nil, "gpt-4o") {
		t.Fatal("no conns → false")
	}
}

func TestGetModelMatchCandidates(t *testing.T) {
	c := getModelMatchCandidates("openai/gpt-4o[1m]")
	// should contain normalized, without [1m], and raw model
	found := map[string]bool{}
	for _, x := range c {
		found[x] = true
	}
	if !found["openai/gpt-4o[1m]"] || !found["openai/gpt-4o"] || !found["gpt-4o"] {
		t.Fatalf("candidates = %v", c)
	}
}
