package domain

import (
	"math/rand"
	"sync"
)

// comboresolver.go — Combo model resolution by strategy.
// Port of src/domain/comboResolver.ts
//
// RACE NOTE: round-robin counters are module-level state in TS (single-threaded
// event loop). In Go they are guarded by a sync.Mutex inside ComboResolver.

// ComboStep is a normalized combo model entry.
type ComboStep struct {
	Model  string
	Weight float64
}

// Combo is the minimal combo shape for resolution.
type Combo struct {
	ID       string
	Name     string
	Strategy string // priority | round-robin | random | least-used
	Models   []any  // raw entries; normalized via NormalizeComboSteps
}

// ComboResolution is the result of combo model resolution.
type ComboResolution struct {
	Model string
	Index int
}

// ComboResolver resolves combo models with persistent round-robin counters.
type ComboResolver struct {
	mu       sync.Mutex
	counters map[string]int // comboKey → round-robin counter
	rng      func() float64 // injectable RNG for deterministic testing
}

// NewComboResolver creates a ComboResolver.
func NewComboResolver() *ComboResolver {
	return &ComboResolver{
		counters: make(map[string]int),
		rng:      rand.Float64,
	}
}

// SetRNG overrides the RNG (for deterministic testing).
func (r *ComboResolver) SetRNG(rng func() float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rng = rng
}

// NormalizeComboSteps normalizes raw combo model entries to {model, weight}.
// Mirrors getComboStepTarget + getComboStepWeight from combos/steps.ts.
func NormalizeComboSteps(models []any) []ComboStep {
	var out []ComboStep
	for _, entry := range models {
		var model string
		var weight float64 = 1
		switch v := entry.(type) {
		case string:
			model = v
			weight = 0 // string entries have weight 0 per TS getComboStepWeight
		case map[string]any:
			if s, ok := v["model"].(string); ok {
				model = s
			}
			if w, ok := v["weight"].(float64); ok && w > 0 {
				weight = w
			}
		}
		if model != "" {
			out = append(out, ComboStep{Model: model, Weight: weight})
		}
	}
	return out
}

// ResolveComboModel resolves which model to use from a combo based on strategy.
func (r *ComboResolver) ResolveComboModel(combo Combo, modelUsageCounts map[string]int) (*ComboResolution, error) {
	normalized := NormalizeComboSteps(combo.Models)
	if len(normalized) == 0 {
		return nil, &comboError{msg: "combo \"" + combo.Name + "\" has no models configured"}
	}

	strategy := combo.Strategy
	if strategy == "" {
		strategy = "priority"
	}

	switch strategy {
	case "priority":
		return &ComboResolution{Model: normalized[0].Model, Index: 0}, nil

	case "round-robin":
		r.mu.Lock()
		comboKey := combo.ID
		if comboKey == "" {
			comboKey = combo.Name
		}
		if comboKey == "" {
			comboKey = "default"
		}
		counter := r.counters[comboKey]
		index := counter % len(normalized)
		r.counters[comboKey] = counter + 1
		r.mu.Unlock()
		return &ComboResolution{Model: normalized[index].Model, Index: index}, nil

	case "random":
		totalWeight := 0.0
		for _, m := range normalized {
			w := m.Weight
			if w <= 0 {
				w = 1
			}
			totalWeight += w
		}
		r.mu.Lock()
		randVal := r.rng() * totalWeight
		r.mu.Unlock()
		for i, m := range normalized {
			w := m.Weight
			if w <= 0 {
				w = 1
			}
			randVal -= w
			if randVal <= 0 {
				return &ComboResolution{Model: m.Model, Index: i}, nil
			}
		}
		return &ComboResolution{Model: normalized[0].Model, Index: 0}, nil

	case "least-used":
		minUsage := int(^uint(0) >> 1) // max int
		minIndex := 0
		for i, m := range normalized {
			usage := 0
			if modelUsageCounts != nil {
				usage = modelUsageCounts[m.Model]
			}
			if usage < minUsage {
				minUsage = usage
				minIndex = i
			}
		}
		return &ComboResolution{Model: normalized[minIndex].Model, Index: minIndex}, nil

	default:
		return &ComboResolution{Model: normalized[0].Model, Index: 0}, nil
	}
}

// GetComboFallbacks returns fallback models (all except primary, wrapping around).
func GetComboFallbacks(combo Combo, primaryIndex int) []string {
	var models []string
	for _, entry := range combo.Models {
		switch v := entry.(type) {
		case string:
			if v != "" {
				models = append(models, v)
			}
		case map[string]any:
			if s, ok := v["model"].(string); ok && s != "" {
				models = append(models, s)
			}
		}
	}
	if primaryIndex < 0 || primaryIndex >= len(models) {
		return models
	}
	// [...models.slice(primaryIndex+1), ...models.slice(0, primaryIndex)]
	var out []string
	out = append(out, models[primaryIndex+1:]...)
	out = append(out, models[:primaryIndex]...)
	return out
}

type comboError struct{ msg string }

func (e *comboError) Error() string { return e.msg }
