// REVIEW.md F-2 / docs/adr/0002 / PRD §8 (R-11, R-15, R-17): "SIGSTOP a
// worker mid-request past lease expiry, let another reclaim and complete,
// SIGCONT the first; its write is dropped, exactly one terminal state, no
// interval with two in-flight attempts."
//
// A dead worker (kill -9) never writes back — the simple kill-mid-delivery
// scenario covers that. This one is the harder case ADR-0002 exists for: a
// *stalled* (SIGSTOPped, not dead) worker whose outbound request may have
// already reached the receiver, but who hasn't written the outcome back
// yet. Because SIGSTOP freezes the process entirely (no timers, no event
// loop), the receiver's response just sits in the kernel socket buffer,
// unread, for as long as the stop lasts — exactly reproducing "the send
// reached the receiver before the lease was reclaimed."
import { createServer } from "node:http";
import {
  setupChaosDatabase,
  createTenant,
  createEndpoint,
  createPendingDelivery,
  spawnWorker,
  waitUntil,
  killWorker,
  assertChaos,
  runScenario,
} from "./harness.js";

await runScenario("worker-stall-fencing", async () => {
  const pool = await setupChaosDatabase();

  // The first request gets a small delay — just enough for the poll loop
  // below to observe in_flight and SIGSTOP A before the request would
  // otherwise complete on its own (without it, claim-send-respond-write-back
  // can all finish inside a single 50ms poll interval). Once frozen, A stays
  // frozen regardless of how much of that delay remains. Later requests
  // (B's, after reclaim) respond immediately.
  let requestCount = 0;
  const server = createServer((_req, res) => {
    requestCount += 1;
    const respond = (): void => {
      res.writeHead(200);
      res.end();
    };
    if (requestCount === 1) {
      setTimeout(respond, 500);
    } else {
      respond();
    }
  });
  const port = await new Promise<number>((resolve) => {
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      if (address === null || typeof address === "string") throw new Error("expected a bound TCP address");
      resolve(address.port);
    });
  });

  const { id: tenantId } = await createTenant(pool);
  const endpoint = await createEndpoint(pool, tenantId, ["order.created"], { url: `http://chaos-test.local:${port}/hook` });
  const delivery = await createPendingDelivery(pool, tenantId, endpoint.id);

  // Sequential, not concurrent, spawning — deterministically makes A the
  // claimer, rather than racing two fresh workers for who gets there first.
  const workerA = spawnWorker();
  let workerB: ReturnType<typeof spawnWorker> | undefined;
  try {
    await waitUntil(
      async () => {
        const { rows } = await pool.query<{ state: string }>("SELECT state FROM deliveries WHERE id = $1", [delivery.id]);
        return rows[0]!.state === "in_flight";
      },
      { timeoutMs: 5_000, label: "worker A claims the delivery (state -> in_flight)" },
    );

    const { rows: leaseRows } = await pool.query<{ lease_id: string }>("SELECT lease_id FROM endpoints WHERE id = $1", [endpoint.id]);
    const staleLeaseId = leaseRows[0]!.lease_id;
    // Freeze A before it can process the (already-sent) response.
    await killWorker(workerA, "SIGSTOP");

    workerB = spawnWorker();
    await waitUntil(
      async () => {
        const { rows } = await pool.query<{ state: string }>("SELECT state FROM deliveries WHERE id = $1", [delivery.id]);
        return rows[0]!.state === "succeeded";
      },
      { timeoutMs: 10_000, label: "worker B reclaims the stale lease and completes the delivery" },
    );

    const { rows: postReclaimAttempts } = await pool.query<{ error_class: string | null }>(
      "SELECT error_class FROM attempts WHERE delivery_id = $1 ORDER BY attempt_number",
      [delivery.id],
    );
    assertChaos(
      postReclaimAttempts[0]!.error_class === "worker_lease_expired",
      `expected A's orphaned attempt to be closed with worker_lease_expired before resuming A, got ${postReclaimAttempts[0]!.error_class}`,
    );

    // Now let A wake up. Its outbound request already completed (the
    // receiver responded immediately, back when A was still running) — A
    // will finally process that response and attempt to write back. Its
    // lease_id no longer matches the endpoint's current one (B's), so
    // ADR-0002 says this write must be silently dropped.
    await killWorker(workerA, "SIGCONT");
    // Give A's resumed write-back a real window to run (and, if the
    // fencing were broken, to corrupt state) before asserting anything.
    await new Promise((resolve) => setTimeout(resolve, 2_000));

    const { rows: finalEndpointRows } = await pool.query<{ lease_id: string | null; busy: boolean }>(
      "SELECT lease_id, busy FROM endpoints WHERE id = $1",
      [endpoint.id],
    );
    assertChaos(
      finalEndpointRows[0]!.lease_id !== staleLeaseId,
      "the endpoint's lease_id must not have been reset back to A's stale value by A's late write",
    );
    assertChaos(finalEndpointRows[0]!.busy === false, "the endpoint must not be left busy by A's dropped write");

    const { rows: finalAttempts } = await pool.query<{ error_class: string | null; response_status: number | null }>(
      "SELECT error_class, response_status FROM attempts WHERE delivery_id = $1 ORDER BY attempt_number",
      [delivery.id],
    );
    // Exactly one attempt ever recorded a real response — A's write was
    // fenced out entirely, not merely overwritten a second time.
    const attemptsWithResponses = finalAttempts.filter((a) => a.response_status !== null);
    assertChaos(
      attemptsWithResponses.length === 1,
      `expected exactly one attempt with a recorded response (B's), got ${attemptsWithResponses.length}`,
    );

    const { rows: deliveryRows } = await pool.query<{ state: string }>("SELECT state FROM deliveries WHERE id = $1", [delivery.id]);
    assertChaos(deliveryRows[0]!.state === "succeeded", "delivery must remain in exactly one terminal state: succeeded");

    return { attemptCount: finalAttempts.length, finalState: deliveryRows[0]!.state };
  } finally {
    await killWorker(workerA, "SIGKILL");
    if (workerB) await killWorker(workerB, "SIGKILL");
    server.closeAllConnections();
    await new Promise<void>((resolve) => server.close(() => resolve()));
    await pool.end();
  }
});
