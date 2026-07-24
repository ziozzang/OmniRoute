package shared

import (
	"sync"
	"unicode/utf8"
)

// chatadmission.go — Bounded admission for chat completions.
// Port of src/shared/middleware/chatBodyAdmission.ts
//
// The ChatAdmissionController is a process-local semaphore that reserves
// heavyweight capacity before parsing large chat bodies. The capacity check
// and increment are atomic under a sync.Mutex.
//
// RACE NOTE: the TS original uses a single-threaded event loop (#activeHeavy
// mutated in one synchronous turn). In Go, concurrent requests call
// TryAcquireHeavy/Release from multiple goroutines, so a sync.Mutex is
// required. Every public method takes the lock; there is no nested locking.

// Default thresholds (from env in TS; hardcoded defaults here).
const (
	DefaultChatLargeBodyBytes    = 256 * 1024
	DefaultChatHardMaxBodyBytes  = 50 * 1024 * 1024
	DefaultChatMaxHeavyInFlight  = 1
	DefaultChatHeavyMessageCount = 200
	DefaultChatHeavyToolCount    = 64
	DefaultChatHeavyEstTokens    = 32_000
	DefaultChatHardMaxMessages   = 800
)

// ChatAdmissionLease is a heavyweight capacity reservation.
type ChatAdmissionLease struct {
	controller *ChatAdmissionController
	once       sync.Once
}

// Released returns whether the lease has been released.
// Note: in Go we track this via the Once; there's no direct "released" bool
// exposed (TS uses a closure variable). For test parity we track it.
func (l *ChatAdmissionLease) Release() {
	l.once.Do(func() {
		l.controller.mu.Lock()
		if l.controller.activeHeavy > 0 {
			l.controller.activeHeavy--
		}
		l.controller.mu.Unlock()
	})
}

// ChatAdmissionController is a process-local heavyweight semaphore.
type ChatAdmissionController struct {
	mu              sync.Mutex
	activeHeavy     int
	maxHeavyInFlight int
}

// NewChatAdmissionController creates a controller with the given capacity.
// Panics if maxHeavyInFlight < 1 (mirrors TS RangeError).
func NewChatAdmissionController(maxHeavyInFlight int) *ChatAdmissionController {
	if maxHeavyInFlight < 1 {
		panic("maxHeavyInFlight must be a positive integer")
	}
	return &ChatAdmissionController{maxHeavyInFlight: maxHeavyInFlight}
}

// ActiveHeavy returns the current number of active heavyweight reservations.
func (c *ChatAdmissionController) ActiveHeavy() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.activeHeavy
}

// TryAcquireHeavy attempts to reserve heavyweight capacity. Returns nil if
// capacity is unavailable (retryable 503 in the HTTP layer).
func (c *ChatAdmissionController) TryAcquireHeavy() *ChatAdmissionLease {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.activeHeavy >= c.maxHeavyInFlight {
		return nil
	}
	c.activeHeavy++
	return &ChatAdmissionLease{controller: c}
}

// ---------------------------------------------------------------------------
// Token estimation (pure functions)
// ---------------------------------------------------------------------------

// ConservativeStringTokens estimates tokens for a string: ASCII chars cost 0.25,
// non-ASCII cost 1. Stops early when the estimate reaches remaining.
func ConservativeStringTokens(value string, remaining float64) float64 {
	var tokens float64
	for _, r := range value {
		if r < 0x80 {
			tokens += 0.25
		} else {
			tokens += 1
		}
		if tokens >= remaining {
			return remaining
		}
	}
	return tokens
}

// TokenEstimate is the result of a structure token estimation.
type TokenEstimate struct {
	Tokens    float64
	Exhausted bool
}

// EstimateStructureTokens estimates tokens for an arbitrary JSON-like structure
// using bounded DFS (max 10,000 nodes, max depth 12). Mirrors the TS
// estimateStructureTokens.
func EstimateStructureTokens(value any, limit float64) TokenEstimate {
	type stackItem struct {
		value any
		depth int
	}
	var tokens float64
	visited := 0
	const maxNodes = 10_000
	const maxDepth = 12

	stack := []stackItem{{value: value, depth: 0}}
	for len(stack) > 0 && tokens < limit && visited < maxNodes {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		visited++

		switch v := current.value.(type) {
		case string:
			tokens += ConservativeStringTokens(v, limit-tokens)
			continue
		case nil:
			continue
		}

		// Must be an object-like thing (map or slice).
		if current.depth >= maxDepth {
			return TokenEstimate{Tokens: tokens, Exhausted: true}
		}

		switch v := current.value.(type) {
		case []any:
			remainingNodes := maxNodes - visited - len(stack)
			if len(v) > remainingNodes {
				return TokenEstimate{Tokens: tokens, Exhausted: true}
			}
			for _, child := range v {
				stack = append(stack, stackItem{value: child, depth: current.depth + 1})
			}
		case map[string]any:
			remainingNodes := maxNodes - visited - len(stack)
			children := 0
			for key, val := range v {
				children++
				if children > remainingNodes {
					return TokenEstimate{Tokens: tokens, Exhausted: true}
				}
				tokens += ConservativeStringTokens(key, limit-tokens)
				if tokens >= limit {
					return TokenEstimate{Tokens: limit, Exhausted: false}
				}
				stack = append(stack, stackItem{value: val, depth: current.depth + 1})
			}
		default:
			// Not a string, slice, or map — skip (numbers, bools, etc.)
			continue
		}
	}
	return TokenEstimate{Tokens: tokens, Exhausted: len(stack) > 0 && tokens < limit}
}

// ---------------------------------------------------------------------------
// Structure admission
// ---------------------------------------------------------------------------

// ChatStructureAdmissionOptions configures admitChatStructure.
type ChatStructureAdmissionOptions struct {
	Controller    *ChatAdmissionController
	MaxMessages   int
	HeavyMessages int
	HeavyTools    int
	HeavyTokens   float64
}

// ChatStructureAdmission is the result of structure-based admission.
type ChatStructureAdmission struct {
	Admit bool
	Lease *ChatAdmissionLease
	// RejectReason is set when Admit is false: "message_limit" or "structure_limit".
	RejectReason string
	// RejectStatus is 413 (message limit) or 503 (capacity busy).
	RejectStatus int
}

// AdmitChatStructure checks whether a chat request body is structurally
// admissible. Mirrors TS admitChatStructure (without HTTP Request/Response).
//
// body should be a map[string]any (parsed JSON object). Non-object bodies are
// always admitted.
func AdmitChatStructure(body any, lease *ChatAdmissionLease, opts ChatStructureAdmissionOptions) ChatStructureAdmission {
	record, ok := body.(map[string]any)
	if !ok {
		return ChatStructureAdmission{Admit: true, Lease: lease}
	}

	messages, _ := record["messages"].([]any)
	tools, _ := record["tools"].([]any)

	maxMessages := opts.MaxMessages
	if maxMessages <= 0 {
		maxMessages = DefaultChatHardMaxMessages
	}
	if len(messages) > maxMessages {
		return ChatStructureAdmission{Admit: false, RejectReason: "message_limit", RejectStatus: 413}
	}

	heavyMessages := opts.HeavyMessages
	if heavyMessages <= 0 {
		heavyMessages = DefaultChatHeavyMessageCount
	}
	heavyTools := opts.HeavyTools
	if heavyTools <= 0 {
		heavyTools = DefaultChatHeavyToolCount
	}
	heavyTokens := opts.HeavyTokens
	if heavyTokens <= 0 {
		heavyTokens = float64(DefaultChatHeavyEstTokens)
	}

	countHeavy := len(messages) >= heavyMessages || len(tools) >= heavyTools
	if !countHeavy && lease != nil {
		return ChatStructureAdmission{Admit: true, Lease: lease}
	}

	messageEstimate := EstimateStructureTokens(messages, heavyTokens)
	var toolEstimate TokenEstimate
	if messageEstimate.Exhausted {
		toolEstimate = TokenEstimate{Tokens: 0, Exhausted: true}
	} else {
		toolEstimate = EstimateStructureTokens(tools, heavyTokens-messageEstimate.Tokens)
	}
	estimatedTokens := messageEstimate.Tokens + toolEstimate.Tokens
	if estimatedTokens > heavyTokens {
		estimatedTokens = heavyTokens
	}

	heavy := countHeavy || messageEstimate.Exhausted || toolEstimate.Exhausted || estimatedTokens >= heavyTokens
	if !heavy || lease != nil {
		return ChatStructureAdmission{Admit: true, Lease: lease}
	}

	controller := opts.Controller
	if controller == nil {
		controller = NewChatAdmissionController(DefaultChatMaxHeavyInFlight)
	}
	acquired := controller.TryAcquireHeavy()
	if acquired != nil {
		return ChatStructureAdmission{Admit: true, Lease: acquired}
	}
	return ChatStructureAdmission{Admit: false, RejectReason: "structure_limit", RejectStatus: 503}
}

var _ = utf8.RuneError // keep import if needed
