package domain

import (
	"regexp"
	"strings"
)

// connectionmodelrules.go — Per-connection model exclusion rules.
// Port of src/domain/connectionModelRules.ts
//
// Pure, stateless functions — no race concerns.

// ConnectionLike is the minimal connection shape for model eligibility checks.
type ConnectionLike struct {
	ProviderSpecificData any
}

func normalizePattern(value any) string {
	s, ok := value.(string)
	if !ok {
		return ""
	}
	n := strings.TrimSpace(s)
	if n == "" || n == "**" {
		return ""
	}
	return n
}

func toPatternList(value any) []string {
	var raw []any
	switch v := value.(type) {
	case []any:
		raw = v
	case []string:
		for _, s := range v {
			raw = append(raw, s)
		}
	case string:
		for _, part := range strings.Split(v, ",") {
			raw = append(raw, part)
		}
	default:
		return nil
	}
	var out []string
	for _, r := range raw {
		if p := normalizePattern(r); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func uniquePatterns(patterns []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range patterns {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

func getModelMatchCandidates(modelID string) []string {
	normalized := strings.TrimSpace(modelID)
	if normalized == "" {
		return nil
	}
	withoutExtended := normalized
	if strings.HasSuffix(normalized, "[1m]") {
		withoutExtended = normalized[:len(normalized)-4]
	}
	rawModel := withoutExtended
	if idx := strings.LastIndex(withoutExtended, "/"); idx >= 0 {
		rawModel = withoutExtended[idx+1:]
	}
	seen := map[string]bool{}
	var out []string
	for _, c := range []string{normalized, withoutExtended, rawModel} {
		if c != "" && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// WildcardMatch matches a model id against a glob pattern (* and ? wildcards,
// case-insensitive). Port of open-sse wildcardMatch.
func WildcardMatch(model, pattern string) bool {
	if model == "" || pattern == "" {
		return false
	}
	if pattern == "*" || pattern == model {
		return true
	}
	var sb strings.Builder
	for _, r := range pattern {
		switch r {
		case '*':
			sb.WriteString(".*")
		case '?':
			sb.WriteString(".")
		case '.', '+', '^', '$', '{', '}', '(', ')', '|', '[', ']', '\\':
			sb.WriteRune('\\')
			sb.WriteRune(r)
		default:
			sb.WriteRune(r)
		}
	}
	re, err := regexp.Compile("(?i)^" + sb.String() + "$")
	if err != nil {
		return false
	}
	return re.MatchString(model)
}

// NormalizeExcludedModelPatterns normalizes a raw excluded-models value.
func NormalizeExcludedModelPatterns(value any) []string {
	return uniquePatterns(toPatternList(value))
}

// GetConnectionExcludedModels extracts excluded model patterns from provider data.
func GetConnectionExcludedModels(providerSpecificData any) []string {
	m, _ := providerSpecificData.(map[string]any)
	if m == nil {
		return nil
	}
	v := m["excludedModels"]
	if v == nil {
		v = m["excluded_models"]
	}
	return NormalizeExcludedModelPatterns(v)
}

// IsModelExcludedByConnection returns whether a model is excluded by a connection.
func IsModelExcludedByConnection(modelID any, providerSpecificData any) bool {
	s, ok := modelID.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return false
	}
	candidates := getModelMatchCandidates(s)
	excluded := GetConnectionExcludedModels(providerSpecificData)
	if len(candidates) == 0 || len(excluded) == 0 {
		return false
	}
	for _, pattern := range excluded {
		for _, candidate := range candidates {
			if WildcardMatch(candidate, pattern) {
				return true
			}
		}
	}
	return false
}

// HasEligibleConnectionForModel returns whether any connection can serve a model.
func HasEligibleConnectionForModel(connections []ConnectionLike, modelID any) bool {
	if len(connections) == 0 {
		return false
	}
	for _, c := range connections {
		if !IsModelExcludedByConnection(modelID, c.ProviderSpecificData) {
			return true
		}
	}
	return false
}
