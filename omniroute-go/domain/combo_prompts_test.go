package domain

import (
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// ComboResolver
// ---------------------------------------------------------------------------

func TestResolveComboPriority(t *testing.T) {
	r := NewComboResolver()
	combo := Combo{Name: "c", Strategy: "priority", Models: []any{"gpt-4o", "claude-3"}}
	res, err := r.ResolveComboModel(combo, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Model != "gpt-4o" || res.Index != 0 {
		t.Fatalf("priority: got %+v", res)
	}
}

func TestResolveComboRoundRobin(t *testing.T) {
	r := NewComboResolver()
	combo := Combo{ID: "c1", Strategy: "round-robin", Models: []any{"a", "b", "c"}}
	var got []string
	for i := 0; i < 6; i++ {
		res, _ := r.ResolveComboModel(combo, nil)
		got = append(got, res.Model)
	}
	want := []string{"a", "b", "c", "a", "b", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("round-robin[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResolveComboRandom(t *testing.T) {
	r := NewComboResolver()
	// Deterministic RNG: always returns 0.0 → first model
	r.SetRNG(func() float64 { return 0.0 })
	combo := Combo{Name: "c", Strategy: "random", Models: []any{
		map[string]any{"model": "a", "weight": 1.0},
		map[string]any{"model": "b", "weight": 2.0},
	}}
	res, _ := r.ResolveComboModel(combo, nil)
	if res.Model != "a" {
		t.Fatalf("random(0.0) = %q, want a", res.Model)
	}
	// RNG returns 0.99 → should land on b (weight 2/3 of total)
	r.SetRNG(func() float64 { return 0.99 })
	res, _ = r.ResolveComboModel(combo, nil)
	if res.Model != "b" {
		t.Fatalf("random(0.99) = %q, want b", res.Model)
	}
}

func TestResolveComboLeastUsed(t *testing.T) {
	r := NewComboResolver()
	combo := Combo{Name: "c", Strategy: "least-used", Models: []any{"a", "b", "c"}}
	usage := map[string]int{"a": 10, "b": 2, "c": 5}
	res, _ := r.ResolveComboModel(combo, usage)
	if res.Model != "b" {
		t.Fatalf("least-used = %q, want b", res.Model)
	}
}

func TestResolveComboNoModels(t *testing.T) {
	r := NewComboResolver()
	combo := Combo{Name: "empty", Strategy: "priority", Models: nil}
	_, err := r.ResolveComboModel(combo, nil)
	if err == nil {
		t.Fatal("expected error for empty combo")
	}
}

func TestGetComboFallbacks(t *testing.T) {
	combo := Combo{Models: []any{"a", "b", "c", "d"}}
	got := GetComboFallbacks(combo, 1)
	want := []string{"c", "d", "a"}
	if len(got) != 3 || got[0] != "c" || got[1] != "d" || got[2] != "a" {
		t.Fatalf("fallbacks = %v, want %v", got, want)
	}
}

func TestComboResolverConcurrent(t *testing.T) {
	r := NewComboResolver()
	combo := Combo{ID: "c1", Strategy: "round-robin", Models: []any{"a", "b"}}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.ResolveComboModel(combo, nil)
		}()
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// Prompts
// ---------------------------------------------------------------------------

func TestInterpolate(t *testing.T) {
	got := Interpolate("Hello {name}, your task is {task}", map[string]string{
		"name": "Alice", "task": "coding",
	})
	if got != "Hello Alice, your task is coding" {
		t.Fatalf("got %q", got)
	}
	// Unknown variable preserved
	got = Interpolate("Hello {unknown}", map[string]string{})
	if got != "Hello {unknown}" {
		t.Fatalf("unknown var: got %q", got)
	}
}

func TestRenderPrompt(t *testing.T) {
	p, err := RenderPrompt(StagePlan, map[string]string{"original_request": "build a API"})
	if err != nil {
		t.Fatal(err)
	}
	if p.System == "" || p.User == "" {
		t.Fatal("empty prompt")
	}
	if !contains(p.User, "build a API") {
		t.Fatalf("user prompt missing request: %q", p.User)
	}
}

func TestRenderPromptUnknownStage(t *testing.T) {
	_, err := RenderPrompt(StageName("bogus"), nil)
	if err == nil {
		t.Fatal("expected error for unknown stage")
	}
}

func TestAllStagesExist(t *testing.T) {
	for _, stage := range []StageName{StagePlan, StageExecute, StageReflect, StageFix} {
		p, err := RenderPrompt(stage, map[string]string{
			"original_request": "test",
			"plan_context":     "plan",
			"execution_response": "output",
			"reflection_response": "feedback",
		})
		if err != nil {
			t.Fatalf("stage %s: %v", stage, err)
		}
		if p.System == "" || p.User == "" {
			t.Fatalf("stage %s: empty prompt", stage)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstr(s, sub))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
