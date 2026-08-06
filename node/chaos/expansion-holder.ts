// A standalone process that connects, opens a transaction, acquires a
// tenant's expansion advisory lock (the exact primitive
// src/worker/expansion.ts uses), prints a marker once held, then waits to
// be killed — a controllable stand-in for "a real expansion worker died
// mid-transaction, having done nothing beyond acquiring the lock." Used by
// chaos/expansion-crash-order.ts. Never commits, so getting SIGKILLed is
// the only way this process ends: exactly what a crashed worker looks like.
import pg from "pg";

const tenantId = process.argv[2];
if (!tenantId) {
  console.error("usage: expansion-holder.ts <tenantId>");
  process.exit(1);
}

const client = new pg.Client({ connectionString: process.env.DATABASE_URL });
await client.connect();
await client.query("BEGIN");
const { rows } = await client.query<{ locked: boolean }>("SELECT pg_try_advisory_xact_lock(hashtext($1)::bigint) AS locked", [
  tenantId,
]);
if (!rows[0]!.locked) {
  console.error("could not acquire the advisory lock");
  process.exit(1);
}
console.log("LOCK_ACQUIRED");

// Wait indefinitely — this process is meant to be SIGKILLed by its parent
// scenario, never to exit on its own.
await new Promise(() => {});
