// PRD §8 (R-7, R-17): "Kill a worker mid-delivery; lease expires, work
// resumes, no event lost."
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

await runScenario("worker-kill-mid-delivery", async () => {
  const pool = await setupChaosDatabase();

  // Only the *first* request is slow — long enough that worker A is
  // unmistakably still waiting on it when killed, comfortably past both the
  // poll interval used to detect in_flight and A's own (generous) outbound
  // timeout override below. Every request after that responds immediately,
  // so worker B's retry — using the harness's normal, short timeout, not
  // A's — can actually succeed instead of timing out against a receiver
  // that's permanently slower than any worker's patience.
  let requestCount = 0;
  const server = createServer((_req, res) => {
    requestCount += 1;
    if (requestCount === 1) {
      setTimeout(() => {
        res.writeHead(200);
        res.end();
      }, 4_000);
    } else {
      res.writeHead(200);
      res.end();
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

  // Worker A gets a generous outbound timeout so it's still genuinely
  // mid-request (not self-timed-out) at the moment it's killed. Worker B
  // uses the harness's small defaults, so its own lease-staleness check
  // (based on *its* config, not A's) only needs to wait out a couple of
  // seconds, not A's 4s+ window.
  const workerA = spawnWorker({ env: { OUTBOUND_TOTAL_TIMEOUT_MS: "8000", OUTBOUND_CONNECT_TIMEOUT_MS: "8000" } });
  try {
    await waitUntil(
      async () => {
        const { rows } = await pool.query<{ state: string }>("SELECT state FROM deliveries WHERE id = $1", [delivery.id]);
        return rows[0]!.state === "in_flight";
      },
      { timeoutMs: 5_000, label: "worker A claims the delivery (state -> in_flight)" },
    );

    await killWorker(workerA, "SIGKILL");

    const workerB = spawnWorker();
    try {
      await waitUntil(
        async () => {
          const { rows } = await pool.query<{ state: string }>("SELECT state FROM deliveries WHERE id = $1", [delivery.id]);
          return rows[0]!.state === "succeeded";
        },
        { timeoutMs: 15_000, label: "worker B reclaims after lease expiry and completes the delivery" },
      );
    } finally {
      await killWorker(workerB, "SIGKILL");
    }

    const { rows: attempts } = await pool.query<{ error_class: string | null; response_status: number | null }>(
      "SELECT error_class, response_status FROM attempts WHERE delivery_id = $1 ORDER BY attempt_number",
      [delivery.id],
    );
    assertChaos(attempts.length >= 2, `expected at least 2 attempts (killed A's orphan + B's successful one), got ${attempts.length}`);
    assertChaos(
      attempts[0]!.error_class === "worker_lease_expired",
      `expected the orphaned first attempt to be closed with worker_lease_expired, got ${attempts[0]!.error_class}`,
    );
    assertChaos(
      attempts[attempts.length - 1]!.response_status === 200,
      "expected the final attempt to have succeeded with a 200",
    );

    const { rows: deliveryRows } = await pool.query<{ state: string }>("SELECT state FROM deliveries WHERE id = $1", [delivery.id]);
    assertChaos(deliveryRows[0]!.state === "succeeded", "delivery must end in exactly one terminal state: succeeded");

    return { attemptCount: attempts.length, finalState: deliveryRows[0]!.state };
  } finally {
    // Force-close any lingering keep-alive sockets (e.g. from the killed
    // worker A's connection) — plain server.close() waits for existing
    // connections to end on their own, which a SIGKILLed peer never does.
    server.closeAllConnections();
    await new Promise<void>((resolve) => server.close(() => resolve()));
    await pool.end();
  }
});
