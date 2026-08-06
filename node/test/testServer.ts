import { createServer, type Server } from "node:http";

// Shared by any test that needs a real local receiver (httpClient's send
// tests, the delivery-cycle orchestration tests) — a stand-in for the
// receiver a real customer would run, bound to loopback since that's all a
// test process can reach without real network infrastructure.
export function createTestServerHarness(): {
  listen: (handler: Parameters<typeof createServer>[0]) => Promise<number>;
  close: () => Promise<void>;
} {
  let server: Server | undefined;

  async function listen(handler: Parameters<typeof createServer>[0]): Promise<number> {
    server = createServer(handler);
    await new Promise<void>((resolve) => server!.listen(0, "127.0.0.1", resolve));
    const address = server.address();
    if (address === null || typeof address === "string") throw new Error("expected a bound TCP address");
    return address.port;
  }

  async function close(): Promise<void> {
    if (server) {
      await new Promise<void>((resolve) => server!.close(() => resolve()));
      server = undefined;
    }
  }

  return { listen, close };
}
