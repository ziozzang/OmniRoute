package api

import (
	"encoding/json"
	"testing"
)

func TestCreateErrorResponse(t *testing.T) {
	body, status := CreateErrorResponse(ApiErrorPayload{Status: 404, Message: "not found"})
	if status != 404 {
		t.Fatalf("status = %d", status)
	}
	var parsed ApiErrorBody
	json.Unmarshal(body, &parsed)
	if parsed.Error.Message != "not found" || parsed.Error.Type != ErrNotFound {
		t.Fatalf("body = %+v", parsed)
	}
	if parsed.RequestID == "" {
		t.Fatal("requestId should be set")
	}
}

func TestResolveErrorType(t *testing.T) {
	cases := []struct{ status int; want ApiErrorType }{
		{400, ErrInvalidRequest}, {404, ErrNotFound}, {409, ErrConflict},
		{500, ErrServerError}, {503, ErrServerError},
	}
	for _, c := range cases {
		if got := resolveErrorType(ApiErrorPayload{Status: c.status}); got != c.want {
			t.Errorf("status %d → %q, want %q", c.status, got, c.want)
		}
	}
	// explicit type wins
	if got := resolveErrorType(ApiErrorPayload{Status: 400, Type: ErrConflict}); got != ErrConflict {
		t.Fatal("explicit type wins")
	}
}

func TestCreateErrorResponseFromUnknown(t *testing.T) {
	body, status := CreateErrorResponseFromUnknown(nil, "")
	if status != 500 {
		t.Fatalf("nil → 500, got %d", status)
	}
	var parsed ApiErrorBody
	json.Unmarshal(body, &parsed)
	if parsed.Error.Message != "Unexpected server error" {
		t.Fatalf("fallback message, got %q", parsed.Error.Message)
	}
	// with error
	body, status = CreateErrorResponseFromUnknown(&UnknownError{Message: "bad", Status: 422}, "")
	if status != 422 {
		t.Fatalf("status = %d", status)
	}
	json.Unmarshal(body, &parsed)
	if parsed.Error.Message != "bad" {
		t.Fatalf("message = %q", parsed.Error.Message)
	}
}

func TestResolveServerErrorMessage(t *testing.T) {
	// details[0].message preferred
	body := map[string]any{"error": map[string]any{
		"message": "generic",
		"details": []any{map[string]any{"message": "specific field error"}},
	}}
	if got := ResolveServerErrorMessage(body, "fallback"); got != "specific field error" {
		t.Fatalf("got %q", got)
	}
	// error.message fallback
	body2 := map[string]any{"error": map[string]any{"message": "top-level"}}
	if got := ResolveServerErrorMessage(body2, "fallback"); got != "top-level" {
		t.Fatalf("got %q", got)
	}
	// nil → fallback
	if got := ResolveServerErrorMessage(nil, "fb"); got != "fb" {
		t.Fatalf("nil → fallback, got %q", got)
	}
	// malformed → fallback
	if got := ResolveServerErrorMessage("string", "fb"); got != "fb" {
		t.Fatalf("non-object → fallback, got %q", got)
	}
}

func TestComboErrorResponse(t *testing.T) {
	body, status := ComboErrorResponse(ComboNotFound, 0, nil, "req-1")
	if status != 404 {
		t.Fatalf("COMBO_007 → 404, got %d", status)
	}
	var parsed ComboErrorBody
	json.Unmarshal(body, &parsed)
	if parsed.Error.Code != ComboNotFound || parsed.Error.RequestID != "req-1" {
		t.Fatalf("body = %+v", parsed)
	}
	// status override
	_, status = ComboErrorResponse(ComboSchemaFailure, 422, nil, "")
	if status != 422 {
		t.Fatalf("override → 422, got %d", status)
	}
	// unknown code → INTERNAL_001
	body, status = ComboErrorResponse(ComboErrorCode("BOGUS"), 0, nil, "")
	if status != 500 {
		t.Fatalf("unknown → 500, got %d", status)
	}
	json.Unmarshal(body, &parsed)
	if parsed.Error.Code != InternalFallback {
		t.Fatalf("unknown → INTERNAL_001, got %q", parsed.Error.Code)
	}
}

func TestMaskStoredAPIKey(t *testing.T) {
	if got := MaskStoredAPIKey("sk-1234567890abcdef"); got != "sk-12345****cdef" {
		t.Fatalf("got %q", got)
	}
	if got := MaskStoredAPIKey(""); got != "" {
		t.Fatalf("empty → empty, got %q", got)
	}
	if got := MaskStoredAPIKey("short"); got != "s****" {
		t.Fatalf("short → masked, got %q", got)
	}
}

func TestIsAPIKeyRevealEnabled(t *testing.T) {
	if !IsAPIKeyRevealEnabled(map[string]string{"ALLOW_API_KEY_REVEAL": "true"}) {
		t.Fatal("true → enabled")
	}
	if IsAPIKeyRevealEnabled(map[string]string{"ALLOW_API_KEY_REVEAL": "no"}) {
		t.Fatal("no → disabled")
	}
	if IsAPIKeyRevealEnabled(map[string]string{}) {
		t.Fatal("absent → disabled")
	}
}
