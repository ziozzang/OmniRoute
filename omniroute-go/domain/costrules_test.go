package domain

import (
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Normalization
// ---------------------------------------------------------------------------

func TestToNumber(t *testing.T) {
	cases := []struct {
		in       any
		fallback float64
		want     float64
	}{
		{42.5, 0, 42.5},
		{int(7), 0, 7},
		{int64(9), 0, 9},
		{"12.5", 0, 12.5},
		{"  3.14  ", 0, 3.14},
		{"abc", -1, -1},
		{"", -1, -1},
		{nil, -1, -1},
		{[]int{1}, -1, -1},
	}
	for _, c := range cases {
		if got := toNumber(c.in, c.fallback); got != c.want {
			t.Errorf("toNumber(%v, %v) = %v, want %v", c.in, c.fallback, got, c.want)
		}
	}
}

func TestNormalizeResetInterval(t *testing.T) {
	cases := []struct {
		in   any
		want BudgetResetInterval
	}{
		{"daily", ResetDaily},
		{"WEEKLY", ResetWeekly},
		{" monthly ", ResetMonthly},
		{"bogus", ResetDaily},
		{123, ResetDaily},
		{nil, ResetDaily},
	}
	for _, c := range cases {
		if got := normalizeResetInterval(c.in); got != c.want {
			t.Errorf("normalizeResetInterval(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestNormalizeResetTime(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"13:45", "13:45"},
		{" 09:05 ", "09:05"},
		{"25:70", "23:59"}, // clamped
		{"abc", "00:00"},
		{"9:5", "00:00"}, // no leading zeros → no match
		{nil, "00:00"},
	}
	for _, c := range cases {
		if got := normalizeResetTime(c.in); got != c.want {
			t.Errorf("normalizeResetTime(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeTimestamp(t *testing.T) {
	if got := normalizeTimestamp(1700000000000.0); got == nil || *got != 1700000000000 {
		t.Errorf("valid ts: got %v", got)
	}
	if got := normalizeTimestamp(0.0); got != nil {
		t.Errorf("zero → nil, got %v", *got)
	}
	if got := normalizeTimestamp(-5.0); got != nil {
		t.Errorf("negative → nil, got %v", *got)
	}
	if got := normalizeTimestamp(nil); got != nil {
		t.Errorf("nil → nil, got %v", got)
	}
}

func TestNormalizeBudgetConfig(t *testing.T) {
	cfg := BudgetConfig{
		DailyLimitUsd:    -5.0, // clamped to 0
		WeeklyLimitUsd:   "100",
		MonthlyLimitUsd:  2000.0,
		WarningThreshold: 1.5, // clamped to 1
		ResetInterval:    "weekly",
		ResetTime:        "08:30",
	}
	got := NormalizeBudgetConfig(cfg)
	if got.DailyLimitUsd != 0 {
		t.Errorf("dailyLimit clamped to 0, got %v", got.DailyLimitUsd)
	}
	if got.WeeklyLimitUsd != 100 {
		t.Errorf("weeklyLimit = %v, want 100", got.WeeklyLimitUsd)
	}
	if got.MonthlyLimitUsd != 2000 {
		t.Errorf("monthlyLimit = %v, want 2000", got.MonthlyLimitUsd)
	}
	if got.WarningThreshold != 1 {
		t.Errorf("warningThreshold clamped to 1, got %v", got.WarningThreshold)
	}
	if got.ResetInterval != ResetWeekly {
		t.Errorf("resetInterval = %v, want weekly", got.ResetInterval)
	}
	if got.ResetTime != "08:30" {
		t.Errorf("resetTime = %q, want 08:30", got.ResetTime)
	}
}

// ---------------------------------------------------------------------------
// Budget window
// ---------------------------------------------------------------------------

func TestGetBudgetWindowDaily(t *testing.T) {
	// 2026-07-23 12:00 UTC, reset at 00:00 → period started today 00:00.
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC).UnixMilli()
	w := GetBudgetWindow(ResetDaily, "00:00", now)
	wantStart := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC).UnixMilli()
	wantNext := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC).UnixMilli()
	if w.PeriodStartAt != wantStart || w.NextResetAt != wantNext {
		t.Errorf("daily window = %+v, want start=%d next=%d", w, wantStart, wantNext)
	}
}

func TestGetBudgetWindowDailyBeforeReset(t *testing.T) {
	// 2026-07-23 06:00 UTC, reset at 08:00 → today's reset not yet reached,
	// so period started yesterday 08:00.
	now := time.Date(2026, 7, 23, 6, 0, 0, 0, time.UTC).UnixMilli()
	w := GetBudgetWindow(ResetDaily, "08:00", now)
	wantStart := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC).UnixMilli()
	wantNext := time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC).UnixMilli()
	if w.PeriodStartAt != wantStart || w.NextResetAt != wantNext {
		t.Errorf("daily-before-reset window = %+v, want start=%d next=%d", w, wantStart, wantNext)
	}
}

func TestGetBudgetWindowWeekly(t *testing.T) {
	// 2026-07-23 is a Thursday. Monday of that week = 2026-07-20.
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC).UnixMilli()
	w := GetBudgetWindow(ResetWeekly, "00:00", now)
	wantStart := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC).UnixMilli()
	wantNext := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC).UnixMilli()
	if w.PeriodStartAt != wantStart || w.NextResetAt != wantNext {
		t.Errorf("weekly window = %+v, want start=%d next=%d", w, wantStart, wantNext)
	}
}

func TestGetBudgetWindowMonthly(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC).UnixMilli()
	w := GetBudgetWindow(ResetMonthly, "00:00", now)
	wantStart := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	wantNext := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	if w.PeriodStartAt != wantStart || w.NextResetAt != wantNext {
		t.Errorf("monthly window = %+v, want start=%d next=%d", w, wantStart, wantNext)
	}
}

// ---------------------------------------------------------------------------
// Active limit
// ---------------------------------------------------------------------------

func TestGetActiveBudgetLimit(t *testing.T) {
	cases := []struct {
		interval BudgetResetInterval
		daily    float64
		weekly   float64
		monthly  float64
		want     float64
	}{
		{ResetDaily, 10, 50, 100, 10},
		{ResetWeekly, 10, 50, 100, 50},
		{ResetWeekly, 10, 0, 100, 10},   // weekly=0 → fallback to daily
		{ResetMonthly, 10, 50, 100, 100},
		{ResetMonthly, 10, 50, 0, 10},   // monthly=0 → fallback to daily
	}
	for _, c := range cases {
		b := NormalizedBudgetConfig{
			ResetInterval:   c.interval,
			DailyLimitUsd:   c.daily,
			WeeklyLimitUsd:  c.weekly,
			MonthlyLimitUsd: c.monthly,
		}
		if got := GetActiveBudgetLimit(b); got != c.want {
			t.Errorf("GetActiveBudgetLimit(%s, d=%v w=%v m=%v) = %v, want %v",
				c.interval, c.daily, c.weekly, c.monthly, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// BudgetManager with mock store
// ---------------------------------------------------------------------------

type mockCostStore struct {
	budgets   map[string]BudgetConfig
	costTotal map[string]float64
	resetLogs []BudgetResetLogEntry
}

func newMockCostStore() *mockCostStore {
	return &mockCostStore{
		budgets:   make(map[string]BudgetConfig),
		costTotal: make(map[string]float64),
	}
}

func (m *mockCostStore) LoadBudget(id string) (*BudgetConfig, error) {
	if b, ok := m.budgets[id]; ok {
		return &b, nil
	}
	return nil, nil
}
func (m *mockCostStore) SaveBudget(id string, c NormalizedBudgetConfig) error {
	m.budgets[id] = budgetConfigFromNormalized(c)
	return nil
}
func (m *mockCostStore) DeleteBudget(id string) error { delete(m.budgets, id); return nil }
func (m *mockCostStore) LoadAllBudgets() (map[string]BudgetConfig, error) {
	return m.budgets, nil
}
func (m *mockCostStore) LoadCostTotal(id string, since int64) (float64, error) {
	return m.costTotal[id], nil
}
func (m *mockCostStore) LoadCostEntries(id string, since int64) ([]CostEntry, error) {
	return nil, nil
}
func (m *mockCostStore) LoadCostEntriesInRange(id string, s, e int64) ([]CostEntry, error) {
	return nil, nil
}
func (m *mockCostStore) DeleteCostEntries(id string) error { return nil }
func (m *mockCostStore) DeleteAllCostData() error          { return nil }
func (m *mockCostStore) SaveBudgetResetLog(e BudgetResetLogEntry) error {
	m.resetLogs = append(m.resetLogs, e)
	return nil
}

type mockBatcher struct{ pending float64 }

func (m *mockBatcher) Increment(id string, cost float64, ts int64) { m.pending += cost }
func (m *mockBatcher) GetPendingCostTotal(id string, since int64, end ...int64) float64 {
	return m.pending
}
func (m *mockBatcher) GetBufferedEntries(id string, since int64) []CostEntry { return nil }
func (m *mockBatcher) Discard(id string)                                    { m.pending = 0 }
func (m *mockBatcher) Reset()                                               { m.pending = 0 }

func TestBudgetManagerCheckBudgetExceeded(t *testing.T) {
	store := newMockCostStore()
	batcher := &mockBatcher{}
	mgr := NewBudgetManager(store, batcher)

	mgr.SetBudget("k1", BudgetConfig{
		DailyLimitUsd: 10.0,
		ResetInterval: "daily",
		ResetTime:     "00:00",
	})
	store.costTotal["k1"] = 15.0 // over the $10 daily limit

	res := mgr.CheckBudget("k1", 0)
	if res.Allowed {
		t.Fatal("expected budget exceeded → not allowed")
	}
	if res.ActiveLimitUsd != 10 {
		t.Errorf("activeLimit = %v, want 10", res.ActiveLimitUsd)
	}
}

func TestBudgetManagerCheckBudgetAllowed(t *testing.T) {
	store := newMockCostStore()
	batcher := &mockBatcher{}
	mgr := NewBudgetManager(store, batcher)

	mgr.SetBudget("k1", BudgetConfig{
		DailyLimitUsd: 100.0,
		ResetInterval: "daily",
	})
	store.costTotal["k1"] = 5.0

	res := mgr.CheckBudget("k1", 0)
	if !res.Allowed {
		t.Fatal("expected allowed")
	}
	if res.Remaining != 95 {
		t.Errorf("remaining = %v, want 95", res.Remaining)
	}
}

func TestBudgetManagerNoBudget(t *testing.T) {
	mgr := NewBudgetManager(newMockCostStore(), &mockBatcher{})
	res := mgr.CheckBudget("unknown", 0)
	if !res.Allowed {
		t.Fatal("no budget → allowed (fail-open)")
	}
}
