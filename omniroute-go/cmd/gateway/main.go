package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// main.go — Single-binary admission gateway entry point.
//
// Builds to one static binary with no external dependencies:
//   CGO_ENABLED=0 go build -o omniroute-gateway ./cmd/gateway
//
// Environment configuration:
//   GATEWAY_LISTEN          listen address (default :8080)
//   GATEWAY_UPSTREAM_URL    upstream LLM API base URL (empty = dry-run)
//   GATEWAY_TRUST_PROXY     trust X-Forwarded-For (default false)
//   GATEWAY_IP_PER_MINUTE   per-IP rate limit (default 60)
//   GATEWAY_CONNECTION_CAP  max concurrent connections (default 100)
//   GATEWAY_DRY_RUN         force dry-run mode (default true if no upstream)

func main() {
	cfg := LoadConfigFromEnv()
	gw := NewGateway(cfg)

	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      gw,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	mode := "proxy"
	if cfg.DryRun {
		mode = "dry-run"
	}
	log.Printf("omniroute-gateway listening on %s (mode=%s, upstream=%q, ip_limit=%d/min, conn_cap=%d)",
		cfg.ListenAddr, mode, cfg.UpstreamURL, cfg.IPPerMinute, cfg.ConnectionCap)

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
	log.Println("server stopped")
}
