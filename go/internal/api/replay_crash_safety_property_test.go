package api_test

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"webhooks-go/internal/properties"
	"webhooks-go/internal/testsupport"
	"webhooks-go/internal/worker"
)

// REVIEW.md F-8 / docs/adr/0005: "crash-inject between a replay's durable
// ack and its expansion; retry the same key; deliveries created exactly
// once, in original order" (R-19/R-21/R-22). The "crash" is exactly the gap
// between the POST committing the replays row and the worker expanding it —
// retrying the POST any number of times in that gap must never double the
// result once expansion finally runs. Randomizes how many retries land in
// that gap.
func TestReplayCrashSafetyPropertyHoldsForRetriesBeforeExpansion(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	ctx := context.Background()

	seed := properties.GetTestSeed("replay-crash-safety")
	rng := rand.New(rand.NewSource(seed))
	retryCount := 2 + rng.Intn(7) // [2, 8]

	tenantID, apiKey := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})

	// A terminal original delivery, in the replay window, for the worker to
	// find once expansion runs.
	req, err := newJSONRequest(http.MethodPost, ts.URL+"/events", map[string]any{
		"type":    "order.created",
		"payload": map[string]any{},
	})
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Idempotency-Key", "replay-crash-safety-original")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	var published struct {
		ID string `json:"id"`
	}
	decodeJSON(t, resp, &published)
	resp.Body.Close()

	for {
		did, err := worker.RunExpansionCycle(ctx, pool)
		require.NoError(t, err)
		if !did {
			break
		}
	}
	_, err = pool.Exec(ctx, "UPDATE deliveries SET state = 'succeeded' WHERE event_id = $1", published.ID)
	require.NoError(t, err)

	idempotencyKey := fmt.Sprintf("replay-crash-safety-%d", seed)
	replayIDs := map[string]bool{}
	// Simulates the crash-and-retry window: N requests all land before the
	// worker ever runs expansion for this replay.
	for i := 0; i < retryCount; i++ {
		replayReq, err := newJSONRequest(http.MethodPost, ts.URL+"/endpoints/"+endpointID+"/replays", map[string]any{
			"range_start": "2020-01-01T00:00:00.000Z",
			"range_end":   "2030-01-01T00:00:00.000Z",
		})
		require.NoError(t, err)
		replayReq.Header.Set("Authorization", "Bearer "+apiKey)
		replayReq.Header.Set("Idempotency-Key", idempotencyKey)

		replayResp, err := http.DefaultClient.Do(replayReq)
		require.NoError(t, err)
		require.Equal(t, http.StatusAccepted, replayResp.StatusCode)
		var body struct {
			ID string `json:"id"`
		}
		decodeJSON(t, replayResp, &body)
		replayResp.Body.Close()
		replayIDs[body.ID] = true
	}
	require.Len(t, replayIDs, 1) // every retry resolved to the same replays row

	_, err = worker.RunReplayExpansionCycle(ctx, pool)
	require.NoError(t, err)

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FROM deliveries WHERE endpoint_id = $1 AND event_id = $2", endpointID, published.ID,
	).Scan(&count))
	// The one original delivery (already succeeded above) plus exactly one
	// replayed delivery — never more, regardless of retryCount.
	require.Equal(t, 2, count)
}
