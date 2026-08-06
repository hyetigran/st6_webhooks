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
    },
  },
});
