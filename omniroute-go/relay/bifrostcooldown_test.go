package relay

import (
	"sync"
	"testing"
)

func TestBifrostCooldown(t *testing.T) {
	r := NewBifrostCooldownRegistry()
	now := int64(1_700_000_000_000)
	r.SetClock(func() int64 { return now })

	if r.GetActiveCooldown("http://a") != nil {
		t.Fatal("no cooldown initially")
	}
	r.RecordFailure("http://a", "timeout", 5000)
	cd := r.GetActiveCooldown("http://a")
	if cd == nil || cd.RemainingMs != 5000 || cd.Reason != "timeout" {
		t.Fatalf("got %+v", cd)
	}
	// advance past cooldown
	now += 6000
	if r.GetActiveCooldown("http://a") != nil {
		t.Fatal("expired cooldown should be nil")
	}
	// clear
	r.RecordFailure("http://b", "err", 5000)
	r.ClearFailure("http://b")
	if r.GetActiveCooldown("http://b") != nil {
		t.Fatal("cleared")
	}
	// zero cooldown → delete
	r.RecordFailure("http://c", "err", 0)
	if r.GetActiveCooldown("http://c") != nil {
		t.Fatal("zero cooldown → no entry")
	}
}

func TestBifrostCooldownConcurrent(t *testing.T) {
	r := NewBifrostCooldownRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.RecordFailure("http://x", "err", 5000)
			r.GetActiveCooldown("http://x")
			r.ClearFailure("http://x")
		}()
	}
	wg.Wait()
}

func TestGetBifrostFailureCooldownMs(t *testing.T) {
	if GetBifrostFailureCooldownMs(map[string]string{}) != 5000 {
		t.Fatal("default 5000")
	}
	if GetBifrostFailureCooldownMs(map[string]string{"OMNIROUTE_BIFROST_FAILURE_COOLDOWN_MS": "3000"}) != 3000 {
		t.Fatal("3000")
	}
	if GetBifrostFailureCooldownMs(map[string]string{"OMNIROUTE_BIFROST_FAILURE_COOLDOWN_MS": "-1"}) != 5000 {
		t.Fatal("negative → 5000")
	}
}

func TestFinalizeOnce(t *testing.T) {
	var calls int
	var lastErr error
	f := NewFinalizeOnce(func(err error) { calls++; lastErr = err })
	f.Finalize(nil)
	f.Finalize(nil)
	f.Finalize(nil)
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if lastErr != nil {
		t.Fatal("first call wins (nil)")
	}
}

func TestFinalizeOnceConcurrent(t *testing.T) {
	var calls int32
	var mu sync.Mutex
	f := NewFinalizeOnce(func(err error) { mu.Lock(); calls++; mu.Unlock() })
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); f.Finalize(nil) }()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("concurrent calls = %d, want 1", calls)
	}
}
