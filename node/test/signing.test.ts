import { describe, it, expect } from "vitest";
import { createHmac } from "node:crypto";
import { signPayload } from "../src/lib/signing.js";

function expectedSignature(secret: string, timestamp: number, rawBody: string): string {
  return createHmac("sha256", secret).update(`${timestamp}.${rawBody}`, "utf8").digest("hex");
}

describe("signPayload", () => {
  it("signs with a single secret as HMAC-SHA256 over \"{timestamp}.{raw_body}\"", () => {
    const signature = signPayload(["whsec_abc123"], 1_700_000_000, '{"orderId":"1"}');
    expect(signature).toBe(expectedSignature("whsec_abc123", 1_700_000_000, '{"orderId":"1"}'));
  });

  it("signs with every active secret and comma-joins the results during a rotation overlap", () => {
    const signature = signPayload(["whsec_new", "whsec_old"], 1_700_000_000, '{"orderId":"1"}');
    const expected = [
      expectedSignature("whsec_new", 1_700_000_000, '{"orderId":"1"}'),
      expectedSignature("whsec_old", 1_700_000_000, '{"orderId":"1"}'),
    ].join(",");
    expect(signature).toBe(expected);
  });
});
