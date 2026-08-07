// The shared worker pool process (mirrors node/src/worker.ts): one process
// runs every mechanism's poll cycle, not a separate loop per mechanism —
// each iteration tries every cycle in turn, and the idle wait only kicks in
// once none of them found work, so a backlog in any one of them drains
// immediately. Currently just expansion (#23); delivery (#24) and replay
// expansion (#25) join this same loop as their tickets land.
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
	ctx := context.Background()

	pool, err := db.NewPool(ctx, cfg.DB.ConnectionString)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	idle := time.Duration(cfg.Worker.IdlePollIntervalMs) * time.Millisecond
	for {
		didWork := false

		if did, err := worker.RunExpansionCycle(ctx, pool); err != nil {
			log.Printf("expansion cycle failed: %v", err)
		} else {
			didWork = didWork || did
		}

		if !didWork {
			time.Sleep(idle)
		}
	}
}
