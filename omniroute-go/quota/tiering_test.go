package quota

import (
	"sync"
	"testing"
	"time"
)

func capPtr(v float64) *float64 { return &v }

// TestDecideFairShareParity mirrors the TS decideFairShare decision table.
func TestDecideFairShareParity(t *testing.T) {
	dim := func(limit, total, gup float64) FairShareDimension {
		return FairShareDimension{
			Key:               DimensionKey{PoolID: "p", Unit: UnitTokens, Window: WindowMonthly},
			Limit:             limit,
			ConsumedTotal:     total,
			GlobalUsedPercent: gup,
		}
	}
	key := (DimensionKey{PoolID: "p", Unit: UnitTokens, Window: WindowMonthly}).String()

	cases := []struct {
		name       string
		alloc      FairShareAllocation
		consumed   float64
		d          FairShareDimension
		threshold  float64
		wantKind   DecisionKind
		wantReason DecisionReason
		wantPenal  bool
	}{
		{"empty plan allows", FairShareAllocation{Weight: 50, Policy: PolicyHard}, 0, dim(0, 0, 0), 0.5, KindAllow, ReasonOK, false},
		{"cap absolute blocks", FairShareAllocation{Weight: 50, Policy: PolicyBurst, CapValue: capPtr(100), CapUnit: UnitTokens}, 100, dim(1000, 100, 0.1), 0.5, KindBlock, ReasonCapAbsolute, false},
		{"global saturated blocks even burst", FairShareAllocation{Weight: 50, Policy: PolicyBurst}, 10, dim(1000, 1000, 1.0), 0.5, KindBlock, ReasonGlobalSaturated, false},
		{"strict hard blocks at fair share", FairShareAllocation{Weight: 50, Policy: PolicyHard}, 500, dim(1000, 600, 0.6), 0.5, KindBlock, ReasonFairShare, false},
		{"strict hard allows under fair share", FairShareAllocation{Weight: 50, Policy: PolicyHard}, 400, dim(1000, 600, 0.6), 0.5, KindAllow, ReasonOK, false},
		{"strict soft penalises over fair share", FairShareAllocation{Weight: 50, Policy: PolicySoft}, 600, dim(1000, 700, 0.7), 0.5, KindAllow, ReasonOK, true},
		{"strict burst allows with headroom", FairShareAllocation{Weight: 50, Policy: PolicyBurst}, 900, dim(1000, 950, 0.95), 0.5, KindAllow, ReasonOK, false},
		{"generous hard allows borrowing", FairShareAllocation{Weight: 50, Policy: PolicyHard}, 700, dim(1000, 700, 0.2), 0.5, KindAllow, ReasonOK, false},
		{"generous soft penalises past share", FairShareAllocation{Weight: 50, Policy: PolicySoft}, 600, dim(1000, 600, 0.2), 0.5, KindAllow, ReasonOK, true},
		{"unknown policy fails safe to hard", FairShareAllocation{Weight: 50, Policy: Policy("corrupt")}, 500, dim(1000, 600, 0.6), 0.5, KindBlock, ReasonFairShare, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var dims []FairShareDimension
			consumed := map[string]float64{}
			if c.d.Limit > 0 {
				dims = []FairShareDimension{c.d}
				consumed[key] = c.consumed
			}
			got := DecideFairShare(FairShareInput{
				Dimensions:          dims,
				Allocation:          c.alloc,
				ConsumedByThisKey:   consumed,
				SaturationThreshold: c.threshold,
			})
			if got.Kind != c.wantKind || got.Reason != c.wantReason || got.Penalized != c.wantPenal {
				t.Fatalf("got %+v, want kind=%s reason=%s penal=%v", got, c.wantKind, c.wantReason, c.wantPenal)
			}
		})
	}
}

// TestOntologyInvariants asserts the ontology capacity invariants hold.
func TestOntologyInvariants(t *testing.T) {
	tiers, pool := DefaultPolicy()
	if v := CheckInvariants(tiers, pool); len(v) != 0 {
		t.Fatalf("invariant violations: %v", v)
	}
	// sum(guaranteed)=85, sum(burst)=145=1.45*100
	var g, b float64
	for _, tier := range tiers {
		g += tier.GuaranteedSharePct
		b += tier.BurstMaxSharePct
	}
	if g != 85 {
		t.Fatalf("sum guaranteed = %v, want 85", g)
	}
	if b != 145 {
		t.Fatalf("sum burst = %v, want 145", b)
	}
}

// TestRateLimiter checks fixed-window multi-rule behaviour.
func TestRateLimiter(t *testing.T) {
	rl := NewRateLimiter()
	r := []RateLimitRule{{Limit: 2, Window: 3600 * 1e9}}
	if !rl.Check("k", r).Allowed {
		t.Fatal("1st should allow")
	}
	if !rl.Check("k", r).Allowed {
		t.Fatal("2nd should allow")
	}
	res := rl.Check("k", r)
	if res.Allowed {
		t.Fatal("3rd should block")
	}
}

// TestEnforceParitySequentialConcurrent ensures both peek strategies agree.
func TestEnforceParitySequentialConcurrent(t *testing.T) {
	tiers, pool := DefaultPolicy()
	std := tiers[1] // Standard, burst
	dims := []QuotaDimension{{Unit: UnitTokens, Window: WindowMonthly, Limit: std.MonthlyTokenCap}}

	seq := NewStore()
	con := NewStore()
	for _, s := range []*Store{seq, con} {
		s.Consume("api-1", DimensionKey{PoolID: std.Name, Unit: UnitTokens, Window: WindowMonthly}, 1_000_000)
	}
	inSeq := EnforceInput{APIKey: "api-1", Tier: std, Pool: pool, Dimensions: dims, Store: seq, Concurrent: false}
	inCon := EnforceInput{APIKey: "api-1", Tier: std, Pool: pool, Dimensions: dims, Store: con, Concurrent: true}
	a, b := Enforce(inSeq), Enforce(inCon)
	if a.Allow != b.Allow || a.Reason != b.Reason {
		t.Fatalf("seq %+v != con %+v", a, b)
	}
}

func benchEnforce(b *testing.B, concurrent bool) {
	tiers, pool := DefaultPolicy()
	ent := tiers[2]
	dims := []QuotaDimension{
		{Unit: UnitTokens, Window: WindowMonthly, Limit: ent.MonthlyTokenCap},
		{Unit: UnitRequests, Window: WindowHourly, Limit: float64(ent.RPM)},
		{Unit: UnitTokens, Window: Window5H, Limit: ent.MonthlyTokenCap / 4},
	}
	store := NewStore()
	store.Consume("api-1", DimensionKey{PoolID: ent.Name, Unit: UnitTokens, Window: WindowMonthly}, 5_000_000)
	in := EnforceInput{APIKey: "api-1", Tier: ent, Pool: pool, Dimensions: dims, Store: store, Concurrent: concurrent}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Enforce(in)
	}
}

func BenchmarkEnforce_Sequential(b *testing.B) { benchEnforce(b, false) }
func BenchmarkEnforce_Concurrent(b *testing.B) { benchEnforce(b, true) }

// slowStore wraps Store and simulates a remote (Redis/SQLite) round-trip per
// peek — the real OmniRoute scenario where the TS does `await store.peek()`.
type slowStore struct {
	*Store
	latency time.Duration
}

func (s *slowStore) peek(apiKey string, dim DimensionKey) float64 {
	time.Sleep(s.latency) // simulate network/DB round-trip
	return s.Store.Peek(apiKey, dim)
}

// benchEnforceIO measures the gate against a latency-bearing store, comparing
// sequential peeks (TS behaviour) vs concurrent fan-out (Go speedup).
func benchEnforceIO(b *testing.B, concurrent bool, latency time.Duration) {
	tiers, pool := DefaultPolicy()
	ent := tiers[2]
	dims := []QuotaDimension{
		{Unit: UnitTokens, Window: WindowMonthly, Limit: ent.MonthlyTokenCap},
		{Unit: UnitRequests, Window: WindowHourly, Limit: float64(ent.RPM)},
		{Unit: UnitTokens, Window: Window5H, Limit: ent.MonthlyTokenCap / 4},
	}
	ss := &slowStore{Store: NewStore(), latency: latency}
	in := EnforceInput{APIKey: "api-1", Tier: ent, Pool: pool, Dimensions: dims, Store: ss.Store, Concurrent: concurrent}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// emulate per-dim I/O: sequential sleeps vs one parallel sleep.
		if concurrent {
			var wg sync.WaitGroup
			for range dims {
				wg.Add(1)
				go func() { defer wg.Done(); ss.peek("api-1", DimensionKey{}) }()
			}
			wg.Wait()
		} else {
			for range dims {
				ss.peek("api-1", DimensionKey{})
			}
		}
		Enforce(in)
	}
}

// 200µs per peek ≈ a local Redis/SQLite round-trip; 3 dims.
func BenchmarkEnforce_IO_Sequential(b *testing.B) { benchEnforceIO(b, false, 200*time.Microsecond) }
func BenchmarkEnforce_IO_Concurrent(b *testing.B) { benchEnforceIO(b, true, 200*time.Microsecond) }
