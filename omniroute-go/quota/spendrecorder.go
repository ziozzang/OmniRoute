package quota

import (
	"math"
	"strconv"
)

// spendrecorder.go — Fire-and-forget POST-response consumption recording.
// Port of src/lib/quota/spendRecorder.ts
//
// buildConsumptionCost is pure and directly testable. The scheduling and
// streaming-consumption paths use injectable seams.

// ConsumptionCost is the per-request consumption payload.
type ConsumptionCost struct {
	Tokens   float64
	USD      float64
	Requests int
}

// BuildConsumptionCost builds the per-request consumption cost payload.
// Coerces token fields defensively (string/NaN safe) and clamps a negative/zero
// cost to 0 so a bad pricing lookup never records negative USD.
//
// usage may be nil; token fields are read from "prompt_tokens" and
// "completion_tokens" keys (coerced via toFloat64).
func BuildConsumptionCost(usage map[string]any, estimatedCost float64) ConsumptionCost {
	tokens := 0.0
	if usage != nil {
		tokens = toFloat64(usage["prompt_tokens"]) + toFloat64(usage["completion_tokens"])
	}
	usd := 0.0
	if estimatedCost > 0 {
		usd = estimatedCost
	}
	return ConsumptionCost{Tokens: tokens, USD: usd, Requests: 1}
}

// toFloat64 coerces an arbitrary value to float64, returning 0 for nil,
// non-numeric, NaN, or Inf values (matching TS `Number(x) || 0`).
func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return 0
		}
		return n
	case float32:
		f := float64(n)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return 0
		}
		return f
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		// TS Number("123") = 123; Number("abc") = NaN → || 0 → 0.
		f, err := strconv.ParseFloat(n, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0
		}
		return f
	default:
		return 0
	}
}
