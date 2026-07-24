package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"omniroute-go/api"
	"omniroute-go/quota"
	"omniroute-go/relay"
	"omniroute-go/sse"
)

// gateway.go — Single-binary admission gateway wiring all Go packages.
//
// This is the "native admission-control proxy in front of the TypeScript
// gateway" that was the original goal of the Go port. It runs the full
// admission pipeline on every request:
//
//   1. IP rate limiting (relay.IPRateLimiter)
//   2. Token extraction (relay.ExtractToken)
//   3. Body parsing (sse.ResolveChatRequestBody)
//   4. Reasoning intent extraction (quota.ExtractReasoningIntent)
//   5. Reasoning routing decision (quota.ResolveReasoningRoutingRule)
//   6. Quota enforcement (quota.Enforce)
//   7. Capacity check (sse.EvalCapacity)
//
// If admitted → proxy to upstream (or return decision info in dry-run mode).
// If rejected → return standardized error (api.CreateErrorResponse).

// GatewayConfig holds runtime configuration.
type GatewayConfig struct {
	ListenAddr    string
	UpstreamURL   string // empty = dry-run mode (no proxy)
	TrustProxy    bool
	IPPerMinute   int
	ConnectionCap int
	DryRun        bool
}

// Gateway is the admission gateway server.
type Gateway struct {
	cfg         GatewayConfig
	store       *quota.Store
	ipLimiter   *relay.IPRateLimiter
	registry    *sse.StreamRegistry
	activeConns int64 // atomic counter for capacity tracking
	rules       []quota.ReasoningRoutingRule
}

// NewGateway creates a Gateway with the given config.
func NewGateway(cfg GatewayConfig) *Gateway {
	return &Gateway{
		cfg:       cfg,
		store:     quota.NewStore(),
		ipLimiter: relay.NewIPRateLimiter(cfg.IPPerMinute),
		registry:  sse.NewStreamRegistry(100),
		rules:     defaultReasoningRules(),
	}
}

// defaultReasoningRules returns a minimal rule set for demonstration.
func defaultReasoningRules() []quota.ReasoningRoutingRule {
	pat := "*"
	te := quota.EffortHigh
	return []quota.ReasoningRoutingRule{
		{
			ID: "default-high", Name: "Default High Effort", Scope: quota.ScopeGlobal,
			ModelPattern: &pat, SourceEffort: "any", EffortMode: quota.EffortDefault,
			TargetEffort: &te, TargetKind: quota.TargetKeep, BudgetAction: quota.BudgetPreserve,
			Enabled: true,
		},
	}
}

// ServeHTTP implements http.Handler.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/health":
		g.handleHealth(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
		g.handleChatCompletions(w, r)
	default:
		body, status := api.CreateErrorResponse(api.ApiErrorPayload{
			Status:  404,
			Message: "Not found",
			Type:    api.ErrNotFound,
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write(body)
	}
}

func (g *Gateway) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	json.NewEncoder(w).Encode(map[string]any{
		"status":       "ok",
		"active_conns": atomic.LoadInt64(&g.activeConns),
		"timestamp":    time.Now().UTC().Format(time.RFC3339),
	})
}

func (g *Gateway) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	// 1. IP rate limiting
	ip := relay.GetClientIP(r, g.cfg.TrustProxy)
	token := relay.ExtractToken(r)
	tokenHash := relay.HashToken(token)
	ipResult := g.ipLimiter.CheckIPRateLimit(tokenHash, ip)
	if !ipResult.Allowed {
		g.reject(w, 429, "Rate limit exceeded", api.ErrInvalidRequest, map[string]any{
			"retry_after": ipResult.ResetIn,
		})
		return
	}

	// 2. Capacity check
	active := int(atomic.LoadInt64(&g.activeConns))
	capResult := sse.EvalCapacity(active, g.cfg.ConnectionCap)
	if capResult.ShouldReject {
		g.reject(w, capResult.Status, "Server at capacity", api.ErrServerError, map[string]any{
			"retry_after": capResult.RetryAfter,
			"limit":       capResult.Limit,
		})
		return
	}

	// 3. Parse body
	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 10<<20)) // 10MB max
	if err != nil {
		g.reject(w, 400, "Failed to read request body", api.ErrInvalidRequest, nil)
		return
	}
	var bodyMap map[string]any
	if err := json.Unmarshal(bodyBytes, &bodyMap); err != nil {
		g.reject(w, 400, "Invalid JSON body", api.ErrInvalidRequest, nil)
		return
	}
	resolved, ok := sse.ResolveChatRequestBody(bodyMap)
	if !ok {
		g.reject(w, 400, "Invalid chat request body", api.ErrInvalidRequest, nil)
		return
	}
	body, _ := resolved.(map[string]any)

	// 4. Extract reasoning intent
	model, _ := body["model"].(string)
	intent := quota.ExtractReasoningIntent(model, body)

	// 5. Reasoning routing decision
	routingInput := quota.RoutingInput{
		SourceModel:        intent.Model,
		SourceEffort:       intent.SourceEffort,
		HasReasoningSignal: intent.HasReasoningSignal,
		HasThinkingBudget:  intent.HasThinkingBudget,
	}
	resolveCap := func(string) quota.CapabilityInfo {
		s := true
		return quota.CapabilityInfo{SupportsThinking: &s}
	}
	decision, err := quota.ResolveReasoningRoutingRule(routingInput, g.rules, nil, resolveCap)
	if err != nil {
		g.reject(w, 500, "Reasoning routing error", api.ErrServerError, nil)
		return
	}

	// 6. Apply reasoning directive to body
	if decision != nil {
		body = quota.AttachReasoningRuleDirective(body, decision)
		body = quota.ApplyReasoningRuleDirective(body).(map[string]any)
	}

	// 7. Quota enforcement
	tier := quota.TierConfig{
		Name:               "standard",
		GuaranteedSharePct: 30,
		BurstMaxSharePct:   50,
		Admission:          quota.AdmissionThrottleBurst,
		Policy:             quota.PolicyBurst,
		MonthlyTokenCap:    200_000_000,
		RPM:                300,
	}
	pool := quota.PoolConfig{
		HardCapUnits:    1000,
		Unit:            "GPU-equivalent units",
		OvercommitRatio: 1.45,
	}
	dims := []quota.QuotaDimension{
		{Unit: quota.UnitTokens, Window: quota.WindowHourly, Limit: 100_000},
		{Unit: quota.UnitRequests, Window: quota.WindowHourly, Limit: 300},
	}
	enforceDecision := quota.Enforce(quota.EnforceInput{
		APIKey:     tokenHash,
		Tier:       tier,
		Pool:       pool,
		Dimensions: dims,
		Store:      g.store,
		Concurrent: true,
	})
	if !enforceDecision.Allow {
		g.reject(w, 429, "Quota exceeded", api.ErrInvalidRequest, map[string]any{
			"reason": string(enforceDecision.Reason),
		})
		return
	}

	// 8. Admitted — increment active connections
	atomic.AddInt64(&g.activeConns, 1)
	defer atomic.AddInt64(&g.activeConns, -1)

	// 9. Proxy or dry-run
	if g.cfg.DryRun || g.cfg.UpstreamURL == "" {
		g.dryRunResponse(w, model, intent, decision, enforceDecision)
		return
	}
	g.proxyUpstream(w, r, body)
}

func (g *Gateway) dryRunResponse(w http.ResponseWriter, model string, intent quota.ExtractedReasoningIntent, decision *quota.ReasoningRuleDecision, enforceDecision quota.EnforceDecision) {
	resp := map[string]any{
		"admitted": true,
		"model":    model,
		"reasoning": map[string]any{
			"intent_model":       intent.Model,
			"intent_effort":      intent.Effort,
			"source_effort":      intent.SourceEffort,
			"has_reasoning_signal": intent.HasReasoningSignal,
			"has_thinking_budget":  intent.HasThinkingBudget,
		},
		"quota": map[string]any{
			"allow":         enforceDecision.Allow,
			"deprioritize":  enforceDecision.Deprioritize,
			"reason":        string(enforceDecision.Reason),
		},
	}
	if decision != nil {
		resp["routing"] = map[string]any{
			"rule_id":        decision.Rule.ID,
			"target_model":   decision.TargetModel,
			"target_effort":  decision.TargetEffort,
			"capability":     string(decision.Capability),
			"warnings":       decision.Warnings,
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	json.NewEncoder(w).Encode(resp)
}

func (g *Gateway) proxyUpstream(w http.ResponseWriter, r *http.Request, body map[string]any) {
	payload, _ := json.Marshal(body)
	upstreamURL := strings.TrimRight(g.cfg.UpstreamURL, "/") + "/v1/chat/completions"

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upstreamURL, strings.NewReader(string(payload)))
	if err != nil {
		g.reject(w, 500, "Failed to create upstream request", api.ErrServerError, nil)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if auth := r.Header.Get("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	}

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		g.reject(w, 502, "Upstream request failed", api.ErrServerError, nil)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func (g *Gateway) reject(w http.ResponseWriter, status int, message string, errType api.ApiErrorType, details any) {
	body, _ := api.CreateErrorResponse(api.ApiErrorPayload{
		Status:  status,
		Message: message,
		Type:    errType,
		Details: details,
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}

// LoadConfigFromEnv loads gateway config from environment variables.
func LoadConfigFromEnv() GatewayConfig {
	cfg := GatewayConfig{
		ListenAddr:    envOrDefault("GATEWAY_LISTEN", ":8080"),
		UpstreamURL:   os.Getenv("GATEWAY_UPSTREAM_URL"),
		TrustProxy:    os.Getenv("GATEWAY_TRUST_PROXY") == "true",
		IPPerMinute:   envIntOrDefault("GATEWAY_IP_PER_MINUTE", 60),
		ConnectionCap: envIntOrDefault("GATEWAY_CONNECTION_CAP", 100),
		DryRun:        os.Getenv("GATEWAY_DRY_RUN") == "true",
	}
	if cfg.UpstreamURL == "" {
		cfg.DryRun = true
	}
	return cfg
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envIntOrDefault(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
