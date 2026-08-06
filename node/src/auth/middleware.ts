import type { NextFunction, Request, Response } from "express";
import { pool } from "../db/pool.js";
import { hashApiKey } from "../lib/crypto.js";

declare global {
  // eslint-disable-next-line @typescript-eslint/no-namespace
  namespace Express {
    interface Request {
      tenantId: string;
    }
  }
}

// Bearer API key per tenant, resolved server-side to tenant_id — never
// accepted as a URL/body parameter (Shared REST API contract ticket).
export async function requireTenant(req: Request, res: Response, next: NextFunction): Promise<void> {
  try {
    const header = req.header("authorization") ?? "";
    const [scheme, token] = header.split(" ");
    if (scheme !== "Bearer" || !token) {
      res.status(401).json({ error: { code: "unauthorized", message: "Missing or malformed Authorization header" } });
      return;
    }

    const { rows } = await pool.query<{ id: string }>(
      "SELECT id FROM tenants WHERE api_key_hash = $1",
      [hashApiKey(token)],
    );
    const tenant = rows[0];
    if (!tenant) {
      res.status(401).json({ error: { code: "unauthorized", message: "Invalid API key" } });
      return;
    }

    req.tenantId = tenant.id;
    next();
  } catch (err) {
    next(err);
  }
}
