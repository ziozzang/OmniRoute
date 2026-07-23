package quota

import (
	"math"
	"testing"
)

func fptr(v float64) *float64 { return &v }

// TestComputeBurnRateFewSamples verifies <2 samples → zeros.
func TestComputeBurnRateFewSamples(t *testing.T) {
	r := ComputeBurnRate(nil, nil)
	if r.TokensPerSecond != 0 || !math.IsNaN(r.TimeToExhaustionMs) {
		t.Fatalf("empty history: got %+v, want zeros+NaN", r)
	}
	r = ComputeBurnRate([]BurnRateSample{{TS: 1000, Consumed: 10}}, nil)
	if r.TokensPerSecond != 0 {
		t.Fatalf("single sample: got %+v, want zero rate", r)
	}
}

// TestComputeBurnRateConstant verifies a constant-rate series yields that rate.
func TestComputeBurnRateConstant(t *testing.T) {
	// 1000 tokens per second over 5 samples spaced 1s apart.
	hist := []BurnRateSample{
		{TS: 0, Consumed: 0},
		{TS: 1000, Consumed: 1000},
		{TS: 2000, Consumed: 2000},
		{TS: 3000, Consumed: 3000},
		{TS: 4000, Consumed: 4000},
	}
	r := ComputeBurnRate(hist, fptr(10000))
	if math.Abs(r.TokensPerSecond-1000) > 1e-6 {
		t.Fatalf("constant rate: got %v, want 1000", r.TokensPerSecond)
	}
	// 10000 remaining / 1000 per sec = 10s = 10000ms.
	if math.Abs(r.TimeToExhaustionMs-10000) > 1e-6 {
		t.Fatalf("TTE: got %v, want 10000", r.TimeToExhaustionMs)
	}
}

// TestComputeBurnRateEMA verifies EMA smoothing reacts to a rate change.
func TestComputeBurnRateEMA(t *testing.T) {
	// First delta 100/s, second delta 200/s → EMA = 0.3*200 + 0.7*100 = 130.
	hist := []BurnRateSample{
		{TS: 0, Consumed: 0},
		{TS: 1000, Consumed: 100},
		{TS: 2000, Consumed: 300},
	}
	r := ComputeBurnRate(hist, nil)
	want := 0.3*200 + 0.7*100
	if math.Abs(r.TokensPerSecond-want) > 1e-6 {
		t.Fatalf("EMA: got %v, want %v", r.TokensPerSecond, want)
	}
	if !math.IsNaN(r.TimeToExhaustionMs) {
		t.Fatalf("no remaining → NaN TTE, got %v", r.TimeToExhaustionMs)
	}
}

// TestComputeBurnRateSkipsOutOfOrder verifies a duplicate ts (deltaTs=0) is
// skipped. TS semantics: the skip is a `continue`, so the next delta is still
// measured against the immediately-preceding array element (Consumed=50):
// (100-50)/(1000ms) = 50/s — NOT (100-0). This pins the exact TS behaviour.
func TestComputeBurnRateSkipsOutOfOrder(t *testing.T) {
	hist := []BurnRateSample{
		{TS: 0, Consumed: 0},
		{TS: 0, Consumed: 50}, // deltaTs=0 → skipped
		{TS: 1000, Consumed: 100},
	}
	r := ComputeBurnRate(hist, nil)
	if math.Abs(r.TokensPerSecond-50) > 1e-6 {
		t.Fatalf("skip out-of-order: got %v, want 50", r.TokensPerSecond)
	}
}

// TestComputeBurnRateFromWindow verifies the single-snapshot window estimator.
func TestComputeBurnRateFromWindow(t *testing.T) {
	// Zero consumption → zero rate.
	r := ComputeBurnRateFromWindow(0, 18_000_000, 5_000_000, nil)
	if r.TokensPerSecond != 0 {
		t.Fatalf("zero consumed: got %v", r.TokensPerSecond)
	}
	// 1000 consumed, window 5h, now 1h into window → elapsed 3_600_000ms.
	nowMs := 3_600_000.0
	r = ComputeBurnRateFromWindow(1000, 18_000_000, nowMs, fptr(5000))
	wantRate := 1000.0 / 3600.0 // per second
	if math.Abs(r.TokensPerSecond-wantRate) > 1e-6 {
		t.Fatalf("window rate: got %v, want %v", r.TokensPerSecond, wantRate)
	}
	wantTTE := (5000.0 / wantRate) * 1000
	if math.Abs(r.TimeToExhaustionMs-wantTTE) > 1e-3 {
		t.Fatalf("window TTE: got %v, want %v", r.TimeToExhaustionMs, wantTTE)
	}
}
