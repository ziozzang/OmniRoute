package domain

import (
	"regexp"
	"strings"
)

// tagrouter.go — Routing tag normalization and matching.
// Port of src/domain/tagRouter.ts
//
// Pure, stateless functions — no race concerns.

// RoutingTagMatchMode is "any" or "all".
type RoutingTagMatchMode string

const (
	TagMatchAny RoutingTagMatchMode = "any"
	TagMatchAll RoutingTagMatchMode = "all"
)

func normalizeSingleRoutingTag(value any) string {
	s, ok := value.(string)
	if !ok {
		return ""
	}
	n := strings.ToLower(strings.TrimSpace(s))
	return n
}

// NormalizeRoutingTags normalizes and deduplicates routing tags. Accepts a
// []any, []string, or comma-separated string.
func NormalizeRoutingTags(value any) []string {
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
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range raw {
		n := normalizeSingleRoutingTag(r)
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// NormalizeRoutingTagMatchMode normalizes a match mode; anything but "all" → "any".
func NormalizeRoutingTagMatchMode(value any) RoutingTagMatchMode {
	if s, ok := value.(string); ok && strings.ToLower(strings.TrimSpace(s)) == "all" {
		return TagMatchAll
	}
	return TagMatchAny
}

// GetConnectionRoutingTags extracts routing tags from provider-specific data.
func GetConnectionRoutingTags(providerSpecificData any) []string {
	m, _ := providerSpecificData.(map[string]any)
	if m == nil {
		return nil
	}
	return NormalizeRoutingTags(m["tags"])
}

// MatchesRoutingTags returns whether request tags match connection tags.
// Empty requestTags → true (no constraint). Empty connectionTags → false.
func MatchesRoutingTags(connectionTags, requestTags []string, matchMode RoutingTagMatchMode) bool {
	if len(requestTags) == 0 {
		return true
	}
	if len(connectionTags) == 0 {
		return false
	}
	tagSet := make(map[string]bool, len(connectionTags))
	for _, t := range connectionTags {
		tagSet[t] = true
	}
	if matchMode == TagMatchAll {
		for _, t := range requestTags {
			if !tagSet[t] {
				return false
			}
		}
		return true
	}
	for _, t := range requestTags {
		if tagSet[t] {
			return true
		}
	}
	return false
}

// RequestRoutingTags is the resolved routing tags + match mode from a body.
type RequestRoutingTags struct {
	Tags      []string
	MatchMode RoutingTagMatchMode
}

// ResolveRequestRoutingTags extracts routing tags + match mode from a request body.
func ResolveRequestRoutingTags(body map[string]any) RequestRoutingTags {
	var metadata map[string]any
	if body != nil {
		metadata, _ = body["metadata"].(map[string]any)
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	matchModeRaw := metadata["tag_match_mode"]
	if matchModeRaw == nil {
		matchModeRaw = metadata["tagMatchMode"]
	}
	return RequestRoutingTags{
		Tags:      NormalizeRoutingTags(metadata["tags"]),
		MatchMode: NormalizeRoutingTagMatchMode(matchModeRaw),
	}
}

var _ = regexp.MustCompile // reserved for future pattern use
