package sse

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"
)

// cooldownawareretry.go — Cooldown-aware retry decision logic.
// Port of src/sse/services/cooldownAwareRetry.ts
//
// The decision functions are pure. WaitForCooldown uses context for cancellation.

const (
	maxRequestRetry     = 10
	maxRetryIntervalSec = 300
	maxBudgetMs         = 5 * 60 * 1000
)

// CooldownAwareRetrySettings holds resolved retry settings.
type CooldownAwareRetrySettings struct {
	Enabled         bool
	MaxRetries      int
	MaxRetryWaitSec int
	MaxRetryWaitMs  int64
	BudgetMs        int64
}

// normalizeInteger clamps a value to [min, max], truncating floats.
func normalizeInteger(value any, fallback int, min, max int) int {
	switch v := value.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fallback
		}
		n := int(math.Trunc(v))
		return clampInt(n, min, max)
	case int:
		return clampInt(v, min, max)
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return fallback
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return fallback
		}
		return clampInt(int(math.Trunc(f)), min, max)
	default:
		return fallback
	}
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// WaitForCooldownInput is the raw waitForCooldown settings block.
type WaitForCooldownInput struct {
	Enabled         bool
	MaxRetries      any
	MaxRetryWaitSec any
	BudgetMs        any
}

// ResolveCooldownAwareRetrySettings resolves retry settings from raw input.
func ResolveCooldownAwareRetrySettings(wfc WaitForCooldownInput) CooldownAwareRetrySettings {
	maxRetries := normalizeInteger(wfc.MaxRetries, 0, 0, maxRequestRetry)
	maxRetryWaitSec := normalizeInteger(wfc.MaxRetryWaitSec, 0, 0, maxRetryIntervalSec)
	maxRetryWaitMs := int64(maxRetryWaitSec) * 1000
	budgetMs := int64(normalizeInteger(wfc.BudgetMs, int(maxRetryWaitMs), int(maxRetryWaitMs), maxBudgetMs))
	enabled := wfc.Enabled && maxRetries > 0 && maxRetryWaitSec > 0
	return CooldownAwareRetrySettings{
		Enabled:         enabled,
		MaxRetries:      maxRetries,
		MaxRetryWaitSec: maxRetryWaitSec,
		MaxRetryWaitMs:  maxRetryWaitMs,
		BudgetMs:        budgetMs,
	}
}

// ClosestRetryAfter is the parsed retry-after result.
type ClosestRetryAfter struct {
	RetryAfter      *string // ISO string, nil when absent/invalid
	RetryAfterHuman string
	WaitMs          *int64 // nil when absent/invalid
}

// ComputeClosestRetryAfter parses a retry-after value (ISO string or epoch ms).
func ComputeClosestRetryAfter(retryAfter any, nowMs int64) ClosestRetryAfter {
	if retryAfter == nil {
		return ClosestRetryAfter{}
	}
	var retryTimeMs int64
	switch v := retryAfter.(type) {
	case string:
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return ClosestRetryAfter{}
		}
		retryTimeMs = t.UnixMilli()
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return ClosestRetryAfter{}
		}
		retryTimeMs = int64(v)
	case int64:
		retryTimeMs = v
	default:
		return ClosestRetryAfter{}
	}
	iso := time.UnixMilli(retryTimeMs).UTC().Format(time.RFC3339)
	waitMs := retryTimeMs - nowMs
	if waitMs < 0 {
		waitMs = 0
	}
	return ClosestRetryAfter{
		RetryAfter:      &iso,
		RetryAfterHuman: FormatRetryAfter(iso),
		WaitMs:          &waitMs,
	}
}

// FormatRetryAfter formats an ISO timestamp as a human-readable relative time.
func FormatRetryAfter(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return ""
	}
	d := time.Until(t)
	if d <= 0 {
		return "now"
	}
	sec := int(d.Seconds())
	if sec < 60 {
		return strconv.Itoa(sec) + "s"
	}
	min := sec / 60
	if min < 60 {
		return strconv.Itoa(min) + "m"
	}
	return strconv.Itoa(min/60) + "h"
}

// RetryDecision is the outcome of a retry decision.
type RetryDecision struct {
	ShouldRetry     bool
	RetryAfter      *string
	RetryAfterHuman string
	WaitMs          int64
}

// GetCooldownAwareRetryDecision decides whether to retry given settings/attempt/budget.
func GetCooldownAwareRetryDecision(retryAfter any, settings CooldownAwareRetrySettings, attempt int, budgetLeftMs *int64, nowMs int64) RetryDecision {
	closest := ComputeClosestRetryAfter(retryAfter, nowMs)
	effectiveBudget := settings.BudgetMs
	if budgetLeftMs != nil {
		effectiveBudget = *budgetLeftMs
	}

	if !settings.Enabled || settings.MaxRetries <= 0 || settings.MaxRetryWaitMs <= 0 ||
		attempt >= settings.MaxRetries || closest.WaitMs == nil {
		return RetryDecision{
			ShouldRetry:     false,
			RetryAfter:      closest.RetryAfter,
			RetryAfterHuman: closest.RetryAfterHuman,
			WaitMs:          0,
		}
	}

	if *closest.WaitMs > settings.MaxRetryWaitMs || *closest.WaitMs > effectiveBudget {
		return RetryDecision{
			ShouldRetry:     false,
			RetryAfter:      closest.RetryAfter,
			RetryAfterHuman: closest.RetryAfterHuman,
			WaitMs:          *closest.WaitMs,
		}
	}

	return RetryDecision{
		ShouldRetry:     true,
		RetryAfter:      closest.RetryAfter,
		RetryAfterHuman: closest.RetryAfterHuman,
		WaitMs:          *closest.WaitMs,
	}
}

// WaitForCooldown sleeps for waitMs or until ctx is cancelled. Returns false
// if cancelled before the wait completed.
func WaitForCooldown(ctx context.Context, waitMs int64) bool {
	if ctx.Err() != nil {
		return false
	}
	if waitMs <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(time.Duration(waitMs) * time.Millisecond)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
