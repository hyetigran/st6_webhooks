import { createHmac } from "node:crypto";

// ADR-0003: the sender signs with every still-active secret (current, plus
// secondary during a rotation overlap window), not just the current one —
// so a receiver on either secret verifies successfully throughout the
// overlap, closing the bootstrapping gap a receiver-only dual-check has.
export function signPayload(secrets: string[], timestamp: number, rawBody: string): string {
  const signedString = `${timestamp}.${rawBody}`;
  return secrets
    .map((secret) => createHmac("sha256", secret).update(signedString, "utf8").digest("hex"))
    .join(",");
}
