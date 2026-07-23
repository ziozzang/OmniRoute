package domain

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

// configaudit.go — Configuration audit trail with diff detection.
// Port of src/domain/configAudit.ts
//
// RACE NOTE: the global auditLog slice and idCounter are guarded by a sync.Mutex.

type AuditTarget string

const (
	AuditProvider   AuditTarget = "provider"
	AuditCombo      AuditTarget = "combo"
	AuditPolicy     AuditTarget = "policy"
	AuditConnection AuditTarget = "connection"
	AuditSettings   AuditTarget = "settings"
)

type AuditSource string

const (
	AuditSrcDashboard   AuditSource = "dashboard"
	AuditSrcAPI         AuditSource = "api"
	AuditSrcSync        AuditSource = "sync"
	AuditSrcAutoHealing AuditSource = "auto-healing"
	AuditSrcCLI         AuditSource = "cli"
	AuditSrcMCP         AuditSource = "mcp"
)

type AuditAction string

const (
	AuditCreate  AuditAction = "create"
	AuditUpdate  AuditAction = "update"
	AuditDelete  AuditAction = "delete"
	AuditEnable  AuditAction = "enable"
	AuditDisable AuditAction = "disable"
)

type ConfigDiff struct {
	Added   []string
	Removed []string
	Changed []DiffChange
	IsEmpty bool
}

type DiffChange struct {
	Key  string
	From any
	To   any
}

type ConfigAuditEntry struct {
	ID         string
	seq        int // monotonic insertion order; tie-breaker for equal timestamps
	Timestamp  time.Time
	Action     AuditAction
	Target     AuditTarget
	TargetID   string
	TargetName string
	Before     map[string]any
	After      map[string]any
	Source     AuditSource
	Diff       ConfigDiff
	Note       string
}

type ConfigSnapshot struct {
	Timestamp   time.Time
	Version     string
	Description string
	Data        map[string]any
}

var (
	auditMu    sync.Mutex
	auditLog   []ConfigAuditEntry
	idCounter  int
	auditNow   = time.Now
)

// SetAuditClock overrides the clock for deterministic testing.
func SetAuditClock(now func() time.Time) {
	auditMu.Lock()
	defer auditMu.Unlock()
	auditNow = now
}

func generateAuditID() string {
	idCounter++
	ts := fmt.Sprintf("%x", auditNow().UnixMilli())
	return fmt.Sprintf("audit-%s-%04x", ts, idCounter)
}

// ComputeDiff computes a structured diff between two config states.
func ComputeDiff(before, after map[string]any) ConfigDiff {
	beforeKeys := make(map[string]bool)
	for k := range before {
		beforeKeys[k] = true
	}
	afterKeys := make(map[string]bool)
	for k := range after {
		afterKeys[k] = true
	}

	var added, removed []string
	var changed []DiffChange

	for k := range afterKeys {
		if !beforeKeys[k] {
			added = append(added, k)
		}
	}
	for k := range beforeKeys {
		if !afterKeys[k] {
			removed = append(removed, k)
		}
	}
	for k := range beforeKeys {
		if afterKeys[k] {
			bJSON, _ := json.Marshal(before[k])
			aJSON, _ := json.Marshal(after[k])
			if string(bJSON) != string(aJSON) {
				changed = append(changed, DiffChange{Key: k, From: before[k], To: after[k]})
			}
		}
	}
	sort.Strings(added)
	sort.Strings(removed)

	return ConfigDiff{
		Added:   added,
		Removed: removed,
		Changed: changed,
		IsEmpty: len(added) == 0 && len(removed) == 0 && len(changed) == 0,
	}
}

func RecordChange(action AuditAction, target AuditTarget, targetID, targetName string, before, after map[string]any, source AuditSource, note string) ConfigAuditEntry {
	auditMu.Lock()
	defer auditMu.Unlock()
	id := generateAuditID()
	entry := ConfigAuditEntry{
		ID:         id,
		seq:        idCounter,
		Timestamp:  auditNow(),
		Action:     action,
		Target:     target,
		TargetID:   targetID,
		TargetName: targetName,
		Before:     before,
		After:      after,
		Source:     source,
		Diff:       ComputeDiff(before, after),
		Note:       note,
	}
	auditLog = append(auditLog, entry)
	if len(auditLog) > 1000 {
		auditLog = auditLog[len(auditLog)-1000:]
	}
	return entry
}

type AuditLogOptions struct {
	Target   *AuditTarget
	TargetID *string
	Action   *AuditAction
	Source   *AuditSource
	Since    *time.Time
	Limit    int
	Offset   int
}

type AuditLogResult struct {
	Entries []ConfigAuditEntry
	Total   int
}

func GetAuditLog(opts *AuditLogOptions) AuditLogResult {
	auditMu.Lock()
	defer auditMu.Unlock()

	var filtered []ConfigAuditEntry
	for _, e := range auditLog {
		if opts != nil {
			if opts.Target != nil && e.Target != *opts.Target {
				continue
			}
			if opts.TargetID != nil && e.TargetID != *opts.TargetID {
				continue
			}
			if opts.Action != nil && e.Action != *opts.Action {
				continue
			}
			if opts.Source != nil && e.Source != *opts.Source {
				continue
			}
			if opts.Since != nil && e.Timestamp.Before(*opts.Since) {
				continue
			}
		}
		filtered = append(filtered, e)
	}

	total := len(filtered)
	// Sort newest first; break timestamp ties by insertion sequence (higher = newer).
	sort.SliceStable(filtered, func(i, j int) bool {
		if !filtered[i].Timestamp.Equal(filtered[j].Timestamp) {
			return filtered[i].Timestamp.After(filtered[j].Timestamp)
		}
		return filtered[i].seq > filtered[j].seq
	})

	offset := 0
	limit := 50
	if opts != nil {
		if opts.Offset > 0 {
			offset = opts.Offset
		}
		if opts.Limit > 0 {
			limit = opts.Limit
		}
	}
	if offset >= len(filtered) {
		filtered = nil
	} else {
		end := offset + limit
		if end > len(filtered) {
			end = len(filtered)
		}
		filtered = filtered[offset:end]
	}

	return AuditLogResult{Entries: filtered, Total: total}
}

func GetAuditEntry(id string) *ConfigAuditEntry {
	auditMu.Lock()
	defer auditMu.Unlock()
	for i := range auditLog {
		if auditLog[i].ID == id {
			cp := auditLog[i]
			return &cp
		}
	}
	return nil
}

func GetRollbackState(entryID string) map[string]any {
	entry := GetAuditEntry(entryID)
	if entry == nil {
		return nil
	}
	return entry.Before
}

func CreateSnapshot(version, description string, configData map[string]any) ConfigSnapshot {
	// Deep clone via JSON round-trip.
	data, _ := json.Marshal(configData)
	var cloned map[string]any
	json.Unmarshal(data, &cloned)
	return ConfigSnapshot{
		Timestamp:   auditNow(),
		Version:     version,
		Description: description,
		Data:        cloned,
	}
}

type AuditSummary struct {
	TotalEntries int
	ByTarget     map[string]int
	ByAction     map[string]int
	BySource     map[string]int
	OldestEntry  *time.Time
	NewestEntry  *time.Time
}

func GetAuditSummary() AuditSummary {
	auditMu.Lock()
	defer auditMu.Unlock()
	s := AuditSummary{
		TotalEntries: len(auditLog),
		ByTarget:     make(map[string]int),
		ByAction:     make(map[string]int),
		BySource:     make(map[string]int),
	}
	for _, e := range auditLog {
		s.ByTarget[string(e.Target)]++
		s.ByAction[string(e.Action)]++
		s.BySource[string(e.Source)]++
	}
	if len(auditLog) > 0 {
		oldest := auditLog[0].Timestamp
		newest := auditLog[len(auditLog)-1].Timestamp
		s.OldestEntry = &oldest
		s.NewestEntry = &newest
	}
	return s
}

func ResetAuditLog() {
	auditMu.Lock()
	defer auditMu.Unlock()
	auditLog = nil
	idCounter = 0
}
