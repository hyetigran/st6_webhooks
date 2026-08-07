package db

import (
	"context"
	"embed"
	"fmt"
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

// Migrate mirrors node/src/db/migrate.ts: a schema_migrations table tracks
// applied filenames, each unapplied migration runs in its own transaction,
// files apply in sorted-filename order.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `
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

	rows, err := pool.Query(ctx, "SELECT filename FROM schema_migrations")
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

		fmt.Printf("Applying migration %s...\n", file)
		tx, err := pool.Begin(ctx)
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

	fmt.Println("Migrations up to date.")
	return nil
}
