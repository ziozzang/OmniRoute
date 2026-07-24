package relay

import (
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

// bifrostcooldown.go — Bifrost failure cooldown registry.
// Port of src/app/api/v1/relay/chat/completions/bifrostCooldown.ts
//
// RACE NOTE: the cooldowns map is guarded by a sync.Mutex. The TS original
// uses a module-level Map on a single-threaded event loop; in Go concurrent
// reads and writes to the map must be serialized.

type cooldownEntry struct {
	until  int64 // epoch ms
	reason string
}

// ActiveBifrostCooldown reports an active cooldown.
type ActiveBifrostCooldown struct {
	RemainingMs int64
	Reason      string
}

// BifrostCooldownRegistry tracks per-baseURL failure cooldowns.
type BifrostCooldownRegistry struct {
	mu        sync.Mutex
	cooldowns map[string]*cooldownEntry
	now       func() int64 // epoch ms; injectable
}

// NewBifrostCooldownRegistry creates an empty registry.
func NewBifrostCooldownRegistry() *BifrostCooldownRegistry {
	return &BifrostCooldownRegistry{
		cooldowns: make(map[string]*cooldownEntry),
		now:       func() int64 { return time.Now().UnixMilli() },
	}
}

// SetClock overrides the clock (for deterministic testing).
func (r *BifrostCooldownRegistry) SetClock(now func() int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = now
}

// GetActiveCooldown returns the active cooldown for a base URL, or nil.
func (r *BifrostCooldownRegistry) GetActiveCooldown(baseURL string) *ActiveBifrostCooldown {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.cooldowns[baseURL]
	if entry == nil {
		return nil
	}
	if r.now == nil {
		r.now = func() int64 { return time.Now().UnixMilli() }
	}
	now := r.now()
	if entry.until <= now {
		delete(r.cooldowns, baseURL)
		return nil
	}
	return &ActiveBifrostCooldown{
		RemainingMs: entry.until - now,
		Reason:      entry.reason,
	}
}

// RecordFailure records a bifrost failure, starting a cooldown. The expiry is
// computed with saturating addition so an extreme cooldownMs cannot overflow
// int64 and wrap to a past timestamp (which would silently disable cooldown).
func (r *BifrostCooldownRegistry) RecordFailure(baseURL, reason string, cooldownMs int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cooldownMs <= 0 {
		delete(r.cooldowns, baseURL)
		return
	}
	if r.now == nil {
		r.now = func() int64 { return time.Now().UnixMilli() }
	}
	now := r.now()
	until := now + cooldownMs
	if until < now { // overflow → saturate to max
		until = math.MaxInt64
	}
	r.cooldowns[baseURL] = &cooldownEntry{until: until, reason: reason}
}

// ClearFailure clears a cooldown for a base URL.
func (r *BifrostCooldownRegistry) ClearFailure(baseURL string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cooldowns, baseURL)
}

// Reset clears all cooldowns.
func (r *BifrostCooldownRegistry) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cooldowns = make(map[string]*cooldownEntry)
}

// GetBifrostFailureCooldownMs reads OMNIROUTE_BIFROST_FAILURE_COOLDOWN_MS from
// env (default 5000).
func GetBifrostFailureCooldownMs(env map[string]string) int64 {
	raw := env["OMNIROUTE_BIFROST_FAILURE_COOLDOWN_MS"]
	if raw == "" {
		return 5000
	}
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || n < 0 {
		return 5000
	}
	return n
}
