package domain

import (
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// ProviderExpiration
// ---------------------------------------------------------------------------

func TestExpirationStatusCalculation(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	SetExpiryClock(func() time.Time { return now })
	defer SetExpiryClock(time.Now)
	ResetExpirations()

	// Active: expires in 30 days
	exp := now.Add(30 * 24 * time.Hour)
	e := SetExpiration("c1", "claude", "My Claude", &exp, ExpiryOAuthToken, 7, "")
	if e.Status != ExpiryActive {
		t.Fatalf("30d out: status = %q, want active", e.Status)
	}

	// Expiring soon: expires in 3 days (alertDays=7)
	exp2 := now.Add(3 * 24 * time.Hour)
	e2 := SetExpiration("c2", "codex", "My Codex", &exp2, ExpirySubscription, 7, "")
	if e2.Status != ExpiryExpiringSoon {
		t.Fatalf("3d out: status = %q, want expiring_soon", e2.Status)
	}

	// Expired: in the past
	exp3 := now.Add(-1 * time.Hour)
	e3 := SetExpiration("c3", "glm", "My GLM", &exp3, ExpiryAPICredits, 7, "")
	if e3.Status != ExpiryExpired {
		t.Fatalf("past: status = %q, want expired", e3.Status)
	}

	// Unknown: nil expiresAt
	e4 := SetExpiration("c4", "deepseek", "My DS", nil, ExpiryAPICredits, 7, "")
	if e4.Status != ExpiryUnknown {
		t.Fatalf("nil: status = %q, want unknown", e4.Status)
	}
}

func TestGetAllExpirationsSorted(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	SetExpiryClock(func() time.Time { return now })
	defer SetExpiryClock(time.Now)
	ResetExpirations()

	past := now.Add(-1 * time.Hour)
	soon := now.Add(2 * 24 * time.Hour)
	far := now.Add(30 * 24 * time.Hour)

	SetExpiration("c1", "a", "A", &far, ExpiryOAuthToken, 7, "")
	SetExpiration("c2", "b", "B", &past, ExpiryOAuthToken, 7, "")
	SetExpiration("c3", "c", "C", &soon, ExpiryOAuthToken, 7, "")
	SetExpiration("c4", "d", "D", nil, ExpiryOAuthToken, 7, "")

	all := GetAllExpirations()
	if len(all) != 4 {
		t.Fatalf("len = %d", len(all))
	}
	// Order: expired, expiring_soon, active, unknown
	if all[0].Status != ExpiryExpired {
		t.Fatalf("all[0] = %q, want expired", all[0].Status)
	}
	if all[1].Status != ExpiryExpiringSoon {
		t.Fatalf("all[1] = %q, want expiring_soon", all[1].Status)
	}
	if all[2].Status != ExpiryActive {
		t.Fatalf("all[2] = %q, want active", all[2].Status)
	}
	if all[3].Status != ExpiryUnknown {
		t.Fatalf("all[3] = %q, want unknown", all[3].Status)
	}
}

func TestExpirationSummary(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	SetExpiryClock(func() time.Time { return now })
	defer SetExpiryClock(time.Now)
	ResetExpirations()

	past := now.Add(-1 * time.Hour)
	soon := now.Add(2 * 24 * time.Hour)
	far := now.Add(30 * 24 * time.Hour)

	SetExpiration("c1", "a", "A", &far, ExpiryOAuthToken, 7, "")
	SetExpiration("c2", "b", "B", &past, ExpiryOAuthToken, 7, "")
	SetExpiration("c3", "c", "C", &soon, ExpiryOAuthToken, 7, "")

	s := GetExpirationSummary()
	if s.Total != 3 || s.Active != 1 || s.Expired != 1 || s.ExpiringSoon != 1 {
		t.Fatalf("summary = %+v", s)
	}
	if s.NextExpiration == nil || s.NextExpiration.ConnectionID != "c3" {
		t.Fatalf("nextExpiration = %+v, want c3 (nearest future)", s.NextExpiration)
	}
}

func TestDetectExpirationFromResponse(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	SetExpiryClock(func() time.Time { return now })
	defer SetExpiryClock(time.Now)

	// 401 → oauth_token expired
	d := DetectExpirationFromResponse("claude", 401, nil)
	if d == nil || d.ExpiryType != ExpiryOAuthToken {
		t.Fatalf("401: got %+v", d)
	}

	// 402 → subscription expired
	d = DetectExpirationFromResponse("claude", 402, nil)
	if d == nil || d.ExpiryType != ExpirySubscription {
		t.Fatalf("402: got %+v", d)
	}

	// 429 + retry-after (seconds from now)
	d = DetectExpirationFromResponse("openai", 429, map[string]string{"retry-after": "60"})
	if d == nil || d.ExpiryType != ExpiryFreeTierReset {
		t.Fatalf("429: got %+v", d)
	}
	expected := now.Add(60 * time.Second)
	if !d.ExpiresAt.Equal(expected) {
		t.Fatalf("429 expiresAt = %v, want %v", d.ExpiresAt, expected)
	}

	// 429 + epoch seconds
	d = DetectExpirationFromResponse("openai", 429, map[string]string{"x-ratelimit-reset": "1700000000"})
	if d == nil {
		t.Fatal("429 epoch: nil")
	}
	if d.ExpiresAt.Unix() != 1700000000 {
		t.Fatalf("epoch: got %v", d.ExpiresAt.Unix())
	}

	// 200 → nil
	d = DetectExpirationFromResponse("openai", 200, nil)
	if d != nil {
		t.Fatalf("200: got %+v, want nil", d)
	}
}

func TestExpirationConcurrent(t *testing.T) {
	ResetExpirations()
	exp := time.Now().Add(24 * time.Hour)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			SetExpiration("c", "p", "n", &exp, ExpiryOAuthToken, 7, "")
			GetExpiration("c")
			GetAllExpirations()
			GetExpirationSummary()
		}(i)
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// ConfigAudit
// ---------------------------------------------------------------------------

func TestComputeDiff(t *testing.T) {
	before := map[string]any{"a": 1, "b": 2, "c": 3}
	after := map[string]any{"a": 1, "b": 99, "d": 4}
	d := ComputeDiff(before, after)
	if len(d.Added) != 1 || d.Added[0] != "d" {
		t.Fatalf("added = %v", d.Added)
	}
	if len(d.Removed) != 1 || d.Removed[0] != "c" {
		t.Fatalf("removed = %v", d.Removed)
	}
	if len(d.Changed) != 1 || d.Changed[0].Key != "b" {
		t.Fatalf("changed = %v", d.Changed)
	}
	if d.IsEmpty {
		t.Fatal("should not be empty")
	}
}

func TestComputeDiffEmpty(t *testing.T) {
	d := ComputeDiff(map[string]any{"a": 1}, map[string]any{"a": 1})
	if !d.IsEmpty {
		t.Fatal("identical states → empty diff")
	}
	d = ComputeDiff(nil, nil)
	if !d.IsEmpty {
		t.Fatal("nil/nil → empty diff")
	}
}

func TestRecordAndQueryAuditLog(t *testing.T) {
	ResetAuditLog()
	SetAuditClock(func() time.Time { return time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC) })
	defer SetAuditClock(time.Now)

	e1 := RecordChange(AuditCreate, AuditProvider, "p1", "My Provider", nil, map[string]any{"name": "test"}, AuditSrcDashboard, "created")
	if e1.ID == "" {
		t.Fatal("should have ID")
	}
	if len(e1.Diff.Added) != 1 {
		t.Fatalf("create diff added = %v", e1.Diff.Added)
	}

	RecordChange(AuditUpdate, AuditProvider, "p1", "My Provider", map[string]any{"name": "test"}, map[string]any{"name": "updated"}, AuditSrcAPI, "")

	result := GetAuditLog(nil)
	if result.Total != 2 {
		t.Fatalf("total = %d, want 2", result.Total)
	}
	// Newest first
	if result.Entries[0].Action != AuditUpdate {
		t.Fatalf("newest first: got %q", result.Entries[0].Action)
	}
}

func TestAuditLogFiltering(t *testing.T) {
	ResetAuditLog()
	RecordChange(AuditCreate, AuditProvider, "p1", "P1", nil, nil, AuditSrcDashboard, "")
	RecordChange(AuditUpdate, AuditCombo, "c1", "C1", nil, nil, AuditSrcAPI, "")
	RecordChange(AuditDelete, AuditProvider, "p2", "P2", nil, nil, AuditSrcDashboard, "")

	target := AuditProvider
	result := GetAuditLog(&AuditLogOptions{Target: &target})
	if result.Total != 2 {
		t.Fatalf("filter by provider: total = %d, want 2", result.Total)
	}

	action := AuditCreate
	result = GetAuditLog(&AuditLogOptions{Action: &action})
	if result.Total != 1 {
		t.Fatalf("filter by create: total = %d, want 1", result.Total)
	}
}

func TestAuditLogPagination(t *testing.T) {
	ResetAuditLog()
	for i := 0; i < 10; i++ {
		RecordChange(AuditUpdate, AuditSettings, "s", "S", nil, nil, AuditSrcCLI, "")
	}
	result := GetAuditLog(&AuditLogOptions{Limit: 3, Offset: 2})
	if len(result.Entries) != 3 {
		t.Fatalf("page len = %d, want 3", len(result.Entries))
	}
	if result.Total != 10 {
		t.Fatalf("total = %d, want 10", result.Total)
	}
}

func TestGetRollbackState(t *testing.T) {
	ResetAuditLog()
	before := map[string]any{"name": "old", "enabled": true}
	after := map[string]any{"name": "new", "enabled": false}
	e := RecordChange(AuditUpdate, AuditProvider, "p1", "P1", before, after, AuditSrcDashboard, "")

	state := GetRollbackState(e.ID)
	if state == nil || state["name"] != "old" {
		t.Fatalf("rollback state = %v", state)
	}
	if GetRollbackState("nonexistent") != nil {
		t.Fatal("nonexistent → nil")
	}
}

func TestCreateSnapshot(t *testing.T) {
	data := map[string]any{"providers": []any{"a", "b"}, "nested": map[string]any{"x": 1}}
	snap := CreateSnapshot("v1.0", "test snapshot", data)
	if snap.Version != "v1.0" || snap.Description != "test snapshot" {
		t.Fatalf("snap = %+v", snap)
	}
	// Deep clone: modifying original should not affect snapshot
	data["nested"].(map[string]any)["x"] = 999
	if snap.Data["nested"].(map[string]any)["x"] != 1.0 {
		t.Fatal("snapshot should be deep-cloned")
	}
}

func TestAuditSummary(t *testing.T) {
	ResetAuditLog()
	RecordChange(AuditCreate, AuditProvider, "p1", "P1", nil, nil, AuditSrcDashboard, "")
	RecordChange(AuditUpdate, AuditProvider, "p1", "P1", nil, nil, AuditSrcAPI, "")
	RecordChange(AuditDelete, AuditCombo, "c1", "C1", nil, nil, AuditSrcDashboard, "")

	s := GetAuditSummary()
	if s.TotalEntries != 3 {
		t.Fatalf("total = %d", s.TotalEntries)
	}
	if s.ByTarget["provider"] != 2 || s.ByTarget["combo"] != 1 {
		t.Fatalf("byTarget = %v", s.ByTarget)
	}
	if s.ByAction["create"] != 1 || s.ByAction["update"] != 1 || s.ByAction["delete"] != 1 {
		t.Fatalf("byAction = %v", s.ByAction)
	}
	if s.BySource["dashboard"] != 2 || s.BySource["api"] != 1 {
		t.Fatalf("bySource = %v", s.BySource)
	}
}

func TestAuditLogBounded(t *testing.T) {
	ResetAuditLog()
	for i := 0; i < 1005; i++ {
		RecordChange(AuditUpdate, AuditSettings, "s", "S", nil, nil, AuditSrcCLI, "")
	}
	result := GetAuditLog(&AuditLogOptions{Limit: 2000})
	if result.Total != 1000 {
		t.Fatalf("bounded total = %d, want 1000", result.Total)
	}
}

func TestAuditConcurrent(t *testing.T) {
	ResetAuditLog()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			RecordChange(AuditUpdate, AuditSettings, "s", "S", nil, nil, AuditSrcCLI, "")
			GetAuditLog(nil)
			GetAuditSummary()
		}()
	}
	wg.Wait()
}
