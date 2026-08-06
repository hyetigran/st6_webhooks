import { pool } from "./db/pool.js";
import { runExpansionCycle } from "./worker/expansion.js";
import { worker as workerConfig } from "./config.js";

// Shared worker pool per DECISIONS.md — this loop currently only runs
// expansion (ticket #17). The delivery-claim loop (ticket #18) joins this
// same process, not a separate one, per the "one worker pool" decision.
async function loop(): Promise<void> {
  for (;;) {
    let didWork = false;
    try {
      didWork = await runExpansionCycle(pool);
    } catch (err) {
      console.error("expansion cycle failed:", err);
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
