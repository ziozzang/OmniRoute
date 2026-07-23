package quota

import "sync"

// enforce.go — the hot-path admission gate.
//
// Go port of OmniRoute src/lib/quota/enforce.ts enforceQuotaShare(), with the
// key speedup: dimension peeks are fanned out concurrently instead of the
// TS source's sequential `await store.peek()` per dimension (N serial
// round-trips → 1 parallel round).

// EnforceInput is the pre-request gate input.
type EnforceInput struct {
	APIKey       string
	Tier         TierConfig
	Pool         PoolConfig
	Dimensions   []QuotaDimension
	Store        *Store
	Concurrent   bool // fan out peeks concurrently (the speedup)
}

// EnforceDecision is the gate outcome.
type EnforceDecision struct {
	Allow        bool
	Deprioritize bool
	Reason       DecisionReason
}

// peekDims gathers per-dim consumption + pool totals.
func peekDims(in EnforceInput) []FairShareDimension {
	dims := make([]FairShareDimension, len(in.Dimensions))
	for i, d := range in.Dimensions {
		key := DimensionKey{PoolID: in.Tier.Name, Unit: d.Unit, Window: d.Window}
		total := in.Store.PoolConsumedTotal(in.Tier.Name, key)
		gup := 0.0
		if d.Limit > 0 {
			gup = total / d.Limit
		}
		dims[i] = FairShareDimension{Key: key, Limit: d.Limit, ConsumedTotal: total, GlobalUsedPercent: gup}
	}
	return dims
}

// peekDimsConcurrent fans out per-dimension peeks with a WaitGroup — the
// speedup over the TS sequential await loop.
func peekDimsConcurrent(in EnforceInput) []FairShareDimension {
	dims := make([]FairShareDimension, len(in.Dimensions))
	var wg sync.WaitGroup
	for i, d := range in.Dimensions {
		wg.Add(1)
		go func(i int, d QuotaDimension) {
			defer wg.Done()
			key := DimensionKey{PoolID: in.Tier.Name, Unit: d.Unit, Window: d.Window}
			total := in.Store.PoolConsumedTotal(in.Tier.Name, key)
			gup := 0.0
			if d.Limit > 0 {
				gup = total / d.Limit
			}
			dims[i] = FairShareDimension{Key: key, Limit: d.Limit, ConsumedTotal: total, GlobalUsedPercent: gup}
		}(i, d)
	}
	wg.Wait()
	return dims
}

// Enforce runs the admission gate for one request.
func Enforce(in EnforceInput) EnforceDecision {
	if len(in.Dimensions) == 0 {
		return EnforceDecision{Allow: true, Reason: ReasonOK}
	}

	var dims []FairShareDimension
	if in.Concurrent {
		dims = peekDimsConcurrent(in)
	} else {
		dims = peekDims(in)
	}

	consumed := make(map[string]float64, len(dims))
	for _, d := range dims {
		consumed[d.Key.String()] = in.Store.Peek(in.APIKey, d.Key)
	}

	dec := DecideFairShare(FairShareInput{
		Dimensions:          dims,
		Allocation:          FairShareAllocation{Weight: in.Tier.GuaranteedSharePct, Policy: in.Tier.Policy},
		ConsumedByThisKey:   consumed,
		SaturationThreshold: 0.5,
	})

	if dec.Kind == KindBlock {
		return EnforceDecision{Allow: false, Reason: dec.Reason}
	}
	return EnforceDecision{Allow: true, Deprioritize: dec.Penalized, Reason: ReasonOK}
}
