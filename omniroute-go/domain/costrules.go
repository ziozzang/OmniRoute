package domain

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// costrules.go — Cost management business rules.
// Port of src/domain/costRules.ts
//
// Pure functions (normalization, budget window calculation) are directly
// testable. DB-dependent operations use injectable seams.

// BudgetResetInterval is the reset cadence for a budget.
type BudgetResetInterval string

const (
	ResetDaily   BudgetResetInterval = "daily"
	ResetWeekly  BudgetResetInterval = "weekly"
	ResetMonthly BudgetResetInterval = "monthly"
)

var validResetIntervals = map[BudgetResetInterval]bool{
	ResetDaily: true, ResetWeekly: true, ResetMonthly: true,
}

var resetTimeRegex = regexp.MustCompile(`^(\d{2}):(\d{2})$`)

// BudgetConfig is the raw budget configuration input.
type BudgetConfig struct {
	DailyLimitUsd    any
	WeeklyLimitUsd   any
	MonthlyLimitUsd  any
	WarningThreshold any
	ResetInterval    any
	ResetTime        any
	BudgetResetAt    any
	LastBudgetResetAt any
	WarningEmittedAt any
	WarningPeriodStart any
}

// NormalizedBudgetConfig is the validated, normalized budget configuration.
type NormalizedBudgetConfig struct {
	DailyLimitUsd     float64
	WeeklyLimitUsd    float64
	MonthlyLimitUsd   float64
	WarningThreshold  float64
	ResetInterval     BudgetResetInterval
	ResetTime         string
	BudgetResetAt     *int64
	LastBudgetResetAt *int64
	WarningEmittedAt  *int64
	WarningPeriodStart *int64
}

// CostEntry is one recorded cost observation.
type CostEntry struct {
	Cost      float64
	Timestamp float64
}

// BudgetWindow is the current budget period boundaries.
type BudgetWindow struct {
	PeriodStartAt int64
	NextResetAt   int64
}

// ---------------------------------------------------------------------------
// Pure normalization helpers
// ---------------------------------------------------------------------------

func isFinite(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}

func toNumber(value any, fallback float64) float64 {
	switch v := value.(type) {
	case float64:
		if isFinite(v) {
			return v
		}
	case float32:
		f := float64(v)
		if isFinite(f) {
			return f
		}
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case string:
		s := strings.TrimSpace(v)
		if len(s) > 0 {
			f, err := strconv.ParseFloat(s, 64)
			if err == nil && isFinite(f) {
				return f
			}
		}
	}
	return fallback
}

func toCostEntries(value any) []CostEntry {
	arr, ok := value.([]any)
	if !ok {
		return nil
	}
	var entries []CostEntry
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		cost := toNumber(m["cost"], math.NaN())
		ts := toNumber(m["timestamp"], math.NaN())
		if !isFinite(cost) || !isFinite(ts) {
			continue
		}
		entries = append(entries, CostEntry{Cost: cost, Timestamp: ts})
	}
	return entries
}

func sumEntries(entries []CostEntry) float64 {
	var sum float64
	for _, e := range entries {
		sum += e.Cost
	}
	return sum
}

func normalizeResetInterval(value any) BudgetResetInterval {
	if s, ok := value.(string); ok {
		n := BudgetResetInterval(strings.ToLower(strings.TrimSpace(s)))
		if validResetIntervals[n] {
			return n
		}
	}
	return ResetDaily
}

func normalizeResetTime(value any) string {
	if s, ok := value.(string); ok {
		m := resetTimeRegex.FindStringSubmatch(strings.TrimSpace(s))
		if m != nil {
			h, _ := strconv.Atoi(m[1])
			min, _ := strconv.Atoi(m[2])
			h = clampInt(h, 0, 23)
			min = clampInt(min, 0, 59)
			return pad2(h) + ":" + pad2(min)
		}
	}
	return "00:00"
}

func normalizeTimestamp(value any) *int64 {
	n := toNumber(value, math.NaN())
	if isFinite(n) && n > 0 {
		v := int64(n)
		return &v
	}
	return nil
}

// NormalizeBudgetConfig validates and normalizes a raw budget config.
func NormalizeBudgetConfig(config BudgetConfig) NormalizedBudgetConfig {
	return NormalizedBudgetConfig{
		DailyLimitUsd:     math.Max(0, toNumber(config.DailyLimitUsd, 0)),
		WeeklyLimitUsd:    math.Max(0, toNumber(config.WeeklyLimitUsd, 0)),
		MonthlyLimitUsd:   math.Max(0, toNumber(config.MonthlyLimitUsd, 0)),
		WarningThreshold:  math.Min(math.Max(toNumber(config.WarningThreshold, 0.8), 0), 1),
		ResetInterval:     normalizeResetInterval(config.ResetInterval),
		ResetTime:         normalizeResetTime(config.ResetTime),
		BudgetResetAt:     normalizeTimestamp(config.BudgetResetAt),
		LastBudgetResetAt: normalizeTimestamp(config.LastBudgetResetAt),
		WarningEmittedAt:  normalizeTimestamp(config.WarningEmittedAt),
		WarningPeriodStart: normalizeTimestamp(config.WarningPeriodStart),
	}
}

func getResetTimeParts(resetTime string) (int, int) {
	m := resetTimeRegex.FindStringSubmatch(resetTime)
	if m == nil {
		return 0, 0
	}
	h, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	return h, min
}

func getUtcDateMs(year int, month time.Month, day, hours, minutes int) int64 {
	return time.Date(year, month, day, hours, minutes, 0, 0, time.UTC).UnixMilli()
}

// GetBudgetWindow computes the current budget period boundaries.
// now is injectable for deterministic testing.
func GetBudgetWindow(resetInterval BudgetResetInterval, resetTime string, now int64) BudgetWindow {
	current := time.UnixMilli(now).UTC()
	hours, minutes := getResetTimeParts(normalizeResetTime(resetTime))
	year := current.Year()
	month := current.Month()
	day := current.Day()

	switch resetInterval {
	case ResetWeekly:
		daysSinceMonday := (int(current.Weekday()) + 6) % 7
		thisWeekReset := getUtcDateMs(year, month, day-daysSinceMonday, hours, minutes)
		if now >= thisWeekReset {
			return BudgetWindow{
				PeriodStartAt: thisWeekReset,
				NextResetAt:   getUtcDateMs(year, month, day-daysSinceMonday+7, hours, minutes),
			}
		}
		return BudgetWindow{
			PeriodStartAt: getUtcDateMs(year, month, day-daysSinceMonday-7, hours, minutes),
			NextResetAt:   thisWeekReset,
		}

	case ResetMonthly:
		thisMonthReset := getUtcDateMs(year, month, 1, hours, minutes)
		if now >= thisMonthReset {
			return BudgetWindow{
				PeriodStartAt: thisMonthReset,
				NextResetAt:   getUtcDateMs(year, month+1, 1, hours, minutes),
			}
		}
		return BudgetWindow{
			PeriodStartAt: getUtcDateMs(year, month-1, 1, hours, minutes),
			NextResetAt:   thisMonthReset,
		}

	default: // daily
		todayReset := getUtcDateMs(year, month, day, hours, minutes)
		if now >= todayReset {
			return BudgetWindow{
				PeriodStartAt: todayReset,
				NextResetAt:   getUtcDateMs(year, month, day+1, hours, minutes),
			}
		}
		return BudgetWindow{
			PeriodStartAt: getUtcDateMs(year, month, day-1, hours, minutes),
			NextResetAt:   todayReset,
		}
	}
}

// GetActiveBudgetLimit returns the effective limit for the current reset interval.
func GetActiveBudgetLimit(budget NormalizedBudgetConfig) float64 {
	switch budget.ResetInterval {
	case ResetMonthly:
		if budget.MonthlyLimitUsd > 0 {
			return budget.MonthlyLimitUsd
		}
		return budget.DailyLimitUsd
	case ResetWeekly:
		if budget.WeeklyLimitUsd > 0 {
			return budget.WeeklyLimitUsd
		}
		return budget.DailyLimitUsd
	default:
		return budget.DailyLimitUsd
	}
}

// ---------------------------------------------------------------------------
// Injectable DB seams
// ---------------------------------------------------------------------------

// CostStore is the injectable seam for cost persistence.
type CostStore interface {
	LoadBudget(apiKeyID string) (*BudgetConfig, error)
	SaveBudget(apiKeyID string, config NormalizedBudgetConfig) error
	DeleteBudget(apiKeyID string) error
	LoadAllBudgets() (map[string]BudgetConfig, error)
	LoadCostTotal(apiKeyID string, sinceMs int64) (float64, error)
	LoadCostEntries(apiKeyID string, sinceMs int64) ([]CostEntry, error)
	LoadCostEntriesInRange(apiKeyID string, startMs, endMs int64) ([]CostEntry, error)
	DeleteCostEntries(apiKeyID string) error
	DeleteAllCostData() error
	SaveBudgetResetLog(entry BudgetResetLogEntry) error
}

// BudgetResetLogEntry is one budget reset log record.
type BudgetResetLogEntry struct {
	APIKeyID      string
	ResetInterval BudgetResetInterval
	PreviousSpend float64
	ResetAt       int64
	NextResetAt   int64
	PeriodStart   int64
	PeriodEnd     int64
}

// SpendBatchWriter is the injectable seam for batched spend recording.
type SpendBatchWriter interface {
	Increment(apiKeyID string, cost float64, ts int64)
	GetPendingCostTotal(apiKeyID string, sinceMs int64, endMs ...int64) float64
	GetBufferedEntries(apiKeyID string, sinceMs int64) []CostEntry
	Discard(apiKeyID string)
	Reset()
}

// BudgetManager orchestrates budget operations with injectable persistence.
type BudgetManager struct {
	store   CostStore
	batcher SpendBatchWriter
	cache   map[string]NormalizedBudgetConfig
}

// NewBudgetManager creates a BudgetManager with the given persistence seams.
func NewBudgetManager(store CostStore, batcher SpendBatchWriter) *BudgetManager {
	return &BudgetManager{
		store:   store,
		batcher: batcher,
		cache:   make(map[string]NormalizedBudgetConfig),
	}
}

// SyncBudgetSchedule normalizes a budget config and syncs its reset schedule.
func (m *BudgetManager) SyncBudgetSchedule(apiKeyID string, config BudgetConfig, now int64, logReset, persist bool) NormalizedBudgetConfig {
	normalized := NormalizeBudgetConfig(config)
	window := GetBudgetWindow(normalized.ResetInterval, normalized.ResetTime, now)

	resetRolled := normalized.LastBudgetResetAt != nil && window.PeriodStartAt > *normalized.LastBudgetResetAt

	if resetRolled && logReset {
		var previousSpend float64
		if m.store != nil {
			entries, err := m.store.LoadCostEntriesInRange(apiKeyID, *normalized.LastBudgetResetAt, window.PeriodStartAt)
			if err == nil {
				previousSpend = sumEntries(entries)
			}
		}
		if m.batcher != nil {
			previousSpend += m.batcher.GetPendingCostTotal(apiKeyID, *normalized.LastBudgetResetAt, window.PeriodStartAt)
		}
		if m.store != nil {
			_ = m.store.SaveBudgetResetLog(BudgetResetLogEntry{
				APIKeyID:      apiKeyID,
				ResetInterval: normalized.ResetInterval,
				PreviousSpend: previousSpend,
				ResetAt:       window.PeriodStartAt,
				NextResetAt:   window.NextResetAt,
				PeriodStart:   window.PeriodStartAt,
				PeriodEnd:     window.NextResetAt,
			})
		}
	}

	updated := normalized
	updated.BudgetResetAt = &window.NextResetAt
	updated.LastBudgetResetAt = &window.PeriodStartAt
	if resetRolled {
		updated.WarningEmittedAt = nil
		updated.WarningPeriodStart = nil
	}

	if persist && m.store != nil {
		_ = m.store.SaveBudget(apiKeyID, updated)
	}

	m.cache[apiKeyID] = updated
	return updated
}

// SetBudget sets the budget for an API key.
func (m *BudgetManager) SetBudget(apiKeyID string, config BudgetConfig) NormalizedBudgetConfig {
	return m.SyncBudgetSchedule(apiKeyID, config, time.Now().UnixMilli(), false, true)
}

// GetBudget retrieves the budget for an API key (cache → DB → nil).
func (m *BudgetManager) GetBudget(apiKeyID string) *NormalizedBudgetConfig {
	if cached, ok := m.cache[apiKeyID]; ok {
		synced := m.SyncBudgetSchedule(apiKeyID, budgetConfigFromNormalized(cached), time.Now().UnixMilli(), true, true)
		return &synced
	}
	if m.store != nil {
		if fromDB, err := m.store.LoadBudget(apiKeyID); err == nil && fromDB != nil {
			synced := m.SyncBudgetSchedule(apiKeyID, *fromDB, time.Now().UnixMilli(), true, true)
			return &synced
		}
	}
	return nil
}

// CheckBudget checks whether an API key has remaining budget.
func (m *BudgetManager) CheckBudget(apiKeyID string, additionalCost float64) BudgetCheckResult {
	budget := m.GetBudget(apiKeyID)
	if budget == nil {
		return BudgetCheckResult{Allowed: true}
	}

	now := time.Now().UnixMilli()
	window := GetBudgetWindow(budget.ResetInterval, budget.ResetTime, now)

	var periodUsed float64
	if m.store != nil {
		if total, err := m.store.LoadCostTotal(apiKeyID, window.PeriodStartAt); err == nil {
			periodUsed = total
		}
	}
	if m.batcher != nil {
		periodUsed += m.batcher.GetPendingCostTotal(apiKeyID, window.PeriodStartAt)
	}

	projectedTotal := periodUsed + additionalCost
	activeLimitUsd := GetActiveBudgetLimit(*budget)
	warningReached := activeLimitUsd > 0 && projectedTotal >= activeLimitUsd*budget.WarningThreshold
	remaining := math.Max(activeLimitUsd-projectedTotal, 0)

	if warningReached && (budget.WarningPeriodStart == nil || *budget.WarningPeriodStart != window.PeriodStartAt) {
		updated := *budget
		nowMs := time.Now().UnixMilli()
		updated.WarningEmittedAt = &nowMs
		updated.WarningPeriodStart = &window.PeriodStartAt
		m.cache[apiKeyID] = updated
		if m.store != nil {
			_ = m.store.SaveBudget(apiKeyID, updated)
		}
	}

	if activeLimitUsd > 0 && projectedTotal > activeLimitUsd {
		interval := string(budget.ResetInterval)
		reason := strings.ToUpper(interval[:1]) + interval[1:] + " budget exceeded"
		return BudgetCheckResult{
			Allowed:           false,
			Reason:            reason,
			PeriodUsed:        periodUsed,
			ActiveLimitUsd:    activeLimitUsd,
			WarningReached:    true,
			Remaining:         remaining,
			ResetInterval:     budget.ResetInterval,
			ResetTime:         budget.ResetTime,
			BudgetResetAt:     window.NextResetAt,
			LastBudgetResetAt: window.PeriodStartAt,
			PeriodStartAt:     window.PeriodStartAt,
		}
	}

	return BudgetCheckResult{
		Allowed:           true,
		PeriodUsed:        periodUsed,
		ActiveLimitUsd:    activeLimitUsd,
		WarningReached:    warningReached,
		Remaining:         remaining,
		ResetInterval:     budget.ResetInterval,
		ResetTime:         budget.ResetTime,
		BudgetResetAt:     window.NextResetAt,
		LastBudgetResetAt: window.PeriodStartAt,
		PeriodStartAt:     window.PeriodStartAt,
	}
}

// BudgetCheckResult is the outcome of a budget check.
type BudgetCheckResult struct {
	Allowed           bool
	Reason            string
	PeriodUsed        float64
	ActiveLimitUsd    float64
	WarningReached    bool
	Remaining         float64
	ResetInterval     BudgetResetInterval
	ResetTime         string
	BudgetResetAt     int64
	LastBudgetResetAt int64
	PeriodStartAt     int64
}

// RecordCost records a cost for an API key.
func (m *BudgetManager) RecordCost(apiKeyID string, cost float64) {
	if m.batcher != nil {
		m.batcher.Increment(apiKeyID, cost, time.Now().UnixMilli())
	}
}

// DeleteBudget deletes budget config and recorded spend for an API key.
func (m *BudgetManager) DeleteBudget(apiKeyID string) {
	delete(m.cache, apiKeyID)
	if m.batcher != nil {
		m.batcher.Discard(apiKeyID)
	}
	if m.store != nil {
		_ = m.store.DeleteBudget(apiKeyID)
		_ = m.store.DeleteCostEntries(apiKeyID)
	}
}

// SyncAllBudgetSchedules syncs all budgets against the current clock.
func (m *BudgetManager) SyncAllBudgetSchedules(now int64) (processed, resetCount int) {
	if m.store == nil {
		return 0, 0
	}
	allBudgets, err := m.store.LoadAllBudgets()
	if err != nil {
		return 0, 0
	}
	for apiKeyID, config := range allBudgets {
		processed++
		synced := m.SyncBudgetSchedule(apiKeyID, config, now, true, true)
		if config.LastBudgetResetAt != synced.LastBudgetResetAt {
			resetCount++
		}
	}
	return processed, resetCount
}

// ResetCostData clears all cost data (for testing).
func (m *BudgetManager) ResetCostData() {
	m.cache = make(map[string]NormalizedBudgetConfig)
	if m.batcher != nil {
		m.batcher.Reset()
	}
	if m.store != nil {
		_ = m.store.DeleteAllCostData()
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func budgetConfigFromNormalized(n NormalizedBudgetConfig) BudgetConfig {
	return BudgetConfig{
		DailyLimitUsd:     n.DailyLimitUsd,
		WeeklyLimitUsd:    n.WeeklyLimitUsd,
		MonthlyLimitUsd:   n.MonthlyLimitUsd,
		WarningThreshold:  n.WarningThreshold,
		ResetInterval:     string(n.ResetInterval),
		ResetTime:         n.ResetTime,
		BudgetResetAt:     ptrToInt64Any(n.BudgetResetAt),
		LastBudgetResetAt: ptrToInt64Any(n.LastBudgetResetAt),
		WarningEmittedAt:  ptrToInt64Any(n.WarningEmittedAt),
		WarningPeriodStart: ptrToInt64Any(n.WarningPeriodStart),
	}
}

func ptrToInt64Any(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}
