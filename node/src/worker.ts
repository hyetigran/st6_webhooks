import http from "node:http";
import https from "node:https";
import { pool } from "./db/pool.js";
import { runExpansionCycle } from "./worker/expansion.js";
import { runDeliveryCycle } from "./worker/delivery.js";
import { worker as workerConfig, outboundHttp } from "./config.js";

// Shared per-host connection limit (R-16): http.Agent/https.Agent's
// maxSockets already caps concurrent sockets per host:port, so one shared
// agent pair for the process's lifetime is all this needs.
const agents = {
  http: new http.Agent({ maxSockets: outboundHttp.maxConnectionsPerHost }),
  https: new https.Agent({ maxSockets: outboundHttp.maxConnectionsPerHost }),
};

// Shared worker pool per DECISIONS.md: one process runs both expansion
// (ticket #17) and delivery (ticket #18), not two separate loops — each
// cycle tries both mechanisms, and the poll interval only kicks in when
// neither found work, so a backlog in either drains immediately.
async function loop(): Promise<void> {
  for (;;) {
    let didWork = false;
    try {
      didWork = (await runExpansionCycle(pool)) || didWork;
    } catch (err) {
      console.error("expansion cycle failed:", err);
    }
    try {
      didWork = (await runDeliveryCycle(pool, { agents })) || didWork;
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
