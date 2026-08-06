// PRD §8 (R-11, R-12): "Fail a partition head; followers report Blocked,
// then drain in order on recovery."
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
import { HEAD_DELIVERY_SELECT, computeBlockedOnDeliveryId } from "../src/lib/deliveryQueries.js";

await runScenario("partition-head-blocked-then-drains", async () => {
  const pool = await setupChaosDatabase();

  // The very first request (E1's first attempt) fails; every request after
  // that succeeds — so E1 recovers on its own next backoff retry.
  let requestCount = 0;
  const server = createServer((_req, res) => {
    requestCount += 1;
    if (requestCount === 1) {
      res.writeHead(500);
      res.end();
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

  // Three deliveries on the same endpoint, in publish order — E1 is the
  // head; E2/E3 are the followers that must report Blocked while it's
  // unresolved.
  const e1 = await createPendingDelivery(pool, tenantId, endpoint.id, { payload: { seq: 1 } });
  const e2 = await createPendingDelivery(pool, tenantId, endpoint.id, { payload: { seq: 2 } });
  const e3 = await createPendingDelivery(pool, tenantId, endpoint.id, { payload: { seq: 3 } });

  // A longer backoff after E1's failure — enough for the poll loop below to
  // reliably observe the "E1 failed, awaiting retry" window and check
  // blocked_on_delivery_id live, instead of racing a retry that (with the
  // harness's normal fast test defaults) could otherwise fire within a
  // single poll interval.
  const worker = spawnWorker({ env: { BACKOFF_BASE_DELAY_MS: "2000", BACKOFF_MAX_DELAY_MS: "3000" } });
  try {
    // E1 has failed its first attempt and is scheduled for retry (still the
    // head — a pending delivery being retried is still unresolved), but
    // hasn't succeeded yet.
    await waitUntil(
      async () => {
        const { rows } = await pool.query<{ state: string; attempt_count: number }>(
          "SELECT state, attempt_count FROM deliveries WHERE id = $1",
          [e1.id],
        );
        return rows[0]!.state === "pending" && rows[0]!.attempt_count >= 1;
      },
      { timeoutMs: 5_000, label: "E1 fails its first attempt and is scheduled for retry" },
    );

    const { rows: blockedCheck } = await pool.query<{ id: string; state: string; head_delivery_id: string | null }>(
      `SELECT d.id, d.state, ${HEAD_DELIVERY_SELECT} FROM deliveries d WHERE d.id = ANY($1) ORDER BY d.seq`,
      [[e2.id, e3.id]],
    );
    for (const row of blockedCheck) {
      assertChaos(
        computeBlockedOnDeliveryId(row) === e1.id,
        `expected delivery ${row.id} to report blocked_on_delivery_id = E1 (${e1.id}) while E1 is unresolved, got ${computeBlockedOnDeliveryId(row)}`,
      );
      assertChaos(row.state === "pending", `expected follower ${row.id} to still be pending (never dequeued while blocked)`);
    }

    // Recovery: E1 eventually succeeds on retry, then E2 and E3 drain.
    await waitUntil(
      async () => {
        const { rows } = await pool.query<{ state: string }>(
          "SELECT state FROM deliveries WHERE id = ANY($1)",
          [[e1.id, e2.id, e3.id]],
        );
        return rows.every((r) => r.state === "succeeded");
      },
      { timeoutMs: 10_000, label: "E1, E2, and E3 all drain to succeeded after recovery" },
    );

    const { rows: sentOrder } = await pool.query<{ id: string; sent_at: Date }>(
      `SELECT d.id, a.sent_at FROM deliveries d
       JOIN attempts a ON a.delivery_id = d.id AND a.response_status = 200
       WHERE d.id = ANY($1)
       ORDER BY a.sent_at`,
      [[e1.id, e2.id, e3.id]],
    );
    assertChaos(
      sentOrder.map((r) => r.id).join(",") === [e1.id, e2.id, e3.id].join(","),
      `expected the successful attempts to land in publish order E1,E2,E3, got ${sentOrder.map((r) => r.id).join(",")}`,
    );

    return { drainOrder: sentOrder.map((r) => r.id) };
  } finally {
    await killWorker(worker, "SIGKILL");
    server.closeAllConnections();
    await new Promise<void>((resolve) => server.close(() => resolve()));
    await pool.end();
  }
});
