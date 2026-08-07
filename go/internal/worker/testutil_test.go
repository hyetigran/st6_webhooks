package worker_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// publishEvent bypasses the API (direct insert) — expansion-cycle tests
// only need an event row to exist, not the publish route's own behavior
// (that's internal/api/events_test.go's job).
func publishEvent(t *testing.T, pool *pgxpool.Pool, tenantID, eventType, idempotencyKey string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO events (tenant_id, idempotency_key, type, payload) VALUES ($1, $2, $3, '{}') RETURNING id`,
		tenantID, idempotencyKey, eventType,
	).Scan(&id)
	require.NoError(t, err)
	return id
}
