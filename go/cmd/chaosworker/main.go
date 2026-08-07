// A chaos/load-testing-only worker entrypoint (PRD §8). Structurally
// identical to cmd/worker's real poll loop (same RunExpansionCycle/
// RunDeliveryCycle/RunReplayExpansionCycle calls, same real process
// lifecycle — this is the process chaos scenarios kill/SIGSTOP/SIGCONT),
// except ResolveAndPinFn is swapped for a permissive stub via the same
// DeliveryCycleDeps injection seam the test suite already uses
// (internal/api/conforming_receiver_test.go and friends).
//
// Why this exists rather than reusing cmd/worker directly: chaos/load
// scenarios need a receiver they control the timing/behavior of, which
// means a local HTTP server — and the real, production ResolveAndPin
// correctly rejects loopback/private addresses (that's the SSRF defense
// working as designed, not a bug). Rather than weakening that check in
// production code for testing convenience, this is a separate, clearly-
// labeled entrypoint that never ships — cmd/worker is untouched. Single
// poll loop, no goroutine pool: chaos scenarios spawn multiple *processes*
// (workerA, workerB) to get multiple independent workers, matching
// node/chaos/worker-entrypoint.ts exactly.
package main

import (
	"context"
	"log"
	"time"

	"webhooks-go/internal/config"
	"webhooks-go/internal/db"
	"webhooks-go/internal/worker"
)

func trustAnyAddress(ctx context.Context, hostname string) worker.ResolveAndPinResult {
	return worker.ResolveAndPinResult{Allowed: true, IP: "127.0.0.1"}
}

func main() {
	cfg := config.Load()
	secretEncryptionKey := config.SecretEncryptionKey()
	ctx := context.Background()

	pool, err := db.NewPool(ctx, cfg.DB.ConnectionString)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	deliveryConfig := worker.DeliveryConfig{
		SecretEncryptionKey: secretEncryptionKey,
		LeaseDurationMs:     cfg.LeaseDurationMs(),
		Outbound: worker.OutboundConfig{
			ConnectTimeoutMs:     cfg.OutboundHTTP.ConnectTimeoutMs,
			TotalTimeoutMs:       cfg.OutboundHTTP.TotalTimeoutMs,
			MaxResponseBodyBytes: cfg.OutboundHTTP.MaxResponseBodyBytes,
		},
		Backoff: worker.BackoffConfig{
			BaseDelayMs: cfg.Backoff.BaseDelayMs,
			Multiplier:  cfg.Backoff.Multiplier,
			MaxDelayMs:  cfg.Backoff.MaxDelayMs,
			MaxAttempts: cfg.Backoff.MaxAttempts,
		},
	}
	deliveryDeps := worker.DeliveryCycleDeps{ResolveAndPinFn: trustAnyAddress}
	idle := time.Duration(cfg.Worker.IdlePollIntervalMs) * time.Millisecond

	for {
		didWork := false

		if did, err := worker.RunExpansionCycle(ctx, pool); err != nil {
			log.Printf("expansion cycle failed: %v", err)
		} else {
			didWork = didWork || did
		}

		if did, err := worker.RunReplayExpansionCycle(ctx, pool); err != nil {
			log.Printf("replay expansion cycle failed: %v", err)
		} else {
			didWork = didWork || did
		}

		if did, err := worker.RunDeliveryCycle(ctx, pool, deliveryConfig, deliveryDeps); err != nil {
			log.Printf("delivery cycle failed: %v", err)
		} else {
			didWork = didWork || did
		}

		if !didWork {
			time.Sleep(idle)
		}
	}
}
