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

// PRD §8: "Repeated publish and replay keys create no additional
// deliveries" (R-6/R-21). Randomizes how many times the same key is
// replayed (retried request, network retry, at-least-once caller, etc.) —
// the invariant must hold regardless of the repeat count.
func TestRepeatedPublishKeyPropertyCreatesNoAdditionalDeliveries(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	ctx := context.Background()

	seed := properties.GetTestSeed("repeated-publish-key")
	rng := rand.New(rand.NewSource(seed))
	repeatCount := 2 + rng.Intn(9) // [2, 10]

	tenantID, apiKey := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})

	idempotencyKey := fmt.Sprintf("repeat-property-%d", seed)
	eventIDs := map[string]bool{}
	for i := 0; i < repeatCount; i++ {
		req, err := newJSONRequest(http.MethodPost, ts.URL+"/events", map[string]any{
			"type":    "order.created",
			"payload": map[string]any{"attempt": i},
		})
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Idempotency-Key", idempotencyKey)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusAccepted, resp.StatusCode)
		var body struct {
			ID string `json:"id"`
		}
		decodeJSON(t, resp, &body)
		resp.Body.Close()
		eventIDs[body.ID] = true
	}
	require.Len(t, eventIDs, 1) // every repeat resolved to the same event

	for {
		did, err := worker.RunExpansionCycle(ctx, pool)
		require.NoError(t, err)
		if !did {
			break
		}
	}

	var count int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM deliveries WHERE endpoint_id = $1", endpointID).Scan(&count))
	require.Equal(t, 1, count) // exactly one fan-out, regardless of repeatCount
}
