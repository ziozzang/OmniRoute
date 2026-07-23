package domain

import (
	"sort"
	"sync"
	"time"
)

// degradation.go — Graceful degradation framework.
// Port of src/domain/degradation.ts
//
// Provides a standardized pattern for services that depend on external
// systems to degrade capability instead of failing completely.
// Hierarchy: Full → Reduced → Minimal → Safe Default
//
// RACE NOTE: the global registry is guarded by a sync.RWMutex.

// DegradationLevel represents the operational level of a service.
type DegradationLevel string

const (
	DegradationFull    DegradationLevel = "full"
	DegradationReduced DegradationLevel = "reduced"
	DegradationMinimal DegradationLevel = "minimal"
	DegradationDefault DegradationLevel = "default"
)

// DegradationStatus is a status report for a degraded service.
type DegradationStatus struct {
	Level      DegradationLevel
	Feature    string
	Capability string
	Reason     string
	Since      string // ISO 8601 timestamp
}

// DegradedResult wraps a result with degradation info.
type DegradedResult[T any] struct {
	Result T
	Status DegradationStatus
}

// ---------------------------------------------------------------------------
// Global degradation registry
// ---------------------------------------------------------------------------

var (
	registryMu sync.RWMutex
	registry   = make(map[string]DegradationStatus)
)

func updateRegistry(feature string, status DegradationStatus) {
	registryMu.Lock()
	defer registryMu.Unlock()
	existing, ok := registry[feature]
	if ok && existing.Level == status.Level {
		status.Since = existing.Since
	}
	registry[feature] = status
}

// WithDegradationSync executes an operation with graceful degradation.
// Tries primary first, then fallback, then returns safeDefault.
func WithDegradationSync[T any](
	feature string,
	primary func() (T, error),
	fallback func() (T, error),
	safeDefault T,
	opts *DegradationOptions,
) DegradedResult[T] {
	now := time.Now().UTC().Format(time.RFC3339)

	// Try primary.
	result, primaryErr := primary()
	if primaryErr == nil {
		status := DegradationStatus{
			Level:      DegradationFull,
			Feature:    feature,
			Capability: optOr(opts, "full", "Full capability"),
			Reason:     "",
			Since:      now,
		}
		updateRegistry(feature, status)
		return DegradedResult[T]{Result: result, Status: status}
	}

	// Primary failed, try fallback.
	result, fallbackErr := fallback()
	if fallbackErr == nil {
		status := DegradationStatus{
			Level:      DegradationReduced,
			Feature:    feature,
			Capability: optOr(opts, "reduced", "Reduced capability (fallback active)"),
			Reason:     primaryErr.Error(),
			Since:      now,
		}
		updateRegistry(feature, status)
		if opts != nil && opts.OnDegrade != nil {
			opts.OnDegrade(status)
		}
		return DegradedResult[T]{Result: result, Status: status}
	}

	// Both failed, return safe default.
	reason := primaryErr.Error() + " → " + fallbackErr.Error()
	status := DegradationStatus{
		Level:      DegradationDefault,
		Feature:    feature,
		Capability: optOr(opts, "default", "Safe default (all backends unavailable)"),
		Reason:     reason,
		Since:      now,
	}
	updateRegistry(feature, status)
	if opts != nil && opts.OnDegrade != nil {
		opts.OnDegrade(status)
	}
	return DegradedResult[T]{Result: safeDefault, Status: status}
}

// DegradationOptions configures degradation behavior.
type DegradationOptions struct {
	FullCapability    string
	ReducedCapability string
	DefaultCapability string
	OnDegrade         func(DegradationStatus)
}

func optOr(opts *DegradationOptions, key, defaultVal string) string {
	if opts == nil {
		return defaultVal
	}
	switch key {
	case "full":
		if opts.FullCapability != "" {
			return opts.FullCapability
		}
	case "reduced":
		if opts.ReducedCapability != "" {
			return opts.ReducedCapability
		}
	case "default":
		if opts.DefaultCapability != "" {
			return opts.DefaultCapability
		}
	}
	return defaultVal
}

// ---------------------------------------------------------------------------
// Registry queries
// ---------------------------------------------------------------------------

var levelOrder = map[DegradationLevel]int{
	DegradationDefault: 0,
	DegradationMinimal: 1,
	DegradationReduced: 2,
	DegradationFull:    3,
}

// GetDegradationReport returns all tracked features sorted by severity.
func GetDegradationReport() []DegradationStatus {
	registryMu.RLock()
	defer registryMu.RUnlock()
	var out []DegradationStatus
	for _, s := range registry {
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		oi, oki := levelOrder[out[i].Level]
		oj, okj := levelOrder[out[j].Level]
		if !oki {
			oi = 4
		}
		if !okj {
			oj = 4
		}
		return oi < oj
	})
	return out
}

// GetFeatureStatus returns the status for a specific feature.
func GetFeatureStatus(feature string) *DegradationStatus {
	registryMu.RLock()
	defer registryMu.RUnlock()
	s, ok := registry[feature]
	if !ok {
		return nil
	}
	return &s
}

// HasAnyDegradation returns true if any feature is not at full capability.
func HasAnyDegradation() bool {
	registryMu.RLock()
	defer registryMu.RUnlock()
	for _, s := range registry {
		if s.Level != DegradationFull {
			return true
		}
	}
	return false
}

// GetDegradationSummary returns counts at each degradation level.
func GetDegradationSummary() map[DegradationLevel]int {
	registryMu.RLock()
	defer registryMu.RUnlock()
	summary := map[DegradationLevel]int{
		DegradationFull:    0,
		DegradationReduced: 0,
		DegradationMinimal: 0,
		DegradationDefault: 0,
	}
	for _, s := range registry {
		summary[s.Level]++
	}
	return summary
}

// ResetDegradationRegistry clears the registry (for testing).
func ResetDegradationRegistry() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = make(map[string]DegradationStatus)
}
