import cors from "cors";
import express from "express";
import { requireTenant } from "./auth/middleware.js";
import { cors as corsConfig } from "./config.js";
import { endpointsRouter } from "./routes/endpoints.js";
import { eventsRouter } from "./routes/events.js";
import { replaysRouter } from "./routes/replays.js";
import { deliveriesRouter } from "./routes/deliveries.js";

export function createApp() {
  const app = express();
  // Must run before requireTenant: a browser's CORS preflight (OPTIONS)
  // never carries the Authorization header, so requireTenant would reject
  // it with 401 — cors() answers preflights itself and never reaches the
  // auth middleware.
  app.use(
    cors({
      origin: corsConfig.origin,
      allowedHeaders: ["authorization", "content-type", "idempotency-key"],
    }),
  );
  app.use(express.json());

  app.get("/healthz", (_req, res) => {
    res.json({ ok: true });
  });

  app.use(requireTenant, endpointsRouter, eventsRouter, replaysRouter, deliveriesRouter);

  app.use((_req, res) => {
    res.status(404).json({ error: { code: "not_found", message: "Not found" } });
  });

  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  app.use((err: unknown, _req: express.Request, res: express.Response, _next: express.NextFunction) => {
    console.error(err);
    res.status(500).json({ error: { code: "internal_error", message: "Internal error" } });
  });

  return app;
}
