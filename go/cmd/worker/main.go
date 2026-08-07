// The shared worker pool process (mirrors node/src/worker.ts's cycle shape,
// extended with real OS-thread concurrency — docs/adr/0001/"ADR-001": Go
// was chosen partly to measure actual multi-core scheduling behavior, which
// a single goroutine could never exercise). WORKER_POOL_SIZE goroutines
// each run the identical loop concurrently: every cycle tries every
// mechanism in turn, and the idle wait only kicks in once none of them
// found work, so a backlog in any one of them drains immediately. Every
// claim/complete/expansion query is already safe under concurrent
// execution (row locks, per-tenant advisory locks, lease fencing) — no
// extra coordination between goroutines is needed beyond sharing one
// pgxpool.Pool and one outbound *http.Transport. Expansion (#23) and
// delivery (#24) so far; replay expansion (#25) joins this same loop as
// its ticket lands.
package main

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

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

	// Shared across every goroutine and every delivery for real connection
	// pooling and R-16's per-host connection limit (MaxConnsPerHost) — a
	// fresh Transport per call couldn't provide either.
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

	poolSize := cfg.Worker.PoolSize
	if poolSize < 1 {
		poolSize = 1
	}
	log.Printf("worker pool starting with %d goroutines", poolSize)

	var wg sync.WaitGroup
	for i := 0; i < poolSize; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pollLoop(ctx, pool, deliveryConfig, deliveryDeps, idle)
		}()
	}
	wg.Wait() // pollLoop never returns; blocks the process alive
}

func pollLoop(ctx context.Context, pool *pgxpool.Pool, cfg worker.DeliveryConfig, deps worker.DeliveryCycleDeps, idle time.Duration) {
	for {
		didWork := false

		if did, err := worker.RunExpansionCycle(ctx, pool); err != nil {
			log.Printf("expansion cycle failed: %v", err)
		} else {
			didWork = didWork || did
		}

		if did, err := worker.RunDeliveryCycle(ctx, pool, cfg, deps); err != nil {
			log.Printf("delivery cycle failed: %v", err)
		} else {
			didWork = didWork || did
		}

		if !didWork {
			time.Sleep(idle)
		}
	}
}
