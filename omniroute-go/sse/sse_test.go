package sse

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestEvalCapacity(t *testing.T) {
	if r := EvalCapacity(5, 10); r.ShouldReject {
		t.Fatal("under cap → allow")
	}
	if r := EvalCapacity(10, 10); !r.ShouldReject {
		t.Fatal("at cap → reject")
	}
	if r := EvalCapacity(10, 0); r.ShouldReject {
		t.Fatal("cap 0 → disabled")
	}
	r := EvalCapacity(20, 10)
	if !r.ShouldReject || r.Status != 429 || r.RetryAfter < 1 {
		t.Fatalf("over cap: %+v", r)
	}
}

func TestReadConnectionCap(t *testing.T) {
	if ReadConnectionCap(map[string]string{}) != 0 {
		t.Fatal("default 0")
	}
	if ReadConnectionCap(map[string]string{"OMNI_MAX_CONCURRENT_CONNECTIONS": "100"}) != 100 {
		t.Fatal("100")
	}
	if ReadConnectionCap(map[string]string{"OMNI_MAX_CONCURRENT_CONNECTIONS": "abc"}) != 0 {
		t.Fatal("invalid → 0")
	}
}

func TestStreamTrackerTransitions(t *testing.T) {
	tr := NewStreamTracker("req1", StreamMetadata{Model: "gpt-4o", Provider: "openai"})
	if tr.State() != StateInitialized {
		t.Fatal("initial state")
	}
	if !tr.Transition(StateConnecting) {
		t.Fatal("init → connecting valid")
	}
	if !tr.Transition(StateStreaming) {
		t.Fatal("connecting → streaming valid")
	}
	if s := tr.GetSummary(); s.TTFB < 0 {
		t.Fatal("firstChunkAt set on streaming (TTFB >= 0)")
	}
	tr.RecordChunk(100)
	tr.RecordChunk(200)
	if !tr.Transition(StateCompleted) {
		t.Fatal("streaming → completed valid")
	}
	if !tr.IsTerminal() {
		t.Fatal("completed is terminal")
	}
	// invalid transition from terminal
	if tr.Transition(StateStreaming) {
		t.Fatal("completed → streaming invalid")
	}
	s := tr.GetSummary()
	if s.ChunkCount != 2 || s.TotalBytes != 300 || s.State != StateCompleted {
		t.Fatalf("summary = %+v", s)
	}
}

func TestStreamTrackerFail(t *testing.T) {
	tr := NewStreamTracker("req2", StreamMetadata{})
	tr.Transition(StateConnecting)
	tr.Fail("connection reset")
	if tr.State() != StateFailed {
		t.Fatalf("state=%s, want failed", tr.State())
	}
	if s := tr.GetSummary(); s.Error != "connection reset" {
		t.Fatalf("error=%q", s.Error)
	}
	if !tr.IsTerminal() {
		t.Fatal("failed is terminal")
	}
}

func TestStreamRegistry(t *testing.T) {
	reg := NewStreamRegistry(2)
	reg.CreateStreamTracker("r1", StreamMetadata{Model: "m1"})
	reg.CreateStreamTracker("r2", StreamMetadata{Model: "m2"})
	if len(reg.GetActiveStreams()) != 2 {
		t.Fatal("2 active")
	}
	reg.ArchiveStream("r1")
	if len(reg.GetActiveStreams()) != 1 {
		t.Fatal("1 active after archive")
	}
	// history bounded
	reg.CreateStreamTracker("r3", StreamMetadata{})
	reg.ArchiveStream("r2")
	reg.ArchiveStream("r3")
	reg.mu.Lock()
	histLen := len(reg.completed)
	reg.mu.Unlock()
	if histLen > 2 {
		t.Fatalf("history bounded to 2, got %d", histLen)
	}
}

func TestStreamRegistryConcurrent(t *testing.T) {
	reg := NewStreamRegistry(50)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rid := "r" + string(rune('a'+id))
			reg.CreateStreamTracker(rid, StreamMetadata{})
			reg.GetActiveStreams()
			reg.ArchiveStream(rid)
		}(i)
	}
	wg.Wait()
}

// TestStreamTrackerConcurrentRace proves the critical race fix: concurrent
// Transition/RecordChunk/Fail vs GetSummary must not trigger -race.
func TestStreamTrackerConcurrentRace(t *testing.T) {
	tr := NewStreamTracker("race-test", StreamMetadata{Model: "m"})
	tr.Transition(StateConnecting)
	tr.Transition(StateStreaming)

	var wg sync.WaitGroup
	// Writers: record chunks and transition concurrently.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				tr.RecordChunk(10)
			}
		}()
	}
	// Readers: GetSummary concurrently (this raced before the fix).
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = tr.GetSummary()
				_ = tr.State()
				_ = tr.IsTerminal()
			}
		}()
	}
	wg.Wait()
	// Final state must be consistent.
	s := tr.GetSummary()
	if s.ChunkCount != 8*200 {
		t.Fatalf("chunkCount = %d, want %d (lost updates = race)", s.ChunkCount, 8*200)
	}
}

func TestCooldownAwareRetryDecision(t *testing.T) {
	settings := CooldownAwareRetrySettings{
		Enabled: true, MaxRetries: 3, MaxRetryWaitSec: 60,
		MaxRetryWaitMs: 60000, BudgetMs: 120000,
	}
	nowMs := int64(1_700_000_000_000)
	// retry-after 10s in future → should retry
	retryAt := time.UnixMilli(nowMs + 10000).UTC().Format(time.RFC3339)
	d := GetCooldownAwareRetryDecision(retryAt, settings, 0, nil, nowMs)
	if !d.ShouldRetry || d.WaitMs != 10000 {
		t.Fatalf("10s wait: %+v", d)
	}
	// attempt >= maxRetries → no retry
	d = GetCooldownAwareRetryDecision(retryAt, settings, 3, nil, nowMs)
	if d.ShouldRetry {
		t.Fatal("at maxRetries → no retry")
	}
	// wait > maxRetryWaitMs → no retry
	farRetry := time.UnixMilli(nowMs + 120000).UTC().Format(time.RFC3339)
	d = GetCooldownAwareRetryDecision(farRetry, settings, 0, nil, nowMs)
	if d.ShouldRetry {
		t.Fatal("wait > max → no retry")
	}
	// wait > budget → no retry
	budget := int64(5000)
	d = GetCooldownAwareRetryDecision(retryAt, settings, 0, &budget, nowMs)
	if d.ShouldRetry {
		t.Fatal("wait > budget → no retry")
	}
	// disabled → no retry
	disabled := settings
	disabled.Enabled = false
	d = GetCooldownAwareRetryDecision(retryAt, disabled, 0, nil, nowMs)
	if d.ShouldRetry {
		t.Fatal("disabled → no retry")
	}
}

func TestComputeClosestRetryAfter(t *testing.T) {
	nowMs := int64(1_700_000_000_000)
	// nil → empty
	if r := ComputeClosestRetryAfter(nil, nowMs); r.WaitMs != nil {
		t.Fatal("nil → empty")
	}
	// ISO string
	iso := time.UnixMilli(nowMs + 5000).UTC().Format(time.RFC3339)
	r := ComputeClosestRetryAfter(iso, nowMs)
	if r.WaitMs == nil || *r.WaitMs < 4000 || *r.WaitMs > 6000 {
		t.Fatalf("ISO wait: %+v", r)
	}
	// past → clamped to 0
	past := time.UnixMilli(nowMs - 5000).UTC().Format(time.RFC3339)
	r = ComputeClosestRetryAfter(past, nowMs)
	if r.WaitMs == nil || *r.WaitMs != 0 {
		t.Fatalf("past → 0, got %+v", r)
	}
}

func TestWaitForCooldown(t *testing.T) {
	// immediate (0ms)
	if !WaitForCooldown(context.Background(), 0) {
		t.Fatal("0ms → true")
	}
	// cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if WaitForCooldown(ctx, 1000) {
		t.Fatal("cancelled → false")
	}
	// short wait completes
	if !WaitForCooldown(context.Background(), 10) {
		t.Fatal("10ms → true")
	}
}

func TestResolveChatRequestBody(t *testing.T) {
	body, ok := ResolveChatRequestBody(map[string]any{"model": "gpt-4o"})
	if !ok || body == nil {
		t.Fatal("pre-parsed → returned")
	}
	_, ok = ResolveChatRequestBody(nil)
	if ok {
		t.Fatal("nil → not pre-parsed")
	}
}

func TestReadStreamHistoryMax(t *testing.T) {
	if ReadStreamHistoryMax(map[string]string{}) != 50 {
		t.Fatal("default 50")
	}
	if ReadStreamHistoryMax(map[string]string{"STREAM_HISTORY_MAX": "100"}) != 100 {
		t.Fatal("100")
	}
}
