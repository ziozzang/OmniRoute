package quota

// quotakey.go — Resolve which connections/providers an API key may use.
// Port of src/lib/quota/quotaKey.ts
//
// The pure intersection logic (ConstrainConnectionsToQuota) is directly
// testable. The DB-dependent scope resolution and exclusivity reconciliation
// use injectable seams so they remain unit-testable without a database.

// QuotaKeyScope is the resolved access scope for an API key's allowedQuotas.
type QuotaKeyScope struct {
	ConnectionIDs []string // provider-connection IDs the key may use
	Providers     []string // provider slugs (deduplicated)
	PoolSlugs     []string // group slugs (deduplicated; one per distinct group)
}

// ConstrainConnectionsToQuota constrains an existing connection allow-list to
// the connections belonging to a quota key's pool scope.
//
// Semantics (mirror intersectAllowedConnectionIds in chat.ts):
//   - Empty quotaConnectionIds (non-quota key) → return existing unchanged.
//   - Empty existing (no prior constraint)     → return quotaConnectionIds.
//   - Both non-empty                           → intersection.
//   - Disjoint sets                            → empty slice.
//
// Pure and synchronous — no DB access.
func ConstrainConnectionsToQuota(existing, quotaConnectionIDs []string) []string {
	if len(quotaConnectionIDs) == 0 {
		return existing
	}
	if len(existing) == 0 {
		return quotaConnectionIDs
	}
	quotaSet := make(map[string]struct{}, len(quotaConnectionIDs))
	for _, id := range quotaConnectionIDs {
		quotaSet[id] = struct{}{}
	}
	var out []string
	for _, id := range existing {
		if _, ok := quotaSet[id]; ok {
			out = append(out, id)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Exclusivity reconciliation (Phase C3) — injectable key store
// ---------------------------------------------------------------------------

// APIKeyRow is the minimal shape of an API key row for reconciliation.
type APIKeyRow struct {
	ID            string
	AllowedQuotas []string
}

// KeyStore is the injectable seam for API key persistence, mirroring
// getApiKeyById + updateApiKeyPermissions in the TS source.
type KeyStore interface {
	GetAPIKey(id string) (*APIKeyRow, error)
	UpdateAllowedQuotas(id string, quotas []string) error
}

// ReconcilePoolExclusivity keeps each affected API key's allowedQuotas in sync
// when a pool's allocations are saved with an exclusive flag.
//
// Rules:
//   - exclusive=true  → keys in nextKeyIDs get poolID ADDED; keys removed from
//     the allocation get poolID REMOVED.
//   - exclusive=false → poolID is REMOVED from ALL keys in the union.
//
// Only writes when the set actually changed. Missing keys are silently skipped.
// A single key failure never aborts reconciliation for others.
func ReconcilePoolExclusivity(store KeyStore, poolID string, prevKeyIDs, nextKeyIDs []string, exclusive bool) {
	affected := make(map[string]struct{})
	for _, id := range prevKeyIDs {
		affected[id] = struct{}{}
	}
	for _, id := range nextKeyIDs {
		affected[id] = struct{}{}
	}
	nextSet := make(map[string]struct{}, len(nextKeyIDs))
	for _, id := range nextKeyIDs {
		nextSet[id] = struct{}{}
	}

	for keyID := range affected {
		row, err := store.GetAPIKey(keyID)
		if err != nil || row == nil {
			continue // missing key → skip
		}
		current := row.AllowedQuotas
		if current == nil {
			current = []string{}
		}

		var next []string
		if exclusive {
			if _, inNext := nextSet[keyID]; inNext {
				// Key is in the new allocation AND pool is exclusive → ensure present.
				if contains(current, poolID) {
					continue // no change
				}
				next = append(append([]string{}, current...), poolID)
			} else {
				// Key was removed → ensure absent.
				if !contains(current, poolID) {
					continue
				}
				next = filterOut(current, poolID)
			}
		} else {
			// Non-exclusive → ensure absent from all affected keys.
			if !contains(current, poolID) {
				continue
			}
			next = filterOut(current, poolID)
		}

		_ = store.UpdateAllowedQuotas(keyID, next) // fire-and-forget; error swallowed
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func filterOut(s []string, v string) []string {
	var out []string
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}
