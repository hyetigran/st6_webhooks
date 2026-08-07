// A standalone process that connects, opens a transaction, acquires a
// tenant's expansion advisory lock (the exact primitive
// internal/worker/expansion.go uses), prints a marker once held, then waits
// to be killed — a controllable stand-in for "a real expansion worker died
// mid-transaction, having done nothing beyond acquiring the lock." Used by
// go/chaos/expansioncrashorder. Never commits, so getting SIGKILLed is the
// only way this process ends: exactly what a crashed worker looks like.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: expansionholder <tenantID>")
		os.Exit(1)
	}
	tenantID := os.Args[1]
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Deliberately never committed or rolled back — this process is meant
	// to be SIGKILLed by its parent scenario, never to exit on its own.

	var locked bool
	if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(hashtext($1)::bigint)", tenantID).Scan(&locked); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if !locked {
		fmt.Fprintln(os.Stderr, "could not acquire the advisory lock")
		os.Exit(1)
	}
	fmt.Println("LOCK_ACQUIRED")

	select {} // wait indefinitely
}
