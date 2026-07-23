package quota

import (
	"sync"
	"time"
)

// ratelimit.go — fixed-window multi-rule rate limiter.
//
// Go port of src/shared/utils/rateLimiter.ts checkInMemoryRateLimit +
// RATE_LIMIT_SCRIPT semantics: a request is allowed only if EVERY rule's
// current fixed-window count is below its limit; on allow all rules increment.

// RateLimitRule is one (limit, window) constraint.
type RateLimitRule struct {
	Limit  int
	Window time.Duration
}

// RateLimitResult reports whether the request is allowed and which window
// blocked it (if any).
type RateLimitResult struct {
	Allowed       bool
	FailedWindow  time.Duration
}

// RateLimiter is a concurrency-safe fixed-window limiter.
type RateLimiter struct {
	mu    sync.Mutex
	store map[string]int
	now   func() time.Time
}

// NewRateLimiter returns an empty limiter using wall-clock time.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{store: make(map[string]int), now: time.Now}
}

func windowKey(keyID string, window time.Duration, now time.Time) string {
	ws := int64(window.Seconds())
	if ws <= 0 {
		ws = 1
	}
	wn := now.Unix() / ws
	return "rl:" + keyID + ":" + itoa(ws) + ":" + itoa(wn)
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// evictStale mirrors evictStaleRateLimitWindows: drop keys whose window ended.
func (r *RateLimiter) evictStale(now time.Time) {
	nowS := now.Unix()
	for k := range r.store {
		// k = rl:<id>:<windowSec>:<windowNum>
		// parse last two colon fields
		last := lastIndexByte(k, ':')
		if last < 0 {
			continue
		}
		second := lastIndexByte(k[:last], ':')
		if second < 0 {
			continue
		}
		ws := atoi(k[second+1 : last])
		wn := atoi(k[last+1:])
		if ws <= 0 {
			continue
		}
		if (wn+1)*ws <= nowS {
			delete(r.store, k)
		}
	}
}

func lastIndexByte(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func atoi(s string) int64 {
	var v int64
	neg := false
	i := 0
	if len(s) > 0 && s[0] == '-' {
		neg = true
		i = 1
	}
	for ; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0
		}
		v = v*10 + int64(s[i]-'0')
	}
	if neg {
		return -v
	}
	return v
}

const evictionThreshold = 50

// Check applies all rules atomically: allow only if every rule has headroom.
func (r *RateLimiter) Check(keyID string, rules []RateLimitRule) RateLimitResult {
	if len(rules) == 0 {
		return RateLimitResult{Allowed: true}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if len(r.store) > evictionThreshold {
		r.evictStale(now)
	}
	// First pass: check all limits.
	for _, rule := range rules {
		k := windowKey(keyID, rule.Window, now)
		if r.store[k] >= rule.Limit {
			return RateLimitResult{Allowed: false, FailedWindow: rule.Window}
		}
	}
	// Second pass: increment all.
	for _, rule := range rules {
		k := windowKey(keyID, rule.Window, now)
		r.store[k]++
	}
	return RateLimitResult{Allowed: true}
}
