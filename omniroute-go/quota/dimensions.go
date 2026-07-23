// Package tiering is a Go port of OmniRoute's quota-sharing hot path
// (src/lib/quota/*) extended with the ops-plan ontology tiering policy.
//
// It is intentionally dependency-free so it can be embedded as a sidecar or
// compiled to a native admission-control proxy in front of the TypeScript
// gateway. The hot path (Enforce) runs on every request, so latency matters.
package quota

// QuotaUnit is the axis a quota is measured on.
// Mirrors src/lib/quota/dimensions.ts QuotaUnitSchema.
type QuotaUnit string

const (
	UnitPercent  QuotaUnit = "percent"
	UnitRequests QuotaUnit = "requests"
	UnitTokens   QuotaUnit = "tokens"
	UnitUSD      QuotaUnit = "usd"
)

// QuotaWindow is the rolling/fixed window a quota is measured over.
type QuotaWindow string

const (
	Window5H      QuotaWindow = "5h"
	WindowHourly  QuotaWindow = "hourly"
	WindowDaily   QuotaWindow = "daily"
	WindowWeekly  QuotaWindow = "weekly"
	WindowMonthly QuotaWindow = "monthly"
)

// Policy is the fair-share enforcement policy for one allocation.
// Mirrors PolicySchema: hard | soft | burst.
type Policy string

const (
	PolicyHard  Policy = "hard"  // block once consumed >= fair share
	PolicySoft  Policy = "soft"  // allow but penalise (deprioritise) past fair share
	PolicyBurst Policy = "burst" // allow while global headroom exists
)

// DimensionKey identifies one (pool, unit, window) counter bucket.
type DimensionKey struct {
	PoolID string
	Unit   QuotaUnit
	Window QuotaWindow
}

// String is the canonical bucket key, matching dimensionKeyToString in TS.
func (k DimensionKey) String() string {
	return k.PoolID + ":" + string(k.Unit) + ":" + string(k.Window)
}

// WindowMS returns the window length in milliseconds, matching WINDOW_MS in TS.
func WindowMS(w QuotaWindow) int64 {
	switch w {
	case WindowHourly:
		return 60 * 60 * 1000
	case Window5H:
		return 5 * 60 * 60 * 1000
	case WindowDaily:
		return 24 * 60 * 60 * 1000
	case WindowWeekly:
		return 7 * 24 * 60 * 60 * 1000
	case WindowMonthly:
		return 30 * 24 * 60 * 60 * 1000
	default:
		return 0
	}
}

// QuotaDimension is one plan limit axis.
type QuotaDimension struct {
	Unit   QuotaUnit
	Window QuotaWindow
	Limit  float64
}

// knownPolicies mirrors KNOWN_POLICIES in fairShare.ts.
var knownPolicies = map[Policy]struct{}{
	PolicyHard:  {},
	PolicySoft:  {},
	PolicyBurst: {},
}

// normalizePolicy is the fail-safe from fairShare.ts: any value outside
// hard|soft|burst (e.g. a corrupted DB row) is treated as the most
// restrictive policy, hard. This closes a fail-OPEN hole where an unknown
// policy would fall through every switch case and silently allow.
func normalizePolicy(p Policy) Policy {
	if _, ok := knownPolicies[p]; ok {
		return p
	}
	return PolicyHard
}
