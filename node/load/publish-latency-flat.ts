// PRD §8 (R-8): "Publish latency flat from 10 to 10,000 subscribers;
// expansion completes." POST /events only ever inserts one events row — no
// fan-out at publish time — so latency shouldn't grow with subscriber
// count; expansion (fan-out) happens later, asynchronously, in the worker.
import {
  setupLoadDatabase,
  createTenant,
  createEndpointsBulk,
  spawnApiServer,
  waitForServer,
  killProcess,
  percentile,
  assertLoad,
  runScenario,
} from "./harness.js";

const SUBSCRIBER_LEVELS = [10, 100, 1_000, 10_000];
const REQUESTS_PER_LEVEL = 30;
const PORT = 4100;
const BASE_URL = `http://127.0.0.1:${PORT}`;

await runScenario("publish-latency-flat", async () => {
  const pool = await setupLoadDatabase();
  const { id: tenantId, apiKey } = await createTenant(pool);

  const server = spawnApiServer(PORT);
  try {
    await waitForServer(BASE_URL);

    const resultsByLevel: Record<number, { p50: number; p99: number }> = {};
    let cumulativeEndpoints = 0;

    for (const level of SUBSCRIBER_LEVELS) {
      const toAdd = level - cumulativeEndpoints;
      if (toAdd > 0) {
        await createEndpointsBulk(pool, tenantId, toAdd);
        cumulativeEndpoints = level;
      }

      const latenciesMs: number[] = [];
      for (let i = 0; i < REQUESTS_PER_LEVEL; i++) {
        const startedAt = performance.now();
        const res = await fetch(`${BASE_URL}/events`, {
          method: "POST",
          headers: {
            "content-type": "application/json",
            authorization: `Bearer ${apiKey}`,
            "idempotency-key": `publish-latency-${level}-${i}`,
          },
          body: JSON.stringify({ type: "order.created", payload: { i } }),
        });
        const elapsedMs = performance.now() - startedAt;
        assertLoad(res.status === 202, `expected 202 from POST /events at subscriber level ${level}, got ${res.status}`);
        latenciesMs.push(elapsedMs);
      }

      latenciesMs.sort((a, b) => a - b);
      resultsByLevel[level] = { p50: percentile(latenciesMs, 50), p99: percentile(latenciesMs, 99) };
    }

    const baseline = resultsByLevel[SUBSCRIBER_LEVELS[0]!]!;
    const largest = resultsByLevel[SUBSCRIBER_LEVELS[SUBSCRIBER_LEVELS.length - 1]!]!;
    // Generous multiple, not an exact match — this is asserting "doesn't
    // scale with subscriber count," not "is bit-for-bit identical," since
    // real scheduling/GC jitter means run-to-run noise exists regardless.
    assertLoad(
      largest.p99 <= baseline.p99 * 5 + 50,
      `expected p99 publish latency at ${SUBSCRIBER_LEVELS.at(-1)} subscribers (${largest.p99.toFixed(1)}ms) to stay roughly flat versus ${SUBSCRIBER_LEVELS[0]} subscribers (${baseline.p99.toFixed(1)}ms)`,
    );

    return { resultsByLevel };
  } finally {
    await killProcess(server);
    await pool.end();
  }
});
