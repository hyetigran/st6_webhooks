import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    globalSetup: "./test/global-setup.ts",
    setupFiles: ["./test/setup.ts"],
    // Sequential, not parallel — every test truncates shared tables, so
    // concurrent test files would stomp on each other's fixtures.
    fileParallelism: false,
    env: {
      DATABASE_URL: "postgres://webhooks:webhooks@localhost:5532/webhooks_node_test",
      SECRET_ENCRYPTION_KEY: "uVnfLJGuLvn8ZwxpLXFXIw8irrEhzVUIqM6SneLB6Sc=",
      // PRD §8's "time is injected": every retry/timeout-driven config is
      // env-var-configurable specifically so the test suite can use small,
      // fast values instead of the production defaults (base_delay=1s,
      // outbound timeout=10s) — a test that actually waits out a real
      // backoff/timeout would otherwise take tens of seconds per case.
      // Tests needing a *specific* small config still pass one explicitly
      // (completeDelivery's BackoffConfig param); this sets the floor for
      // anything going through the global config, like runDeliveryCycle.
      BACKOFF_BASE_DELAY_MS: "10",
      BACKOFF_MAX_DELAY_MS: "100",
      OUTBOUND_CONNECT_TIMEOUT_MS: "500",
      OUTBOUND_TOTAL_TIMEOUT_MS: "500",
      WORKER_IDLE_POLL_INTERVAL_MS: "10",
    },
  },
});
