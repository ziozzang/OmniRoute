package quota

// planresolver.go — Resolve the effective quota plan for a provider connection.
// Port of src/lib/quota/planResolver.ts
//
// Precedence (highest to lowest):
//  1. Manual DB override (provider_plans table via getProviderPlan)
//  2. Known catalog (planregistry.go)
//  3. Empty plan (no dimensions — manual configuration required)
//
// Runtime signals (upstream response headers) are accepted for future
// extensibility but ignored in v1, matching the TS source.

// PlanSource records where a resolved plan came from.
type PlanSource string

const (
	// SourceAuto = resolved from the known catalog (or a DB row whose own
	// source is "auto").
	SourceAuto PlanSource = "auto"
	// SourceManual = no catalog/DB plan; the operator must configure dimensions.
	SourceManual PlanSource = "manual"
)

// ProviderPlan is the effective quota plan for one connection.
// Mirrors ProviderPlanSchema in dimensions.ts. ConnectionID is "" when the plan
// is not bound to a specific DB connection (catalog and empty plans), matching
// the TS `connectionId: null`.
type ProviderPlan struct {
	ConnectionID string // "" == null (catalog / empty plans)
	Provider     string
	Dimensions   []QuotaDimension
	Source       PlanSource
}

// RuntimeSignals carries upstream response signals (e.g. ratelimit headers).
// Reserved for future use; ignored by ResolvePlan in v1 (TS parity).
type RuntimeSignals struct {
	Headers map[string]string
}

// ProviderPlanLookup resolves a manual DB override for a connection, mirroring
// getProviderPlan(connectionId) in planResolver.ts.
//
// Contract (fail-open, matching the TS try/catch that swallows DB-unavailable):
//   - return (plan, true) with len(plan.Dimensions) > 0 → the override wins;
//   - return (_, false), an empty-dimensions plan, or panic → the resolver
//     falls through to the catalog. A nil lookup means "no DB available".
type ProviderPlanLookup func(connectionID string) (*ProviderPlan, bool)

// ResolvePlan returns the effective ProviderPlan for a connection. It never
// panics: a nil or panicking lookup is treated as "no DB override" and the
// resolver falls through to the catalog, then to an empty manual plan.
//
// signals is reserved for future use and currently ignored (TS parity).
func ResolvePlan(connectionID, provider string, lookup ProviderPlanLookup, signals *RuntimeSignals) ProviderPlan {
	_ = signals // v1: ignored, reserved for future header-driven limits.

	// 1. Manual DB override — guarded so a panicking/absent DB fails open.
	if plan, ok := safeLookup(lookup, connectionID); ok {
		return plan
	}

	// 2. Known catalog.
	if catalog := GetKnownPlan(provider); catalog != nil {
		return ProviderPlan{
			ConnectionID: "",
			Provider:     catalog.Provider,
			Dimensions:   catalog.Dimensions,
			Source:       SourceAuto,
		}
	}

	// 3. Empty (manual configuration required).
	return ProviderPlan{
		ConnectionID: "",
		Provider:     provider,
		Dimensions:   []QuotaDimension{},
		Source:       SourceManual,
	}
}

// safeLookup invokes the DB override seam, recovering from any panic so a
// missing/unmigrated DB never breaks plan resolution (TS try/catch parity).
// It returns ok=false when the lookup is nil, panics, reports no override, or
// yields a plan with no dimensions — all of which fall through to the catalog.
func safeLookup(lookup ProviderPlanLookup, connectionID string) (plan ProviderPlan, ok bool) {
	if lookup == nil {
		return ProviderPlan{}, false
	}
	defer func() {
		if r := recover(); r != nil {
			plan, ok = ProviderPlan{}, false
		}
	}()
	dbPlan, found := lookup(connectionID)
	if !found || dbPlan == nil || len(dbPlan.Dimensions) == 0 {
		return ProviderPlan{}, false
	}
	return *dbPlan, true
}
