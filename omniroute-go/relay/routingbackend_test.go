package relay

import "testing"

func TestGetBifrostRoutingConfig(t *testing.T) {
	env := map[string]string{"BIFROST_BASE_URL": "http://localhost:8080/"}
	cfg := GetBifrostRoutingConfig(env, "")
	if cfg == nil || cfg.BaseURL != "http://localhost:8080" {
		t.Fatalf("trailing slash stripped, got %+v", cfg)
	}
	if cfg.TimeoutMs != 30000 {
		t.Fatalf("default timeout, got %d", cfg.TimeoutMs)
	}
	if !cfg.Enabled || !cfg.StreamingEnabled {
		t.Fatal("default enabled")
	}
	// no URL → nil
	if GetBifrostRoutingConfig(map[string]string{}, "") != nil {
		t.Fatal("no URL → nil")
	}
	// supervisor fallback
	cfg2 := GetBifrostRoutingConfig(map[string]string{}, "http://127.0.0.1:9000")
	if cfg2 == nil || cfg2.BaseURL != "http://127.0.0.1:9000" {
		t.Fatalf("supervisor fallback, got %+v", cfg2)
	}
}

func TestResolveRelayRoutingBackend(t *testing.T) {
	if ResolveRelayRoutingBackend(map[string]string{"OMNIROUTE_RELAY_BACKEND": "bifrost"}, nil) != BackendBifrost {
		t.Fatal("explicit bifrost")
	}
	if ResolveRelayRoutingBackend(map[string]string{}, nil) != BackendTS {
		t.Fatal("default ts")
	}
	cfg := &BifrostRoutingConfig{Enabled: true}
	if ResolveRelayRoutingBackend(map[string]string{}, cfg) != BackendAuto {
		t.Fatal("enabled config → auto")
	}
}

func TestShouldTryBifrostForRequest(t *testing.T) {
	cfg := &BifrostRoutingConfig{Enabled: true}
	lookup := func(model string) *SidecarEligibility {
		if model == "gpt-4o" {
			return &SidecarEligibility{Eligible: true}
		}
		return &SidecarEligibility{Eligible: false}
	}
	// bifrost backend → always try
	d := ShouldTryBifrostForRequest(BackendBifrost, cfg, "anything", lookup)
	if !d.TryBifrost {
		t.Fatal("bifrost backend → try")
	}
	// auto + eligible
	d = ShouldTryBifrostForRequest(BackendAuto, cfg, "gpt-4o", lookup)
	if !d.TryBifrost {
		t.Fatal("auto + eligible → try")
	}
	// auto + ineligible
	d = ShouldTryBifrostForRequest(BackendAuto, cfg, "claude", lookup)
	if d.TryBifrost || d.FallbackReason != string(FallbackBifrostIneligible) {
		t.Fatalf("auto + ineligible, got %+v", d)
	}
	// ts backend → never
	d = ShouldTryBifrostForRequest(BackendTS, cfg, "gpt-4o", lookup)
	if d.TryBifrost {
		t.Fatal("ts → never try")
	}
}

func TestGetRoutingFallbackReasonHeader(t *testing.T) {
	if GetRoutingFallbackReasonHeader("bifrost-cooldown; remaining=1234") != FallbackBifrostCooldown {
		t.Fatal("strip suffix")
	}
	if GetRoutingFallbackReasonHeader("bifrost-error") != FallbackBifrostError {
		t.Fatal("exact code")
	}
	if GetRoutingFallbackReasonHeader("unknown-reason") != "" {
		t.Fatal("unknown → empty")
	}
	if GetRoutingFallbackReasonHeader("") != "" {
		t.Fatal("empty → empty")
	}
}

func TestGetRoutingFallbackHeader(t *testing.T) {
	cfg := &BifrostRoutingConfig{Enabled: true}
	if GetRoutingFallbackHeader(BackendAuto, cfg) != "bifrost" {
		t.Fatal("auto+enabled → bifrost")
	}
	if GetRoutingFallbackHeader(BackendTS, cfg) != "" {
		t.Fatal("ts → empty")
	}
}
