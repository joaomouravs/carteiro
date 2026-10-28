import assert from "node:assert/strict";
import { test } from "node:test";

import { deriveKey, seal, unseal } from "./seal.ts";

const key = deriveKey("a-test-session-secret-0123456789abcdef");

test("a sealed value opens with the same key", () => {
  const sealed = seal("crt_secret_api_key", key);
  assert.equal(unseal(sealed, key), "crt_secret_api_key");
  assert.ok(!sealed.includes("crt_secret_api_key"), "the plaintext shows through the cookie");
});

test("sealing twice gives different cookies", () => {
  // A random nonce per seal: equal cookies would show that two sessions
  // hold the same key.
  assert.notEqual(seal("same", key), seal("same", key));
});

test("anything not sealed with the key fails to open", () => {
  const sealed = seal("crt_secret_api_key", key);
  const parts = sealed.split(".");
  const flip = (s: string) => (s[0] === "A" ? "B" : "A") + s.slice(1);
  const cases: Record<string, string> = {
    "another secret": seal("crt_secret_api_key", deriveKey("another-session-secret-0123456789abcdef")),
    "tampered ciphertext": [parts[0], parts[1], flip(parts[2] ?? ""), parts[3]].join("."),
    "tampered tag": [parts[0], parts[1], parts[2], flip(parts[3] ?? "")].join("."),
    "unknown version": ["v2", ...parts.slice(1)].join("."),
    truncated: parts.slice(0, 3).join("."),
    "extra part": [...parts, "x"].join("."),
    "not sealed at all": "crt_secret_api_key",
    empty: "",
  };
  for (const [name, value] of Object.entries(cases)) {
    assert.equal(unseal(value, key), null, name);
  }
});

test("a short secret is refused", () => {
  assert.throws(() => deriveKey("too-short"), /at least 32 characters/);
});
