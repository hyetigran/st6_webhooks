package db

import (
	"context"
	"embed"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Embedded (not read from disk) so the compiled binary carries its own
// migrations regardless of working directory — no separate "copy
// migrations into the image" step the way node/Dockerfile needs.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationLockKey is an arbitrary fixed key for the session-level advisory
// lock Migrate holds for its duration — docker-compose's api and worker
// services each run their own `./migrate` on startup, and without this lock
// two of them racing "CREATE TABLE IF NOT EXISTS schema_migrations" against
// a fresh database can both pass the existence check before either commits,
// then collide creating the table's implicit composite type (Postgres
// error 23505 on pg_type_typname_nsp_index) — a real failure observed when
// live-verifying `docker compose up`, not a hypothetical.
const migrationLockKey = "webhooks_go_migrations"

// Migrate mirrors node/src/db/migrate.ts: a schema_migrations table tracks
// applied filenames, each unapplied migration runs in its own transaction,
// files apply in sorted-filename order. Serialized across concurrent
// callers via a single held connection's advisory lock.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("db: acquire connection for migration lock: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(hashtext($1))", migrationLockKey); err != nil {
		return fmt.Errorf("db: acquire migration lock: %w", err)
	}
	// Explicit unlock (not just letting the connection return to the pool)
	// — pg_advisory_lock is session-scoped, so an unreleased lock would
	// stay held by this connection for as long as it sits in the pool,
	// blocking any later migration attempt from ever acquiring it.
	defer func() {
		_, _ = conn.Exec(ctx, "SELECT pg_advisory_unlock(hashtext($1))", migrationLockKey)
	}()

	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			filename    TEXT PRIMARY KEY,
			applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`); err != nil {
		return fmt.Errorf("db: create schema_migrations: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("db: read migrations dir: %w", err)
	}
	var files []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	rows, err := conn.Query(ctx, "SELECT filename FROM schema_migrations")
	if err != nil {
		return fmt.Errorf("db: query applied migrations: %w", err)
	}
	applied := map[string]bool{}
	for rows.Next() {
		var filename string
		if err := rows.Scan(&filename); err != nil {
			rows.Close()
			return fmt.Errorf("db: scan applied migration: %w", err)
		}
		applied[filename] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("db: iterate applied migrations: %w", err)
	}

	for _, file := range files {
		if applied[file] {
			continue
		}
		sql, err := migrationsFS.ReadFile("migrations/" + file)
		if err != nil {
			return fmt.Errorf("db: read migration %s: %w", file, err)
		}

		log.Printf("Applying migration %s...", file)
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("db: begin tx for %s: %w", file, err)
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("db: apply migration %s: %w", file, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (filename) VALUES ($1)", file); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("db: record migration %s: %w", file, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("db: commit migration %s: %w", file, err)
		}
	}

	log.Println("Migrations up to date.")
	return nil
}
