// One-off dev-data seeding command — NOT part of `make verify`/test/chaos/
// load, never shipped. Populates the REAL local dev database (whatever
// DATABASE_URL .env points at — the same one `go run ./cmd/api`/
// `go run ./cmd/worker` use) with a large, varied volume of endpoints/
// events/deliveries so the console dashboard has something interesting to
// show instead of near-empty demo data. Mirrors
// node/scripts/seedHeavyTraffic.ts exactly — same endpoint mix, same event
// streams, same reasoning; see that file's header comment for the full
// rationale (real pipeline end to end, local-receiver SSRF bypass via the
// same seam cmd/chaosworker already established, why the real dev worker
// needs to be stopped first).
//
// The one structural difference from the Node version: Go's real
// concurrency doesn't need separate OS processes for parallelism the way
// Node's single-threaded workers do — this runs the permissive delivery
// loop as goroutines directly, sharing one pgxpool.Pool, the same shape
// cmd/worker's own WORKER_POOL_SIZE goroutines already use.
//
// Usage: go run ./cmd/seedheavytraffic [apiKey]
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"webhooks-go/internal/config"
	"webhooks-go/internal/crypto"
	"webhooks-go/internal/db"
	"webhooks-go/internal/worker"

	"github.com/jackc/pgx/v5/pgxpool"
)

const demoAPIKey = "tenant_efc3135881cbe7d93fa828c4dc0219558d0440e1fe48590c"
const receiverPort = 4901
const workerGoroutineCount = 6

// Bounded, not "until fully drained" — see waitForExpansionDone's comment.
const deliveryWindow = 8 * time.Minute

type endpointProfile struct {
	key        string
	path       string
	eventTypes []string
	paused     bool
}

// Same mix as the Node version: five endpoints that just work (the bulk of
// the volume), one a bit slower, one flaky-then-succeeds probabilistically,
// one that deterministically needs exactly 3 attempts (via the real
// Webhook-Attempt header the worker already sends), one that always fails
// (halts after its first delivery exhausts attempts — R-11 head-of-line
// ordering then freezes every delivery queued behind it), and one paused
// outright.
var profiles = []endpointProfile{
	{key: "billing", path: "/hooks/billing", eventTypes: []string{"order.placed", "invoice.paid"}},
	{key: "inventory", path: "/hooks/inventory", eventTypes: []string{"order.placed"}},
	{key: "shipping", path: "/hooks/shipping", eventTypes: []string{"order.placed", "invoice.paid"}},
	{key: "notifications", path: "/hooks/notifications", eventTypes: []string{"order.placed"}},
	{key: "crm", path: "/hooks/crm", eventTypes: []string{"crm.contact-synced"}},
	{key: "analytics", path: "/hooks/analytics", eventTypes: []string{"analytics.pageview-batch"}},
	{key: "fraud", path: "/hooks/fraud", eventTypes: []string{"fraud.review-requested"}},
	{key: "legacy", path: "/hooks/legacy", eventTypes: []string{"order.placed"}},
	{key: "reporting", path: "/hooks/reporting", eventTypes: []string{"reports.nightly-summary"}},
	{key: "partner", path: "/hooks/partner", eventTypes: []string{"order.placed"}, paused: true},
}

type eventStream struct {
	eventType string
	count     int
	payload   func(i int) map[string]any
}

// order.placed (not order.created) deliberately avoids whatever event type
// any pre-existing endpoint in this tenant might already subscribe to — the
// Node version's smoke test found a real pre-existing httpbin.org endpoint
// subscribed to "order.created" and nearly fanned 17k real external
// requests into it.
var streams = []eventStream{
	{
		eventType: "order.placed", count: 17_000,
		payload: func(i int) map[string]any { return map[string]any{"order_id": fmt.Sprintf("ord_%d", i), "amount_cents": 500 + i%9000, "currency": "USD"} },
	},
	{
		eventType: "invoice.paid", count: 3_000,
		payload: func(i int) map[string]any {
			return map[string]any{"invoice_id": fmt.Sprintf("inv_%d", i), "amount_cents": 1000 + i%20000, "currency": "USD"}
		},
	},
	{
		eventType: "crm.contact-synced", count: 800,
		payload: func(i int) map[string]any { return map[string]any{"contact_id": fmt.Sprintf("cnt_%d", i), "source": "webform"} },
	},
	{
		eventType: "analytics.pageview-batch", count: 600,
		payload: func(i int) map[string]any { return map[string]any{"batch_id": fmt.Sprintf("batch_%d", i), "pageviews": 40 + i%200} },
	},
	{
		eventType: "fraud.review-requested", count: 600,
		payload: func(i int) map[string]any { return map[string]any{"review_id": fmt.Sprintf("rev_%d", i), "risk_score": float64(i%100) / 100} },
	},
	{
		eventType: "reports.nightly-summary", count: 40,
		payload: func(i int) map[string]any { return map[string]any{"report_id": fmt.Sprintf("rpt_%d", i), "rows": 1000 + i*37} },
	},
}

func logMsg(format string, args ...any) {
	fmt.Printf("[seed] %s %s\n", time.Now().UTC().Format("15:04:05"), fmt.Sprintf(format, args...))
}

func findOrCreateTenant(ctx context.Context, pool *pgxpool.Pool, apiKey string) (string, error) {
	hash := crypto.HashAPIKey(apiKey)
	var id string
	err := pool.QueryRow(ctx, "SELECT id FROM tenants WHERE api_key_hash = $1", hash).Scan(&id)
	if err == nil {
		return id, nil
	}
	// Unlike the Node dev DB (already had a "demo" tenant matching the
	// browser's stored key from earlier sessions), Go's dev DB never has —
	// same api_key_hash so the frontend's single stored key authenticates
	// against both backends when switching the backend selector.
	err = pool.QueryRow(ctx, "INSERT INTO tenants (name, api_key_hash) VALUES ($1, $2) RETURNING id", "demo", hash).Scan(&id)
	return id, err
}

func insertEndpoints(ctx context.Context, pool *pgxpool.Pool, tenantID string, secretEncryptionKey []byte) (map[string]string, error) {
	encryptedSecret, err := crypto.EncryptSecret("whsec_seed_heavy_traffic", secretEncryptionKey)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]string, len(profiles))
	for _, p := range profiles {
		url := fmt.Sprintf("http://127.0.0.1:%d%s", receiverPort, p.path)
		status := "active"
		if p.paused {
			status = "paused"
		}
		var id string
		err := pool.QueryRow(ctx,
			`INSERT INTO endpoints (tenant_id, url, event_types, signing_secret, status)
			 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			tenantID, url, p.eventTypes, encryptedSecret, status,
		).Scan(&id)
		if err != nil {
			return nil, err
		}
		ids[p.key] = id
		suffix := ""
		if p.paused {
			suffix = " (paused)"
		}
		logMsg("registered %s -> %s%s", p.key, url, suffix)
	}
	return ids, nil
}

func insertEventStream(ctx context.Context, pool *pgxpool.Pool, tenantID string, s eventStream) error {
	const batchSize = 1000
	keyPrefix := "seed-" + s.eventType
	for start := 0; start < s.count; start += batchSize {
		n := batchSize
		if start+n > s.count {
			n = s.count - start
		}
		values := make([]string, 0, n)
		params := make([]any, 0, n*4)
		for i := start; i < start+n; i++ {
			payload, err := json.Marshal(s.payload(i))
			if err != nil {
				return err
			}
			params = append(params, tenantID, fmt.Sprintf("%s-%d", keyPrefix, i), s.eventType, payload)
			base := len(params) - 4
			values = append(values, fmt.Sprintf("($%d, $%d, $%d, $%d)", base+1, base+2, base+3, base+4))
		}
		query := "INSERT INTO events (tenant_id, idempotency_key, type, payload) VALUES " + joinStrings(values, ",")
		if _, err := pool.Exec(ctx, query, params...); err != nil {
			return err
		}
	}
	logMsg("published %d × %s", s.count, s.eventType)
	return nil
}

func joinStrings(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

// startReceiver mirrors the Node version's per-path behavior exactly —
// same endpoint keys, same success/failure shape — using the real
// Webhook-Attempt header (internal/worker/delivery.go) for fraud's
// deterministic "needs exactly 3 attempts" behavior.
func startReceiver() *http.Server {
	mux := http.NewServeMux()
	respond := func(w http.ResponseWriter, status int) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte("{}"))
	}
	slow := func(w http.ResponseWriter, minMs, jitterMs int) {
		time.Sleep(time.Duration(minMs+rand.Intn(jitterMs)) * time.Millisecond)
		respond(w, 200)
	}

	mux.HandleFunc("/hooks/legacy", func(w http.ResponseWriter, r *http.Request) { respond(w, 500) })
	mux.HandleFunc("/hooks/fraud", func(w http.ResponseWriter, r *http.Request) {
		attempt, _ := strconv.Atoi(r.Header.Get("Webhook-Attempt"))
		if attempt < 3 {
			respond(w, 503)
			return
		}
		respond(w, 200)
	})
	mux.HandleFunc("/hooks/analytics", func(w http.ResponseWriter, r *http.Request) {
		if rand.Float64() < 0.2 {
			respond(w, 503)
			return
		}
		respond(w, 200)
	})
	mux.HandleFunc("/hooks/reporting", func(w http.ResponseWriter, r *http.Request) { slow(w, 2000, 1500) })
	mux.HandleFunc("/hooks/crm", func(w http.ResponseWriter, r *http.Request) { slow(w, 100, 150) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { slow(w, 2, 8) })

	server := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", receiverPort), Handler: mux}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("receiver error: %v", err)
		}
	}()
	return server
}

// trustAnyAddress is the same permissive stub cmd/chaosworker uses — a
// local receiver fails the real, correct SSRF check by design, and this is
// the sanctioned bypass seam for exactly that (DeliveryCycleDeps), not a
// weakening of production code.
func trustAnyAddress(ctx context.Context, hostname string) worker.ResolveAndPinResult {
	return worker.ResolveAndPinResult{Allowed: true, IP: "127.0.0.1"}
}

func runPermissiveWorkerLoop(ctx context.Context, pool *pgxpool.Pool, deliveryConfig worker.DeliveryConfig, idle time.Duration, stop <-chan struct{}) {
	deps := worker.DeliveryCycleDeps{ResolveAndPinFn: trustAnyAddress}
	for {
		select {
		case <-stop:
			return
		default:
		}
		didWork := false
		if did, err := worker.RunExpansionCycle(ctx, pool); err != nil {
			log.Printf("expansion cycle failed: %v", err)
		} else {
			didWork = didWork || did
		}
		if did, err := worker.RunReplayExpansionCycle(ctx, pool); err != nil {
			log.Printf("replay expansion cycle failed: %v", err)
		} else {
			didWork = didWork || did
		}
		if did, err := worker.RunDeliveryCycle(ctx, pool, deliveryConfig, deps); err != nil {
			log.Printf("delivery cycle failed: %v", err)
		} else {
			didWork = didWork || did
		}
		if !didWork {
			select {
			case <-stop:
				return
			case <-time.After(idle):
			}
		}
	}
}

type progressCounts struct {
	pendingExpansion int
	claimablePending int
	inFlight         int
	succeeded        int
	failed           int
	total            int
}

func fetchProgress(ctx context.Context, pool *pgxpool.Pool, tenantID string) (progressCounts, error) {
	var p progressCounts
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM events WHERE tenant_id = $1 AND status = 'pending_expansion'`, tenantID,
	).Scan(&p.pendingExpansion); err != nil {
		return p, err
	}

	rows, err := pool.Query(ctx,
		`SELECT d.state, e.status AS endpoint_status, count(*)
		 FROM deliveries d
		 JOIN endpoints e ON e.id = d.endpoint_id
		 JOIN events ev ON ev.id = d.event_id
		 WHERE ev.tenant_id = $1
		 GROUP BY d.state, e.status`, tenantID)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var state, endpointStatus string
		var n int
		if err := rows.Scan(&state, &endpointStatus, &n); err != nil {
			return p, err
		}
		p.total += n
		switch {
		case state == "in_flight":
			p.inFlight += n
		case state == "succeeded":
			p.succeeded += n
		case state == "failed":
			p.failed += n
		case state == "pending" && endpointStatus == "active":
			p.claimablePending += n
		}
	}
	return p, rows.Err()
}

// Waits only for EXPANSION to finish — that's what actually creates the
// delivery rows (fast: one tenant-serialized cycle per event, ~a few ms
// each), which is what makes "100k+ deliveries" real. Draining every last
// one to a terminal state is a much slower, effectively unbounded tail
// (single-flight-per-endpoint means throughput is capped by the number of
// distinct active endpoints, not by however many goroutines exist —
// confirmed live: a full-scale run took ~29 minutes to fully drain, well
// past any reasonable background-process budget) — and isn't actually
// necessary: a dashboard meant to look like heavy traffic should show a
// visible backlog (pending/in-flight rows, real queue depth), not a queue
// that's already been fully drained by the time anyone looks at it.
func waitForExpansionDone(ctx context.Context, pool *pgxpool.Pool, tenantID string) error {
	const pollInterval = 3 * time.Second
	const maxWait = 5 * time.Minute
	startedAt := time.Now()
	for {
		p, err := fetchProgress(ctx, pool, tenantID)
		if err != nil {
			return err
		}
		logMsg("expansion progress: %d awaiting expansion, %d succeeded, %d failed, %d in flight, %d pending, %d total delivery rows",
			p.pendingExpansion, p.succeeded, p.failed, p.inFlight, p.claimablePending, p.total)
		if p.pendingExpansion == 0 {
			return nil
		}
		if time.Since(startedAt) > maxWait {
			logMsg("hit the %s expansion safety timeout with %d still unexpanded — proceeding anyway.", maxWait, p.pendingExpansion)
			return nil
		}
		time.Sleep(pollInterval)
	}
}

// Runs delivery for a fixed window rather than until fully drained — see
// waitForExpansionDone's comment for why a partial drain is the actual
// goal here, not a shortcut around one.
func runDeliveryWindow(ctx context.Context, pool *pgxpool.Pool, tenantID string, window time.Duration) error {
	const pollInterval = 5 * time.Second
	startedAt := time.Now()
	for {
		p, err := fetchProgress(ctx, pool, tenantID)
		if err != nil {
			return err
		}
		logMsg("delivery progress: %d succeeded, %d failed, %d in flight, %d pending (claimable), %d total delivery rows",
			p.succeeded, p.failed, p.inFlight, p.claimablePending, p.total)
		if p.claimablePending+p.inFlight == 0 {
			logMsg("fully drained already — no need to wait out the rest of the delivery window.")
			return nil
		}
		if time.Since(startedAt) > window {
			logMsg("delivery window (%s) elapsed with %d still outstanding — that backlog is staying, by design.", window, p.claimablePending+p.inFlight)
			return nil
		}
		time.Sleep(pollInterval)
	}
}

func main() {
	apiKey := demoAPIKey
	if len(os.Args) > 1 {
		apiKey = os.Args[1]
	}
	if os.Getenv("WORKER_IDLE_POLL_INTERVAL_MS") == "" {
		_ = os.Setenv("WORKER_IDLE_POLL_INTERVAL_MS", "20")
	}

	cfg := config.Load()
	secretEncryptionKey := config.SecretEncryptionKey()
	ctx := context.Background()

	pool, err := db.NewPool(ctx, cfg.DB.ConnectionString)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	tenantID, err := findOrCreateTenant(ctx, pool, apiKey)
	if err != nil {
		log.Fatal(err)
	}
	logMsg("seeding tenant %s", tenantID)

	receiver := startReceiver()
	logMsg("local receiver listening on http://127.0.0.1:%d", receiverPort)

	deliveryConfig := worker.DeliveryConfig{
		SecretEncryptionKey: secretEncryptionKey,
		LeaseDurationMs:     cfg.LeaseDurationMs(),
		Outbound: worker.OutboundConfig{
			ConnectTimeoutMs:     cfg.OutboundHTTP.ConnectTimeoutMs,
			TotalTimeoutMs:       cfg.OutboundHTTP.TotalTimeoutMs,
			MaxResponseBodyBytes: cfg.OutboundHTTP.MaxResponseBodyBytes,
		},
		Backoff: worker.BackoffConfig{
			BaseDelayMs: cfg.Backoff.BaseDelayMs,
			Multiplier:  cfg.Backoff.Multiplier,
			MaxDelayMs:  cfg.Backoff.MaxDelayMs,
			MaxAttempts: cfg.Backoff.MaxAttempts,
		},
	}
	idle := time.Duration(cfg.Worker.IdlePollIntervalMs) * time.Millisecond

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < workerGoroutineCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runPermissiveWorkerLoop(ctx, pool, deliveryConfig, idle, stop)
		}()
	}
	logMsg("started %d permissive delivery worker goroutines (local-receiver SSRF bypass, same pattern as make chaos/load)", workerGoroutineCount)

	if _, err := insertEndpoints(ctx, pool, tenantID, secretEncryptionKey); err != nil {
		log.Fatal(err)
	}

	totalEvents := 0
	for _, s := range streams {
		if err := insertEventStream(ctx, pool, tenantID, s); err != nil {
			log.Fatal(err)
		}
		totalEvents += s.count
	}
	logMsg("published %d events total — expansion now running for real.", totalEvents)

	if err := waitForExpansionDone(ctx, pool, tenantID); err != nil {
		log.Fatal(err)
	}
	logMsg("expansion done — running delivery for up to %s (not to full drain, see comment above).", deliveryWindow)
	if err := runDeliveryWindow(ctx, pool, tenantID, deliveryWindow); err != nil {
		log.Fatal(err)
	}

	final, err := fetchProgress(ctx, pool, tenantID)
	if err != nil {
		log.Fatal(err)
	}
	logMsg("done. final: %d succeeded, %d failed, %d total delivery rows.", final.succeeded, final.failed, final.total)

	close(stop)
	wg.Wait()
	_ = receiver.Close()
	logMsg("stopped local receiver and seeding worker goroutines.")
}
