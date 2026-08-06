// PRD §8 (R-15): "Crash after a successful send; event ID identical across
// both attempts, one terminal state."
//
// Reuses the SIGSTOP mechanic from worker-stall-fencing.ts to reliably
// create a genuine "the receiver actually responded 2xx, the worker just
// never got to process/write it back" window (rather than racing a
// microsecond gap between response-received and write-back in a live
// process) — then kills outright instead of resuming, so the dying
// worker's send truly never gets written anywhere.
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

await runScenario("crash-after-successful-send", async () => {
  const pool = await setupChaosDatabase();

  const receivedEventIds: string[] = [];
  let requestCount = 0;
  const server = createServer((req, res) => {
    requestCount += 1;
    receivedEventIds.push(req.headers["webhook-event-id"] as string);
    const respond = (): void => {
      res.writeHead(200);
      res.end();
    };
    // Delayed only enough to reliably observe in_flight before it would
    // otherwise complete within a single poll interval; the delay elapses
    // (and the response is genuinely sent) while worker A is stopped below.
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
    await killWorker(workerA, "SIGSTOP");

    // Give the receiver's delayed response time to actually go out — this
    // is the "successful send" itself, from the receiver's point of view,
    // even though the frozen worker A can never act on it.
    await waitUntil(async () => receivedEventIds.length >= 1, { timeoutMs: 5_000, label: "the receiver sends A's response" });

    // A dies outright — unlike worker-stall-fencing.ts, it never gets a
    // chance to write anything back at all.
    await killWorker(workerA, "SIGKILL");

    workerB = spawnWorker();
    await waitUntil(
      async () => {
        const { rows } = await pool.query<{ state: string }>("SELECT state FROM deliveries WHERE id = $1", [delivery.id]);
        return rows[0]!.state === "succeeded";
      },
      { timeoutMs: 10_000, label: "worker B reclaims and completes the delivery" },
    );

    assertChaos(
      receivedEventIds.length === 2,
      `expected the receiver to have been hit exactly twice (A's send, B's retry), got ${receivedEventIds.length}`,
    );
    assertChaos(
      receivedEventIds[0] === receivedEventIds[1],
      `expected the same Webhook-Event-Id across both attempts, got ${receivedEventIds[0]} then ${receivedEventIds[1]}`,
    );

    const { rows: attempts } = await pool.query<{ error_class: string | null; response_status: number | null }>(
      "SELECT error_class, response_status FROM attempts WHERE delivery_id = $1 ORDER BY attempt_number",
      [delivery.id],
    );
    assertChaos(
      attempts[0]!.error_class === "worker_lease_expired",
      `expected A's orphaned attempt closed with worker_lease_expired, got ${attempts[0]!.error_class}`,
    );
    const attemptsWithResponses = attempts.filter((a) => a.response_status !== null);
    assertChaos(
      attemptsWithResponses.length === 1,
      `expected exactly one recorded terminal outcome despite two real sends, got ${attemptsWithResponses.length}`,
    );

    const { rows: deliveryRows } = await pool.query<{ state: string }>("SELECT state FROM deliveries WHERE id = $1", [delivery.id]);
    assertChaos(deliveryRows[0]!.state === "succeeded", "delivery must end in exactly one terminal state: succeeded");

    return { receivedEventIds, finalState: deliveryRows[0]!.state };
  } finally {
    await killWorker(workerA, "SIGKILL");
    if (workerB) await killWorker(workerB, "SIGKILL");
    server.closeAllConnections();
    await new Promise<void>((resolve) => server.close(() => resolve()));
    await pool.end();
  }
});
