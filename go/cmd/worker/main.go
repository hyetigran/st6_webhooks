// The shared worker pool process (mirrors node/src/worker.ts): one process
// runs every mechanism's poll cycle, not a separate loop per mechanism —
// each iteration tries every cycle in turn, and the idle wait only kicks in
// once none of them found work, so a backlog in any one of them drains
// immediately. Expansion (#23) and delivery (#24) so far; replay expansion
// (#25) joins this same loop as its ticket lands.
package main

import (
	"context"
	"log"
	"time"

	"webhooks-go/internal/config"
	"webhooks-go/internal/db"
	"webhooks-go/internal/worker"
)

func main() {
	cfg := config.Load()
	secretEncryptionKey := config.SecretEncryptionKey()
	ctx := context.Background()

	pool, err := db.NewPool(ctx, cfg.DB.ConnectionString)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	// Shared across every delivery for real connection pooling and R-16's
	// per-host connection limit (MaxConnsPerHost) — a fresh Transport per
	// call couldn't provide either.
	transport := worker.NewTransport(cfg.OutboundHTTP.ConnectTimeoutMs, cfg.OutboundHTTP.MaxConnectionsPerHost)
	defer transport.CloseIdleConnections()

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
	deliveryDeps := worker.DeliveryCycleDeps{Transport: transport}

	idle := time.Duration(cfg.Worker.IdlePollIntervalMs) * time.Millisecond
	for {
		didWork := false

		if did, err := worker.RunExpansionCycle(ctx, pool); err != nil {
			log.Printf("expansion cycle failed: %v", err)
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
