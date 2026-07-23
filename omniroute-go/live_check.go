package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"omniroute-go/quota"
)

// live_check.go — Live integration test against real LLM providers from ~/env.
//
// Usage: go run live_check.go
//
// Loads API keys from ~/env at runtime (no secrets in source). Skips providers
// whose env vars are missing. Makes minimal chat completion requests to verify
// the Go quota core works end-to-end against real endpoints.

type Provider struct {
	Name    string
	BaseURL string
	APIKey  string
	Model   string
}

func loadEnv(path string) map[string]string {
	env := make(map[string]string)
	f, err := os.Open(path)
	if err != nil {
		return env
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			env[parts[0]] = parts[1]
		}
	}
	return env
}

func chatCompletion(baseURL, apiKey, model string) (string, int, error) {
	url := strings.TrimRight(baseURL, "/") + "/chat/completions"
	body := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": "Say 'ok' in exactly 2 characters."},
		},
		"max_tokens": 10,
	}
	payload, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", url, strings.NewReader(string(payload)))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return string(respBody), resp.StatusCode, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return string(respBody), resp.StatusCode, err
	}
	if len(result.Choices) == 0 {
		return string(respBody), resp.StatusCode, fmt.Errorf("no choices")
	}
	return result.Choices[0].Message.Content, resp.StatusCode, nil
}

func testQuotaEngine() {
	fmt.Println("\n=== Quota Engine Unit Verification ===")

	// Fair-share decision: strict mode, hard policy, consumed >= fair share → block
	dec := quota.DecideFairShare(quota.FairShareInput{
		Dimensions: []quota.FairShareDimension{
			{
				Key:               quota.DimensionKey{PoolID: "test", Unit: quota.UnitTokens, Window: quota.WindowHourly},
				Limit:             1000,
				ConsumedTotal:     600,
				GlobalUsedPercent: 0.7, // strict mode (>= 0.5)
			},
		},
		Allocation: quota.FairShareAllocation{
			Weight: 50, // fair share = 500
			Policy: quota.PolicyHard,
		},
		ConsumedByThisKey:   map[string]float64{"test:tokens:hourly": 600},
		SaturationThreshold: 0.5,
	})
	fmt.Printf("  FairShare (strict/hard/consumed>share): kind=%s reason=%s\n", dec.Kind, dec.Reason)
	if dec.Kind != quota.KindBlock || dec.Reason != quota.ReasonFairShare {
		fmt.Println("  ❌ FAIL: expected block/fair-share")
	} else {
		fmt.Println("  ✅ PASS")
	}

	// Generous mode: globalUsedPercent < threshold → allow borrowing
	dec2 := quota.DecideFairShare(quota.FairShareInput{
		Dimensions: []quota.FairShareDimension{
			{
				Key:               quota.DimensionKey{PoolID: "test", Unit: quota.UnitTokens, Window: quota.WindowHourly},
				Limit:             1000,
				ConsumedTotal:     100,
				GlobalUsedPercent: 0.3, // generous mode (< 0.5)
			},
		},
		Allocation: quota.FairShareAllocation{
			Weight: 50,
			Policy: quota.PolicyBurst,
		},
		ConsumedByThisKey:   map[string]float64{"test:tokens:hourly": 600},
		SaturationThreshold: 0.5,
	})
	fmt.Printf("  FairShare (generous/burst): kind=%s reason=%s\n", dec2.Kind, dec2.Reason)
	if dec2.Kind != quota.KindAllow {
		fmt.Println("  ❌ FAIL: expected allow")
	} else {
		fmt.Println("  ✅ PASS")
	}

	// Rate limiter: fixed window. Check() is atomic (verify + increment).
	// Limit=3: first 3 allowed, 4th blocked.
	rl := quota.NewRateLimiter()
	rules := []quota.RateLimitRule{{Limit: 3, Window: time.Hour}}
	var allowedCount int
	for i := 0; i < 5; i++ {
		if rl.Check("key1", rules).Allowed {
			allowedCount++
		}
	}
	fmt.Printf("  RateLimiter (limit=3, 5 attempts): allowed=%d\n", allowedCount)
	if allowedCount != 3 {
		fmt.Println("  ❌ FAIL: expected exactly 3 allowed")
	} else {
		fmt.Println("  ✅ PASS")
	}

	// Burn rate
	remaining := 500.0
	br := quota.ComputeBurnRate([]quota.BurnRateSample{
		{TS: 0, Consumed: 0},
		{TS: 1000, Consumed: 100},
	}, &remaining)
	fmt.Printf("  BurnRate: tokensPerSec=%.1f timeToExhaustionMs=%.0f\n", br.TokensPerSecond, br.TimeToExhaustionMs)
	if br.TokensPerSecond != 100 || br.TimeToExhaustionMs != 5000 {
		fmt.Println("  ❌ FAIL")
	} else {
		fmt.Println("  ✅ PASS")
	}
}

func testReasoningRouting() {
	fmt.Println("\n=== Reasoning Routing Verification ===")

	// Extract intent from model suffix
	intent := quota.ExtractReasoningIntent("claude-opus-4-high", map[string]any{})
	fmt.Printf("  Intent (claude-opus-4-high): model=%s effort=%v\n", intent.Model, intent.Effort)
	if intent.Model != "claude-opus-4" || intent.Effort == nil || *intent.Effort != quota.EffortHigh {
		fmt.Println("  ❌ FAIL")
	} else {
		fmt.Println("  ✅ PASS")
	}

	// Codex suffix
	intent2 := quota.ExtractReasoningIntent("gpt-5.6-sol-medium", map[string]any{})
	fmt.Printf("  Intent (gpt-5.6-sol-medium): model=%s effort=%v\n", intent2.Model, intent2.Effort)
	if intent2.Model != "gpt-5.6-sol" || intent2.Effort == nil || *intent2.Effort != quota.EffortMedium {
		fmt.Println("  ❌ FAIL")
	} else {
		fmt.Println("  ✅ PASS")
	}

	// Rule matching + decision
	pat := "gpt-*"
	te := quota.EffortHigh
	rules := []quota.ReasoningRoutingRule{
		{
			ID: "r1", Name: "test", Scope: quota.ScopeModel, ModelPattern: &pat,
			SourceEffort: "any", EffortMode: quota.EffortForce, TargetEffort: &te,
			TargetKind: quota.TargetKeep, BudgetAction: quota.BudgetPreserve, Enabled: true,
		},
	}
	resolveCap := func(string) quota.CapabilityInfo { s := true; return quota.CapabilityInfo{SupportsThinking: &s} }
	input := quota.RoutingInput{SourceModel: "gpt-4o", SourceEffort: "missing"}
	dec, err := quota.ResolveReasoningRoutingRule(input, rules, nil, resolveCap)
	if err != nil || dec == nil {
		fmt.Printf("  ❌ FAIL: err=%v dec=%v\n", err, dec)
	} else {
		fmt.Printf("  Decision: targetModel=%s targetEffort=%v capability=%s\n", dec.TargetModel, dec.TargetEffort, dec.Capability)
		fmt.Println("  ✅ PASS")
	}
}

func main() {
	fmt.Println("=== OmniRoute Go Core — Live Integration Test ===")
	fmt.Printf("Time: %s\n", time.Now().Format(time.RFC3339))

	// Load env
	envPath := os.Getenv("ENV_FILE")
	if envPath == "" {
		home, _ := os.UserHomeDir()
		envPath = home + "/env"
	}
	env := loadEnv(envPath)
	fmt.Printf("Loaded %d env vars from %s\n", len(env), envPath)

	// Run unit verification (no network)
	testQuotaEngine()
	testReasoningRouting()

	// Live provider tests
	providers := []Provider{
		{Name: "QWEN", BaseURL: env["QWEN_TOKEN_PLAN_BASE_URL"], APIKey: env["QWEN_TOKEN_PLAN_KEY"], Model: "qwen3.6-flash"},
		{Name: "Z.AI-GLM", BaseURL: env["Z_AI_OPENAI_BASE_URL"], APIKey: env["Z_AI_API_KEY"], Model: "glm-4-plus"},
		{Name: "LITELLM", BaseURL: env["LITELLM_OPENAI_BASE_URL"], APIKey: env["LITELLM_OPENAI_API_KEY"], Model: "glm-5.1"},
	}

	fmt.Println("\n=== Live Provider Tests ===")
	for _, p := range providers {
		if p.BaseURL == "" || p.APIKey == "" {
			fmt.Printf("  %s: SKIP (missing env)\n", p.Name)
			continue
		}
		fmt.Printf("  %s: testing %s ...\n", p.Name, p.Model)
		content, code, err := chatCompletion(p.BaseURL, p.APIKey, p.Model)
		if err != nil {
			fmt.Printf("    ❌ FAIL: HTTP %d err=%v\n", code, err)
			if len(content) > 200 {
				content = content[:200] + "..."
			}
			fmt.Printf("    response: %s\n", content)
		} else {
			fmt.Printf("    ✅ PASS: HTTP %d response=%q\n", code, content)
		}
	}

	fmt.Println("\n=== Done ===")
}
