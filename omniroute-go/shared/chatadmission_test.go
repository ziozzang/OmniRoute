package shared

import (
	"sync"
	"testing"
)

func TestChatAdmissionSemaphore(t *testing.T) {
	c := NewChatAdmissionController(2)
	l1 := c.TryAcquireHeavy()
	l2 := c.TryAcquireHeavy()
	if l1 == nil || l2 == nil {
		t.Fatal("first 2 should acquire")
	}
	if c.TryAcquireHeavy() != nil {
		t.Fatal("3rd should fail (capacity 2)")
	}
	if c.ActiveHeavy() != 2 {
		t.Fatalf("active = %d, want 2", c.ActiveHeavy())
	}
	l1.Release()
	if c.ActiveHeavy() != 1 {
		t.Fatalf("after release = %d, want 1", c.ActiveHeavy())
	}
	// double release is idempotent
	l1.Release()
	if c.ActiveHeavy() != 1 {
		t.Fatalf("double release = %d, want 1", c.ActiveHeavy())
	}
	l2.Release()
	if c.ActiveHeavy() != 0 {
		t.Fatalf("final = %d, want 0", c.ActiveHeavy())
	}
}

func TestChatAdmissionInvalidCapacity(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for capacity < 1")
		}
	}()
	NewChatAdmissionController(0)
}

func TestConservativeStringTokens(t *testing.T) {
	// 4 ASCII chars = 1.0 token
	if got := ConservativeStringTokens("abcd", 100); got != 1.0 {
		t.Fatalf("abcd = %v, want 1.0", got)
	}
	// non-ASCII costs 1 each
	if got := ConservativeStringTokens("한글", 100); got != 2.0 {
		t.Fatalf("한글 = %v, want 2.0", got)
	}
	// early stop at remaining
	if got := ConservativeStringTokens("abcdefgh", 1.0); got != 1.0 {
		t.Fatalf("capped = %v, want 1.0", got)
	}
}

func TestEstimateStructureTokens(t *testing.T) {
	// simple string array
	est := EstimateStructureTokens([]any{"hello", "world"}, 1000)
	if est.Tokens <= 0 || est.Exhausted {
		t.Fatalf("simple: %+v", est)
	}
	// nested map
	est = EstimateStructureTokens(map[string]any{"key": "value", "nested": map[string]any{"a": "b"}}, 1000)
	if est.Tokens <= 0 {
		t.Fatalf("nested: %+v", est)
	}
	// depth exhaustion: deeply nested beyond 12
	deep := map[string]any{}
	cur := deep
	for i := 0; i < 15; i++ {
		next := map[string]any{}
		cur["child"] = next
		cur = next
	}
	est = EstimateStructureTokens(deep, 100000)
	if !est.Exhausted {
		t.Fatalf("deep should exhaust, got %+v", est)
	}
}

func TestAdmitChatStructureMessageLimit(t *testing.T) {
	msgs := make([]any, 900) // > 800 default
	body := map[string]any{"messages": msgs}
	res := AdmitChatStructure(body, nil, ChatStructureAdmissionOptions{})
	if res.Admit || res.RejectStatus != 413 || res.RejectReason != "message_limit" {
		t.Fatalf("over-limit: %+v", res)
	}
}

func TestAdmitChatStructureNonObject(t *testing.T) {
	res := AdmitChatStructure("not an object", nil, ChatStructureAdmissionOptions{})
	if !res.Admit {
		t.Fatal("non-object → admit")
	}
}

func TestAdmitChatStructureHeavyAcquiresLease(t *testing.T) {
	c := NewChatAdmissionController(1)
	msgs := make([]any, 250) // >= 200 heavyMessages
	for i := range msgs {
		msgs[i] = map[string]any{"content": "x"}
	}
	body := map[string]any{"messages": msgs}
	res := AdmitChatStructure(body, nil, ChatStructureAdmissionOptions{Controller: c})
	if !res.Admit || res.Lease == nil {
		t.Fatalf("heavy should acquire lease: %+v", res)
	}
	if c.ActiveHeavy() != 1 {
		t.Fatalf("active = %d", c.ActiveHeavy())
	}
	res.Lease.Release()
}

func TestAdmitChatStructureCapacityBusy(t *testing.T) {
	c := NewChatAdmissionController(1)
	c.TryAcquireHeavy() // exhaust capacity
	msgs := make([]any, 250)
	for i := range msgs {
		msgs[i] = map[string]any{"content": "x"}
	}
	body := map[string]any{"messages": msgs}
	res := AdmitChatStructure(body, nil, ChatStructureAdmissionOptions{Controller: c})
	if res.Admit || res.RejectStatus != 503 || res.RejectReason != "structure_limit" {
		t.Fatalf("busy: %+v", res)
	}
}

func TestAdmitChatStructureExistingLeasePassthrough(t *testing.T) {
	c := NewChatAdmissionController(1)
	existing := c.TryAcquireHeavy()
	msgs := make([]any, 250)
	body := map[string]any{"messages": msgs}
	// with an existing lease, heavy request is admitted without new acquisition
	res := AdmitChatStructure(body, existing, ChatStructureAdmissionOptions{Controller: c})
	if !res.Admit || res.Lease != existing {
		t.Fatalf("existing lease passthrough: %+v", res)
	}
	if c.ActiveHeavy() != 1 {
		t.Fatalf("should not double-acquire: active = %d", c.ActiveHeavy())
	}
	existing.Release()
}

func TestChatAdmissionConcurrent(t *testing.T) {
	c := NewChatAdmissionController(4)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if lease := c.TryAcquireHeavy(); lease != nil {
					_ = c.ActiveHeavy()
					lease.Release()
				}
			}
		}()
	}
	wg.Wait()
	if c.ActiveHeavy() != 0 {
		t.Fatalf("after concurrent: active = %d, want 0", c.ActiveHeavy())
	}
}
