package quota

import "strings"

// naming.go — Deterministic naming helpers for quota virtual models.
// Port of src/lib/quota/quotaModelNaming.ts
//
// FORMAT: `qtSd/<groupSlug>/<provider>/<model>`
//
// The groupSlug is pure alphanumeric (no "-", no "/"), and the segments are
// separated by "/" which makes parsing unambiguous: split on "/", take the
// first 3 segments (prefix literal "qtSd", groupSlug, provider) and join the
// remainder as the model id (model ids may contain "/" for namespaced models).
//
// Pure, stateless functions — no race concerns.

// QuotaModelPrefix is the literal prefix for all quota virtual model names.
const QuotaModelPrefix = "qtSd/"

// QuotaGroupSlug converts an arbitrary group name into a safe, alphanumeric
// slug. Lowercases the name then strips every character that is not [a-z0-9].
// Falls back to "pool" when the result would be empty.
func QuotaGroupSlug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	slug := b.String()
	if len(slug) == 0 {
		return "pool"
	}
	return slug
}

// QuotaPoolSlug is a backward-compat alias for QuotaGroupSlug.
func QuotaPoolSlug(poolName string) string {
	return QuotaGroupSlug(poolName)
}

// QuotaModelName builds the canonical virtual model name for a quota-shared
// target. The first argument is the GROUP name. Provider and model are kept
// verbatim (not slugged).
//
// Example: QuotaModelName("Pool Principal", "codex", "gpt-5.5")
//
//	→ "qtSd/poolprincipal/codex/gpt-5.5"
func QuotaModelName(groupName, provider, model string) string {
	return QuotaModelPrefix + QuotaGroupSlug(groupName) + "/" + provider + "/" + model
}

// ParsedQuotaModel holds the components of a parsed quota virtual model name.
type ParsedQuotaModel struct {
	GroupSlug string
	Provider  string
	Model     string
}

// ParseQuotaModelName parses a quota virtual model name back into its
// components. Returns nil when the name is not a valid quota model name.
//
// Segments: ["qtSd", groupSlug, provider, ...modelParts]
// Requires at least 4 segments. Model is the remainder joined by "/".
func ParseQuotaModelName(name string) *ParsedQuotaModel {
	if !strings.HasPrefix(name, QuotaModelPrefix) {
		return nil
	}
	rest := name[len(QuotaModelPrefix):]
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 3 {
		return nil
	}
	groupSlug, provider, model := parts[0], parts[1], parts[2]
	if groupSlug == "" || provider == "" || model == "" {
		return nil
	}
	return &ParsedQuotaModel{GroupSlug: groupSlug, Provider: provider, Model: model}
}

// IsQuotaModelName is a fast prefix check — does not validate the full
// structure.
func IsQuotaModelName(name string) bool {
	return strings.HasPrefix(name, QuotaModelPrefix)
}
