package quota

import (
	"regexp"
	"sync"
	"time"
)

// buckets.go — Saturating per-connection, per-window buckets.
// Port of src/lib/quota/accountBuckets.ts
//
// Tracks whether a connection has hit 100% of a quota window (5h / 7d /
// per-model 7d). Each bucket is lazily reset: on read, if now >= resetsAt the
// entry is cleared and the connection is eligible again. No cron needed.
//
// Fail-open: missing entry → not saturated. All time input is injectable
// (the nowMs param) so unit tests drive the clock deterministically.
//
// RACE NOTE: the TS original uses a module-level Map and is single-threaded
// (event loop). In Go the read path (IsBucketSaturated) MUTATES the map via
// lazy reset (delete), so a plain sync.Mutex is required — an RWMutex would be
// incorrect because RLock holders would concurrently write. All public methods
// take the single mutex; there is no nested locking.

// SaturationThresholdPct is the utilization (0..100) at/above which a window is
// considered saturated. 100 = exhausted.
const SaturationThresholdPct = 100

// bucketEntry is in-process state for one (connectionId, windowKey) pair.
type bucketEntry struct {
	saturated  bool
	resetsAtMs int64 // 0 when the reset instant is unknown
}

// UsageQuotaSlim is the minimal shape of a parsed UsageQuota entry.
// Used = % consumed (0..100); ResetAt = ISO 8601 string or "" (unknown).
type UsageQuotaSlim struct {
	Used    float64
	ResetAt string // ISO 8601 or "" when unknown
}

// ClaudeUsageResult is the partial shape of getClaudeUsage() this module needs.
type ClaudeUsageResult struct {
	Quotas map[string]UsageQuotaSlim
}

// BucketStore is a concurrency-safe saturating-bucket store.
type BucketStore struct {
	mu      sync.Mutex
	buckets map[string]*bucketEntry // key: connectionId::windowKey
	now     func() int64            // epoch ms; injectable
}

// NewBucketStore returns an empty store using wall-clock time.
func NewBucketStore() *BucketStore {
	return &BucketStore{buckets: make(map[string]*bucketEntry), now: func() int64 { return time.Now().UnixMilli() }}
}

func bucketKey(connectionID, windowKey string) string {
	return connectionID + "::" + windowKey
}

// parseResetAtMs parses an ISO 8601 resetAt to epoch ms. Returns 0 on any
// failure (unknown reset → lazy reset cannot fire for that bucket).
func parseResetAtMs(resetAt string) int64 {
	if resetAt == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339, resetAt)
	if err != nil {
		return 0
	}
	ms := t.UnixMilli()
	if ms <= 0 {
		return 0
	}
	return ms
}

// IsBucketSaturated reports whether the bucket for (connectionId, windowKey) is
// currently saturated.
//
// Lazy reset: if nowMs >= entry.resetsAtMs (and resetsAtMs > 0) the entry is
// cleared and false is returned. Fail-open: a missing entry returns false.
//
// If nowMs < 0 the store's injected clock is used.
func (s *BucketStore) IsBucketSaturated(connectionID, windowKey string, nowMs int64) bool {
	if connectionID == "" || windowKey == "" {
		return false // fail-open
	}
	if nowMs < 0 {
		nowMs = s.now()
	}
	key := bucketKey(connectionID, windowKey)

	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.buckets[key]
	if entry == nil {
		return false // fail-open
	}
	// Lazy reset: the window rolled over → the saturation is stale.
	if entry.resetsAtMs > 0 && nowMs >= entry.resetsAtMs {
		delete(s.buckets, key)
		return false
	}
	return entry.saturated
}

// RecordUsage records a usage observation for one (connectionId, windowKey).
//
// Marks saturated when usedPct >= SaturationThresholdPct and the window has NOT
// already rolled over. Below threshold (or past reset) clears any existing
// entry so eligibility is restored promptly.
//
// If nowMs < 0 the store's injected clock is used.
func (s *BucketStore) RecordUsage(connectionID, windowKey string, usedPct float64, resetAt string, nowMs int64) {
	if connectionID == "" || windowKey == "" {
		return
	}
	if nowMs < 0 {
		nowMs = s.now()
	}
	key := bucketKey(connectionID, windowKey)
	resetsAtMs := parseResetAtMs(resetAt)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Stale signal: the window already reset — discard state and bail.
	if resetsAtMs > 0 && nowMs >= resetsAtMs {
		delete(s.buckets, key)
		return
	}

	saturated := usedPct >= SaturationThresholdPct
	if !saturated {
		delete(s.buckets, key)
		return
	}
	s.buckets[key] = &bucketEntry{saturated: true, resetsAtMs: resetsAtMs}
}

var weeklyModelRe = regexp.MustCompile(`^weekly (.+) \(7d\)$`)

// UpdateAccountBuckets parses the quotas map from a getClaudeUsage() result and
// records each known window into its bucket.
//
// Window key mapping:
//
//	"session (5h)"        → "5h"
//	"weekly (7d)"         → "7d"
//	"weekly <model> (7d)" → "7d:<model>"
//
// Fail-open: nil result, missing quotas, or malformed entries are skipped.
// If nowMs < 0 the store's injected clock is used.
func (s *BucketStore) UpdateAccountBuckets(connectionID string, usage *ClaudeUsageResult, nowMs int64) {
	if connectionID == "" || usage == nil || usage.Quotas == nil {
		return
	}
	if nowMs < 0 {
		nowMs = s.now()
	}
	// Fixed windows — only when the key is actually present in the response.
	// TS parity: processQuotaEntry does `if (!entry) return;`, so a missing key
	// is a no-op and must NOT clear an existing saturation. A Go map lookup of
	// an absent key yields the zero value {Used:0}; passing that to RecordUsage
	// would wrongly delete the bucket (below-threshold path). Guard on presence.
	if e, ok := usage.Quotas["session (5h)"]; ok {
		s.processQuotaEntry(connectionID, "5h", e, nowMs)
	}
	if e, ok := usage.Quotas["weekly (7d)"]; ok {
		s.processQuotaEntry(connectionID, "7d", e, nowMs)
	}
	// Per-model weekly windows.
	for key, entry := range usage.Quotas {
		if m := weeklyModelRe.FindStringSubmatch(key); m != nil && m[1] != "" {
			s.processQuotaEntry(connectionID, "7d:"+m[1], entry, nowMs)
		}
	}
}

func (s *BucketStore) processQuotaEntry(connectionID, windowKey string, entry UsageQuotaSlim, nowMs int64) {
	s.RecordUsage(connectionID, windowKey, entry.Used, entry.ResetAt, nowMs)
}

// BucketCount returns the current bucket count (test helper).
func (s *BucketStore) BucketCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.buckets)
}
