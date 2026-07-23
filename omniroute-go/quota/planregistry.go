package quota

// planregistry.go — Static catalog of known provider quota plans.
// Port of src/lib/quota/planRegistry.ts
//
// Each entry describes the quota dimensions a provider exposes upstream. A
// limit of LimitUnknown (== JS Number.EPSILON) is a sentinel meaning "the real
// cap is not published / no balance API — set it manually in the Wizard". The
// fair-share engine treats such a tiny positive limit as "manual required".

// LimitUnknown is the sentinel "limit" for plans whose real cap is unknown
// upstream and must be configured manually. It matches JS Number.EPSILON
// exactly (2.220446049250313e-16) so parity tests comparing the raw float pass.
const LimitUnknown = 2.220446049250313e-16 // == JS Number.EPSILON

// KnownPlan is one entry in the static provider plan catalog.
type KnownPlan struct {
	Provider   string
	Dimensions []QuotaDimension
}

// knownPlans mirrors KNOWN_PLANS in planRegistry.ts. It is read-only after
// package init; GetKnownPlan returns defensive copies so callers cannot mutate
// the shared catalog.
var knownPlans = map[string]KnownPlan{
	"codex": {
		Provider: "codex",
		Dimensions: []QuotaDimension{
			{Unit: UnitPercent, Window: Window5H, Limit: 100},
			{Unit: UnitPercent, Window: WindowWeekly, Limit: 100},
		},
	},
	// Claude Code (Pro / Max 5x / Max 20x): percentage-of-plan over a 5h rolling
	// window + weekly cap, shared across Claude and Claude Code. Exact token caps
	// are unpublished and vary by task, so % is the practical unit.
	"claude": {
		Provider: "claude",
		Dimensions: []QuotaDimension{
			{Unit: UnitPercent, Window: Window5H, Limit: 100},
			{Unit: UnitPercent, Window: WindowWeekly, Limit: 100},
		},
	},
	// glm: limit unknown (no balance API) → LimitUnknown = "manual required".
	"glm": {
		Provider: "glm",
		Dimensions: []QuotaDimension{
			{Unit: UnitTokens, Window: Window5H, Limit: LimitUnknown},
			{Unit: UnitTokens, Window: WindowWeekly, Limit: LimitUnknown},
		},
	},
	// minimax: monthly token allowance over 5h-rolling + weekly. EPSILON = pick
	// your tier manually.
	"minimax": {
		Provider: "minimax",
		Dimensions: []QuotaDimension{
			{Unit: UnitTokens, Window: Window5H, Limit: LimitUnknown},
			{Unit: UnitTokens, Window: WindowWeekly, Limit: LimitUnknown},
		},
	},
	// deepseek: prepaid USD balance (balance API wired upstream). fair-share
	// supports the usd unit, so a USD budget is set here ("fixado por valor").
	"deepseek": {
		Provider: "deepseek",
		Dimensions: []QuotaDimension{
			{Unit: UnitUSD, Window: WindowMonthly, Limit: LimitUnknown},
		},
	},
	"bailian": {
		Provider: "bailian",
		Dimensions: []QuotaDimension{
			{Unit: UnitPercent, Window: Window5H, Limit: 100},
			{Unit: UnitPercent, Window: WindowWeekly, Limit: 100},
			{Unit: UnitPercent, Window: WindowMonthly, Limit: 100},
		},
	},
	"kimi": {
		Provider: "kimi",
		Dimensions: []QuotaDimension{
			{Unit: UnitRequests, Window: WindowHourly, Limit: 1500},
		},
	},
	// kimi-coding: no upstream balance API → LimitUnknown = manual.
	"kimi-coding": {
		Provider: "kimi-coding",
		Dimensions: []QuotaDimension{
			{Unit: UnitTokens, Window: Window5H, Limit: LimitUnknown},
			{Unit: UnitTokens, Window: WindowWeekly, Limit: LimitUnknown},
		},
	},
	// xiaomi-mimo: MONTHLY token allowance, no balance API. Default seeds the
	// "lite" plan's 4.1B-token monthly cap so the Wizard pre-fills a usable limit.
	"xiaomi-mimo": {
		Provider: "xiaomi-mimo",
		Dimensions: []QuotaDimension{
			{Unit: UnitTokens, Window: WindowMonthly, Limit: 4_100_000_000},
		},
	},
	"alibaba": {
		Provider: "alibaba",
		Dimensions: []QuotaDimension{
			{Unit: UnitRequests, Window: WindowMonthly, Limit: 90_000},
		},
	},
	// grok-cli: static estimate from x-ratelimit-* headers; used only as a
	// fallback when the live grok-cli quota fetcher returns null (fail-open).
	"grok-cli": {
		Provider: "grok-cli",
		Dimensions: []QuotaDimension{
			{Unit: UnitRequests, Window: WindowDaily, Limit: 864},
			{Unit: UnitTokens, Window: WindowDaily, Limit: 18_000_000},
			{Unit: UnitRequests, Window: WindowWeekly, Limit: 6048},
			{Unit: UnitTokens, Window: WindowWeekly, Limit: 126_000_000},
		},
	},
}

// knownPlanOrder preserves the TS Object.keys() insertion order (all keys are
// non-integer strings, so JS returns them in insertion order). KnownProviders
// returns this order for parity with `Object.keys(KNOWN_PLANS)`.
var knownPlanOrder = []string{
	"codex",
	"claude",
	"glm",
	"minimax",
	"deepseek",
	"bailian",
	"kimi",
	"kimi-coding",
	"xiaomi-mimo",
	"alibaba",
	"grok-cli",
}

// GetKnownPlan returns the catalog entry for a provider, or nil if unknown.
// The returned plan is a defensive copy (struct + dimensions slice) so callers
// cannot mutate the shared catalog.
func GetKnownPlan(provider string) *KnownPlan {
	p, ok := knownPlans[provider]
	if !ok {
		return nil
	}
	dims := make([]QuotaDimension, len(p.Dimensions))
	copy(dims, p.Dimensions)
	return &KnownPlan{Provider: p.Provider, Dimensions: dims}
}

// KnownProviders returns the catalog provider slugs in insertion order,
// mirroring `Object.keys(KNOWN_PLANS)` in the TS source.
func KnownProviders() []string {
	out := make([]string, len(knownPlanOrder))
	copy(out, knownPlanOrder)
	return out
}
