package scenariosupport

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"webhooks-go/internal/crypto"
)

// ScenarioSecretEncryptionKey is the fixed key every chaos/load scenario
// and the spawned api/chaosworker binaries use — chaos/load databases are
// scratch/throwaway, so a fixed local-dev-style key is fine (never used
// against real data).
var ScenarioSecretEncryptionKey = []byte("abcdefghijklmnopqrstuvwxyz012345")

// LoadDatabaseName/LoadDatabaseURL are a dedicated database for `make
// load` scenarios, separate from the chaos database — so a chaos and load
// run (or `make test`) never race each other's TRUNCATE-based setup.
const LoadDatabaseName = "webhooks_go_load"

var LoadDatabaseURL = "postgres://webhooks:webhooks@localhost:5533/" + LoadDatabaseName

// CreateEndpointsBulk inserts count endpoints in one statement — creating
// thousands of endpoints one row at a time would dominate setup time and
// make the measured publish latency meaningless by comparison.
func CreateEndpointsBulk(ctx context.Context, pool *pgxpool.Pool, tenantID string, count int, url string, eventTypes []string) error {
	if url == "" {
		url = "https://example.com/hook"
	}
	if eventTypes == nil {
		eventTypes = []string{"order.created"}
	}
	encrypted, err := crypto.EncryptSecret("whsec_load", ScenarioSecretEncryptionKey)
	if err != nil {
		return err
	}

	values := make([]string, 0, count)
	args := make([]any, 0, count*4)
	for i := 0; i < count; i++ {
		args = append(args, tenantID, url, eventTypes, encrypted)
		base := len(args) - 4
		values = append(values, fmt.Sprintf("($%d, $%d, $%d, $%d)", base+1, base+2, base+3, base+4))
	}
	query := "INSERT INTO endpoints (tenant_id, url, event_types, signing_secret) VALUES "
	for i, v := range values {
		if i > 0 {
			query += ","
		}
		query += v
	}
	_, err = pool.Exec(ctx, query, args...)
	return err
}

// CreateTerminalDeliveriesBulk bulk-creates count terminal (succeeded)
// deliveries, each with its own event — for replay-window-size scenarios,
// where "large window" means large history to scan, not large fan-out. One
// multi-row INSERT per table rather than N round trips.
func CreateTerminalDeliveriesBulk(ctx context.Context, pool *pgxpool.Pool, tenantID, endpointID string, count int) error {
	eventValues := make([]string, 0, count)
	eventArgs := make([]any, 0, count*4)
	for i := 0; i < count; i++ {
		eventArgs = append(eventArgs, tenantID, "load-replay-fixture-"+uuid.NewString(), "order.created", "{}")
		base := len(eventArgs) - 4
		eventValues = append(eventValues, fmt.Sprintf("($%d, $%d, $%d, $%d, 'expanded')", base+1, base+2, base+3, base+4))
	}
	eventQuery := "INSERT INTO events (tenant_id, idempotency_key, type, payload, status) VALUES "
	for i, v := range eventValues {
		if i > 0 {
			eventQuery += ","
		}
		eventQuery += v
	}
	eventQuery += " RETURNING id"

	rows, err := pool.Query(ctx, eventQuery, eventArgs...)
	if err != nil {
		return err
	}
	var eventIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		eventIDs = append(eventIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	deliveryValues := make([]string, 0, len(eventIDs))
	deliveryArgs := make([]any, 0, len(eventIDs)*2)
	for _, eventID := range eventIDs {
		deliveryArgs = append(deliveryArgs, eventID, endpointID)
		base := len(deliveryArgs) - 2
		deliveryValues = append(deliveryValues, fmt.Sprintf("($%d, $%d, 'succeeded')", base+1, base+2))
	}
	deliveryQuery := "INSERT INTO deliveries (event_id, endpoint_id, state) VALUES "
	for i, v := range deliveryValues {
		if i > 0 {
			deliveryQuery += ","
		}
		deliveryQuery += v
	}
	_, err = pool.Exec(ctx, deliveryQuery, deliveryArgs...)
	return err
}

// SpawnAPIServer starts the pre-built api binary against the load database
// on the given port. Always exec's bin/api directly, never `go run` — same
// signal-delivery reasoning as SpawnWorker/ManagedProcess.
func SpawnAPIServer(binPath string, port int, envOverrides map[string]string) (*ManagedProcess, error) {
	defaults := map[string]string{
		"DATABASE_URL":          LoadDatabaseURL,
		"SECRET_ENCRYPTION_KEY": base64.StdEncoding.EncodeToString(ScenarioSecretEncryptionKey),
		"PORT":                  fmt.Sprintf("%d", port),
	}
	for k, v := range envOverrides {
		defaults[k] = v
	}
	env := os.Environ()
	for k, v := range defaults {
		env = append(env, k+"="+v)
	}
	return StartProcess(binPath, nil, env)
}

func WaitForServer(ctx context.Context, baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/healthz", nil)
		if err == nil {
			if resp, err := client.Do(req); err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("scenariosupport: server at %s did not become healthy within %s", baseURL, timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Percentile returns the p-th percentile of an already-sorted slice.
func Percentile(sortedMs []float64, p float64) float64 {
	if len(sortedMs) == 0 {
		return 0
	}
	index := int(p / 100 * float64(len(sortedMs)))
	if index >= len(sortedMs) {
		index = len(sortedMs) - 1
	}
	return sortedMs[index]
}

// LatencyResult is p50/p99 over one batch of measured requests — the shape
// both publish-latency-flat and replay-latency-flat report per level.
type LatencyResult struct {
	P50 float64 `json:"p50"`
	P99 float64 `json:"p99"`
}

// MeasureLatencies calls request count times, once per i in [0, count),
// timing each call and collecting the elapsed milliseconds request itself
// returns (so the caller controls exactly what's measured and what counts
// as a failure — e.g. asserting a 202 status before returning the elapsed
// time). Returns the resulting p50/p99.
func MeasureLatencies(count int, request func(i int) (elapsedMs float64, err error)) (LatencyResult, error) {
	latenciesMs := make([]float64, 0, count)
	for i := 0; i < count; i++ {
		elapsedMs, err := request(i)
		if err != nil {
			return LatencyResult{}, err
		}
		latenciesMs = append(latenciesMs, elapsedMs)
	}
	sort.Float64s(latenciesMs)
	return LatencyResult{P50: Percentile(latenciesMs, 50), P99: Percentile(latenciesMs, 99)}, nil
}

// AssertLatencyFlat checks the R-8 "latency doesn't scale with X" claim: a
// generous multiple, not an exact match, since real scheduling/GC jitter
// means run-to-run noise exists regardless.
func AssertLatencyFlat(baseline, largest LatencyResult, label string) error {
	if largest.P99 > baseline.P99*5+50 {
		return fmt.Errorf("expected p99 %s (%.1fms) to stay roughly flat versus the baseline (%.1fms)", label, largest.P99, baseline.P99)
	}
	return nil
}
