// Cursor-based pagination keyed on created_at+id (Shared REST API contract
// ticket) — avoids the skip-based consistency issues offset/limit would have
// against tables that are being inserted into continuously.

export interface Cursor {
  createdAt: string;
  id: string;
}

export function encodeCursor(cursor: Cursor): string {
  return Buffer.from(JSON.stringify(cursor), "utf8").toString("base64url");
}

export function decodeCursor(raw: string): Cursor | null {
  try {
    const parsed = JSON.parse(Buffer.from(raw, "base64url").toString("utf8"));
    if (typeof parsed.createdAt === "string" && typeof parsed.id === "string") {
      return parsed as Cursor;
    }
    return null;
  } catch {
    return null;
  }
}

export function parseLimit(raw: unknown, fallback = 20, max = 100): number {
  const n = Number(raw);
  if (!Number.isFinite(n) || n <= 0) return fallback;
  return Math.min(Math.floor(n), max);
}

// deliveries.seq (docs/adr/0007) is already unique and monotonic, so a
// delivery-queue cursor needs no id tiebreak the way created_at+id does —
// reusing created_at here would hit the exact tie risk ADR-0007 exists to
// avoid (Postgres's now() is transaction-stable; a bulk same-endpoint
// insert can give several rows the same created_at).
export interface SeqCursor {
  seq: number;
}

export function encodeSeqCursor(cursor: SeqCursor): string {
  return Buffer.from(JSON.stringify(cursor), "utf8").toString("base64url");
}

export function decodeSeqCursor(raw: string): SeqCursor | null {
  try {
    const parsed = JSON.parse(Buffer.from(raw, "base64url").toString("utf8"));
    if (typeof parsed.seq === "number") {
      return parsed as SeqCursor;
    }
    return null;
  } catch {
    return null;
  }
}
