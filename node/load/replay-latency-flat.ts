// PRD §8 (R-8, R-19, R-22): "Large-window replay leaves replay-API latency
// flat (async expansion, not synchronous)." POST /endpoints/:id/replays
// only ever inserts one replays row — the window scan and bulk delivery
// insert happen later, in the worker — so latency shouldn't grow with how
// much history the window covers.
import {
  setupLoadDatabase,
  createTenant,
  createEndpointsBulk,
  createTerminalDeliveriesBulk,
  spawnApiServer,
  waitForServer,
  killProcess,
  percentile,
  assertLoad,
  runScenario,
} from "./harness.js";

const WINDOW_SIZES = [100, 1_000, 10_000];
const REQUESTS_PER_LEVEL = 15;
const PORT = 4101;
const BASE_URL = `http://127.0.0.1:${PORT}`;

await runScenario("replay-latency-flat", async () => {
  const pool = await setupLoadDatabase();
  const { id: tenantId, apiKey } = await createTenant(pool);
  await createEndpointsBulk(pool, tenantId, 1);
  const { rows: endpointRows } = await pool.query<{ id: string }>("SELECT id FROM endpoints LIMIT 1");
  const endpointId = endpointRows[0]!.id;

  const server = spawnApiServer(PORT);
  try {
    await waitForServer(BASE_URL);

    const resultsByWindow: Record<number, { p50: number; p99: number }> = {};
    let cumulativeHistory = 0;

    for (const windowSize of WINDOW_SIZES) {
      const toAdd = windowSize - cumulativeHistory;
      if (toAdd > 0) {
        await createTerminalDeliveriesBulk(pool, tenantId, endpointId, toAdd);
        cumulativeHistory = windowSize;
      }

      const latenciesMs: number[] = [];
      for (let i = 0; i < REQUESTS_PER_LEVEL; i++) {
        const startedAt = performance.now();
        const res = await fetch(`${BASE_URL}/endpoints/${endpointId}/replays`, {
          method: "POST",
          headers: {
            "content-type": "application/json",
            authorization: `Bearer ${apiKey}`,
            "idempotency-key": `replay-latency-${windowSize}-${i}`,
          },
          body: JSON.stringify({ range_start: "2020-01-01T00:00:00.000Z", range_end: "2030-01-01T00:00:00.000Z" }),
        });
        const elapsedMs = performance.now() - startedAt;
        assertLoad(res.status === 202, `expected 202 from POST /endpoints/:id/replays at window size ${windowSize}, got ${res.status}`);
        latenciesMs.push(elapsedMs);
      }

      latenciesMs.sort((a, b) => a - b);
      resultsByWindow[windowSize] = { p50: percentile(latenciesMs, 50), p99: percentile(latenciesMs, 99) };
    }

    const baseline = resultsByWindow[WINDOW_SIZES[0]!]!;
    const largest = resultsByWindow[WINDOW_SIZES[WINDOW_SIZES.length - 1]!]!;
    assertLoad(
      largest.p99 <= baseline.p99 * 5 + 50,
      `expected p99 replay latency at window size ${WINDOW_SIZES.at(-1)} (${largest.p99.toFixed(1)}ms) to stay roughly flat versus window size ${WINDOW_SIZES[0]} (${baseline.p99.toFixed(1)}ms)`,
    );

    return { resultsByWindow };
  } finally {
    await killProcess(server);
    await pool.end();
  }
});
