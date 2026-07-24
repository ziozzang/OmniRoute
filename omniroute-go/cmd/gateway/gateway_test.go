package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayHealth(t *testing.T) {
	gw := NewGateway(GatewayConfig{ListenAddr: ":0", DryRun: true, IPPerMinute: 60, ConnectionCap: 100})
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	gw.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("health: %d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "ok" {
		t.Fatalf("status = %v", resp["status"])
	}
}

func TestGatewayChatDryRun(t *testing.T) {
	gw := NewGateway(GatewayConfig{ListenAddr: ":0", DryRun: true, IPPerMinute: 60, ConnectionCap: 100})
	body := `{"model":"claude-opus-4-high","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test")
	w := httptest.NewRecorder()
	gw.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("chat: %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["admitted"] != true {
		t.Fatalf("admitted = %v", resp["admitted"])
	}
	reasoning := resp["reasoning"].(map[string]any)
	if reasoning["intent_model"] != "claude-opus-4" {
		t.Fatalf("intent_model = %v", reasoning["intent_model"])
	}
}

func TestGateway404(t *testing.T) {
	gw := NewGateway(GatewayConfig{ListenAddr: ":0", DryRun: true, IPPerMinute: 60, ConnectionCap: 100})
	req := httptest.NewRequest("GET", "/nonexistent", nil)
	w := httptest.NewRecorder()
	gw.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("404: %d", w.Code)
	}
}

func TestGatewayRateLimit(t *testing.T) {
	gw := NewGateway(GatewayConfig{ListenAddr: ":0", DryRun: true, IPPerMinute: 2, ConnectionCap: 100})
	body := `{"model":"gpt-4o","messages":[]}`
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer ratelimit-test")
		req.RemoteAddr = "10.0.0.1:1234"
		w := httptest.NewRecorder()
		gw.ServeHTTP(w, req)
		if i < 2 && w.Code != 200 {
			t.Fatalf("request %d: expected 200, got %d", i, w.Code)
		}
		if i == 2 && w.Code != 429 {
			t.Fatalf("request 3: expected 429, got %d", w.Code)
		}
	}
}

func TestGatewayInvalidBody(t *testing.T) {
	gw := NewGateway(GatewayConfig{ListenAddr: ":0", DryRun: true, IPPerMinute: 60, ConnectionCap: 100})
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("not json"))
	req.Header.Set("Authorization", "Bearer test")
	w := httptest.NewRecorder()
	gw.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("invalid body: %d", w.Code)
	}
}
