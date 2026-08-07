package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"webhooks-go/internal/testsupport"
)

func TestEndpointDeliveriesOrderedHeadFirstWithBlockedOn(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	tenantID, apiKey := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	headID, _ := testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{State: "pending"})
	secondID, _ := testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{State: "pending"})

	resp := doRequest(t, http.MethodGet, ts.URL+"/endpoints/"+endpointID+"/deliveries", apiKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Deliveries []struct {
			ID                  string  `json:"id"`
			BlockedOnDeliveryID *string `json:"blocked_on_delivery_id"`
		} `json:"deliveries"`
		NextCursor *string `json:"next_cursor"`
	}
	decodeJSON(t, resp, &body)
	require.Len(t, body.Deliveries, 2)
	require.Equal(t, headID, body.Deliveries[0].ID)
	require.Equal(t, secondID, body.Deliveries[1].ID)
	require.Nil(t, body.Deliveries[0].BlockedOnDeliveryID)
	require.NotNil(t, body.Deliveries[1].BlockedOnDeliveryID)
	require.Equal(t, headID, *body.Deliveries[1].BlockedOnDeliveryID)
}

func TestEndpointDeliveriesOnlyReturnsThisEndpoints(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	tenantID, apiKey := testsupport.CreateTenant(t, pool)
	endpointA := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	endpointB := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	forA, _ := testsupport.CreateDelivery(t, pool, tenantID, endpointA, testsupport.DeliveryOptions{})
	testsupport.CreateDelivery(t, pool, tenantID, endpointB, testsupport.DeliveryOptions{})

	resp := doRequest(t, http.MethodGet, ts.URL+"/endpoints/"+endpointA+"/deliveries", apiKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Deliveries []struct {
			ID string `json:"id"`
		} `json:"deliveries"`
	}
	decodeJSON(t, resp, &body)
	require.Len(t, body.Deliveries, 1)
	require.Equal(t, forA, body.Deliveries[0].ID)
}

func TestEndpointDeliveriesPaginatesWithSeqCursor(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	tenantID, apiKey := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	firstID, _ := testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{})
	secondID, _ := testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{})

	firstPageResp := doRequest(t, http.MethodGet, ts.URL+"/endpoints/"+endpointID+"/deliveries?limit=1", apiKey, nil)
	require.Equal(t, http.StatusOK, firstPageResp.StatusCode)
	var firstPage struct {
		Deliveries []struct {
			ID string `json:"id"`
		} `json:"deliveries"`
		NextCursor *string `json:"next_cursor"`
	}
	decodeJSON(t, firstPageResp, &firstPage)
	require.Len(t, firstPage.Deliveries, 1)
	require.Equal(t, firstID, firstPage.Deliveries[0].ID)
	require.NotNil(t, firstPage.NextCursor)

	secondPageResp := doRequest(t, http.MethodGet, fmt.Sprintf("%s/endpoints/%s/deliveries?limit=1&after=%s", ts.URL, endpointID, *firstPage.NextCursor), apiKey, nil)
	require.Equal(t, http.StatusOK, secondPageResp.StatusCode)
	var secondPage struct {
		Deliveries []struct {
			ID string `json:"id"`
		} `json:"deliveries"`
		NextCursor *string `json:"next_cursor"`
	}
	decodeJSON(t, secondPageResp, &secondPage)
	require.Len(t, secondPage.Deliveries, 1)
	require.Equal(t, secondID, secondPage.Deliveries[0].ID)
	require.Nil(t, secondPage.NextCursor)
}

func TestEndpointDeliveriesReturnsNotFoundForCrossTenantEndpoint(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	otherTenantID, _ := testsupport.CreateTenant(t, pool)
	foreignEndpointID := testsupport.CreateEndpoint(t, pool, otherTenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	_, apiKey := testsupport.CreateTenant(t, pool)

	resp := doRequest(t, http.MethodGet, ts.URL+"/endpoints/"+foreignEndpointID+"/deliveries", apiKey, nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}
