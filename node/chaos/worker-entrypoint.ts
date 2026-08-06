// A chaos-testing-only worker entrypoint. Structurally identical to
// src/worker.ts's real poll loop (same runExpansionCycle/runDeliveryCycle/
// runReplayExpansionCycle calls, same real process lifecycle — this is the
// process chaos scenarios kill/SIGSTOP/SIGCONT), except resolveAndPin is
// swapped for a permissive stub via the same DeliveryCycleDeps injection
// seam the vitest suite already uses (test/delivery.runDeliveryCycle.test.ts).
//
// Why this exists rather than reusing src/worker.ts directly: chaos
// scenarios need a receiver they control the timing/behavior of, which
// means a local HTTP server — and the real, production resolveAndPin
// correctly rejects loopback/private addresses (that's the SSRF defense
// working as designed, not a bug). Rather than weakening that check in
// production code for testing convenience, this is a separate, clearly-
// labeled entrypoint that never ships — src/worker.ts is untouched.
import { pool } from "../src/db/pool.js";
import { runExpansionCycle } from "../src/worker/expansion.js";
import { runDeliveryCycle } from "../src/worker/delivery.js";
import { runReplayExpansionCycle } from "../src/worker/replayExpansion.js";
import { worker as workerConfig } from "../src/config.js";

const trustAnyAddress = async (): Promise<{ allowed: true; ip: string }> => ({ allowed: true, ip: "127.0.0.1" });

async function loop(): Promise<void> {
  for (;;) {
    let didWork = false;
    try {
      didWork = (await runExpansionCycle(pool)) || didWork;
    } catch (err) {
      console.error("expansion cycle failed:", err);
    }
    try {
      didWork = (await runReplayExpansionCycle(pool)) || didWork;
    } catch (err) {
      console.error("replay expansion cycle failed:", err);
    }
    try {
      didWork = (await runDeliveryCycle(pool, { resolveAndPin: trustAnyAddress })) || didWork;
    } catch (err) {
      console.error("delivery cycle failed:", err);
    }
    if (!didWork) {
      await new Promise((resolve) => setTimeout(resolve, workerConfig.idlePollIntervalMs));
    }
  }
}

loop().catch((err) => {
  console.error(err);
  process.exit(1);
});
