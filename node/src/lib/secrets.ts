import { randomBytes } from "node:crypto";

export function generateSecret(prefix: string): string {
  return `${prefix}_${randomBytes(24).toString("hex")}`;
}
