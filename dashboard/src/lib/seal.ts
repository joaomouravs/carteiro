// Authenticated encryption for the session cookie.
//
// The cookie holds the producer's API key, so it is encrypted, not just
// signed: the browser stores it but cannot read it, and any change to it makes
// it fail to open. AES-256-GCM is the construction Carteiro uses for endpoint
// secrets (internal/crypto/cipher.go).
import { createCipheriv, createDecipheriv, hkdfSync, randomBytes } from "node:crypto";

const ALGORITHM = "aes-256-gcm";
// A version prefix lets the format change later without misreading old
// cookies: anything else simply fails to open.
const VERSION = "v1";
const IV_BYTES = 12;

export const MIN_SECRET_LENGTH = 32;

// deriveKey turns SESSION_SECRET into a 256-bit key with HKDF. The info string
// ties the key to this one use, so the same secret reused elsewhere would not
// produce the same key.
export function deriveKey(secret: string): Buffer {
  if (secret.length < MIN_SECRET_LENGTH) {
    throw new Error(`SESSION_SECRET must have at least ${MIN_SECRET_LENGTH} characters`);
  }
  return Buffer.from(hkdfSync("sha256", secret, "", "carteiro-dashboard session v1", 32));
}

export function seal(plaintext: string, key: Buffer): string {
  // A fresh random nonce per cookie; GCM breaks if one ever repeats.
  const iv = randomBytes(IV_BYTES);
  const cipher = createCipheriv(ALGORITHM, key, iv);
  const ciphertext = Buffer.concat([cipher.update(plaintext, "utf8"), cipher.final()]);
  return [VERSION, iv, ciphertext, cipher.getAuthTag()]
    .map((part) => (typeof part === "string" ? part : part.toString("base64url")))
    .join(".");
}

// unseal returns null for anything that is not a cookie sealed with key:
// tampered, truncated, from another secret, or not a sealed value at all. The
// cases are not told apart, so a failure reveals nothing.
export function unseal(sealed: string, key: Buffer): string | null {
  const [version, iv, ciphertext, tag, ...rest] = sealed.split(".");
  if (version !== VERSION || iv === undefined || ciphertext === undefined || tag === undefined || rest.length > 0) {
    return null;
  }
  try {
    const decipher = createDecipheriv(ALGORITHM, key, Buffer.from(iv, "base64url"));
    decipher.setAuthTag(Buffer.from(tag, "base64url"));
    return Buffer.concat([decipher.update(Buffer.from(ciphertext, "base64url")), decipher.final()]).toString("utf8");
  } catch {
    return null;
  }
}
