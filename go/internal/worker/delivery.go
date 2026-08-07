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
		// Reclaiming a stale lease: the previous worker's attempt row
		// (sent_at set, no response fields yet) must not strand the
		// delivery.
		if _, err := pool.Exec(ctx,
			`UPDATE attempts SET error_class = 'worker_lease_expired'
			 WHERE delivery_id = (SELECT id FROM deliveries WHERE endpoint_id = $1 AND state = 'in_flight')
			   AND sent_at IS NOT NULL AND response_status IS NULL AND error_class IS NULL`,
			candidate.ID,
		); err != nil {
			return nil, err
		}
		if _, err := pool.Exec(ctx,
			"UPDATE deliveries SET state = 'pending' WHERE endpoint_id = $1 AND state = 'in_flight'",
			candidate.ID,
		); err != nil {
			return nil, err
		}
	}

	var (
		deliveryID               string
		eventID                  string
		attemptCount             int
		nextAttemptAt            time.Time
		eventType                string
		payload                  json.RawMessage
		endpointURL              string
		signingSecretEnc         string
		secondarySecretEnc       *string
		secondarySecretExpiresAt *time.Time
	)
	// R-11: strict per-endpoint order means the *true* head — lowest seq,
	// period — is always what's next, never a later delivery that merely
	// happens to be immediately eligible. Filtering on next_attempt_at here
	// (as an earlier version of this query did) would let a later delivery
	// jump ahead of a head that's still cooling down from a prior failure's
	// backoff. So: fetch the head unconditionally, then decide eligibility
	// in application code below.
	err = pool.QueryRow(ctx,
		`SELECT d.id, d.event_id, d.attempt_count, d.next_attempt_at, e.type AS event_type, e.payload,
		        ep.url, ep.signing_secret, ep.secondary_secret, ep.secondary_secret_expires_at
		 FROM deliveries d
		 JOIN events e ON e.id = d.event_id
		 JOIN endpoints ep ON ep.id = d.endpoint_id
		 WHERE d.endpoint_id = $1 AND d.state = 'pending'
		 ORDER BY d.seq
		 LIMIT 1`,
		candidate.ID,
	).Scan(&deliveryID, &eventID, &attemptCount, &nextAttemptAt, &eventType, &payload,
		&endpointURL, &signingSecretEnc, &secondarySecretEnc, &secondarySecretExpiresAt)

	noneFound := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !noneFound {
		return nil, err
	}
	if noneFound || nextAttemptAt.After(time.Now()) {
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

	attemptNumber := attemptCount + 1
	var attemptID string
	if err := pool.QueryRow(ctx,
		"INSERT INTO attempts (delivery_id, attempt_number, sent_at) VALUES ($1, $2, now()) RETURNING id",
		deliveryID, attemptNumber,
	).Scan(&attemptID); err != nil {
		return nil, err
	}
	if _, err := pool.Exec(ctx,
		"UPDATE deliveries SET state = 'in_flight', attempt_count = $2 WHERE id = $1",
		deliveryID, attemptNumber,
	); err != nil {
		return nil, err
	}

	signingSecret, err := crypto.DecryptSecret(signingSecretEnc, secretEncryptionKey)
	if err != nil {
		return nil, err
	}

	var secondarySecret string
	hasActiveSecondary := secondarySecretEnc != nil && secondarySecretExpiresAt != nil && secondarySecretExpiresAt.After(time.Now())
	if hasActiveSecondary {
		secondarySecret, err = crypto.DecryptSecret(*secondarySecretEnc, secretEncryptionKey)
		if err != nil {
			return nil, err
		}
	}

	return &ClaimedDelivery{
		EndpointID:      candidate.ID,
		TenantID:        candidate.TenantID,
		LeaseID:         leaseID,
		DeliveryID:      deliveryID,
		EventID:         eventID,
		EventType:       eventType,
		Payload:         payload,
		AttemptNumber:   attemptNumber,
		AttemptID:       attemptID,
		URL:             endpointURL,
		SigningSecret:   signingSecret,
		SecondarySecret: secondarySecret,
	}, nil
}

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
		return CompleteOutcomeLeaseLost, tx.Rollback(ctx)
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

type OutboundConfig struct {
	ConnectTimeoutMs     int
	TotalTimeoutMs       int
	MaxResponseBodyBytes int
}

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
