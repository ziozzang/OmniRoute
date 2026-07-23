package quota

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// saturation.go — Global saturation signal (0..1) per connection+dimension.
// Port of src/lib/quota/saturationSignals.ts
//
// Strategy: provider-specific fetchers (codex, bailian, anthropic, generic)
// behind injectable interfaces. A 30s TTL in-memory cache prevents per-request
// polling of rate-limited upstream endpoints. Fail-open: any error → 0.
//
// RACE NOTE: three maps (_cache, _rateLimitHeaders, _tokenHeaders) are accessed
// concurrently by the hot path (getSaturation reads/writes _cache) and response
// handlers (storeRateLimitHeaders writes _rateLimitHeaders/_tokenHeaders). All
// three are guarded by a single sync.Mutex. No nested locking.

const (
	// CacheTTLMS is the saturation cache TTL. The 30s TTL keeps rate-limited
	// upstream endpoints (e.g. anthropic oauth/usage) from being polled per
	// request — they return 429 under load.
	CacheTTLMS = 30_000
	// RLHeaderTTLMS is the rate-limit header cache TTL (5 minutes).
	RLHeaderTTLMS = 5 * 60 * 1000
)

// DimensionSpec identifies a quota dimension for saturation lookup.
type DimensionSpec struct {
	Unit   QuotaUnit
	Window QuotaWindow
}

// SaturationFetcher is the injectable seam for provider-specific saturation
// fetchers. Each provider registers a fetcher; the default returns 0 (fail-open).
type SaturationFetcher func(connectionID string, dim DimensionSpec, connection map[string]any) (float64, error)

// cacheEntry is one cached saturation value.
type cacheEntry struct {
	value float64 // 0..1
	ts    int64   // epoch ms
}

// rateLimitHeaderEntry is a per-minute REQUEST rate-limit header snapshot.
type rateLimitHeaderEntry struct {
	limit     float64
	remaining float64
	ts        int64
}

// tokenHeaderEntry is a TOKEN rate-limit header snapshot (rides on every
// upstream response, enabling proactive throttling before a 429).
type tokenHeaderEntry struct {
	limit     float64
	remaining float64
	resetAt   int64 // epoch ms; 0 when unknown
	ts        int64
}

// SaturationStore is a concurrency-safe saturation signal store with caching.
type SaturationStore struct {
	mu              sync.Mutex
	cache           map[string]cacheEntry
	rateLimitHdrs   map[string]rateLimitHeaderEntry
	tokenHdrs       map[string]tokenHeaderEntry
	fetchers        map[string]SaturationFetcher // provider → fetcher
	now             func() int64                 // injectable clock (epoch ms)
}

// NewSaturationStore returns an empty store with wall-clock time.
func NewSaturationStore() *SaturationStore {
	return &SaturationStore{
		cache:         make(map[string]cacheEntry),
		rateLimitHdrs: make(map[string]rateLimitHeaderEntry),
		tokenHdrs:     make(map[string]tokenHeaderEntry),
		fetchers:      make(map[string]SaturationFetcher),
		now:           func() int64 { return time.Now().UnixMilli() },
	}
}

// RegisterFetcher sets the saturation fetcher for a provider slug.
func (s *SaturationStore) RegisterFetcher(provider string, f SaturationFetcher) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fetchers[provider] = f
}

// GetSaturation returns the current global saturation signal (0..1) for a
// connection+dimension. A value of 0 means "no saturation detected" (generous
// mode allowed). Always fail-open: returns 0 on any error.
func (s *SaturationStore) GetSaturation(connectionID, provider string, dim DimensionSpec, connection map[string]any) float64 {
	key := saturationCacheKey(connectionID, provider, dim)
	now := s.now()

	s.mu.Lock()
	if cached, ok := s.cache[key]; ok && now-cached.ts < CacheTTLMS {
		s.mu.Unlock()
		return cached.value
	}
	s.mu.Unlock()

	// Fetch outside the lock to avoid holding it during I/O.
	value := s.fetchSaturation(connectionID, provider, dim, connection)

	s.mu.Lock()
	s.cache[key] = cacheEntry{value: value, ts: s.now()}
	s.mu.Unlock()
	return value
}

func (s *SaturationStore) fetchSaturation(connectionID, provider string, dim DimensionSpec, connection map[string]any) float64 {
	s.mu.Lock()
	fetcher := s.fetchers[provider]
	s.mu.Unlock()

	if fetcher == nil {
		return 0 // fail-open: no fetcher registered
	}
	v, err := fetcher(connectionID, dim, connection)
	if err != nil {
		return 0 // fail-open
	}
	return clamp01(v)
}

func saturationCacheKey(connectionID, provider string, dim DimensionSpec) string {
	return provider + ":" + connectionID + ":" + string(dim.Unit) + ":" + string(dim.Window)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// ---------------------------------------------------------------------------
// Rate-limit header parsing (pure functions, no state)
// ---------------------------------------------------------------------------

var durationRe = regexp.MustCompile(`(\d+(?:\.\d+)?)(ms|h|m|s)`)

// parseDurationMS parses an OpenAI-style duration string ("6m0s", "1.5s") into
// milliseconds. Returns (0, false) when unparseable.
func parseDurationMS(raw string) (int64, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, false
	}
	matches := durationRe.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return 0, false
	}
	var total int64
	for _, m := range matches {
		val, err := strconv.ParseFloat(m[1], 64)
		if err != nil || math.IsNaN(val) || math.IsInf(val, 0) {
			return 0, false
		}
		switch m[2] {
		case "h":
			total += int64(val * 3_600_000)
		case "m":
			total += int64(val * 60_000)
		case "s":
			total += int64(val * 1000)
		case "ms":
			total += int64(val)
		}
	}
	return total, true
}

var isoDateRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
var isoTimeSepRe = regexp.MustCompile(`[T:]`)

// normalizeTokenReset normalizes a token-reset header value to epoch ms.
//   - Anthropic: RFC3339 timestamp → time.Parse
//   - OpenAI: duration ("6m0s") → now + parsed ms
//
// Returns (0, false) when absent or unparseable.
func normalizeTokenReset(raw string, nowMS int64) (int64, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, false
	}
	// RFC3339 / ISO-8601 if it looks like a date with a time separator.
	if isoDateRe.MatchString(s) && isoTimeSepRe.MatchString(s) {
		t, err := time.Parse(time.RFC3339, s)
		if err == nil {
			return t.UnixMilli(), true
		}
	}
	// Otherwise treat as an OpenAI-style duration relative to now.
	durMS, ok := parseDurationMS(s)
	if !ok {
		return 0, false
	}
	return nowMS + durMS, true
}

// tokenTripleCandidate is one set of header keys for a token rate-limit triple.
type tokenTripleCandidate struct {
	limit     string
	remaining string
	reset     string
}

// pickTokenTriple picks the first usable {limit, remaining, reset} triple from
// a list of header-key candidates. Returns (0, 0, "", false) when none are usable.
func pickTokenTriple(headers map[string]string, candidates []tokenTripleCandidate) (limit, remaining float64, reset string, ok bool) {
	for _, c := range candidates {
		limitStr, lok := headers[c.limit]
		remainingStr, rok := headers[c.remaining]
		if !lok || !rok {
			continue
		}
		l, err1 := strconv.ParseFloat(limitStr, 64)
		r, err2 := strconv.ParseFloat(remainingStr, 64)
		if err1 != nil || err2 != nil || math.IsNaN(l) || math.IsNaN(r) || math.IsInf(l, 0) || math.IsInf(r, 0) {
			continue
		}
		if l > 0 {
			return l, r, headers[c.reset], true
		}
	}
	return 0, 0, "", false
}

// ---------------------------------------------------------------------------
// Rate-limit header storage (response handler → saturation signal)
// ---------------------------------------------------------------------------

// StoreRateLimitHeaders stores rate-limit headers from an upstream response.
// Called by the response handler after a successful request. Captures two
// independent signals keyed `${provider}:${connectionId}`:
//   - REQUEST headers (per-minute RPM burst) — legacy, anthropic fallback.
//   - TOKEN headers (per-window TPM) — universal proactive saturation.
func (s *SaturationStore) StoreRateLimitHeaders(connectionID, provider string, headers map[string]string) {
	key := provider + ":" + connectionID
	now := s.now()

	// REQUEST headers (legacy path).
	limitStr := firstPresent(headers,
		"anthropic-ratelimit-requests-limit",
		"x-ratelimit-limit-requests",
		"x-ratelimit-limit")
	remainingStr := firstPresent(headers,
		"anthropic-ratelimit-requests-remaining",
		"x-ratelimit-remaining-requests",
		"x-ratelimit-remaining")

	if limitStr != "" && remainingStr != "" {
		l, err1 := strconv.ParseFloat(limitStr, 64)
		r, err2 := strconv.ParseFloat(remainingStr, 64)
		if err1 == nil && err2 == nil && l > 0 && !math.IsNaN(l) && !math.IsNaN(r) {
			s.mu.Lock()
			s.rateLimitHdrs[key] = rateLimitHeaderEntry{limit: l, remaining: r, ts: now}
			s.mu.Unlock()
		}
	}

	// TOKEN headers (universal proactive saturation).
	limit, remaining, reset, ok := pickTokenTriple(headers, []tokenTripleCandidate{
		{"anthropic-ratelimit-tokens-limit", "anthropic-ratelimit-tokens-remaining", "anthropic-ratelimit-tokens-reset"},
		{"x-ratelimit-limit-tokens", "x-ratelimit-remaining-tokens", "x-ratelimit-reset-tokens"},
		{"anthropic-ratelimit-input-tokens-limit", "anthropic-ratelimit-input-tokens-remaining", "anthropic-ratelimit-input-tokens-reset"},
		{"anthropic-ratelimit-output-tokens-limit", "anthropic-ratelimit-output-tokens-remaining", "anthropic-ratelimit-output-tokens-reset"},
	})
	if ok {
		resetAt, _ := normalizeTokenReset(reset, now)
		s.mu.Lock()
		s.tokenHdrs[key] = tokenHeaderEntry{limit: limit, remaining: remaining, resetAt: resetAt, ts: now}
		s.mu.Unlock()
	}
}

// GetTokenHeaderSaturation returns the token-header saturation signal for a
// (provider, connectionId). Returns (saturation, resetAt, true) when fresh data
// exists, or (0, 0, false) when no fresh token-header data is available.
func (s *SaturationStore) GetTokenHeaderSaturation(provider, connectionID string) (saturation float64, resetAt int64, ok bool) {
	key := provider + ":" + connectionID
	now := s.now()

	s.mu.Lock()
	entry, found := s.tokenHdrs[key]
	s.mu.Unlock()

	if !found || now-entry.ts > RLHeaderTTLMS || entry.limit <= 0 {
		return 0, 0, false
	}
	used := entry.limit - entry.remaining
	sat := clamp01(used / entry.limit)
	return sat, entry.resetAt, true
}

// anthropicHeaderSaturation returns the per-minute REQUEST rate-limit header
// saturation for an anthropic connection. Used only as a fallback when OAuth
// plan-window utilization is unavailable.
func (s *SaturationStore) anthropicHeaderSaturation(connectionID string) float64 {
	key := "anthropic:" + connectionID
	now := s.now()

	s.mu.Lock()
	entry, found := s.rateLimitHdrs[key]
	s.mu.Unlock()

	if !found || now-entry.ts > RLHeaderTTLMS || entry.limit <= 0 {
		return 0
	}
	used := entry.limit - entry.remaining
	return clamp01(used / entry.limit)
}

func firstPresent(headers map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := headers[k]; ok && v != "" {
			return v
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// ClearSaturationCache clears the saturation cache (test isolation).
func (s *SaturationStore) ClearSaturationCache() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache = make(map[string]cacheEntry)
}

// ClearRateLimitHeaders clears the rate-limit + token header caches.
func (s *SaturationStore) ClearRateLimitHeaders() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rateLimitHdrs = make(map[string]rateLimitHeaderEntry)
	s.tokenHdrs = make(map[string]tokenHeaderEntry)
}
