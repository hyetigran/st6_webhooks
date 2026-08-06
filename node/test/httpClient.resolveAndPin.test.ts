import { describe, it, expect, vi } from "vitest";
import { resolveAndPin } from "../src/worker/httpClient.js";

describe("resolveAndPin", () => {
  it("pins a literal public IPv4 address as-is", async () => {
    const result = await resolveAndPin("93.184.216.34");
    expect(result).toEqual({ allowed: true, ip: "93.184.216.34" });
  });

  it("rejects a literal private IPv4 address", async () => {
    const result = await resolveAndPin("10.0.0.5");
    expect(result.allowed).toBe(false);
  });

  it("rejects the IPv4-mapped IPv6 loopback literal ::ffff:127.0.0.1", async () => {
    const result = await resolveAndPin("::ffff:127.0.0.1");
    expect(result.allowed).toBe(false);
  });

  it("resolves a hostname via the injected resolver and pins the validated address", async () => {
    const resolver = vi.fn().mockResolvedValue([{ address: "93.184.216.34", family: 4 }]);
    const result = await resolveAndPin("example.com", resolver);
    expect(result).toEqual({ allowed: true, ip: "93.184.216.34" });
    expect(resolver).toHaveBeenCalledWith("example.com");
  });

  it("rejects a hostname that resolves to a private address", async () => {
    const resolver = vi.fn().mockResolvedValue([{ address: "169.254.169.254", family: 4 }]);
    const result = await resolveAndPin("metadata.internal", resolver);
    expect(result.allowed).toBe(false);
  });

  it("resolves the hostname exactly once, so a rebinding resolver can't hand back a different address than the one that was validated", async () => {
    let calls = 0;
    const resolver = vi.fn().mockImplementation(async () => {
      calls += 1;
      // First (and, if called correctly, only) call returns a public address.
      // A second call would return a private one — proving a rebind attack
      // only works if the implementation re-resolves, which it must not.
      return calls === 1
        ? [{ address: "93.184.216.34", family: 4 }]
        : [{ address: "127.0.0.1", family: 4 }];
    });

    const result = await resolveAndPin("rebinding-host.example", resolver);

    expect(resolver).toHaveBeenCalledTimes(1);
    expect(result).toEqual({ allowed: true, ip: "93.184.216.34" });
  });
});
