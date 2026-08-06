import express from "express";
import { requireTenant } from "./auth/middleware.js";
import { endpointsRouter } from "./routes/endpoints.js";

export function createApp() {
  const app = express();
  app.use(express.json());

  app.get("/healthz", (_req, res) => {
    res.json({ ok: true });
  });

  app.use(requireTenant, endpointsRouter);

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
