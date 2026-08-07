// PRD §8 (R-8): "Publish latency flat from 10 to 10,000 subscribers;
// expansion completes." POST /events only ever inserts one events row — no
// fan-out at publish time — so latency shouldn't grow with subscriber
// count; expansion (fan-out) happens later, asynchronously, in the worker.
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

var subscriberLevels = []int{10, 100, 1_000, 10_000}

const requestsPerLevel = 30
const port = 4100

func main() {
	scenariosupport.RunScenario("load", "publish-latency-flat", run)
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

	server, err := scenariosupport.SpawnAPIServer("./bin/api", port, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = server.Kill(syscall.SIGTERM) }()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := scenariosupport.WaitForServer(ctx, baseURL, 10*time.Second); err != nil {
		return nil, err
	}

	resultsByLevel := map[int]levelResult{}
	cumulativeEndpoints := 0
	client := &http.Client{Timeout: 5 * time.Second}

	for _, level := range subscriberLevels {
		toAdd := level - cumulativeEndpoints
		if toAdd > 0 {
			if err := scenariosupport.CreateEndpointsBulk(ctx, pool, tenantID, toAdd, "", nil); err != nil {
				return nil, err
			}
			cumulativeEndpoints = level
		}

		latenciesMs := make([]float64, 0, requestsPerLevel)
		for i := 0; i < requestsPerLevel; i++ {
			body, err := json.Marshal(map[string]any{"type": "order.created", "payload": map[string]any{"i": i}})
			if err != nil {
				return nil, err
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/events", bytes.NewReader(body))
			if err != nil {
				return nil, err
			}
			req.Header.Set("content-type", "application/json")
			req.Header.Set("authorization", "Bearer "+apiKey)
			req.Header.Set("idempotency-key", fmt.Sprintf("publish-latency-%d-%d", level, i))

			startedAt := time.Now()
			resp, err := client.Do(req)
			if err != nil {
				return nil, err
			}
			elapsedMs := float64(time.Since(startedAt).Microseconds()) / 1000.0
			resp.Body.Close()
			if resp.StatusCode != http.StatusAccepted {
				return nil, fmt.Errorf("expected 202 from POST /events at subscriber level %d, got %d", level, resp.StatusCode)
			}
			latenciesMs = append(latenciesMs, elapsedMs)
		}

		sort.Float64s(latenciesMs)
		resultsByLevel[level] = levelResult{
			P50: scenariosupport.Percentile(latenciesMs, 50),
			P99: scenariosupport.Percentile(latenciesMs, 99),
		}
	}

	baseline := resultsByLevel[subscriberLevels[0]]
	largest := resultsByLevel[subscriberLevels[len(subscriberLevels)-1]]
	// Generous multiple, not an exact match — this is asserting "doesn't
	// scale with subscriber count," not "is bit-for-bit identical," since
	// real scheduling/GC jitter means run-to-run noise exists regardless.
	if largest.P99 > baseline.P99*5+50 {
		return nil, fmt.Errorf("expected p99 publish latency at %d subscribers (%.1fms) to stay roughly flat versus %d subscribers (%.1fms)",
			subscriberLevels[len(subscriberLevels)-1], largest.P99, subscriberLevels[0], baseline.P99)
	}

	return map[string]any{"resultsByLevel": resultsByLevel}, nil
}
