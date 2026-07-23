package quota

// fairshare.go — Work-conserving fair-share algorithm for quota allocation.
//
// Faithful Go port of OmniRoute src/lib/quota/fairShare.ts decideFairShare().
//
// Two modes:
//   - Generous: globalUsedPercent < saturationThreshold → allow borrowing from
//     the unallocated pool while global capacity remains.
//   - Strict:   globalUsedPercent >= saturationThreshold → enforce strict
//     shares (hard blocks at fair_share, soft penalises, burst still allows
//     while global headroom exists).
//
// The absolute cap is always enforced regardless of mode or policy.

// FairShareDimension is one pool dimension with its live consumption state.
type FairShareDimension struct {
	Key               DimensionKey
	Limit             float64 // global pool limit for this dimension
	ConsumedTotal     float64 // total consumed by ALL keys so far
	GlobalUsedPercent float64 // 0..1 saturation signal
}

// FairShareAllocation is this key's share of the pool.
type FairShareAllocation struct {
	Weight   float64  // 0..100 — this key's share percentage
	CapValue *float64 // absolute cap (optional; nil = none)
	CapUnit  QuotaUnit
	Policy   Policy // hard | soft | burst
}

// FairShareInput is the full decision input.
type FairShareInput struct {
	Dimensions          []FairShareDimension
	Allocation          FairShareAllocation
	ConsumedByThisKey   map[string]float64 // dimensionKeyString -> amount
	SaturationThreshold float64            // default 0.5
}

// DecisionKind is allow or block.
type DecisionKind string

const (
	KindAllow DecisionKind = "allow"
	KindBlock DecisionKind = "block"
)

// DecisionReason explains a decision.
type DecisionReason string

const (
	ReasonOK              DecisionReason = "ok"
	ReasonFairShare       DecisionReason = "fair-share"
	ReasonCapAbsolute     DecisionReason = "cap-absolute"
	ReasonGlobalSaturated DecisionReason = "global-saturated"
)

// FairShareDecision is the outcome of DecideFairShare.
type FairShareDecision struct {
	Kind      DecisionKind
	Reason    DecisionReason
	Penalized bool
}

// DecideFairShare decides whether to allow/block/penalise a request for one
// API key across all dimensions of a quota pool.
//
// This is a line-for-line port of decideFairShare in fairShare.ts.
func DecideFairShare(input FairShareInput) FairShareDecision {
	// Empty plan → always allow.
	if len(input.Dimensions) == 0 {
		return FairShareDecision{Kind: KindAllow, Reason: ReasonOK}
	}

	// Fail-safe: an unknown/corrupted policy is treated as hard (most
	// restrictive) so it can never silently bypass fair-share enforcement.
	effectivePolicy := normalizePolicy(input.Allocation.Policy)

	anyPenalized := false

	for _, dim := range input.Dimensions {
		dKey := dim.Key.String()
		consumed := input.ConsumedByThisKey[dKey] // missing key → 0
		fairShare := (input.Allocation.Weight / 100) * dim.Limit

		// ── Absolute cap (intransponível, sempre) ──────────────────────────
		if input.Allocation.CapValue != nil &&
			input.Allocation.CapUnit == dim.Key.Unit &&
			consumed >= *input.Allocation.CapValue {
			return FairShareDecision{Kind: KindBlock, Reason: ReasonCapAbsolute}
		}

		// ── Global ceiling (intransponível) ────────────────────────────────
		// In the TS source both the burst and non-burst branches return the
		// same "global-saturated" block, so the policy test is dead code; we
		// preserve the observable behaviour with a single check.
		if dim.ConsumedTotal >= dim.Limit {
			return FairShareDecision{Kind: KindBlock, Reason: ReasonGlobalSaturated}
		}

		isStrict := dim.GlobalUsedPercent >= input.SaturationThreshold

		if isStrict {
			// ── Strict mode ────────────────────────────────────────────────
			switch effectivePolicy {
			case PolicyHard:
				if consumed >= fairShare {
					return FairShareDecision{Kind: KindBlock, Reason: ReasonFairShare}
				}
			case PolicySoft:
				if consumed >= fairShare {
					anyPenalized = true
				}
			case PolicyBurst:
				// allow while global headroom exists (checked above)
			}
		} else {
			// ── Generous mode ──────────────────────────────────────────────
			switch effectivePolicy {
			case PolicyHard:
				// Only block if this key alone has consumed the whole pool.
				if consumed >= dim.Limit {
					return FairShareDecision{Kind: KindBlock, Reason: ReasonGlobalSaturated}
				}
			case PolicySoft:
				if consumed >= fairShare {
					anyPenalized = true
				}
			case PolicyBurst:
				// allow while global headroom exists
			}
		}
	}

	return FairShareDecision{Kind: KindAllow, Reason: ReasonOK, Penalized: anyPenalized}
}
