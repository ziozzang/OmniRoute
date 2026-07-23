package domain

import (
	"sort"
	"sync"
	"time"
)

// fallbackpolicy.go — Declarative fallback chain for model routing.
// Port of src/domain/fallbackPolicy.ts
//
// When a primary provider is unavailable, the policy resolves to alternative
// providers in priority order. State is cached in-memory with an injectable
// DB seam for persistence.
//
// RACE NOTE: the TS original uses a module-level Map (single-threaded event
// loop). In Go the cache is guarded by a sync.RWMutex — reads (resolve,
// hasFallback) take RLock, writes (register, remove) take Lock.

// FallbackEntry is one provider in a fallback chain.
type FallbackEntry struct {
	Provider string
	Priority int  // lower = higher priority
	Enabled  bool
}

// FallbackStore is the injectable DB seam for fallback chain persistence.
type FallbackStore interface {
	SaveFallbackChain(model string, chain []FallbackEntry) error
	LoadAllFallbackChains() (map[string][]FallbackEntry, error)
	DeleteFallbackChain(model string) error
	DeleteAllFallbackChains() error
}

// FallbackManager manages per-model fallback chains.
type FallbackManager struct {
	mu     sync.RWMutex
	chains map[string][]FallbackEntry
	store  FallbackStore
	loaded bool
}

// NewFallbackManager creates a FallbackManager with an optional DB seam.
func NewFallbackManager(store FallbackStore) *FallbackManager {
	return &FallbackManager{
		chains: make(map[string][]FallbackEntry),
		store:  store,
	}
}

// ensureLoaded hydrates the cache from the DB once. Caller must hold write lock.
func (m *FallbackManager) ensureLoaded() {
	if m.loaded {
		return
	}
	if m.store != nil {
		if all, err := m.store.LoadAllFallbackChains(); err == nil {
			for model, chain := range all {
				m.chains[model] = chain
			}
		}
	}
	m.loaded = true
}

// RegisterFallback registers a fallback chain for a model, sorted by priority.
func (m *FallbackManager) RegisterFallback(model string, chain []FallbackEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLoaded()

	sorted := make([]FallbackEntry, len(chain))
	copy(sorted, chain)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Priority < sorted[j].Priority
	})

	m.chains[model] = sorted
	if m.store != nil {
		_ = m.store.SaveFallbackChain(model, sorted)
	}
}

// ResolveFallbackChain returns enabled providers for a model, sorted by
// priority, excluding the given providers.
func (m *FallbackManager) ResolveFallbackChain(model string, excludeProviders []string) []FallbackEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	chain, ok := m.chains[model]
	if !ok {
		return nil
	}
	exclude := make(map[string]bool, len(excludeProviders))
	for _, p := range excludeProviders {
		exclude[p] = true
	}
	var out []FallbackEntry
	for _, e := range chain {
		if e.Enabled && !exclude[e.Provider] {
			out = append(out, e)
		}
	}
	return out
}

// GetNextFallback returns the next provider in the chain, or "" if exhausted.
func (m *FallbackManager) GetNextFallback(model string, excludeProviders []string) string {
	chain := m.ResolveFallbackChain(model, excludeProviders)
	if len(chain) > 0 {
		return chain[0].Provider
	}
	return ""
}

// HasFallback returns whether a model has any enabled fallback providers.
func (m *FallbackManager) HasFallback(model string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	chain, ok := m.chains[model]
	if !ok {
		return false
	}
	for _, e := range chain {
		if e.Enabled {
			return true
		}
	}
	return false
}

// RemoveFallback removes a model's fallback chain. Returns true if removed.
func (m *FallbackManager) RemoveFallback(model string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLoaded()
	_, ok := m.chains[model]
	if ok {
		delete(m.chains, model)
		if m.store != nil {
			_ = m.store.DeleteFallbackChain(model)
		}
	}
	return ok
}

// GetAllFallbackChains returns all registered chains (for dashboard).
func (m *FallbackManager) GetAllFallbackChains() map[string][]FallbackEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string][]FallbackEntry, len(m.chains))
	for model, chain := range m.chains {
		result[model] = chain
	}
	return result
}

// ResetAllFallbacks clears all chains (for testing).
func (m *FallbackManager) ResetAllFallbacks() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chains = make(map[string][]FallbackEntry)
	m.loaded = false
	if m.store != nil {
		_ = m.store.DeleteAllFallbackChains()
	}
}

var _ = time.Now // keep time import if clock injection added later
