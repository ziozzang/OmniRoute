package shared

import "strings"

// injectionseverity.go — Prompt-injection severity scoring / block threshold.
// Port of src/shared/utils/injectionSeverity.ts
//
// Kept separate from prompt injection sanitization to avoid circular imports.
// Pure functions — no race concerns.

// InjectionSeverity is the severity level of a prompt-injection detection.
type InjectionSeverity string

const (
	SeverityLow    InjectionSeverity = "low"
	SeverityMedium InjectionSeverity = "medium"
	SeverityHigh   InjectionSeverity = "high"
)

// SeverityScores maps each severity to a numeric score.
var SeverityScores = map[InjectionSeverity]int{
	SeverityLow:    1,
	SeverityMedium: 2,
	SeverityHigh:   3,
}

// DetectionLike is the minimal shape of an injection detection.
type DetectionLike struct {
	Severity string
}

// ShouldBlockDetections returns whether any detection meets the block threshold.
// Default threshold is "high" — medium patterns are observe-only unless lowered.
func ShouldBlockDetections(detections []DetectionLike, threshold InjectionSeverity) bool {
	minScore, ok := SeverityScores[threshold]
	if !ok {
		minScore = SeverityScores[SeverityHigh]
	}
	for _, d := range detections {
		sev := InjectionSeverity(d.Severity)
		if sev == "" {
			sev = SeverityHigh // TS: detection.severity || "high"
		}
		score, ok := SeverityScores[sev]
		if !ok {
			score = 0 // TS: || 0
		}
		if score >= minScore {
			return true
		}
	}
	return false
}

// ResolveBlockThreshold resolves the block threshold from an explicit value or
// env vars (INPUT_SANITIZER_BLOCK_THRESHOLD, INJECTION_GUARD_BLOCK_THRESHOLD).
// Allowed: low | medium | high (default high). envLookup is injectable for tests.
func ResolveBlockThreshold(explicit *string, envLookup func(string) string) InjectionSeverity {
	var raw string
	if explicit != nil && *explicit != "" {
		raw = *explicit
	} else if envLookup != nil {
		if v := envLookup("INPUT_SANITIZER_BLOCK_THRESHOLD"); v != "" {
			raw = v
		} else if v := envLookup("INJECTION_GUARD_BLOCK_THRESHOLD"); v != "" {
			raw = v
		}
	}
	if raw == "" {
		raw = "high"
	}
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch normalized {
	case "low", "medium", "high":
		return InjectionSeverity(normalized)
	default:
		return SeverityHigh
	}
}
