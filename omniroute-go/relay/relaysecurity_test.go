package relay

import (
	"net/http/httptest"
	"sync"
	"testing"
)

func TestSanitizeForensicHeader(t *testing.T) {
	if got := SanitizeForensicHeader("", 256); got != "unknown" {
		t.Fatalf("empty → unknown, got %q", got)
	}
	if got := SanitizeForensicHeader("a\r\nb\nc", 256); got != "a b c" {
		t.Fatalf("CRLF stripped, got %q", got)
	}
	if got := SanitizeForensicHeader("abcdef", 3); got != "abc" {
		t.Fatalf("capped, got %q", got)
	}
}

func TestGetClientIPTrustedProxy(t *testing.T) {
	// trustProxy=true → honor X-Forwarded-For (first entry).
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	if got := GetClientIP(r, true); got != "1.2.3.4" {
		t.Fatalf("xff first, got %q", got)
	}
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.Header.Set("X-Real-IP", "9.9.9.9")
	if got := GetClientIP(r2, true); got != "9.9.9.9" {
		t.Fatalf("x-real-ip, got %q", got)
	}
	r3 := httptest.NewRequest("GET", "/", nil)
	if got := GetClientIP(r3, true); got != "unknown" {
		t.Fatalf("none → unknown, got %q", got)
	}
}

func TestGetClientIPDefaultUsesRemoteAddr(t *testing.T) {
	// trustProxy=false (default) → ignore spoofable XFF, use the real peer.
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.7:54321"
	r.Header.Set("X-Forwarded-For", "1.2.3.4") // attacker-supplied, must be ignored
	if got := GetClientIP(r, false); got != "203.0.113.7" {
		t.Fatalf("default should use RemoteAddr host, got %q", got)
	}
}

func TestExtractToken(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer abc123")
	if got := ExtractToken(r); got != "abc123" {
		t.Fatalf("bearer, got %q", got)
	}
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.Header.Set("X-Relay-Token", "xyz")
	if got := ExtractToken(r2); got != "xyz" {
		t.Fatalf("x-relay-token, got %q", got)
	}
	r3 := httptest.NewRequest("GET", "/", nil)
	if got := ExtractToken(r3); got != "" {
		t.Fatalf("none → empty, got %q", got)
	}
}

func TestHashToken(t *testing.T) {
	h := HashToken("secret")
	if len(h) != 64 {
		t.Fatalf("sha256 hex = 64 chars, got %d", len(h))
	}
	if HashToken("secret") != h {
		t.Fatal("deterministic")
	}
}

func TestIPRateLimiter(t *testing.T) {
	rl := NewIPRateLimiter(3)
	now := int64(1_700_000_000)
	rl.SetClock(func() int64 { return now })

	for i := 0; i < 3; i++ {
		res := rl.CheckIPRateLimit("tok", "1.2.3.4")
		if !res.Allowed {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
	}
	res := rl.CheckIPRateLimit("tok", "1.2.3.4")
	if res.Allowed {
		t.Fatal("4th should be blocked")
	}
	// different IP is independent
	if !rl.CheckIPRateLimit("tok", "5.6.7.8").Allowed {
		t.Fatal("different IP should be allowed")
	}
	// next minute resets
	now += 61
	if !rl.CheckIPRateLimit("tok", "1.2.3.4").Allowed {
		t.Fatal("next minute should reset")
	}
}

func TestIPRateLimiterDisabled(t *testing.T) {
	rl := NewIPRateLimiter(0)
	for i := 0; i < 100; i++ {
		if !rl.CheckIPRateLimit("t", "ip").Allowed {
			t.Fatal("disabled → always allowed")
		}
	}
}

func TestIPRateLimiterConcurrent(t *testing.T) {
	rl := NewIPRateLimiter(1000)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				rl.CheckIPRateLimit("tok", "ip")
			}
		}()
	}
	wg.Wait()
}

func TestParseIPPerMinute(t *testing.T) {
	if ParseIPPerMinute(map[string]string{}) != 30 {
		t.Fatal("default 30")
	}
	if ParseIPPerMinute(map[string]string{"RELAY_IP_PER_MINUTE": "50"}) != 50 {
		t.Fatal("50")
	}
	if ParseIPPerMinute(map[string]string{"RELAY_IP_PER_MINUTE": "abc"}) != 30 {
		t.Fatal("invalid → 30")
	}
}
