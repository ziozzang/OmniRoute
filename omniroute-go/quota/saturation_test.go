package quota

import (
	"errors"
	"sync"
	"testing"
)

// TestParseDurationMS verifies OpenAI-style duration parsing.
func TestParseDurationMS(t *testing.T) {
	cases := []struct {
		raw  string
		want int64
		ok   bool
	}{
		{"6m0s", 360_000, true},
		{"1s", 1000, true},
		{"1h30m15s", 5_415_000, true},
		{"1.5s", 1500, true},
		{"500ms", 500, true},
		{"", 0, false},
		{"garbage", 0, false},
	}
	for _, c := range cases {
		got, ok := parseDurationMS(c.raw)
		if ok != c.ok || got != c.want {
			t.Errorf("parseDurationMS(%q) = (%d, %v), want (%d, %v)", c.raw, got, ok, c.want, c.ok)
		}
	}
}

// TestNormalizeTokenReset verifies RFC3339 and duration normalization.
func TestNormalizeTokenReset(t *testing.T) {
	nowMS := int64(1_700_000_000_000) // fixed epoch for determinism

	// RFC3339 timestamp.
	rfc := "2026-01-01T00:00:30Z"
	got, ok := normalizeTokenReset(rfc, nowMS)
	if !ok || got <= 0 {
		t.Fatalf("RFC3339 parse failed: got=%d ok=%v", got, ok)
	}

	// OpenAI duration → now + parsed.
	dur, ok := normalizeTokenReset("6m0s", nowMS)
	if !ok || dur != nowMS+360_000 {
		t.Fatalf("duration parse: got=%d ok=%v, want %d", dur, ok, nowMS+360_000)
	}

	// Empty / unparseable → false.
	if _, ok := normalizeTokenReset("", nowMS); ok {
		t.Fatal("empty should return false")
	}
	if _, ok := normalizeTokenReset("garbage", nowMS); ok {
		t.Fatal("garbage should return false")
	}
}

// TestPickTokenTriple verifies header candidate selection.
func TestPickTokenTriple(t *testing.T) {
	headers := map[string]string{
		"x-ratelimit-limit-tokens":     "1000",
		"x-ratelimit-remaining-tokens": "200",
		"x-ratelimit-reset-tokens":     "6m0s",
	}
	candidates := []tokenTripleCandidate{
		{"anthropic-ratelimit-tokens-limit", "anthropic-ratelimit-tokens-remaining", "anthropic-ratelimit-tokens-reset"},
		{"x-ratelimit-limit-tokens", "x-ratelimit-remaining-tokens", "x-ratelimit-reset-tokens"},
	}
	limit, remaining, reset, ok := pickTokenTriple(headers, candidates)
	if !ok || limit != 1000 || remaining != 200 || reset != "6m0s" {
		t.Fatalf("pickTokenTriple = (%v, %v, %q, %v), want (1000, 200, 6m0s, true)", limit, remaining, reset, ok)
	}

	// No usable triple.
	_, _, _, ok = pickTokenTriple(map[string]string{}, candidates)
	if ok {
		t.Fatal("empty headers should return false")
	}
}

// TestStoreAndGetTokenHeaderSaturation verifies the round-trip.
func TestStoreAndGetTokenHeaderSaturation(t *testing.T) {
	s := NewSaturationStore()
	s.StoreRateLimitHeaders("conn1", "openai", map[string]string{
		"x-ratelimit-limit-tokens":     "1000",
		"x-ratelimit-remaining-tokens": "200",
		"x-ratelimit-reset-tokens":     "6m0s",
	})
	sat, resetAt, ok := s.GetTokenHeaderSaturation("openai", "conn1")
	if !ok {
		t.Fatal("expected fresh token header data")
	}
	// saturation = 1 - 200/1000 = 0.8
	if sat != 0.8 {
		t.Fatalf("saturation = %v, want 0.8", sat)
	}
	if resetAt <= 0 {
		t.Fatal("resetAt should be positive (now + 6m)")
	}

	// Missing connection → false.
	_, _, ok = s.GetTokenHeaderSaturation("openai", "conn-missing")
	if ok {
		t.Fatal("missing connection should return false")
	}
}

// TestGetSaturationCacheAndFailOpen verifies cache hit, fetcher dispatch, and
// fail-open on error.
func TestGetSaturationCacheAndFailOpen(t *testing.T) {
	s := NewSaturationStore()
	callCount := 0
	s.RegisterFetcher("test-provider", func(connID string, dim DimensionSpec, conn map[string]any) (float64, error) {
		callCount++
		if connID == "error-conn" {
			return 0, errors.New("upstream failure")
		}
		return 0.75, nil
	})

	dim := DimensionSpec{Unit: UnitTokens, Window: Window5H}

	// First call → fetcher invoked, cached.
	v := s.GetSaturation("conn1", "test-provider", dim, nil)
	if v != 0.75 || callCount != 1 {
		t.Fatalf("first call: v=%v callCount=%d, want 0.75, 1", v, callCount)
	}

	// Second call → cache hit, fetcher NOT invoked.
	v = s.GetSaturation("conn1", "test-provider", dim, nil)
	if v != 0.75 || callCount != 1 {
		t.Fatalf("cached call: v=%v callCount=%d, want 0.75, 1 (cache hit)", v, callCount)
	}

	// Error → fail-open to 0.
	v = s.GetSaturation("error-conn", "test-provider", dim, nil)
	if v != 0 {
		t.Fatalf("error should fail-open to 0, got %v", v)
	}

	// No fetcher registered → fail-open to 0.
	v = s.GetSaturation("conn1", "unknown-provider", dim, nil)
	if v != 0 {
		t.Fatalf("no fetcher should fail-open to 0, got %v", v)
	}
}

// TestSaturationStoreConcurrent is a -race stress test: many goroutines
// concurrently read/write all three caches (saturation, rate-limit headers,
// token headers) and invoke fetchers.
func TestSaturationStoreConcurrent(t *testing.T) {
	s := NewSaturationStore()
	s.RegisterFetcher("p1", func(string, DimensionSpec, map[string]any) (float64, error) {
		return 0.5, nil
	})
	dim := DimensionSpec{Unit: UnitTokens, Window: Window5H}

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				connID := "conn" + string(rune('A'+g%4))
				_ = s.GetSaturation(connID, "p1", dim, nil)
				s.StoreRateLimitHeaders(connID, "openai", map[string]string{
					"x-ratelimit-limit-tokens":     "1000",
					"x-ratelimit-remaining-tokens": "500",
				})
				_, _, _ = s.GetTokenHeaderSaturation("openai", connID)
				s.ClearSaturationCache()
				s.ClearRateLimitHeaders()
			}
		}(g)
	}
	wg.Wait()
}
