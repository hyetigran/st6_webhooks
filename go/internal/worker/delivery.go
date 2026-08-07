package worker

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"webhooks-go/internal/crypto"
	"webhooks-go/internal/signing"
)

// BackoffConfig is the full-jitter retry schedule (global-only for v0.2.0 —
// per-endpoint tuning is out of scope).
type BackoffConfig struct {
	BaseDelayMs int
	Multiplier  int
	MaxDelayMs  int
	MaxAttempts int
}

// ComputeBackoffDelayMs implements full jitter (AWS-documented retry-storm
// mitigation): delay = random(0, min(base * multiplier^(attempt-1), cap)).
// attemptNumber is 1-indexed — the attempt that just failed — so attempt
// 1's ceiling is the base delay, not multiplied yet. randFloat is injected
// (production passes rand.Float64) so tests can pin the jitter draw.
func ComputeBackoffDelayMs(attemptNumber int, cfg BackoffConfig, randFloat func() float64) int {
	ceiling := float64(cfg.BaseDelayMs) * math.Pow(float64(cfg.Multiplier), float64(attemptNumber-1))
	if ceiling > float64(cfg.MaxDelayMs) {
		ceiling = float64(cfg.MaxDelayMs)
	}
	return int(math.Floor(randFloat() * ceiling))
}

// ClaimedDelivery is everything RunDeliveryCycle needs to sign and send one
// attempt, plus the lease/attempt bookkeeping CompleteDelivery fences its
// write-back on.
type ClaimedDelivery struct {
	EndpointID      string
	TenantID        string
	LeaseID         string
	DeliveryID      string
	EventID         string
	EventType       string
	Payload         json.RawMessage
	AttemptNumber   int
	AttemptID       string
	URL             string
	SigningSecret   string
	SecondarySecret string // "" means no active rotation-overlap secret
}

type candidateRow struct {
	ID       string
	TenantID string
	Busy     bool
}

// ClaimDelivery orders candidates by tenant fairness (least-recently-served
// first) then the endpoint's oldest ready delivery (docs/adr/0002/0004).
// Claiming is a short-lived row lock (the busy flag), not held for the
// outbound HTTP call — that happens entirely outside this function. Passive
// lease reclaim is handled inline: an endpoint whose busy_since is past the
// lease duration is claimable again, and if it had an attempt in flight,
// that attempt is closed with a synthetic worker_lease_expired outcome and
// its delivery requeued before the new claim proceeds.
func ClaimDelivery(ctx context.Context, pool *pgxpool.Pool, leaseDurationMs int, secretEncryptionKey []byte) (*ClaimedDelivery, error) {
	cutoff := time.Now().Add(-time.Duration(leaseDurationMs) * time.Millisecond)

	rows, err := pool.Query(ctx,
		`SELECT e.id, e.tenant_id, e.busy
		 FROM endpoints e
		 JOIN tenants t ON t.id = e.tenant_id
		 WHERE e.status = 'active'
		   AND (e.busy = false OR e.busy_since < $1)
		   AND EXISTS (
		     SELECT 1 FROM deliveries d
		     WHERE d.endpoint_id = e.id
		       AND ((d.state = 'pending' AND d.next_attempt_at <= now()) OR d.state = 'in_flight')
		   )
		 ORDER BY t.last_served_at ASC NULLS FIRST,
		   (SELECT MIN(d.seq) FROM deliveries d
		    WHERE d.endpoint_id = e.id
		      AND ((d.state = 'pending' AND d.next_attempt_at <= now()) OR d.state = 'in_flight')) ASC
		 LIMIT 20`,
		cutoff,
	)
	if err != nil {
		return nil, err
	}
	var candidates []candidateRow
	for rows.Next() {
		var c candidateRow
		if err := rows.Scan(&c.ID, &c.TenantID, &c.Busy); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, candidate := range candidates {
		claimed, err := tryClaimEndpoint(ctx, pool, candidate, cutoff, secretEncryptionKey)
		if err != nil {
			return nil, err
		}
		if claimed != nil {
			return claimed, nil
		}
	}
	return nil, nil
}

func tryClaimEndpoint(ctx context.Context, pool *pgxpool.Pool, candidate candidateRow, cutoff time.Time, secretEncryptionKey []byte) (*ClaimedDelivery, error) {
	var leaseID string
	err := pool.QueryRow(ctx,
		`UPDATE endpoints
		 SET busy = true, busy_since = now(), lease_id = gen_random_uuid()
		 WHERE id = $1 AND status = 'active' AND (busy = false OR busy_since < $2)
		 RETURNING lease_id`,
		candidate.ID, cutoff,
	).Scan(&leaseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil // lost the race to another worker
	}
	if err != nil {
		return nil, err
	}

	if _, err := pool.Exec(ctx, "UPDATE tenants SET last_served_at = now() WHERE id = $1", candidate.TenantID); err != nil {
		return nil, err
	}

	if candidate.Busy {
		if err := reclaimStaleLease(ctx, pool, candidate.ID); err != nil {
			return nil, err
		}
	}

	head, eligible, err := fetchClaimableHead(ctx, pool, candidate.ID)
	if err != nil {
		return nil, err
	}
	if !eligible {
		// Either nothing pending at all (the candidate filter guarantees
		// this shouldn't happen, but release defensively rather than leave
		// the endpoint stuck busy), or the true head just isn't ready yet —
		// either way, nothing on this endpoint may be claimed right now.
		if _, err := pool.Exec(ctx,
			"UPDATE endpoints SET busy = false, busy_since = NULL, lease_id = NULL WHERE id = $1 AND lease_id = $2",
			candidate.ID, leaseID,
		); err != nil {
			return nil, err
		}
		return nil, nil
	}

	attemptNumber := head.AttemptCount + 1
	var attemptID string
	if err := pool.QueryRow(ctx,
		"INSERT INTO attempts (delivery_id, attempt_number, sent_at) VALUES ($1, $2, now()) RETURNING id",
		head.DeliveryID, attemptNumber,
	).Scan(&attemptID); err != nil {
		return nil, err
	}
	if _, err := pool.Exec(ctx,
		"UPDATE deliveries SET state = 'in_flight', attempt_count = $2 WHERE id = $1",
		head.DeliveryID, attemptNumber,
	); err != nil {
		return nil, err
	}

	signingSecret, secondarySecret, err := decryptSecrets(head, secretEncryptionKey)
	if err != nil {
		return nil, err
	}

	return &ClaimedDelivery{
		EndpointID:      candidate.ID,
		TenantID:        candidate.TenantID,
		LeaseID:         leaseID,
		DeliveryID:      head.DeliveryID,
		EventID:         head.EventID,
		EventType:       head.EventType,
		Payload:         head.Payload,
		AttemptNumber:   attemptNumber,
		AttemptID:       attemptID,
		URL:             head.URL,
		SigningSecret:   signingSecret,
		SecondarySecret: secondarySecret,
	}, nil
}

// reclaimStaleLease closes the previous worker's orphaned in-flight attempt
// (sent_at set, no response fields yet — it must not strand the delivery)
// and requeues its delivery so the new claim below can pick it up.
func reclaimStaleLease(ctx context.Context, pool *pgxpool.Pool, endpointID string) error {
	if _, err := pool.Exec(ctx,
		`UPDATE attempts SET error_class = 'worker_lease_expired'
		 WHERE delivery_id = (SELECT id FROM deliveries WHERE endpoint_id = $1 AND state = 'in_flight')
		   AND sent_at IS NOT NULL AND response_status IS NULL AND error_class IS NULL`,
		endpointID,
	); err != nil {
		return err
	}
	_, err := pool.Exec(ctx, "UPDATE deliveries SET state = 'pending' WHERE endpoint_id = $1 AND state = 'in_flight'", endpointID)
	return err
}

type headDeliveryRow struct {
	DeliveryID               string
	EventID                  string
	AttemptCount             int
	NextAttemptAt            time.Time
	EventType                string
	Payload                  json.RawMessage
	URL                      string
	SigningSecretEnc         string
	SecondarySecretEnc       *string
	SecondarySecretExpiresAt *time.Time
}

// fetchClaimableHead fetches an endpoint's true head delivery and reports
// whether it's eligible to claim right now. R-11: strict per-endpoint order
// means the true head — lowest seq, period — is always what's next, never a
// later delivery that merely happens to be immediately eligible. Filtering
// on next_attempt_at in the query itself (as an earlier version did) would
// let a later delivery jump ahead of a head that's still cooling down from
// a prior failure's backoff — found via chaos testing in the Node stack,
// not by inspection. So: fetch the head unconditionally, decide eligibility
// here in application code instead.
func fetchClaimableHead(ctx context.Context, pool *pgxpool.Pool, endpointID string) (headDeliveryRow, bool, error) {
	var row headDeliveryRow
	err := pool.QueryRow(ctx,
		`SELECT d.id, d.event_id, d.attempt_count, d.next_attempt_at, e.type AS event_type, e.payload,
		        ep.url, ep.signing_secret, ep.secondary_secret, ep.secondary_secret_expires_at
		 FROM deliveries d
		 JOIN events e ON e.id = d.event_id
		 JOIN endpoints ep ON ep.id = d.endpoint_id
		 WHERE d.endpoint_id = $1 AND d.state = 'pending'
		 ORDER BY d.seq
		 LIMIT 1`,
		endpointID,
	).Scan(&row.DeliveryID, &row.EventID, &row.AttemptCount, &row.NextAttemptAt, &row.EventType, &row.Payload,
		&row.URL, &row.SigningSecretEnc, &row.SecondarySecretEnc, &row.SecondarySecretExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return headDeliveryRow{}, false, nil
	}
	if err != nil {
		return headDeliveryRow{}, false, err
	}
	return row, !row.NextAttemptAt.After(time.Now()), nil
}

func decryptSecrets(head headDeliveryRow, secretEncryptionKey []byte) (signingSecret, secondarySecret string, err error) {
	signingSecret, err = crypto.DecryptSecret(head.SigningSecretEnc, secretEncryptionKey)
	if err != nil {
		return "", "", err
	}
	hasActiveSecondary := head.SecondarySecretEnc != nil && head.SecondarySecretExpiresAt != nil && head.SecondarySecretExpiresAt.After(time.Now())
	if hasActiveSecondary {
		secondarySecret, err = crypto.DecryptSecret(*head.SecondarySecretEnc, secretEncryptionKey)
		if err != nil {
			return "", "", err
		}
	}
	return signingSecret, secondarySecret, nil
}

// AttemptOutcome is what CompleteDelivery writes back for one attempt —
// either a real response (ResponseStatus set, ErrorClass "") or a network-
// layer failure (ResponseStatus nil, ErrorClass one of httpclient.go's
// ErrClass* constants, or "url_not_allowed" for a delivery-time SSRF
// rejection).
type AttemptOutcome struct {
	ResponseStatus        *int
	ResponseBodyTruncated string
	DurationMs            int64
	// "" whenever a response was received, even a non-2xx one.
	ErrorClass string
}

const (
	CompleteOutcomeSucceeded = "succeeded"
	CompleteOutcomeRetrying  = "retrying"
	CompleteOutcomeHalted    = "halted"
	CompleteOutcomeLeaseLost = "lease_lost"
)

// CompleteDelivery fences every write on the endpoint's current lease_id
// still matching what ClaimDelivery captured (docs/adr/0002). A mismatch
// means another worker has since reclaimed this endpoint (this worker
// stalled past its lease) — the write is dropped silently rather than
// corrupting state the new owner already wrote. 2xx is the only success
// criterion (R-16/PRD §6); everything else, including no response at all,
// retries until the attempt ceiling halts it.
func CompleteDelivery(ctx context.Context, pool *pgxpool.Pool, claimed *ClaimedDelivery, result AttemptOutcome, backoffCfg BackoffConfig) (string, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var leaseID *string
	if err := tx.QueryRow(ctx, "SELECT lease_id FROM endpoints WHERE id = $1 FOR UPDATE", claimed.EndpointID).Scan(&leaseID); err != nil {
		return "", err
	}
	if leaseID == nil || *leaseID != claimed.LeaseID {
		// The deferred Rollback above handles cleanup; nothing was written.
		return CompleteOutcomeLeaseLost, nil
	}

	var errorClass *string
	if result.ErrorClass != "" {
		errorClass = &result.ErrorClass
	}
	if _, err := tx.Exec(ctx,
		`UPDATE attempts SET response_status = $2, response_body_truncated = $3, duration_ms = $4, error_class = $5 WHERE id = $1`,
		claimed.AttemptID, result.ResponseStatus, result.ResponseBodyTruncated, result.DurationMs, errorClass,
	); err != nil {
		return "", err
	}

	success := result.ResponseStatus != nil && *result.ResponseStatus >= 200 && *result.ResponseStatus < 300

	var outcome string
	switch {
	case success:
		if _, err := tx.Exec(ctx, "UPDATE deliveries SET state = 'succeeded' WHERE id = $1", claimed.DeliveryID); err != nil {
			return "", err
		}
		outcome = CompleteOutcomeSucceeded
	case claimed.AttemptNumber >= backoffCfg.MaxAttempts:
		if _, err := tx.Exec(ctx, "UPDATE deliveries SET state = 'failed' WHERE id = $1", claimed.DeliveryID); err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, "UPDATE endpoints SET status = 'halted' WHERE id = $1", claimed.EndpointID); err != nil {
			return "", err
		}
		outcome = CompleteOutcomeHalted
	default:
		delayMs := ComputeBackoffDelayMs(claimed.AttemptNumber, backoffCfg, rand.Float64)
		if _, err := tx.Exec(ctx,
			`UPDATE deliveries SET state = 'pending', next_attempt_at = now() + ($2 * interval '1 millisecond') WHERE id = $1`,
			claimed.DeliveryID, delayMs,
		); err != nil {
			return "", err
		}
		outcome = CompleteOutcomeRetrying
	}

	if _, err := tx.Exec(ctx, "UPDATE endpoints SET busy = false, busy_since = NULL, lease_id = NULL WHERE id = $1", claimed.EndpointID); err != nil {
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return outcome, nil
}

// OutboundConfig bounds the actual outbound webhook call (R-16).
type OutboundConfig struct {
	ConnectTimeoutMs     int
	TotalTimeoutMs       int
	MaxResponseBodyBytes int
}

// DeliveryConfig bundles every tunable RunDeliveryCycle needs, built once
// by cmd/worker from the process's env-loaded config.
type DeliveryConfig struct {
	SecretEncryptionKey []byte
	LeaseDurationMs     int
	Outbound            OutboundConfig
	Backoff             BackoffConfig
}

// DeliveryCycleDeps are the test seams: ResolveAndPinFn defaults to the
// real ResolveAndPin. A test that wants to exercise the real SSRF logic
// with a controlled DNS answer overrides this with a stub resolver. A
// happy-path test against a real local receiver has no choice but to
// override this wholesale instead: the receiver is necessarily on
// loopback, which the real check correctly always rejects (docs/adr/0006),
// so there is no DNS answer that both resolves there and passes
// validation. Transport, if set, is shared across calls for connection
// pooling and R-16's per-host limit (build one via NewTransport).
type DeliveryCycleDeps struct {
	ResolveAndPinFn func(ctx context.Context, hostname string) ResolveAndPinResult
	Transport       *http.Transport
}

// RunDeliveryCycle orchestrates one claim -> sign -> resolve-and-pin ->
// send -> write-back cycle. A delivery-time SSRF rejection (re-validated at
// delivery time, since DNS can change after registration) is treated as an
// ordinary retryable failure — same ceiling and backoff as any other
// non-2xx outcome — rather than a special case.
func RunDeliveryCycle(ctx context.Context, pool *pgxpool.Pool, cfg DeliveryConfig, deps DeliveryCycleDeps) (bool, error) {
	claimed, err := ClaimDelivery(ctx, pool, cfg.LeaseDurationMs, cfg.SecretEncryptionKey)
	if err != nil {
		return false, err
	}
	if claimed == nil {
		return false, nil
	}

	u, err := url.Parse(claimed.URL)
	if err != nil {
		return false, err
	}

	resolvePin := deps.ResolveAndPinFn
	if resolvePin == nil {
		resolvePin = func(ctx context.Context, hostname string) ResolveAndPinResult {
			return ResolveAndPin(ctx, hostname, nil)
		}
	}
	pin := resolvePin(ctx, u.Hostname())

	var result AttemptOutcome
	if !pin.Allowed {
		result = AttemptOutcome{ErrorClass: "url_not_allowed"}
	} else {
		timestamp := time.Now().Unix()
		// claimed.Payload is the raw jsonb bytes straight from Postgres
		// (never decoded into a Go map and re-marshaled), so this is
		// guaranteed to be exactly what gets signed AND exactly what's
		// sent — immune to Go's json.Marshal alphabetizing map keys on
		// re-encode, which could otherwise desync the signature from the
		// wire body. This is why it won't byte-match Node's
		// JSON.stringify()'d rawBody (Postgres's jsonb canonical text
		// includes a space after `:`/`,`, compact JSON.stringify doesn't):
		// the cross-backend "same HTTP surface" requirement
		// (systemPatterns.md) is scoped to the REST API the shared
		// frontend consumes, not to outbound webhook bodies delivered to a
		// tenant's own receiver — each backend only needs to be
		// self-consistent with what it signs and sends, which this
		// guarantees by construction.
		rawBody := string(claimed.Payload)
		secretsList := []string{claimed.SigningSecret}
		if claimed.SecondarySecret != "" {
			secretsList = append(secretsList, claimed.SecondarySecret)
		}

		sendResult := SendOutboundRequest(ctx, pin.IP, u, OutboundRequestOptions{
			Method: http.MethodPost,
			Headers: map[string]string{
				"content-type":      "application/json",
				"webhook-id":        claimed.DeliveryID,
				"webhook-event-id":  claimed.EventID,
				"webhook-attempt":   strconv.Itoa(claimed.AttemptNumber),
				"webhook-timestamp": strconv.FormatInt(timestamp, 10),
				"webhook-signature": signing.SignPayload(secretsList, timestamp, rawBody),
			},
			Body:                 rawBody,
			ConnectTimeoutMs:     cfg.Outbound.ConnectTimeoutMs,
			TotalTimeoutMs:       cfg.Outbound.TotalTimeoutMs,
			MaxResponseBodyBytes: cfg.Outbound.MaxResponseBodyBytes,
			Transport:            deps.Transport,
		})
		result = AttemptOutcome{
			ResponseStatus:        sendResult.ResponseStatus,
			ResponseBodyTruncated: sendResult.ResponseBodyTruncated,
			DurationMs:            sendResult.DurationMs,
			ErrorClass:            sendResult.ErrorClass,
		}
	}

	if _, err := CompleteDelivery(ctx, pool, claimed, result, cfg.Backoff); err != nil {
		return false, err
	}
	return true, nil
}
