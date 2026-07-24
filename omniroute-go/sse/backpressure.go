package sse

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// backpressure.go — Connection back-pressure for SSE/streaming endpoints.
// Port of src/sse/utils/backpressure.ts
//
// Pure functions — no race concerns.

// CapacityResult reports whether a request should be rejected.
type CapacityResult struct {
	ShouldReject bool
	Status       int
	Body         []byte
	RetryAfter   int
	Limit        int
}

// EvalCapacity is the pure capacity evaluator. Cap <= 0 means disabled.
func EvalCapacity(active, cap int) CapacityResult {
	if cap <= 0 || active < cap {
		return CapacityResult{ShouldReject: false}
	}
	retryAfter := int(math.Max(1, math.Ceil(float64(active)/float64(cap))*30))
	body, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"message":     "Server busy — " + itoa(active) + " active connections (limit " + itoa(cap) + "). Retry after " + itoa(retryAfter) + "s.",
			"type":        "rate_limit",
			"retry_after": retryAfter,
		},
	})
	return CapacityResult{
		ShouldReject: true,
		Status:       429,
		Body:         body,
		RetryAfter:   retryAfter,
		Limit:        cap,
	}
}

// ReadConnectionCap reads OMNI_MAX_CONCURRENT_CONNECTIONS from env (default 0 = disabled).
func ReadConnectionCap(env map[string]string) int {
	raw := env["OMNI_MAX_CONCURRENT_CONNECTIONS"]
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func itoa(v int) string {
	return strconv.Itoa(v)
}
