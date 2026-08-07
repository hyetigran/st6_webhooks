// PRD §8 (R-8, R-19, R-22): "Large-window replay leaves replay-API latency
// flat (async expansion, not synchronous)." POST /endpoints/:id/replays
// only ever inserts one replays row — the window scan and bulk delivery
// insert happen later, in the worker — so latency shouldn't grow with how
// much history the window covers.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"syscall"
	"time"

	"webhooks-go/internal/scenariosupport"
)

var windowSizes = []int{100, 1_000, 10_000}

const requestsPerLevel = 15
const port = 4101

func main() {
	scenariosupport.RunScenario("load", "replay-latency-flat", run)
}

type levelResult struct {
	P50 float64 `json:"p50"`
	P99 float64 `json:"p99"`
}

func run() (map[string]any, error) {
	ctx := context.Background()
	pool, err := scenariosupport.SetupDatabase(ctx, scenariosupport.LoadDatabaseName, scenariosupport.LoadDatabaseURL)
	if err != nil {
		return nil, err
	}
	defer pool.Close()

	tenantID, apiKey, err := scenariosupport.CreateTenant(ctx, pool, "load-tenant")
	if err != nil {
		return nil, err
	}
	if err := scenariosupport.CreateEndpointsBulk(ctx, pool, tenantID, 1, "", nil); err != nil {
		return nil, err
	}
	var endpointID string
	if err := pool.QueryRow(ctx, "SELECT id FROM endpoints LIMIT 1").Scan(&endpointID); err != nil {
		return nil, err
	}

	server, err := scenariosupport.SpawnAPIServer("./bin/api", port, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = server.Kill(syscall.SIGTERM) }()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := scenariosupport.WaitForServer(ctx, baseURL, 10*time.Second); err != nil {
		return nil, err
	}

	resultsByWindow := map[int]levelResult{}
	cumulativeHistory := 0
	client := &http.Client{Timeout: 5 * time.Second}

	for _, windowSize := range windowSizes {
		toAdd := windowSize - cumulativeHistory
		if toAdd > 0 {
			if err := scenariosupport.CreateTerminalDeliveriesBulk(ctx, pool, tenantID, endpointID, toAdd); err != nil {
				return nil, err
			}
			cumulativeHistory = windowSize
		}

		latenciesMs := make([]float64, 0, requestsPerLevel)
		for i := 0; i < requestsPerLevel; i++ {
			body, err := json.Marshal(map[string]any{
				"range_start": "2020-01-01T00:00:00.000Z",
				"range_end":   "2030-01-01T00:00:00.000Z",
			})
			if err != nil {
				return nil, err
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/endpoints/"+endpointID+"/replays", bytes.NewReader(body))
			if err != nil {
				return nil, err
			}
			req.Header.Set("content-type", "application/json")
			req.Header.Set("authorization", "Bearer "+apiKey)
			req.Header.Set("idempotency-key", fmt.Sprintf("replay-latency-%d-%d", windowSize, i))

			startedAt := time.Now()
			resp, err := client.Do(req)
			if err != nil {
				return nil, err
			}
			elapsedMs := float64(time.Since(startedAt).Microseconds()) / 1000.0
			resp.Body.Close()
			if resp.StatusCode != http.StatusAccepted {
				return nil, fmt.Errorf("expected 202 from POST /endpoints/:id/replays at window size %d, got %d", windowSize, resp.StatusCode)
			}
			latenciesMs = append(latenciesMs, elapsedMs)
		}

		sort.Float64s(latenciesMs)
		resultsByWindow[windowSize] = levelResult{
			P50: scenariosupport.Percentile(latenciesMs, 50),
			P99: scenariosupport.Percentile(latenciesMs, 99),
		}
	}

	baseline := resultsByWindow[windowSizes[0]]
	largest := resultsByWindow[windowSizes[len(windowSizes)-1]]
	if largest.P99 > baseline.P99*5+50 {
		return nil, fmt.Errorf("expected p99 replay latency at window size %d (%.1fms) to stay roughly flat versus window size %d (%.1fms)",
			windowSizes[len(windowSizes)-1], largest.P99, windowSizes[0], baseline.P99)
	}

	return map[string]any{"resultsByWindow": resultsByWindow}, nil
}
