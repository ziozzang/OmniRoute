package domain

import (
	"sort"
	"strconv"
	"sync"
	"time"
)

// providerexpiration.go — Provider credential expiration tracking.
// Port of src/domain/providerExpiration.ts
//
// RACE NOTE: the global expirations map AND the expNow clock are guarded by a
// single sync.RWMutex. Every public function snapshots `now := expNow()` while
// holding the lock, and stored entries are never mutated in place (status is
// computed into copies) so concurrent readers never race on shared state.

type ExpiryType string

const (
	ExpiryOAuthToken    ExpiryType = "oauth_token"
	ExpirySubscription  ExpiryType = "subscription"
	ExpiryAPICredits    ExpiryType = "api_credits"
	ExpiryFreeTierReset ExpiryType = "free_tier_reset"
)

type ExpiryStatus string

const (
	ExpiryActive       ExpiryStatus = "active"
	ExpiryExpiringSoon ExpiryStatus = "expiring_soon"
	ExpiryExpired      ExpiryStatus = "expired"
	ExpiryUnknown      ExpiryStatus = "unknown"
)

type ProviderExpiration struct {
	ConnectionID   string
	Provider       string
	ConnectionName string
	ExpiresAt      *time.Time
	ExpiryType     ExpiryType
	AlertDays      int
	LastChecked    time.Time
	Status         ExpiryStatus
	Note           string
}

type ExpirationSummary struct {
	Total          int
	Active         int
	ExpiringSoon   int
	Expired        int
	Unknown        int
	NextExpiration *ProviderExpiration
}

var (
	expMu    sync.RWMutex
	expStore = make(map[string]ProviderExpiration) // stored by value; never mutated in place
	expNow   = time.Now                            // guarded by expMu
)

// SetExpiryClock overrides the clock for deterministic testing.
func SetExpiryClock(now func() time.Time) {
	expMu.Lock()
	defer expMu.Unlock()
	expNow = now
}

// calculateStatus computes status from an expiry date relative to now.
// Pure: takes now as a parameter (caller snapshots it under the lock).
func calculateStatus(expiresAt *time.Time, alertDays int, now time.Time) ExpiryStatus {
	if expiresAt == nil {
		return ExpiryUnknown
	}
	if !expiresAt.After(now) {
		return ExpiryExpired
	}
	daysUntil := expiresAt.Sub(now).Hours() / 24
	if daysUntil <= float64(alertDays) {
		return ExpiryExpiringSoon
	}
	return ExpiryActive
}

func SetExpiration(connectionID, provider, connectionName string, expiresAt *time.Time, expiryType ExpiryType, alertDays int, note string) ProviderExpiration {
	if alertDays <= 0 {
		alertDays = 7
	}
	expMu.Lock()
	now := expNow()
	entry := ProviderExpiration{
		ConnectionID:   connectionID,
		Provider:       provider,
		ConnectionName: connectionName,
		ExpiresAt:      expiresAt,
		ExpiryType:     expiryType,
		AlertDays:      alertDays,
		LastChecked:    now,
		Status:         calculateStatus(expiresAt, alertDays, now),
		Note:           note,
	}
	expStore[connectionID] = entry
	expMu.Unlock()
	return entry
}

func GetExpiration(connectionID string) *ProviderExpiration {
	expMu.RLock()
	entry, ok := expStore[connectionID]
	now := expNow()
	expMu.RUnlock()
	if !ok {
		return nil
	}
	entry.Status = calculateStatus(entry.ExpiresAt, entry.AlertDays, now) // mutate the local copy
	return &entry
}

func GetAllExpirations() []ProviderExpiration {
	expMu.RLock()
	now := expNow()
	out := make([]ProviderExpiration, 0, len(expStore))
	for _, e := range expStore {
		e.Status = calculateStatus(e.ExpiresAt, e.AlertDays, now) // mutate the local copy
		out = append(out, e)
	}
	expMu.RUnlock()
	order := map[ExpiryStatus]int{ExpiryExpired: 0, ExpiryExpiringSoon: 1, ExpiryActive: 2, ExpiryUnknown: 3}
	sort.SliceStable(out, func(i, j int) bool {
		return order[out[i].Status] < order[out[j].Status]
	})
	return out
}

func GetExpiringSoon() []ProviderExpiration {
	all := GetAllExpirations()
	var out []ProviderExpiration
	for _, e := range all {
		if e.Status == ExpiryExpired || e.Status == ExpiryExpiringSoon {
			out = append(out, e)
		}
	}
	return out
}

func GetExpirationSummary() ExpirationSummary {
	expMu.RLock()
	now := expNow()
	expMu.RUnlock()

	all := GetAllExpirations()
	s := ExpirationSummary{Total: len(all)}
	var nearestMs int64 = -1
	for i := range all {
		switch all[i].Status {
		case ExpiryActive:
			s.Active++
		case ExpiryExpiringSoon:
			s.ExpiringSoon++
		case ExpiryExpired:
			s.Expired++
		case ExpiryUnknown:
			s.Unknown++
		}
		if all[i].ExpiresAt != nil {
			ms := all[i].ExpiresAt.Sub(now).Milliseconds()
			if ms > 0 && (nearestMs < 0 || ms < nearestMs) {
				nearestMs = ms
				s.NextExpiration = &all[i]
			}
		}
	}
	return s
}

func RemoveExpiration(connectionID string) bool {
	expMu.Lock()
	defer expMu.Unlock()
	_, ok := expStore[connectionID]
	delete(expStore, connectionID)
	return ok
}

type DetectedExpiration struct {
	ExpiresAt  time.Time
	ExpiryType ExpiryType
}

func DetectExpirationFromResponse(provider string, status int, headers map[string]string) *DetectedExpiration {
	expMu.RLock()
	now := expNow()
	expMu.RUnlock()

	if status == 401 {
		return &DetectedExpiration{ExpiresAt: now, ExpiryType: ExpiryOAuthToken}
	}
	if status == 402 {
		return &DetectedExpiration{ExpiresAt: now, ExpiryType: ExpirySubscription}
	}
	resetHeader := headers["x-ratelimit-reset"]
	if resetHeader == "" {
		resetHeader = headers["x-ratelimit-reset-tokens"]
	}
	if resetHeader == "" {
		resetHeader = headers["retry-after"]
	}
	if resetHeader != "" && status == 429 {
		resetTime, err := strconv.ParseInt(resetHeader, 10, 64)
		if err == nil {
			var t time.Time
			if resetTime > 1_000_000_000 {
				t = time.Unix(resetTime, 0)
			} else {
				t = now.Add(time.Duration(resetTime) * time.Second)
			}
			return &DetectedExpiration{ExpiresAt: t, ExpiryType: ExpiryFreeTierReset}
		}
	}
	return nil
}

func ResetExpirations() {
	expMu.Lock()
	defer expMu.Unlock()
	expStore = make(map[string]ProviderExpiration)
}
