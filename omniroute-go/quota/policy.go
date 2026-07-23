package quota

// policy.go — the ops-plan ontology tiering policy encoded as Go config.
//
// Source of truth: billing-tiering.ttl (graph billing-tiering). The three
// tiers and their capacity allocations are read from the ontology via SPARQL
// and frozen here as the admission-control config.

// AdmissionPolicy mirrors the ontology :AdmissionPolicy individuals.
type AdmissionPolicy string

const (
	AdmissionRejectOnCap   AdmissionPolicy = "RejectOnCap"   // Trial: hard reject at cap
	AdmissionThrottleBurst AdmissionPolicy = "ThrottleBurst" // Standard/Enterprise: throttle back to guaranteed
)

// TierConfig is one tier's capacity allocation + quotas.
type TierConfig struct {
	Name              string
	Order             int
	GuaranteedSharePct float64 // CapacityAllocation.guaranteedSharePct
	BurstMaxSharePct   float64 // CapacityAllocation.burstMaxSharePct
	UnlimitedAllowed   bool    // always false in the ontology
	Admission          AdmissionPolicy
	Policy             Policy // mapped fair-share policy
	MonthlyTokenCap    float64
	RPM                int
	ConcurrentBatchJobs int
}

// PoolConfig is the shared GPU resource pool.
type PoolConfig struct {
	HardCapUnits    float64 // ResourcePool.hardCap = 1000
	Unit            string  // "GPU-equivalent units"
	OvercommitRatio float64 // 1.45
}

// DefaultPolicy returns the ontology's 3-tier policy (Trial/Standard/Enterprise).
//
// Tier→Policy mapping rationale:
//   - Trial uses RejectOnCap → PolicyHard (block at fair share; batch-only to
//     protect interactive capacity).
//   - Standard/Enterprise use ThrottleBurst → PolicyBurst (allow borrowing
//     while global headroom exists, throttle back to guaranteed under contention).
func DefaultPolicy() ([]TierConfig, PoolConfig) {
	tiers := []TierConfig{
		{
			Name: "Trial", Order: 1,
			GuaranteedSharePct: 5, BurstMaxSharePct: 15,
			UnlimitedAllowed: false, Admission: AdmissionRejectOnCap, Policy: PolicyHard,
			MonthlyTokenCap: 5_000_000, RPM: 30, ConcurrentBatchJobs: 2,
		},
		{
			Name: "Standard", Order: 2,
			GuaranteedSharePct: 30, BurstMaxSharePct: 50,
			UnlimitedAllowed: false, Admission: AdmissionThrottleBurst, Policy: PolicyBurst,
			MonthlyTokenCap: 200_000_000, RPM: 300, ConcurrentBatchJobs: 10,
		},
		{
			Name: "Enterprise", Order: 3,
			GuaranteedSharePct: 50, BurstMaxSharePct: 80,
			UnlimitedAllowed: false, Admission: AdmissionThrottleBurst, Policy: PolicyBurst,
			MonthlyTokenCap: 2_000_000_000, RPM: 2000, ConcurrentBatchJobs: 50,
		},
	}
	pool := PoolConfig{HardCapUnits: 1000, Unit: "GPU-equivalent units", OvercommitRatio: 1.45}
	return tiers, pool
}

// CheckInvariants verifies the ontology's capacity invariants hold:
//   - sum(guaranteed) <= 100  (always leave burst headroom)
//   - sum(burst) == overcommit*100  (statistical multiplexing bound)
//   - no tier allows unlimited
func CheckInvariants(tiers []TierConfig, pool PoolConfig) []string {
	var violations []string
	var gSum, bSum float64
	for _, t := range tiers {
		gSum += t.GuaranteedSharePct
		bSum += t.BurstMaxSharePct
		if t.UnlimitedAllowed {
			violations = append(violations, "tier "+t.Name+" allows unlimited (forbidden)")
		}
	}
	if gSum > 100 {
		violations = append(violations, "sum(guaranteed) > 100")
	}
	want := pool.OvercommitRatio * 100
	if bSum != want {
		violations = append(violations, "sum(burst) != overcommit*100")
	}
	return violations
}
