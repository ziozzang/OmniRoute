package relay

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// relaysecurity.go — Relay request security helpers.
// Port of src/app/api/v1/relay/chat/completions/relaySecurity.ts
//
// RACE NOTE: the IP rate-limit bucket map is guarded by a sync.Mutex. The TS
// original uses a module-level Map on a single-threaded event loop; in Go the
// check-and-increment is a read-modify-write that must be atomic.

// crlfRe matches CR/LF; ctrlRe matches remaining C0 control chars (NUL, ESC,
// tab, etc.) and Unicode line separators that can break log viewers.
var (
	crlfRe = regexp.MustCompile(`[\r\n]+`)
	ctrlRe = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f\x7f\x{2028}\x{2029}]`)
)

// SanitizeForensicHeader strips CR/LF and other control characters
// (log-injection / terminal-escape defence) and caps length UTF-8-safely.
// Forensic-only: client IP / user-agent come from untrusted headers.
func SanitizeForensicHeader(value string, max int) string {
	if max <= 0 {
		max = 256
	}
	if value == "" {
		return "unknown"
	}
	s := crlfRe.ReplaceAllString(value, " ")
	s = ctrlRe.ReplaceAllString(s, "")
	// UTF-8-safe truncation: don't split a multi-byte rune.
	if len(s) > max {
		s = s[:max]
		for len(s) > 0 && !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}

// GetClientIP extracts the client IP. Prefers RemoteAddr (the actual TCP peer,
// not spoofable) unless a trusted proxy is configured. Falls back to
// X-Forwarded-For / X-Real-IP only when trustProxy is true. This prevents
// XFF-spoofing rate-limit bypass when clients connect directly.
func GetClientIP(r *http.Request, trustProxy bool) string {
	if !trustProxy {
		// Use the actual peer address (host:port → host).
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			return SanitizeForensicHeader(host, 256)
		}
		return SanitizeForensicHeader(r.RemoteAddr, 256)
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first := strings.TrimSpace(strings.Split(xff, ",")[0])
		if first != "" {
			return SanitizeForensicHeader(first, 256)
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return SanitizeForensicHeader(xri, 256)
	}
	return "unknown"
}

var bearerRe = regexp.MustCompile(`(?i)^Bearer\s+(.+)$`)

// ExtractToken extracts a bearer token from the Authorization header, falling
// back to the X-Relay-Token header. Returns "" when absent.
func ExtractToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if m := bearerRe.FindStringSubmatch(auth); m != nil {
		return m[1]
	}
	return r.Header.Get("X-Relay-Token")
}

// HashToken returns the hex SHA-256 of a token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------------------
// In-memory per-(token,IP) rate limit
// ---------------------------------------------------------------------------

type ipBucket struct {
	count       int
	windowStart int64 // epoch seconds, floored to the minute
}

// IPRateLimiter is a defence-in-depth per-(token,IP) fixed-window limiter.
// Per instance only — behind a load balancer the effective ceiling is
// perMinute * replicas.
type IPRateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*ipBucket
	perMinute int
	now       func() int64 // epoch seconds; injectable
}

// NewIPRateLimiter creates a limiter. perMinute <= 0 disables limiting.
func NewIPRateLimiter(perMinute int) *IPRateLimiter {
	return &IPRateLimiter{
		buckets:   make(map[string]*ipBucket),
		perMinute: perMinute,
		now:       func() int64 { return time.Now().Unix() },
	}
}

// SetClock overrides the clock (for deterministic testing).
func (l *IPRateLimiter) SetClock(now func() int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.now = now
}

// IPRateLimitResult reports whether a request is allowed and seconds to reset.
type IPRateLimitResult struct {
	Allowed bool
	ResetIn int
}

// CheckIPRateLimit caps a single IP using a token to perMinute req/min.
func (l *IPRateLimiter) CheckIPRateLimit(tokenID, ip string) IPRateLimitResult {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.perMinute <= 0 {
		return IPRateLimitResult{Allowed: true, ResetIn: 0}
	}
	// Guard against zero-value construction (nil map / nil clock).
	if l.buckets == nil {
		l.buckets = make(map[string]*ipBucket)
	}
	if l.now == nil {
		l.now = func() int64 { return time.Now().Unix() }
	}

	now := l.now()
	windowStart := (now / 60) * 60
	key := tokenID + "|" + ip
	bucket := l.buckets[key]

	if bucket == nil || bucket.windowStart != windowStart {
		l.buckets[key] = &ipBucket{count: 1, windowStart: windowStart}
		// Bound memory: drop stale buckets past 10k.
		if len(l.buckets) > 10_000 {
			cutoff := windowStart - 60
			for k, b := range l.buckets {
				if b.windowStart < cutoff {
					delete(l.buckets, k)
				}
			}
		}
		return IPRateLimitResult{Allowed: true, ResetIn: int(60 - (now % 60))}
	}

	if bucket.count >= l.perMinute {
		return IPRateLimitResult{Allowed: false, ResetIn: int(60 - (now % 60))}
	}

	bucket.count++
	return IPRateLimitResult{Allowed: true, ResetIn: int(60 - (now % 60))}
}

// ParseIPPerMinute reads RELAY_IP_PER_MINUTE from an env map (default 30).
func ParseIPPerMinute(env map[string]string) int {
	raw := env["RELAY_IP_PER_MINUTE"]
	if raw == "" {
		return 30
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return 30
	}
	return n
}
