import { describe, it, expect, vi, afterEach } from "vitest";
import { computeBackoffDelayMs } from "../src/worker/delivery.js";

const config = { baseDelayMs: 1_000, multiplier: 2, maxDelayMs: 30_000 };

afterEach(() => {
  vi.restoreAllMocks();
});

describe("computeBackoffDelayMs", () => {
  it("returns 0 at minimum jitter, for any attempt number", () => {
    vi.spyOn(Math, "random").mockReturnValue(0);
    expect(computeBackoffDelayMs(1, config)).toBe(0);
    expect(computeBackoffDelayMs(3, config)).toBe(0);
  });

  it("doubles the jitter ceiling with each attempt (full jitter: base * multiplier^(attempt-1))", () => {
    vi.spyOn(Math, "random").mockReturnValue(0.5);
    expect(computeBackoffDelayMs(1, config)).toBe(500); // ceiling 1000
    expect(computeBackoffDelayMs(2, config)).toBe(1_000); // ceiling 2000
    expect(computeBackoffDelayMs(3, config)).toBe(2_000); // ceiling 4000
    expect(computeBackoffDelayMs(5, config)).toBe(8_000); // ceiling 16000
  });

  it("caps the jitter ceiling at maxDelayMs once the exponential growth exceeds it", () => {
    vi.spyOn(Math, "random").mockReturnValue(0.5);
    // Uncapped ceiling at attempt 10 would be 1000 * 2^9 = 512000ms.
    expect(computeBackoffDelayMs(10, config)).toBe(15_000); // half of the 30000ms cap
  });
});
