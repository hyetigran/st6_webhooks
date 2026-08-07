package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"webhooks-go/internal/testsupport"
)

// insertDelivery links an already-created event to an endpoint directly —
// bypasses expansion for tests that only need the fan-out relationship to
// exist, not a real expansion cycle to have run.
func insertDelivery(t *testing.T, pool *pgxpool.Pool, eventID, endpointID string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		"INSERT INTO deliveries (event_id, endpoint_id) VALUES ($1, $2)", eventID, endpointID,
	)
	require.NoError(t, err)
}

func TestListEventsNewestFirst(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	tenantID, apiKey := testsupport.CreateTenant(t, pool)
	older := time.Now().Add(-60 * time.Second)
	newer := time.Now()
	olderID := testsupport.CreateEvent(t, pool, tenantID, testsupport.EventOptions{CreatedAt: &older})
	newerID := testsupport.CreateEvent(t, pool, tenantID, testsupport.EventOptions{CreatedAt: &newer})

	resp := doRequest(t, http.MethodGet, ts.URL+"/events", apiKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Events []struct{ ID string } `json:"events"`
	}
	decodeJSON(t, resp, &body)
	require.Len(t, body.Events, 2)
	require.Equal(t, []string{newerID, olderID}, []string{body.Events[0].ID, body.Events[1].ID})
}

func TestListEventsNeverReturnsAnotherTenantsEvents(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	_, apiKey := testsupport.CreateTenant(t, pool)
	otherTenantID, _ := testsupport.CreateTenant(t, pool)
	testsupport.CreateEvent(t, pool, otherTenantID, testsupport.EventOptions{})

	resp := doRequest(t, http.MethodGet, ts.URL+"/events", apiKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Events []struct{ ID string } `json:"events"`
	}
	decodeJSON(t, resp, &body)
	require.Empty(t, body.Events)
}

func TestListEventsFiltersByType(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	tenantID, apiKey := testsupport.CreateTenant(t, pool)
	shippedID := testsupport.CreateEvent(t, pool, tenantID, testsupport.EventOptions{Type: "order.shipped"})
	testsupport.CreateEvent(t, pool, tenantID, testsupport.EventOptions{Type: "order.created"})

	resp := doRequest(t, http.MethodGet, ts.URL+"/events?type=order.shipped", apiKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Events []struct{ ID string } `json:"events"`
	}
	decodeJSON(t, resp, &body)
	require.Len(t, body.Events, 1)
	require.Equal(t, shippedID, body.Events[0].ID)
}

func TestListEventsFiltersByID(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	tenantID, apiKey := testsupport.CreateTenant(t, pool)
	targetID := testsupport.CreateEvent(t, pool, tenantID, testsupport.EventOptions{})
	testsupport.CreateEvent(t, pool, tenantID, testsupport.EventOptions{})

	resp := doRequest(t, http.MethodGet, ts.URL+"/events?id="+targetID, apiKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Events []struct{ ID string } `json:"events"`
	}
	decodeJSON(t, resp, &body)
	require.Len(t, body.Events, 1)
	require.Equal(t, targetID, body.Events[0].ID)
}

func TestListEventsFiltersByFromToRange(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	tenantID, apiKey := testsupport.CreateTenant(t, pool)
	inRange := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	outOfRange := time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC)
	inRangeID := testsupport.CreateEvent(t, pool, tenantID, testsupport.EventOptions{CreatedAt: &inRange})
	testsupport.CreateEvent(t, pool, tenantID, testsupport.EventOptions{CreatedAt: &outOfRange})

	resp := doRequest(t, http.MethodGet, ts.URL+"/events?from=2026-01-01T00:00:00.000Z&to=2026-01-31T00:00:00.000Z", apiKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Events []struct{ ID string } `json:"events"`
	}
	decodeJSON(t, resp, &body)
	require.Len(t, body.Events, 1)
	require.Equal(t, inRangeID, body.Events[0].ID)
}

func TestListEventsFiltersByEndpointID(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	tenantID, apiKey := testsupport.CreateTenant(t, pool)
	endpointA := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	endpointB := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	eventForA := testsupport.CreateEvent(t, pool, tenantID, testsupport.EventOptions{})
	insertDelivery(t, pool, eventForA, endpointA)
	eventForB := testsupport.CreateEvent(t, pool, tenantID, testsupport.EventOptions{})
	insertDelivery(t, pool, eventForB, endpointB)

	resp := doRequest(t, http.MethodGet, ts.URL+"/events?endpoint_id="+endpointA, apiKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Events []struct{ ID string } `json:"events"`
	}
	decodeJSON(t, resp, &body)
	require.Len(t, body.Events, 1)
	require.Equal(t, eventForA, body.Events[0].ID)
}

func TestGetEventReturnsFanOutDeliveries(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	tenantID, apiKey := testsupport.CreateTenant(t, pool)
	endpointA := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	endpointB := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	eventID := testsupport.CreateEvent(t, pool, tenantID, testsupport.EventOptions{Type: "order.created", Payload: `{"orderId":"abc"}`})
	insertDelivery(t, pool, eventID, endpointA)
	insertDelivery(t, pool, eventID, endpointB)

	resp := doRequest(t, http.MethodGet, ts.URL+"/events/"+eventID, apiKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		ID         string         `json:"id"`
		Type       string         `json:"type"`
		Payload    map[string]any `json:"payload"`
		Deliveries []struct {
			EndpointID string `json:"endpoint_id"`
		} `json:"deliveries"`
	}
	decodeJSON(t, resp, &body)
	require.Equal(t, eventID, body.ID)
	require.Equal(t, "order.created", body.Type)
	require.Equal(t, "abc", body.Payload["orderId"])
	require.Len(t, body.Deliveries, 2)
	endpointIDs := []string{body.Deliveries[0].EndpointID, body.Deliveries[1].EndpointID}
	require.ElementsMatch(t, []string{endpointA, endpointB}, endpointIDs)
}

func TestGetEventReturnsNotFoundForCrossTenantEvent(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	otherTenantID, _ := testsupport.CreateTenant(t, pool)
	foreignEventID := testsupport.CreateEvent(t, pool, otherTenantID, testsupport.EventOptions{})
	_, apiKey := testsupport.CreateTenant(t, pool)

	resp := doRequest(t, http.MethodGet, ts.URL+"/events/"+foreignEventID, apiKey, nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}
