package domain

import (
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// FallbackPolicy
// ---------------------------------------------------------------------------

func TestFallbackSortByPriority(t *testing.T) {
	mgr := NewFallbackManager(nil)
	mgr.RegisterFallback("gpt-4o", []FallbackEntry{
		{Provider: "azure", Priority: 2, Enabled: true},
		{Provider: "openai", Priority: 0, Enabled: true},
		{Provider: "aws", Priority: 1, Enabled: true},
	})
	chain := mgr.ResolveFallbackChain("gpt-4o", nil)
	if len(chain) != 3 || chain[0].Provider != "openai" || chain[1].Provider != "aws" || chain[2].Provider != "azure" {
		t.Fatalf("sorted chain = %+v", chain)
	}
}

func TestFallbackExcludeAndDisabled(t *testing.T) {
	mgr := NewFallbackManager(nil)
	mgr.RegisterFallback("m", []FallbackEntry{
		{Provider: "a", Priority: 0, Enabled: true},
		{Provider: "b", Priority: 1, Enabled: false},
		{Provider: "c", Priority: 2, Enabled: true},
	})
	chain := mgr.ResolveFallbackChain("m", []string{"a"})
	if len(chain) != 1 || chain[0].Provider != "c" {
		t.Fatalf("expected [c], got %+v", chain)
	}
}

func TestGetNextFallback(t *testing.T) {
	mgr := NewFallbackManager(nil)
	mgr.RegisterFallback("m", []FallbackEntry{
		{Provider: "a", Priority: 0, Enabled: true},
		{Provider: "b", Priority: 1, Enabled: true},
	})
	if got := mgr.GetNextFallback("m", nil); got != "a" {
		t.Fatalf("next = %q, want a", got)
	}
	if got := mgr.GetNextFallback("m", []string{"a"}); got != "b" {
		t.Fatalf("next after exclude a = %q, want b", got)
	}
	if got := mgr.GetNextFallback("m", []string{"a", "b"}); got != "" {
		t.Fatalf("exhausted = %q, want empty", got)
	}
}

func TestHasFallback(t *testing.T) {
	mgr := NewFallbackManager(nil)
	if mgr.HasFallback("m") {
		t.Fatal("no chain → false")
	}
	mgr.RegisterFallback("m", []FallbackEntry{{Provider: "a", Enabled: false}})
	if mgr.HasFallback("m") {
		t.Fatal("all disabled → false")
	}
	mgr.RegisterFallback("m", []FallbackEntry{{Provider: "a", Enabled: true}})
	if !mgr.HasFallback("m") {
		t.Fatal("enabled → true")
	}
}

func TestRemoveFallback(t *testing.T) {
	mgr := NewFallbackManager(nil)
	mgr.RegisterFallback("m", []FallbackEntry{{Provider: "a", Enabled: true}})
	if !mgr.RemoveFallback("m") {
		t.Fatal("should remove existing")
	}
	if mgr.RemoveFallback("m") {
		t.Fatal("should not remove twice")
	}
}

func TestFallbackConcurrent(t *testing.T) {
	mgr := NewFallbackManager(nil)
	mgr.RegisterFallback("m", []FallbackEntry{{Provider: "a", Priority: 0, Enabled: true}})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mgr.ResolveFallbackChain("m", nil)
			mgr.HasFallback("m")
			mgr.GetNextFallback("m", nil)
		}()
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// LockoutPolicy
// ---------------------------------------------------------------------------

func TestLockoutBasic(t *testing.T) {
	mgr := NewLockoutManager(nil)
	now := int64(1_700_000_000_000)
	mgr.SetClock(func() int64 { return now })

	cfg := LockoutConfig{MaxAttempts: 3, LockoutDurationMs: 60000, AttemptWindowMs: 300000}

	// 2 failures → not locked
	for i := 0; i < 2; i++ {
		res := mgr.RecordFailedAttempt("ip1", cfg)
		if res.Locked {
			t.Fatalf("attempt %d: should not be locked", i+1)
		}
	}
	// 3rd failure → locked
	res := mgr.RecordFailedAttempt("ip1", cfg)
	if !res.Locked {
		t.Fatal("3rd attempt should lock")
	}
	if res.RemainingMs != 60000 {
		t.Fatalf("remainingMs = %d, want 60000", res.RemainingMs)
	}

	// Check while locked
	check := mgr.CheckLockout("ip1", cfg)
	if !check.Locked {
		t.Fatal("should still be locked")
	}

	// Advance past lockout
	now += 61000
	check = mgr.CheckLockout("ip1", cfg)
	if check.Locked {
		t.Fatal("lockout should have expired")
	}
}

func TestLockoutWindowExpiry(t *testing.T) {
	mgr := NewLockoutManager(nil)
	now := int64(1_700_000_000_000)
	mgr.SetClock(func() int64 { return now })

	cfg := LockoutConfig{MaxAttempts: 3, LockoutDurationMs: 60000, AttemptWindowMs: 10000}

	mgr.RecordFailedAttempt("ip1", cfg)
	mgr.RecordFailedAttempt("ip1", cfg)

	// Advance past the attempt window → old attempts expire
	now += 11000
	check := mgr.CheckLockout("ip1", cfg)
	if check.Attempts != 0 {
		t.Fatalf("attempts after window expiry = %d, want 0", check.Attempts)
	}
}

func TestLockoutRecordSuccess(t *testing.T) {
	mgr := NewLockoutManager(nil)
	now := int64(1_700_000_000_000)
	mgr.SetClock(func() int64 { return now })
	cfg := LockoutConfig{MaxAttempts: 2, LockoutDurationMs: 60000, AttemptWindowMs: 300000}

	mgr.RecordFailedAttempt("ip1", cfg)
	mgr.RecordFailedAttempt("ip1", cfg) // locked
	mgr.RecordSuccess("ip1")

	check := mgr.CheckLockout("ip1", cfg)
	if check.Locked || check.Attempts != 0 {
		t.Fatalf("after success: locked=%v attempts=%d", check.Locked, check.Attempts)
	}
}

func TestGetLockedIdentifiers(t *testing.T) {
	mgr := NewLockoutManager(nil)
	now := int64(1_700_000_000_000)
	mgr.SetClock(func() int64 { return now })
	cfg := LockoutConfig{MaxAttempts: 1, LockoutDurationMs: 60000, AttemptWindowMs: 300000}

	mgr.RecordFailedAttempt("ip1", cfg)
	mgr.RecordFailedAttempt("ip2", cfg)

	locked := mgr.GetLockedIdentifiers()
	if len(locked) != 2 {
		t.Fatalf("locked count = %d, want 2", len(locked))
	}
}

func TestLockoutConcurrent(t *testing.T) {
	mgr := NewLockoutManager(nil)
	now := int64(1_700_000_000_000)
	mgr.SetClock(func() int64 { return now })
	cfg := DefaultLockoutConfig

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ident := "ip"
			mgr.RecordFailedAttempt(ident, cfg)
			mgr.CheckLockout(ident, cfg)
			mgr.GetLockedIdentifiers()
		}(i)
	}
	wg.Wait()
}
