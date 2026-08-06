import { describe, it, expect, afterEach } from "vitest";
import { sendOutboundRequest } from "../src/worker/httpClient.js";
import { createTestServerHarness } from "./testServer.js";

const testServer = createTestServerHarness();
afterEach(() => testServer.close());
const listen = testServer.listen;

const baseOptions = {
  method: "POST",
  headers: { "content-type": "application/json" },
  body: '{"hello":"world"}',
  connectTimeoutMs: 2_000,
  totalTimeoutMs: 2_000,
  maxResponseBodyBytes: 1_000,
};

describe("sendOutboundRequest", () => {
  it("sends the request to the pinned IP and returns the response status/body", async () => {
    const port = await listen((req, res) => {
      res.writeHead(200, { "content-type": "text/plain" });
      res.end("ok");
    });

    const result = await sendOutboundRequest("127.0.0.1", new URL(`http://original-hostname.test:${port}/webhook`), baseOptions);

    expect(result).toMatchObject({ responseStatus: 200, responseBodyTruncated: "ok", errorClass: null });
  });

  it("sends the request with the Host header set to the original hostname, not the pinned IP", async () => {
    let receivedHost = "";
    const port = await listen((req, res) => {
      receivedHost = req.headers.host ?? "";
      res.writeHead(200);
      res.end();
    });

    await sendOutboundRequest("127.0.0.1", new URL(`http://original-hostname.test:${port}/webhook`), baseOptions);

    expect(receivedHost).toBe(`original-hostname.test:${port}`);
  });

  it("treats a 3xx response as terminal and does not follow the redirect", async () => {
    const port = await listen((req, res) => {
      res.writeHead(302, { location: "http://169.254.169.254/latest/meta-data" });
      res.end();
    });

    const result = await sendOutboundRequest("127.0.0.1", new URL(`http://original-hostname.test:${port}/webhook`), baseOptions);

    expect(result.responseStatus).toBe(302);
    expect(result.errorClass).toBeNull();
  });

  it("caps the captured response body at maxResponseBodyBytes without treating it as an error", async () => {
    const port = await listen((req, res) => {
      res.writeHead(200);
      res.end("x".repeat(5_000));
    });

    const result = await sendOutboundRequest("127.0.0.1", new URL(`http://original-hostname.test:${port}/webhook`), {
      ...baseOptions,
      maxResponseBodyBytes: 100,
    });

    expect(result.responseBodyTruncated.length).toBe(100);
    expect(result.responseStatus).toBe(200);
    expect(result.errorClass).toBeNull();
  });

  it("aborts with a total_timeout error class when the response takes longer than the total timeout", async () => {
    const port = await listen((req, res) => {
      setTimeout(() => {
        res.writeHead(200);
        res.end("too slow");
      }, 500);
    });

    const result = await sendOutboundRequest("127.0.0.1", new URL(`http://original-hostname.test:${port}/webhook`), {
      ...baseOptions,
      totalTimeoutMs: 100,
    });

    expect(result.responseStatus).toBeNull();
    expect(result.errorClass).toBe("total_timeout");
  });

  it("times out a slow-loris receiver that starts responding but trickles the body without ever finishing (R-16)", async () => {
    const port = await listen((req, res) => {
      res.writeHead(200);
      res.write("a"); // headers + first byte arrive immediately...
      const trickle = setInterval(() => res.write("b"), 20); // ...then it just dribbles, never calling end()
      res.on("close", () => clearInterval(trickle));
    });

    const result = await sendOutboundRequest("127.0.0.1", new URL(`http://original-hostname.test:${port}/webhook`), {
      ...baseOptions,
      totalTimeoutMs: 100,
    });

    expect(result.responseStatus).toBeNull();
    expect(result.errorClass).toBe("total_timeout");
  });

  it("reports a connection_refused error class when nothing is listening", async () => {
    const result = await sendOutboundRequest("127.0.0.1", new URL("http://original-hostname.test:1/webhook"), baseOptions);

    expect(result.responseStatus).toBeNull();
    expect(result.errorClass).toBe("connection_refused");
  });
});
