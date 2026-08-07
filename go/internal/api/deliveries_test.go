package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"webhooks-go/internal/testsupport"
)

func TestGetDeliveryReturnsHeadDeliveryDetailWithNoBlockedOn(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	tenantID, apiKey := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	deliveryID, eventID := testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{State: "pending"})

	resp := doRequest(t, http.MethodGet, ts.URL+"/deliveries/"+deliveryID, apiKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		ID                  string      `json:"id"`
		EventID             string      `json:"event_id"`
		EndpointID          string      `json:"endpoint_id"`
		State               string      `json:"state"`
		AttemptCount        int         `json:"attempt_count"`
		BlockedOnDeliveryID *string     `json:"blocked_on_delivery_id"`
		LastResponse        interface{} `json:"last_response"`
		Attempts            []any       `json:"attempts"`
	}
	decodeJSON(t, resp, &body)
	require.Equal(t, deliveryID, body.ID)
	require.Equal(t, eventID, body.EventID)
	require.Equal(t, endpointID, body.EndpointID)
	require.Equal(t, "pending", body.State)
	require.Equal(t, 0, body.AttemptCount)
	require.Nil(t, body.BlockedOnDeliveryID)
	require.Nil(t, body.LastResponse)
	require.Empty(t, body.Attempts)
}

func TestGetDeliveryReportsBlockedOnDeliveryIDForNonHeadPending(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	tenantID, apiKey := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	headID, _ := testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{State: "pending"})
	blockedID, _ := testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{State: "pending"})

	resp := doRequest(t, http.MethodGet, ts.URL+"/deliveries/"+blockedID, apiKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		BlockedOnDeliveryID *string `json:"blocked_on_delivery_id"`
	}
	decodeJSON(t, resp, &body)
	require.NotNil(t, body.BlockedOnDeliveryID)
	require.Equal(t, headID, *body.BlockedOnDeliveryID)
}

func TestGetDeliveryNeverBlockedWhileInFlight(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	tenantID, apiKey := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	deliveryID, _ := testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{State: "in_flight"})

	resp := doRequest(t, http.MethodGet, ts.URL+"/deliveries/"+deliveryID, apiKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		BlockedOnDeliveryID *string `json:"blocked_on_delivery_id"`
	}
	decodeJSON(t, resp, &body)
	require.Nil(t, body.BlockedOnDeliveryID)
}

func TestGetDeliveryNeverBlockedOnceTerminal(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	tenantID, apiKey := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	succeededID, _ := testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{State: "succeeded"})
	testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{State: "pending"}) // unrelated, still pending

	resp := doRequest(t, http.MethodGet, ts.URL+"/deliveries/"+succeededID, apiKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		BlockedOnDeliveryID *string `json:"blocked_on_delivery_id"`
	}
	decodeJSON(t, resp, &body)
	require.Nil(t, body.BlockedOnDeliveryID)
}

func TestGetDeliveryEmbedsAttemptsAndDerivesLastResponse(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	tenantID, apiKey := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	deliveryID, _ := testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{State: "pending"})
	status500 := 500
	body500 := "server error"
	duration10 := 10
	testsupport.CreateAttempt(t, pool, deliveryID, testsupport.AttemptOptions{
		AttemptNumber: 1, ResponseStatus: &status500, ResponseBodyTruncated: &body500, DurationMs: &duration10,
	})
	errClass := "total_timeout"
	testsupport.CreateAttempt(t, pool, deliveryID, testsupport.AttemptOptions{AttemptNumber: 2, ErrorClass: &errClass})

	resp := doRequest(t, http.MethodGet, ts.URL+"/deliveries/"+deliveryID, apiKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var respBody struct {
		Attempts []struct {
			AttemptNumber int `json:"attempt_number"`
		} `json:"attempts"`
		LastResponse struct {
			ResponseStatus *int    `json:"response_status"`
			ErrorClass     *string `json:"error_class"`
		} `json:"last_response"`
	}
	decodeJSON(t, resp, &respBody)
	require.Len(t, respBody.Attempts, 2)
	require.Equal(t, 1, respBody.Attempts[0].AttemptNumber)
	require.Equal(t, 2, respBody.Attempts[1].AttemptNumber)
	require.Nil(t, respBody.LastResponse.ResponseStatus)
	require.NotNil(t, respBody.LastResponse.ErrorClass)
	require.Equal(t, "total_timeout", *respBody.LastResponse.ErrorClass)
}

func TestGetDeliveryCapsAttemptsAtSixKeepingMostRecent(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	tenantID, apiKey := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	deliveryID, _ := testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{State: "pending"})
	status500 := 500
	for i := 1; i <= 8; i++ {
		testsupport.CreateAttempt(t, pool, deliveryID, testsupport.AttemptOptions{AttemptNumber: i, ResponseStatus: &status500})
	}

	resp := doRequest(t, http.MethodGet, ts.URL+"/deliveries/"+deliveryID, apiKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var respBody struct {
		Attempts []struct {
			AttemptNumber int `json:"attempt_number"`
		} `json:"attempts"`
	}
	decodeJSON(t, resp, &respBody)
	require.Len(t, respBody.Attempts, 6)
	var numbers []int
	for _, a := range respBody.Attempts {
		numbers = append(numbers, a.AttemptNumber)
	}
	require.Equal(t, []int{3, 4, 5, 6, 7, 8}, numbers)
}

func TestGetDeliveryReturnsNotFoundForCrossTenantDelivery(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	otherTenantID, _ := testsupport.CreateTenant(t, pool)
	foreignEndpointID := testsupport.CreateEndpoint(t, pool, otherTenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	foreignDeliveryID, _ := testsupport.CreateDelivery(t, pool, otherTenantID, foreignEndpointID, testsupport.DeliveryOptions{})
	_, apiKey := testsupport.CreateTenant(t, pool)

	resp := doRequest(t, http.MethodGet, ts.URL+"/deliveries/"+foreignDeliveryID, apiKey, nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestGetDeliveryReturnsNotFoundForMissingDelivery(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	_, apiKey := testsupport.CreateTenant(t, pool)

	resp := doRequest(t, http.MethodGet, ts.URL+"/deliveries/00000000-0000-0000-0000-000000000000", apiKey, nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}
