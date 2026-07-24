package sse

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

// streamstate.go — Stream lifecycle state machine + active stream registry.
// Port of src/sse/services/streamState.ts
//
// RACE NOTE (fixed per adversarial review): each StreamTracker owns a
// sync.RWMutex protecting ALL of its mutable fields. The registry mutex only
// protects the active/completed collections; it does NOT protect tracker
// fields, because callers hold *StreamTracker pointers and mutate them
// independently. GetSummary takes the tracker's RLock, so concurrent
// Transition/RecordChunk/Fail are properly synchronized.

// StreamState is a stream lifecycle state.
type StreamState string

const (
	StateInitialized StreamState = "initialized"
	StateConnecting  StreamState = "connecting"
	StateStreaming   StreamState = "streaming"
	StateCompleted   StreamState = "completed"
	StateFailed      StreamState = "failed"
	StateCancelled   StreamState = "cancelled"
)

// validTransitions defines the allowed state graph.
var validTransitions = map[StreamState][]StreamState{
	StateInitialized: {StateConnecting, StateCancelled},
	StateConnecting:  {StateStreaming, StateFailed, StateCancelled},
	StateStreaming:   {StateCompleted, StateFailed, StateCancelled},
	StateCompleted:   {},
	StateFailed:      {},
	StateCancelled:   {},
}

// StreamTransition records one state change.
type StreamTransition struct {
	From    StreamState
	To      StreamState
	At      int64
	Elapsed int64
}

// StreamMetadata holds optional stream context.
type StreamMetadata struct {
	Model    string
	Provider string
}

// StreamTracker tracks the lifecycle of a single SSE stream. All mutable
// fields are guarded by mu.
type StreamTracker struct {
	mu           sync.RWMutex
	requestID    string
	state        StreamState
	metadata     StreamMetadata
	transitions  []StreamTransition
	startedAt    int64
	completedAt  int64 // 0 = not completed
	firstChunkAt int64 // 0 = no chunk yet
	chunkCount   int
	totalBytes   int64
	errMsg       string
	now          func() int64
}

// NewStreamTracker creates a tracker in the INITIALIZED state.
func NewStreamTracker(requestID string, meta StreamMetadata) *StreamTracker {
	now := time.Now().UnixMilli()
	return &StreamTracker{
		requestID: requestID,
		state:     StateInitialized,
		metadata:  meta,
		startedAt: now,
		now:       func() int64 { return time.Now().UnixMilli() },
	}
}

// SetClock overrides the clock (for deterministic testing).
func (t *StreamTracker) SetClock(now func() int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if now != nil {
		t.now = now
	}
}

// RequestID returns the tracker's request ID (immutable after creation).
func (t *StreamTracker) RequestID() string {
	return t.requestID
}

// State returns the current state.
func (t *StreamTracker) State() StreamState {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.state
}

// transitionLocked performs a transition. Caller must hold t.mu (write).
func (t *StreamTracker) transitionLocked(newState StreamState) bool {
	allowed := validTransitions[t.state]
	ok := false
	for _, s := range allowed {
		if s == newState {
			ok = true
			break
		}
	}
	if !ok {
		return false
	}
	now := t.now()
	t.transitions = append(t.transitions, StreamTransition{
		From:    t.state,
		To:      newState,
		At:      now,
		Elapsed: now - t.startedAt,
	})
	t.state = newState
	if newState == StateStreaming && t.firstChunkAt == 0 {
		t.firstChunkAt = now
	}
	if newState == StateCompleted || newState == StateFailed || newState == StateCancelled {
		t.completedAt = now
	}
	return true
}

// Transition moves to a new state. Returns false if the transition is invalid.
func (t *StreamTracker) Transition(newState StreamState) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.transitionLocked(newState)
}

// RecordChunk records a received chunk.
func (t *StreamTracker) RecordChunk(bytes int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.chunkCount++
	t.totalBytes += bytes
}

// Fail marks the stream failed with an error, atomically with the transition.
func (t *StreamTracker) Fail(err string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.errMsg = err
	t.transitionLocked(StateFailed)
}

// IsTerminal returns whether the stream is in a terminal state.
func (t *StreamTracker) IsTerminal() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.state == StateCompleted || t.state == StateFailed || t.state == StateCancelled
}

// StreamSummary is the telemetry summary for a stream.
type StreamSummary struct {
	RequestID   string
	State       StreamState
	Model       string
	Provider    string
	Duration    int64
	TTFB        int64 // -1 = no first chunk
	ChunkCount  int
	TotalBytes  int64
	Transitions int
	Error       string
}

// GetSummary returns the telemetry summary (takes the tracker RLock).
func (t *StreamTracker) GetSummary() StreamSummary {
	t.mu.RLock()
	defer t.mu.RUnlock()
	endTime := t.completedAt
	if endTime == 0 {
		endTime = t.now()
	}
	ttfb := int64(-1)
	if t.firstChunkAt != 0 {
		ttfb = t.firstChunkAt - t.startedAt
	}
	return StreamSummary{
		RequestID:   t.requestID,
		State:       t.state,
		Model:       t.metadata.Model,
		Provider:    t.metadata.Provider,
		Duration:    endTime - t.startedAt,
		TTFB:        ttfb,
		ChunkCount:  t.chunkCount,
		TotalBytes:  t.totalBytes,
		Transitions: len(t.transitions),
		Error:       t.errMsg,
	}
}

// ---------------------------------------------------------------------------
// Active Stream Registry
// ---------------------------------------------------------------------------

// StreamRegistry tracks active and completed streams.
type StreamRegistry struct {
	mu         sync.Mutex
	active     map[string]*StreamTracker
	completed  []StreamSummary
	maxHistory int
}

// NewStreamRegistry creates a registry with a bounded completed history.
func NewStreamRegistry(maxHistory int) *StreamRegistry {
	if maxHistory <= 0 {
		maxHistory = 50
	}
	return &StreamRegistry{
		active:     make(map[string]*StreamTracker),
		maxHistory: maxHistory,
	}
}

// CreateStreamTracker creates and registers a new tracker. If a tracker with
// the same requestID already exists, the existing one is returned (no silent
// overwrite — prevents an old owner from archiving a newer tracker).
func (r *StreamRegistry) CreateStreamTracker(requestID string, meta StreamMetadata) *StreamTracker {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing := r.active[requestID]; existing != nil {
		return existing
	}
	t := NewStreamTracker(requestID, meta)
	r.active[requestID] = t
	return t
}

// ArchiveStream moves a stream from active to completed history.
func (r *StreamRegistry) ArchiveStream(requestID string) {
	r.mu.Lock()
	t := r.active[requestID]
	if t == nil {
		r.mu.Unlock()
		return
	}
	delete(r.active, requestID)
	// Snapshot the summary while still holding r.mu so the tracker cannot be
	// re-registered under the same id mid-archive; GetSummary takes t's own
	// RLock for field consistency.
	summary := t.GetSummary()
	r.completed = append(r.completed, summary)
	for len(r.completed) > r.maxHistory {
		r.completed = r.completed[1:]
	}
	r.mu.Unlock()
}

// GetActiveStreams returns summaries of all active streams.
func (r *StreamRegistry) GetActiveStreams() []StreamSummary {
	r.mu.Lock()
	trackers := make([]*StreamTracker, 0, len(r.active))
	for _, t := range r.active {
		trackers = append(trackers, t)
	}
	r.mu.Unlock()
	// Summarize outside the registry lock; each GetSummary takes the tracker's
	// own RLock, so this is race-free against concurrent Transition/RecordChunk.
	out := make([]StreamSummary, 0, len(trackers))
	for _, t := range trackers {
		out = append(out, t.GetSummary())
	}
	return out
}

// ReadStreamHistoryMax reads STREAM_HISTORY_MAX from env (default 50).
func ReadStreamHistoryMax(env map[string]string) int {
	raw := env["STREAM_HISTORY_MAX"]
	if raw == "" {
		return 50
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return 50
	}
	return n
}
