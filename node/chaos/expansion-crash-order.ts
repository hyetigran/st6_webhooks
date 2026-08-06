// PRD §8 (R-8, R-11) / REVIEW.md F-1's `make chaos` half: "stall/kill E1's
// expansion, publish E2, assert D(E2) is never delivered before D(E1)."
// (The `make properties` half — racing several concurrent-but-healthy
// expansion workers — is expansionOrder.property.test.ts; this scenario is
// specifically about a worker that *dies* mid-expansion, not just races.)
//
// docs/adr/0001's whole claim is that expansion needs no lease/reaper
// because pg_try_advisory_xact_lock releases automatically on transaction
// abort — including a killed connection, not just an explicit ROLLBACK.
// This scenario proves that specific claim: a real process is SIGKILLed
// while holding a tenant's expansion lock, having done zero work, and
// verifies a subsequent real expansion still processes strictly in seq
// order — the crashed holder leaves no stale lock and no corrupted state.
import { spawn } from "node:child_process";
import { setupChaosDatabase, createTenant, createEndpoint, killWorker, assertChaos, runScenario, CHAOS_DATABASE_URL } from "./harness.js";
import { nodeDir, tsxNodeArgs } from "../scripts/scenarioHarness.js";
import { runExpansionCycle } from "../src/worker/expansion.js";

async function publishPendingExpansion(pool: Awaited<ReturnType<typeof setupChaosDatabase>>, tenantId: string): Promise<string> {
  const { rows } = await pool.query<{ id: string }>(
    `INSERT INTO events (tenant_id, idempotency_key, type, payload) VALUES ($1, $2, 'order.created', '{}') RETURNING id`,
    [tenantId, `expansion-crash-order-${crypto.randomUUID()}`],
  );
  return rows[0]!.id;
}

await runScenario("expansion-crash-order", async () => {
  const pool = await setupChaosDatabase();

  const { id: tenantId } = await createTenant(pool);
  const endpoint = await createEndpoint(pool, tenantId, ["order.created"]);

  const e1 = await publishPendingExpansion(pool, tenantId);
  const e2 = await publishPendingExpansion(pool, tenantId);

  // A real process acquires E1's tenant's expansion advisory lock and does
  // nothing else — simulating a worker that crashed the instant after
  // claiming, before it could insert a single delivery row or commit.
  const holder = spawn(process.execPath, [...tsxNodeArgs, "chaos/expansion-holder.ts", tenantId], {
    cwd: nodeDir,
    env: { ...process.env, DATABASE_URL: CHAOS_DATABASE_URL },
    stdio: ["ignore", "pipe", "pipe"],
  });

  const holderReady = new Promise<void>((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error("expansion-holder did not report LOCK_ACQUIRED within 5s")), 5_000);
    holder.stdout?.on("data", (chunk: Buffer) => {
      if (chunk.toString().includes("LOCK_ACQUIRED")) {
        clearTimeout(timeout);
        resolve();
      }
    });
  });

  try {
    await holderReady;
    // Confirm the lock is genuinely held before "crashing" it — a real
    // pg_try_advisory_xact_lock from this process for the same tenant must
    // fail while the holder is alive.
    const contested = await pool.query<{ locked: boolean }>("SELECT pg_try_advisory_xact_lock(hashtext($1)::bigint) AS locked", [
      tenantId,
    ]);
    assertChaos(contested.rows[0]!.locked === false, "expected the tenant's expansion lock to be genuinely held by the holder process");
    await pool.query("SELECT pg_advisory_unlock(hashtext($1)::bigint)", [tenantId]); // release our own contest-check attempt

    await killWorker(holder, "SIGKILL");

    // Now run real expansion — the crashed holder's transaction (and its
    // lock) must be gone, and expansion must proceed strictly in seq order.
    const firstExpanded = await runExpansionCycle(pool);
    assertChaos(firstExpanded, "expected the first real expansion cycle after the crash to find and expand an event");
    const secondExpanded = await runExpansionCycle(pool);
    assertChaos(secondExpanded, "expected the second real expansion cycle to expand the remaining event");

    const { rows: deliveries } = await pool.query<{ event_id: string }>(
      "SELECT event_id FROM deliveries WHERE endpoint_id = $1 ORDER BY seq",
      [endpoint.id],
    );
    assertChaos(
      deliveries.map((d) => d.event_id).join(",") === [e1, e2].join(","),
      `expected deliveries in publish order [E1, E2], got [${deliveries.map((d) => d.event_id).join(", ")}]`,
    );

    return { expandedOrder: deliveries.map((d) => d.event_id) };
  } finally {
    await killWorker(holder, "SIGKILL");
    await pool.end();
  }
});
