import { createHash, createCipheriv, createDecipheriv, randomBytes } from "node:crypto";
import { secretEncryptionKey } from "../config.js";

// Bearer API keys: hash with a fast cryptographic hash (SHA-256), not a slow
// password hash (bcrypt/scrypt/argon2). Those exist to slow down brute-forcing
// a low-entropy, human-chosen secret — an API key is a high-entropy random
// token no one is guessing, and a slow hash would just add needless latency
// to every authenticated request for no security benefit.
export function hashApiKey(key: string): string {
  return createHash("sha256").update(key, "utf8").digest("hex");
}

// Signing secrets must be recoverable (HMAC needs the raw value), so they
// can't be hashed like the API key — encrypted at rest instead, so a DB dump
// alone doesn't hand over every customer's signing secret in plaintext.
const ALGORITHM = "aes-256-gcm";

export function encryptSecret(plaintext: string): string {
  const iv = randomBytes(12);
  const cipher = createCipheriv(ALGORITHM, secretEncryptionKey.keyBuffer(), iv);
  const encrypted = Buffer.concat([cipher.update(plaintext, "utf8"), cipher.final()]);
  const authTag = cipher.getAuthTag();
  return Buffer.concat([iv, authTag, encrypted]).toString("base64");
}

export function decryptSecret(stored: string): string {
  const raw = Buffer.from(stored, "base64");
  const iv = raw.subarray(0, 12);
  const authTag = raw.subarray(12, 28);
  const encrypted = raw.subarray(28);
  const decipher = createDecipheriv(ALGORITHM, secretEncryptionKey.keyBuffer(), iv);
  decipher.setAuthTag(authTag);
  return Buffer.concat([decipher.update(encrypted), decipher.final()]).toString("utf8");
}
