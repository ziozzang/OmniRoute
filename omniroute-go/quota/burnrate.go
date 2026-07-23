package quota

import "math"

// burnrate.go — Burn-rate EMA estimator for quota consumption.
// Port of src/lib/quota/burnRate.ts
//
// Computes an exponential moving average (alpha=0.3) over a series of
// (timestamp, consumed) samples and projects time to exhaustion.
//
// Pure functions with injectable clock — no shared state, no race concerns.

const emaAlpha = 0.3

// BurnRateSample is one observation of cumulative consumption at a timestamp.
type BurnRateSample struct {
	TS       float64 // epoch ms
	Consumed float64 // cumulative consumed value at this ts
}

// BurnRateResult holds the estimated burn rate and projected exhaustion time.
type BurnRateResult struct {
	// TokensPerSecond is the estimated units consumed per second.
	TokensPerSecond float64
	// TimeToExhaustionMs is the estimated milliseconds until remaining quota
	// is exhausted. NaN when rate is 0 or remaining was not provided.
	TimeToExhaustionMs float64
}

// ComputeBurnRateFromWindow computes burn rate from a single snapshot using
// the sliding window context.
//
// When only one sample is available (the common case for on-demand pool usage
// queries), we derive the rate from the consumption within the current window:
//
//	rate = consumedTotal / elapsedInWindow
//
// This assumes consumption is roughly uniform within the window.
//
// nowMs is injectable for deterministic testing.
func ComputeBurnRateFromWindow(consumedTotal, windowMs, nowMs float64, remaining *float64) BurnRateResult {
	if consumedTotal <= 0 || windowMs <= 0 {
		return BurnRateResult{TokensPerSecond: 0, TimeToExhaustionMs: math.NaN()}
	}

	currentBucketIndex := math.Floor(nowMs / windowMs)
	windowStartMs := currentBucketIndex * windowMs
	elapsedMs := math.Max(1, nowMs-windowStartMs) // avoid division by zero

	safeRate := consumedTotal / (elapsedMs / 1000) // per second
	tte := math.NaN()
	if safeRate > 0 && remaining != nil && *remaining >= 0 {
		tte = (*remaining / safeRate) * 1000
	}

	return BurnRateResult{TokensPerSecond: safeRate, TimeToExhaustionMs: tte}
}

// ComputeBurnRate computes the current burn rate from a series of samples.
//
// history must be ordered oldest → newest. Needs at least 2 entries; fewer
// returns zeros. When remaining is provided, TimeToExhaustionMs is calculated.
func ComputeBurnRate(history []BurnRateSample, remaining *float64) BurnRateResult {
	if len(history) < 2 {
		return BurnRateResult{TokensPerSecond: 0, TimeToExhaustionMs: math.NaN()}
	}

	var emaRate float64
	initialized := false

	for i := 1; i < len(history); i++ {
		deltaConsumed := history[i].Consumed - history[i-1].Consumed
		deltaTs := history[i].TS - history[i-1].TS // ms

		if deltaTs <= 0 {
			continue // skip duplicate or out-of-order timestamps
		}

		instantRate := deltaConsumed / (deltaTs / 1000) // per second

		if !initialized {
			emaRate = instantRate
			initialized = true
		} else {
			emaRate = emaAlpha*instantRate + (1-emaAlpha)*emaRate
		}
	}

	if !initialized {
		return BurnRateResult{TokensPerSecond: 0, TimeToExhaustionMs: math.NaN()}
	}

	safeRate := math.Max(0, emaRate)
	tte := math.NaN()
	if safeRate > 0 && remaining != nil && *remaining >= 0 {
		tte = (*remaining / safeRate) * 1000
	}

	return BurnRateResult{TokensPerSecond: safeRate, TimeToExhaustionMs: tte}
}
