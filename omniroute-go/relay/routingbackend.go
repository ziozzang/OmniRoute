package relay

import (
	"strconv"
	"strings"
)

// routingbackend.go — Relay routing backend resolution.
// Port of src/app/api/v1/relay/chat/completions/routingBackend.ts
//
// Pure functions — no race concerns.

// RelayRoutingBackend is the routing backend type.
type RelayRoutingBackend string

const (
	BackendTS      RelayRoutingBackend = "ts"
	BackendBifrost RelayRoutingBackend = "bifrost"
	BackendAuto    RelayRoutingBackend = "auto"
)

// BifrostRoutingConfig holds bifrost sidecar configuration.
type BifrostRoutingConfig struct {
	BaseURL          string
	APIKey           string
	TimeoutMs        int
	StreamingEnabled bool
	Enabled          bool
}

// SidecarEligibility reports whether a model is eligible for sidecar routing.
type SidecarEligibility struct {
	Eligible bool
	Reasons  []string
}

// ProviderSidecarLookup resolves sidecar eligibility for a model.
type ProviderSidecarLookup func(model string) *SidecarEligibility

// BifrostRoutingDecision is the routing decision outcome.
type BifrostRoutingDecision struct {
	TryBifrost     bool
	FallbackReason string
}

// RoutingFallbackReasonCode is a stable machine-readable fallback reason.
type RoutingFallbackReasonCode string

const (
	FallbackBifrostCooldown       RoutingFallbackReasonCode = "bifrost-cooldown"
	FallbackBifrostError          RoutingFallbackReasonCode = "bifrost-error"
	FallbackBifrostIneligible     RoutingFallbackReasonCode = "bifrost-ineligible"
	FallbackBifrostProviderUnknown RoutingFallbackReasonCode = "bifrost-provider-unknown"
)

var validFallbackReasonCodes = map[RoutingFallbackReasonCode]bool{
	FallbackBifrostCooldown:       true,
	FallbackBifrostError:          true,
	FallbackBifrostIneligible:     true,
	FallbackBifrostProviderUnknown: true,
}

// GetBifrostRoutingConfig builds a BifrostRoutingConfig from env. Returns nil
// when no base URL is resolvable. The supervisor lookup is injectable.
func GetBifrostRoutingConfig(env map[string]string, supervisorBaseURL string) *BifrostRoutingConfig {
	baseURL := strings.TrimRight(env["BIFROST_BASE_URL"], "/")
	if baseURL == "" {
		baseURL = supervisorBaseURL
	}
	if baseURL == "" {
		return nil
	}

	timeoutMs := 30000
	if raw := env["BIFROST_TIMEOUT_MS"]; raw != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && n > 0 {
			timeoutMs = n
		}
	}

	apiKey := env["BIFROST_API_KEY"]
	if apiKey == "" {
		apiKey = env["OMNIROUTE_BIFROST_KEY"]
	}

	return &BifrostRoutingConfig{
		BaseURL:          baseURL,
		APIKey:           apiKey,
		TimeoutMs:        timeoutMs,
		StreamingEnabled: env["BIFROST_STREAMING_ENABLED"] != "0",
		Enabled:          env["BIFROST_ENABLED"] != "0",
	}
}

// ResolveRelayRoutingBackend determines the routing backend from env.
func ResolveRelayRoutingBackend(env map[string]string, config *BifrostRoutingConfig) RelayRoutingBackend {
	configured := env["OMNIROUTE_RELAY_BACKEND"]
	if configured == "" {
		configured = env["RELAY_ROUTING_BACKEND"]
	}
	switch RelayRoutingBackend(configured) {
	case BackendTS, BackendBifrost, BackendAuto:
		return RelayRoutingBackend(configured)
	}
	if config != nil && config.Enabled {
		return BackendAuto
	}
	return BackendTS
}

// ShouldTryBifrost returns whether bifrost should be attempted.
func ShouldTryBifrost(backend RelayRoutingBackend, config *BifrostRoutingConfig) bool {
	return config != nil && config.Enabled && backend != BackendTS
}

// ShouldTryBifrostForRequest makes a per-request bifrost routing decision.
func ShouldTryBifrostForRequest(
	backend RelayRoutingBackend,
	config *BifrostRoutingConfig,
	model string,
	lookup ProviderSidecarLookup,
) BifrostRoutingDecision {
	if !ShouldTryBifrost(backend, config) {
		return BifrostRoutingDecision{TryBifrost: false}
	}
	if backend == BackendBifrost {
		return BifrostRoutingDecision{TryBifrost: true}
	}
	// auto mode: check provider eligibility
	provider := lookup(model)
	if provider != nil && provider.Eligible {
		return BifrostRoutingDecision{TryBifrost: true}
	}
	reason := string(FallbackBifrostProviderUnknown)
	if provider != nil {
		reason = string(FallbackBifrostIneligible)
	}
	return BifrostRoutingDecision{TryBifrost: false, FallbackReason: reason}
}

// GetRoutingFallbackHeader returns "bifrost" when auto+bifrost-enabled.
func GetRoutingFallbackHeader(backend RelayRoutingBackend, config *BifrostRoutingConfig) string {
	if backend == BackendAuto && config != nil && config.Enabled {
		return "bifrost"
	}
	return ""
}

// GetRoutingFallbackReasonHeader derives a stable reason code from a detail string.
func GetRoutingFallbackReasonHeader(fallbackReason string) RoutingFallbackReasonCode {
	if fallbackReason == "" {
		return ""
	}
	code := strings.TrimSpace(strings.SplitN(fallbackReason, ";", 2)[0])
	if validFallbackReasonCodes[RoutingFallbackReasonCode(code)] {
		return RoutingFallbackReasonCode(code)
	}
	return ""
}
