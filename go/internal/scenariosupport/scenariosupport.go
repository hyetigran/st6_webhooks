// Package scenariosupport is shared infrastructure for the standalone
// `make chaos`/`make load` programs under go/chaos/ and go/load/ (PRD §8) —
// the pieces genuinely identical between them: database bootstrap, tenant
// fixtures, condition polling, managed-process lifecycle, and evidence
// writing. Mirrors node/scripts/scenarioHarness.ts's role for the Node
// stack. Each scenario is its own `package main` under go/chaos/<name>/ or
// go/load/<name>/, run via `go run` from go/ (evidence and repo-root paths
// below assume that working directory).
package scenariosupport

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"webhooks-go/internal/crypto"
	"webhooks-go/internal/db"
	"webhooks-go/internal/secrets"
)

const adminDatabaseURL = "postgres://webhooks:webhooks@localhost:5533/postgres"

func ensureDatabase(ctx context.Context, dbName string) error {
	admin, err := pgxpool.New(ctx, adminDatabaseURL)
	if err != nil {
		return err
	}
	defer admin.Close()

	var exists bool
	if err := admin.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", dbName).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = admin.Exec(ctx, "CREATE DATABASE "+dbName)
	return err
}

// SetupDatabase creates dbName if it doesn't exist, migrates it, and
// truncates every table — a dedicated database per scenario category,
// separate from local dev data, the `go test` database, and each other, so
// chaos/load runs never race concurrent `make test`/`go run ./cmd/api`
// activity.
func SetupDatabase(ctx context.Context, dbName, connectionString string) (*pgxpool.Pool, error) {
	if err := ensureDatabase(ctx, dbName); err != nil {
		return nil, fmt.Errorf("scenariosupport: ensure database %s: %w", dbName, err)
	}
	pool, err := pgxpool.New(ctx, connectionString)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	if _, err := pool.Exec(ctx, "TRUNCATE tenants, endpoints, events, deliveries, attempts, replays RESTART IDENTITY CASCADE"); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func CreateTenant(ctx context.Context, pool *pgxpool.Pool, name string) (id, apiKey string, err error) {
	apiKey = secrets.Generate("tenant")
	err = pool.QueryRow(ctx,
		"INSERT INTO tenants (name, api_key_hash) VALUES ($1, $2) RETURNING id", name, crypto.HashAPIKey(apiKey),
	).Scan(&id)
	return id, apiKey, err
}

// WaitUntil polls check until it returns true, or fails once timeout
// elapses.
func WaitUntil(ctx context.Context, check func(ctx context.Context) (bool, error), timeout time.Duration, label string) error {
	interval := 50 * time.Millisecond
	deadline := time.Now().Add(timeout)
	for {
		ok, err := check(ctx)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("scenariosupport: waitUntil timed out after %s: %s", timeout, label)
		}
		time.Sleep(interval)
	}
}

// ManagedProcess wraps a spawned child process with the double-kill guard
// and exit-wait semantics real SIGKILL/SIGSTOP/SIGCONT chaos scenarios
// need. Always exec's a pre-built binary directly, never `go run` — `go
// run` compiles to a temp binary and executes it as a child of the `go run`
// process itself, so a ManagedProcess wrapping `go run` would only hold a
// handle to that outer wrapper; SIGKILL is uncatchable, so a killed wrapper
// can't relay it to its actual child, leaving the real process orphaned
// and un-killable — the same class of bug Node's tsx CLI wrapper had (see
// progress.md's gotchas).
type ManagedProcess struct {
	cmd    *exec.Cmd
	done   chan struct{}
	exited atomic.Bool
}

func StartProcess(binPath string, args []string, env []string) (*ManagedProcess, error) {
	cmd := exec.Command(binPath, args...)
	cmd.Env = env
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("scenariosupport: start %s: %w", binPath, err)
	}
	mp := &ManagedProcess{cmd: cmd, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		mp.exited.Store(true)
		close(mp.done)
	}()
	return mp, nil
}

// Kill signals the process. A no-op if it has already exited — sending a
// second signal to an already-exited process, or waiting on an 'exit' that
// already happened, would otherwise hang forever.
func (p *ManagedProcess) Kill(sig syscall.Signal) error {
	if p.exited.Load() {
		return nil
	}
	if err := p.cmd.Process.Signal(sig); err != nil {
		return err
	}
	if sig == syscall.SIGKILL || sig == syscall.SIGTERM {
		<-p.done
	}
	return nil
}

// repoRoot assumes the current working directory is go/ — true for every
// `make chaos`/`make load` invocation, which always runs from go/.
func repoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Dir(wd), nil
}

func writeEvidence(category, scenario string, data map[string]any) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	// Namespaced under evidence/go/ (not the flat evidence/<category>/ the
	// Node stack uses) — both stacks name their scenarios identically
	// (e.g. "worker-kill-mid-delivery"), and a shared flat directory would
	// let each stack's make verify silently clobber the other's committed
	// evidence. Confirmed live: an early version of this shared the flat
	// directory, and running Go's chaos suite overwrote Node's ticket #21
	// evidence in the working tree.
	evidenceDir := filepath.Join(root, "evidence", "go", category)
	if err := os.MkdirAll(evidenceDir, 0o755); err != nil {
		return err
	}

	payload := map[string]any{"scenario": scenario, "timestamp": time.Now().UTC().Format(time.RFC3339)}
	for k, v := range data {
		payload[k] = v
	}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(evidenceDir, scenario+".json"), encoded, 0o644)
}

// WaitUntilDeliveryState polls until the given delivery reaches state, or
// fails once timeout elapses. Used by nearly every chaos scenario, which
// otherwise each repeat this identical poll.
func WaitUntilDeliveryState(ctx context.Context, pool *pgxpool.Pool, deliveryID, state string, timeout time.Duration) error {
	return WaitUntil(ctx, func(ctx context.Context) (bool, error) {
		var s string
		if err := pool.QueryRow(ctx, "SELECT state FROM deliveries WHERE id = $1", deliveryID).Scan(&s); err != nil {
			return false, err
		}
		return s == state, nil
	}, timeout, fmt.Sprintf("delivery %s reaches state %s", deliveryID, state))
}

// StartLocalReceiver starts a real HTTP server on an OS-assigned loopback
// port, for scenarios that need a receiver they fully control the
// timing/behavior of. Returns the port and a close func.
func StartLocalReceiver(handler http.HandlerFunc) (port int, closeFn func(), err error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, nil, err
	}
	server := &http.Server{Handler: handler}
	go func() {
		_ = server.Serve(listener)
	}()
	return listener.Addr().(*net.TCPAddr).Port, func() {
		_ = server.Close()
	}, nil
}

// RunScenario runs fn, prints PASS/FAIL with elapsed time, writes evidence
// to evidence/<category>/<name>.json, and exits the process with the
// matching status code — each scenario's main() is just a call to this.
func RunScenario(category, name string, fn func() (map[string]any, error)) {
	startedAt := time.Now()
	result, err := fn()
	durationMs := time.Since(startedAt).Milliseconds()

	if err != nil {
		fmt.Printf("[FAIL] %s (%dms): %v\n", name, durationMs, err)
		if evErr := writeEvidence(category, name, map[string]any{
			"status": "fail", "durationMs": durationMs, "error": err.Error(),
		}); evErr != nil {
			fmt.Printf("warning: failed to write evidence: %v\n", evErr)
		}
		os.Exit(1)
	}

	fmt.Printf("[PASS] %s (%dms)\n", name, durationMs)
	if category == "load" {
		if encoded, err := json.MarshalIndent(result, "", "  "); err == nil {
			fmt.Println(string(encoded))
		}
	}
	evidence := map[string]any{"status": "pass", "durationMs": durationMs}
	for k, v := range result {
		evidence[k] = v
	}
	if evErr := writeEvidence(category, name, evidence); evErr != nil {
		fmt.Printf("warning: failed to write evidence: %v\n", evErr)
	}
	os.Exit(0)
}
