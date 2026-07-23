package domain

import (
	"errors"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// PolicyEngine
// ---------------------------------------------------------------------------

func TestPolicyEngineRouting(t *testing.T) {
	e := NewPolicyEngine()
	e.AddPolicy(Policy{
		ID: "p1", Name: "prefer-openai", Type: "routing", Enabled: true, Priority: 0,
		Conditions: &PolicyConditions{ModelPattern: "gpt-*"},
		Actions:    &PolicyActions{PreferProvider: []string{"openai", "azure"}},
	})
	res := e.Evaluate("gpt-4o")
	if !res.Allowed {
		t.Fatal("should be allowed")
	}
	if len(res.PreferredProviders) != 2 {
		t.Fatalf("preferredProviders = %v", res.PreferredProviders)
	}
	if len(res.AppliedPolicies) != 1 {
		t.Fatalf("appliedPolicies = %v", res.AppliedPolicies)
	}
}

func TestPolicyEngineAccessBlock(t *testing.T) {
	e := NewPolicyEngine()
	e.AddPolicy(Policy{
		ID: "p1", Name: "block-claude", Type: "access", Enabled: true, Priority: 0,
		Actions: &PolicyActions{BlockModel: []string{"claude-*"}},
	})
	res := e.Evaluate("claude-3-opus")
	if res.Allowed {
		t.Fatal("should be blocked")
	}
	if res.Reason == "" {
		t.Fatal("should have reason")
	}
	// Non-matching model passes
	res = e.Evaluate("gpt-4o")
	if !res.Allowed {
		t.Fatal("gpt-4o should pass")
	}
}

func TestPolicyEngineBudget(t *testing.T) {
	e := NewPolicyEngine()
	maxTok := 4096
	e.AddPolicy(Policy{
		ID: "p1", Name: "limit-tokens", Type: "budget", Enabled: true, Priority: 0,
		Actions: &PolicyActions{MaxTokens: &maxTok},
	})
	res := e.Evaluate("gpt-4o")
	if res.MaxTokens == nil || *res.MaxTokens != 4096 {
		t.Fatalf("maxTokens = %v", res.MaxTokens)
	}
}

func TestPolicyEnginePriorityOrder(t *testing.T) {
	e := NewPolicyEngine()
	e.AddPolicy(Policy{ID: "p2", Name: "second", Type: "routing", Enabled: true, Priority: 10,
		Actions: &PolicyActions{PreferProvider: []string{"b"}}})
	e.AddPolicy(Policy{ID: "p1", Name: "first", Type: "routing", Enabled: true, Priority: 1,
		Actions: &PolicyActions{PreferProvider: []string{"a"}}})
	res := e.Evaluate("any")
	// Priority 1 applied first
	if len(res.AppliedPolicies) != 2 || res.AppliedPolicies[0] != "first" {
		t.Fatalf("order = %v", res.AppliedPolicies)
	}
}

func TestPolicyEngineDisabled(t *testing.T) {
	e := NewPolicyEngine()
	e.AddPolicy(Policy{ID: "p1", Name: "disabled", Type: "access", Enabled: false,
		Actions: &PolicyActions{BlockModel: []string{"*"}}})
	res := e.Evaluate("anything")
	if !res.Allowed {
		t.Fatal("disabled policy should not block")
	}
}

func TestPolicyEngineRemove(t *testing.T) {
	e := NewPolicyEngine()
	e.AddPolicy(Policy{ID: "p1", Name: "test", Type: "routing", Enabled: true})
	e.RemovePolicy("p1")
	if len(e.GetPolicies()) != 0 {
		t.Fatal("should be empty after remove")
	}
}

func TestPolicyEngineConcurrent(t *testing.T) {
	e := NewPolicyEngine()
	e.AddPolicy(Policy{ID: "p1", Name: "test", Type: "routing", Enabled: true,
		Actions: &PolicyActions{PreferProvider: []string{"a"}}})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.Evaluate("gpt-4o")
			e.GetPolicies()
		}()
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// Degradation
// ---------------------------------------------------------------------------

func TestWithDegradationFull(t *testing.T) {
	ResetDegradationRegistry()
	res := WithDegradationSync("test",
		func() (string, error) { return "primary", nil },
		func() (string, error) { return "fallback", nil },
		"default",
		nil,
	)
	if res.Result != "primary" || res.Status.Level != DegradationFull {
		t.Fatalf("got %+v", res)
	}
}

func TestWithDegradationReduced(t *testing.T) {
	ResetDegradationRegistry()
	res := WithDegradationSync("test",
		func() (string, error) { return "", errors.New("primary down") },
		func() (string, error) { return "fallback", nil },
		"default",
		nil,
	)
	if res.Result != "fallback" || res.Status.Level != DegradationReduced {
		t.Fatalf("got %+v", res)
	}
	if res.Status.Reason != "primary down" {
		t.Fatalf("reason = %q", res.Status.Reason)
	}
}

func TestWithDegradationDefault(t *testing.T) {
	ResetDegradationRegistry()
	res := WithDegradationSync("test",
		func() (string, error) { return "", errors.New("primary down") },
		func() (string, error) { return "", errors.New("fallback down") },
		"default",
		nil,
	)
	if res.Result != "default" || res.Status.Level != DegradationDefault {
		t.Fatalf("got %+v", res)
	}
	if res.Status.Reason != "primary down → fallback down" {
		t.Fatalf("reason = %q", res.Status.Reason)
	}
}

func TestDegradationRegistry(t *testing.T) {
	ResetDegradationRegistry()
	WithDegradationSync("svc1",
		func() (int, error) { return 1, nil },
		func() (int, error) { return 0, nil },
		0, nil,
	)
	WithDegradationSync("svc2",
		func() (int, error) { return 0, errors.New("fail") },
		func() (int, error) { return 2, nil },
		0, nil,
	)

	if !HasAnyDegradation() {
		t.Fatal("svc2 is reduced → should have degradation")
	}
	summary := GetDegradationSummary()
	if summary[DegradationFull] != 1 || summary[DegradationReduced] != 1 {
		t.Fatalf("summary = %v", summary)
	}
	report := GetDegradationReport()
	if len(report) != 2 {
		t.Fatalf("report len = %d", len(report))
	}
	// Sorted by severity: reduced (0) before full (3)
	if report[0].Level != DegradationReduced {
		t.Fatalf("report[0] = %v, want reduced first", report[0].Level)
	}
}

func TestDegradationOnDegradeCallback(t *testing.T) {
	ResetDegradationRegistry()
	var called bool
	WithDegradationSync("test",
		func() (string, error) { return "", errors.New("fail") },
		func() (string, error) { return "ok", nil },
		"",
		&DegradationOptions{OnDegrade: func(s DegradationStatus) { called = true }},
	)
	if !called {
		t.Fatal("onDegrade should be called on degradation")
	}
}

func TestDegradationSincePreserved(t *testing.T) {
	ResetDegradationRegistry()
	WithDegradationSync("test",
		func() (string, error) { return "ok", nil },
		func() (string, error) { return "", nil },
		"", nil,
	)
	s1 := GetFeatureStatus("test")
	if s1 == nil {
		t.Fatal("status should exist")
	}
	// Same level again → since preserved
	WithDegradationSync("test",
		func() (string, error) { return "ok", nil },
		func() (string, error) { return "", nil },
		"", nil,
	)
	s2 := GetFeatureStatus("test")
	if s2.Since != s1.Since {
		t.Fatalf("since should be preserved: %q vs %q", s1.Since, s2.Since)
	}
}
