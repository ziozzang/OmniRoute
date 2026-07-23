package quota

import "testing"

// TestConstrainConnectionsToQuota verifies the intersection semantics.
func TestConstrainConnectionsToQuota(t *testing.T) {
	// Empty quota → existing unchanged.
	if got := ConstrainConnectionsToQuota([]string{"a", "b"}, nil); len(got) != 2 {
		t.Fatalf("empty quota should return existing, got %v", got)
	}
	// Empty existing → quota returned.
	if got := ConstrainConnectionsToQuota(nil, []string{"x", "y"}); len(got) != 2 {
		t.Fatalf("empty existing should return quota, got %v", got)
	}
	// Intersection.
	got := ConstrainConnectionsToQuota([]string{"a", "b", "c"}, []string{"b", "c", "d"})
	if len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Fatalf("intersection = %v, want [b c]", got)
	}
	// Disjoint → empty.
	if got := ConstrainConnectionsToQuota([]string{"a"}, []string{"z"}); len(got) != 0 {
		t.Fatalf("disjoint should be empty, got %v", got)
	}
}

// mockKeyStore is an in-memory KeyStore for reconciliation tests.
type mockKeyStore struct {
	keys    map[string]*APIKeyRow
	updates map[string][]string
}

func newMockKeyStore(keys map[string]*APIKeyRow) *mockKeyStore {
	return &mockKeyStore{keys: keys, updates: make(map[string][]string)}
}

func (m *mockKeyStore) GetAPIKey(id string) (*APIKeyRow, error) {
	return m.keys[id], nil
}
func (m *mockKeyStore) UpdateAllowedQuotas(id string, quotas []string) error {
	m.updates[id] = quotas
	return nil
}

// TestReconcilePoolExclusivity verifies add/remove rules.
func TestReconcilePoolExclusivity(t *testing.T) {
	store := newMockKeyStore(map[string]*APIKeyRow{
		"k1": {ID: "k1", AllowedQuotas: []string{"other"}},
		"k2": {ID: "k2", AllowedQuotas: []string{"pool1"}},
		"k3": {ID: "k3", AllowedQuotas: []string{}},
	})

	// exclusive=true: k1 added to next → gets pool1; k2 removed → loses pool1.
	ReconcilePoolExclusivity(store, "pool1", []string{"k2"}, []string{"k1"}, true)

	if got := store.updates["k1"]; len(got) != 2 || got[1] != "pool1" {
		t.Fatalf("k1 should gain pool1, got %v", got)
	}
	if got := store.updates["k2"]; len(got) != 0 {
		t.Fatalf("k2 should lose pool1, got %v", got)
	}
	if _, ok := store.updates["k3"]; ok {
		t.Fatal("k3 unaffected, should not be written")
	}
}

// TestReconcileNonExclusive verifies pool removed from all affected keys.
func TestReconcileNonExclusive(t *testing.T) {
	store := newMockKeyStore(map[string]*APIKeyRow{
		"k1": {ID: "k1", AllowedQuotas: []string{"pool1", "other"}},
		"k2": {ID: "k2", AllowedQuotas: []string{"pool1"}},
	})
	ReconcilePoolExclusivity(store, "pool1", []string{"k1", "k2"}, []string{"k1"}, false)
	if got := store.updates["k1"]; len(got) != 1 || got[0] != "other" {
		t.Fatalf("k1 should keep only 'other', got %v", got)
	}
	if got := store.updates["k2"]; len(got) != 0 {
		t.Fatalf("k2 should be emptied, got %v", got)
	}
}

// TestReconcileNoChangeSkipsWrite verifies no write when nothing changes.
func TestReconcileNoChangeSkipsWrite(t *testing.T) {
	store := newMockKeyStore(map[string]*APIKeyRow{
		"k1": {ID: "k1", AllowedQuotas: []string{"pool1"}}, // already has pool1
	})
	ReconcilePoolExclusivity(store, "pool1", nil, []string{"k1"}, true)
	if _, ok := store.updates["k1"]; ok {
		t.Fatal("no change → no write expected")
	}
}
