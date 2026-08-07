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

	resultsByWindow := map[int]scenariosupport.LatencyResult{}
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

		result, err := scenariosupport.MeasureLatencies(requestsPerLevel, func(i int) (float64, error) {
			body, err := json.Marshal(map[string]any{
				"range_start": "2020-01-01T00:00:00.000Z",
				"range_end":   "2030-01-01T00:00:00.000Z",
			})
			if err != nil {
				return 0, err
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/endpoints/"+endpointID+"/replays", bytes.NewReader(body))
			if err != nil {
				return 0, err
			}
			req.Header.Set("content-type", "application/json")
			req.Header.Set("authorization", "Bearer "+apiKey)
			req.Header.Set("idempotency-key", fmt.Sprintf("replay-latency-%d-%d", windowSize, i))

			startedAt := time.Now()
			resp, err := client.Do(req)
			if err != nil {
				return 0, err
			}
			elapsedMs := float64(time.Since(startedAt).Microseconds()) / 1000.0
			resp.Body.Close()
			if resp.StatusCode != http.StatusAccepted {
				return 0, fmt.Errorf("expected 202 from POST /endpoints/:id/replays at window size %d, got %d", windowSize, resp.StatusCode)
			}
			return elapsedMs, nil
		})
		if err != nil {
			return nil, err
		}
		resultsByWindow[windowSize] = result
	}

	baseline := resultsByWindow[windowSizes[0]]
	largest := resultsByWindow[windowSizes[len(windowSizes)-1]]
	if err := scenariosupport.AssertLatencyFlat(baseline, largest, fmt.Sprintf("replay latency at window size %d", windowSizes[len(windowSizes)-1])); err != nil {
		return nil, err
	}

	return map[string]any{"resultsByWindow": resultsByWindow}, nil
}
