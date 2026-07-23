package domain

import (
	"sync"
	"time"
)

// lockoutpolicy.go — Account lockout logic (login attempt tracking).
// Port of src/domain/lockoutPolicy.ts
//
// Manages login attempt tracking and lockout decisions. The clock is
// injectable (now func) so tests drive time deterministically — the tested
// path never calls time.Now() implicitly.
//
// RACE NOTE: the TS original uses a module-level Map (single-threaded). In Go
// the cache is guarded by a sync.Mutex — every public method takes the lock.
// There is no nested locking.

// LockoutConfig tunes lockout behavior.
type LockoutConfig struct {
	MaxAttempts       int   // max failed attempts before lockout (default 5)
	LockoutDurationMs int64 // lockout duration (default 15 min)
	AttemptWindowMs   int64 // window for counting attempts (default 5 min)
}

// DefaultLockoutConfig is the default lockout configuration.
var DefaultLockoutConfig = LockoutConfig{
	MaxAttempts:       5,
	LockoutDurationMs: 15 * 60 * 1000,
	AttemptWindowMs:   5 * 60 * 1000,
}

// LockoutState is the per-identifier lockout state.
type LockoutState struct {
	Attempts    []int64 // epoch ms of recent failed attempts
	LockedUntil *int64  // epoch ms when lockout expires; nil = not locked
}

// LockoutStore is the injectable DB seam for lockout state persistence.
type LockoutStore interface {
	SaveLockoutState(identifier string, state LockoutState) error
	LoadLockoutState(identifier string) (*LockoutState, error)
	DeleteLockoutState(identifier string) error
	LoadAllLockedIdentifiers() ([]LockedIdentifierEntry, error)
}

// LockedIdentifierEntry is one locked identifier from the DB.
type LockedIdentifierEntry struct {
	Identifier  string
	LockedUntil int64
}

// LockoutCheckResult is the outcome of a lockout check.
type LockoutCheckResult struct {
	Locked      bool
	RemainingMs int64
	Attempts    int
}

// LockoutManager manages per-identifier lockout state.
type LockoutManager struct {
	mu    sync.Mutex
	cache map[string]*LockoutState
	store LockoutStore
	now   func() int64 // injectable clock (epoch ms)
}

// NewLockoutManager creates a LockoutManager with an optional DB seam.
func NewLockoutManager(store LockoutStore) *LockoutManager {
	return &LockoutManager{
		cache: make(map[string]*LockoutState),
		store: store,
		now:   func() int64 { return time.Now().UnixMilli() },
	}
}

// SetClock overrides the clock (for deterministic testing).
func (m *LockoutManager) SetClock(now func() int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.now = now
}

// getState loads state from cache or DB. Caller must hold the lock.
func (m *LockoutManager) getState(identifier string) *LockoutState {
	if s, ok := m.cache[identifier]; ok {
		return s
	}
	if m.store != nil {
		if fromDB, err := m.store.LoadLockoutState(identifier); err == nil && fromDB != nil {
			m.cache[identifier] = fromDB
			return fromDB
		}
	}
	return nil
}

// persistState writes state to cache and DB. Caller must hold the lock.
func (m *LockoutManager) persistState(identifier string, state *LockoutState) {
	m.cache[identifier] = state
	if m.store != nil {
		_ = m.store.SaveLockoutState(identifier, *state)
	}
}

// CheckLockout checks whether an identifier is currently locked out.
func (m *LockoutManager) CheckLockout(identifier string, config LockoutConfig) LockoutCheckResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.getState(identifier)
	if state == nil {
		return LockoutCheckResult{Locked: false, Attempts: 0}
	}

	now := m.now()

	// Active lockout?
	if state.LockedUntil != nil && now < *state.LockedUntil {
		return LockoutCheckResult{
			Locked:      true,
			RemainingMs: *state.LockedUntil - now,
			Attempts:    len(state.Attempts),
		}
	}

	// Clear expired lockout.
	if state.LockedUntil != nil {
		state.LockedUntil = nil
		state.Attempts = nil
		m.persistState(identifier, state)
	}

	// Count recent attempts within the window.
	windowStart := now - config.AttemptWindowMs
	recent := filterRecent(state.Attempts, windowStart)
	state.Attempts = recent
	m.persistState(identifier, state)

	return LockoutCheckResult{Locked: false, Attempts: len(recent)}
}

// RecordFailedAttempt records a failed attempt. Returns whether now locked.
func (m *LockoutManager) RecordFailedAttempt(identifier string, config LockoutConfig) LockoutCheckResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.getState(identifier)
	if state == nil {
		state = &LockoutState{}
	}

	now := m.now()
	windowStart := now - config.AttemptWindowMs
	state.Attempts = filterRecent(state.Attempts, windowStart)
	state.Attempts = append(state.Attempts, now)

	if len(state.Attempts) >= config.MaxAttempts {
		lockedUntil := now + config.LockoutDurationMs
		state.LockedUntil = &lockedUntil
		m.persistState(identifier, state)
		return LockoutCheckResult{Locked: true, RemainingMs: config.LockoutDurationMs}
	}

	m.persistState(identifier, state)
	return LockoutCheckResult{Locked: false}
}

// RecordSuccess clears history for an identifier (successful login).
func (m *LockoutManager) RecordSuccess(identifier string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.cache, identifier)
	if m.store != nil {
		_ = m.store.DeleteLockoutState(identifier)
	}
}

// ForceUnlock force-unlocks an identifier (admin action).
func (m *LockoutManager) ForceUnlock(identifier string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.cache, identifier)
	if m.store != nil {
		_ = m.store.DeleteLockoutState(identifier)
	}
}

// LockedIdentifier is one currently-locked identifier.
type LockedIdentifier struct {
	Identifier  string
	LockedUntil int64
	RemainingMs int64
}

// GetLockedIdentifiers returns all currently locked identifiers.
func (m *LockoutManager) GetLockedIdentifiers() []LockedIdentifier {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Merge DB into cache.
	if m.store != nil {
		if fromDB, err := m.store.LoadAllLockedIdentifiers(); err == nil {
			for _, entry := range fromDB {
				if _, ok := m.cache[entry.Identifier]; !ok {
					lu := entry.LockedUntil
					m.cache[entry.Identifier] = &LockoutState{LockedUntil: &lu}
				}
			}
		}
	}

	now := m.now()
	var locked []LockedIdentifier
	for id, state := range m.cache {
		if state.LockedUntil != nil && *state.LockedUntil > now {
			locked = append(locked, LockedIdentifier{
				Identifier:  id,
				LockedUntil: *state.LockedUntil,
				RemainingMs: *state.LockedUntil - now,
			})
		}
	}
	return locked
}

func filterRecent(attempts []int64, windowStart int64) []int64 {
	var out []int64
	for _, t := range attempts {
		if t > windowStart {
			out = append(out, t)
		}
	}
	return out
}
