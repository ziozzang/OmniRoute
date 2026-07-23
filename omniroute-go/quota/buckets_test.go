package quota

import (
	"sync"
	"testing"
	"time"
)

// TestBucketFailOpen verifies missing/empty entries are not saturated.
func TestBucketFailOpen(t *testing.T) {
	s := NewBucketStore()
	if s.IsBucketSaturated("conn1", "5h", 1000) {
		t.Fatal("missing entry should fail-open (not saturated)")
	}
	if s.IsBucketSaturated("", "5h", 1000) {
		t.Fatal("empty connectionId should fail-open")
	}
	if s.IsBucketSaturated("conn1", "", 1000) {
		t.Fatal("empty windowKey should fail-open")
	}
}

// TestBucketSaturateAndReset verifies saturation then lazy reset on window rollover.
func TestBucketSaturateAndReset(t *testing.T) {
	s := NewBucketStore()
	resetISO := time.UnixMilli(5000).UTC().Format(time.RFC3339)

	// Below threshold → not saturated.
	s.RecordUsage("conn1", "5h", 99, resetISO, 1000)
	if s.IsBucketSaturated("conn1", "5h", 1000) {
		t.Fatal("99% should not saturate")
	}
	// At threshold → saturated.
	s.RecordUsage("conn1", "5h", 100, resetISO, 1000)
	if !s.IsBucketSaturated("conn1", "5h", 2000) {
		t.Fatal("100% should saturate")
	}
	// Lazy reset: now >= resetsAt (5000) → cleared, eligible again.
	if s.IsBucketSaturated("conn1", "5h", 5000) {
		t.Fatal("lazy reset at resetsAt should clear saturation")
	}
	if s.BucketCount() != 0 {
		t.Fatalf("bucket should be deleted after lazy reset, count=%d", s.BucketCount())
	}
}

// TestBucketStaleResetDiscards verifies a reset already in the past discards state.
func TestBucketStaleResetDiscards(t *testing.T) {
	s := NewBucketStore()
	// Use a whole-second positive epoch (RFC3339 has second precision).
	resetISO := time.UnixMilli(5000).UTC().Format(time.RFC3339) // → 5000ms
	// nowMs (10000) >= resetsAt (5000) → stale, discard.
	s.RecordUsage("conn1", "7d", 100, resetISO, 10000)
	if s.BucketCount() != 0 {
		t.Fatal("stale reset should discard, not store")
	}
}

// TestBucketUnknownReset verifies an unparseable reset never fires lazy reset.
func TestBucketUnknownReset(t *testing.T) {
	s := NewBucketStore()
	s.RecordUsage("conn1", "5h", 100, "not-a-date", 1000)
	// resetsAtMs=0 → lazy reset never fires; stays saturated forever.
	if !s.IsBucketSaturated("conn1", "5h", 1_000_000_000) {
		t.Fatal("unknown reset should keep saturation indefinitely")
	}
}

// TestUpdateAccountBucketsWindowMapping verifies the quota-key → window mapping.
func TestUpdateAccountBucketsWindowMapping(t *testing.T) {
	s := NewBucketStore()
	usage := &ClaudeUsageResult{Quotas: map[string]UsageQuotaSlim{
		"session (5h)":          {Used: 100, ResetAt: ""},
		"weekly (7d)":           {Used: 50, ResetAt: ""},
		"weekly designer (7d)":  {Used: 100, ResetAt: ""},
		"weekly coder (7d)":     {Used: 10, ResetAt: ""},
	}}
	s.UpdateAccountBuckets("conn1", usage, 1000)

	if !s.IsBucketSaturated("conn1", "5h", 1000) {
		t.Fatal("session (5h) at 100% should saturate 5h")
	}
	if s.IsBucketSaturated("conn1", "7d", 1000) {
		t.Fatal("weekly (7d) at 50% should not saturate 7d")
	}
	if !s.IsBucketSaturated("conn1", "7d:designer", 1000) {
		t.Fatal("weekly designer (7d) at 100% should saturate 7d:designer")
	}
	if s.IsBucketSaturated("conn1", "7d:coder", 1000) {
		t.Fatal("weekly coder (7d) at 10% should not saturate 7d:coder")
	}
}

// TestUpdateAccountBucketsPartialResponsePreservesSaturation is a PARITY test
// against the TS source: a partial usage response that OMITS a fixed window key
// (e.g. "session (5h)" absent) must NOT clear a previously-saturated bucket.
//
// TS accountBuckets.ts processQuotaEntry: `if (!entry) return;` — a missing
// key is a no-op. The Go port must replicate this; treating a missing map key
// as the zero value {Used:0} and calling RecordUsage(...,0,...) would wrongly
// delete the existing saturation (RecordUsage below threshold deletes).
func TestUpdateAccountBucketsPartialResponsePreservesSaturation(t *testing.T) {
	s := NewBucketStore()
	// Saturate both fixed windows first.
	s.RecordUsage("conn1", "5h", 100, "", 1000)
	s.RecordUsage("conn1", "7d", 100, "", 1000)
	if !s.IsBucketSaturated("conn1", "5h", 1000) || !s.IsBucketSaturated("conn1", "7d", 1000) {
		t.Fatal("precondition: both windows saturated")
	}

	// A partial response that reports ONLY the weekly window (5h key absent).
	partial := &ClaudeUsageResult{Quotas: map[string]UsageQuotaSlim{
		"weekly (7d)": {Used: 100, ResetAt: ""},
	}}
	s.UpdateAccountBuckets("conn1", partial, 1000)

	// 5h was absent from the response → must remain saturated (TS parity).
	if !s.IsBucketSaturated("conn1", "5h", 1000) {
		t.Fatal("absent 'session (5h)' key must NOT clear existing saturation (TS parity)")
	}
	if !s.IsBucketSaturated("conn1", "7d", 1000) {
		t.Fatal("present 'weekly (7d)' at 100% must stay saturated")
	}
}

// TestUpdateAccountBucketsFailOpen verifies nil/empty inputs are no-ops.
func TestUpdateAccountBucketsFailOpen(t *testing.T) {
	s := NewBucketStore()
	s.UpdateAccountBuckets("conn1", nil, 1000)
	s.UpdateAccountBuckets("", &ClaudeUsageResult{Quotas: map[string]UsageQuotaSlim{}}, 1000)
	if s.BucketCount() != 0 {
		t.Fatal("nil/empty inputs should be no-ops")
	}
}

// TestBucketStoreConcurrent is a -race stress test: many goroutines record and
// read saturation across overlapping connections/windows. The lazy-reset read
// path mutates the map, so this exercises the mutex under contention.
func TestBucketStoreConcurrent(t *testing.T) {
	s := NewBucketStore()
	resetISO := time.UnixMilli(50_000).UTC().Format(time.RFC3339)
	conns := []string{"c1", "c2", "c3", "c4"}
	wins := []string{"5h", "7d", "7d:m1", "7d:m2"}

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				c := conns[(g+i)%len(conns)]
				w := wins[(g+i)%len(wins)]
				now := int64(1000 + i%40_000)
				s.RecordUsage(c, w, float64(50+i%60), resetISO, now)
				_ = s.IsBucketSaturated(c, w, now)
				_ = s.BucketCount()
			}
		}(g)
	}
	wg.Wait()
}
