// PRD §8's make verify: "seeds are logged" — a small seeded PRNG so
// property-test failures are reproducible by rerunning with the same seed,
// without pulling in a full property-testing library for what's ultimately
// a handful of invariants (repeated-key idempotency, seq-order under
// concurrency, replay-retry exactly-once).

// mulberry32 — a standard small, fast, deterministic PRNG.
export function mulberry32(seed: number): () => number {
  let state = seed | 0;
  return function next(): number {
    state = (state + 0x6d2b79f5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// Reproducible on failure: set PROPERTY_TEST_SEED to the logged value to
// rerun the exact same random sequence.
export function getTestSeed(label: string): number {
  const fromEnv = process.env.PROPERTY_TEST_SEED;
  const seed = fromEnv ? Number(fromEnv) : Math.floor(Math.random() * 2 ** 31);
  console.log(`[property test seed] ${label}: ${seed} (rerun with PROPERTY_TEST_SEED=${seed} to reproduce)`);
  return seed;
}

export function randomInt(rng: () => number, min: number, max: number): number {
  return min + Math.floor(rng() * (max - min + 1));
}
