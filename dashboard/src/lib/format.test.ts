import assert from "node:assert/strict";
import { test } from "node:test";

import { absoluteTime, duration, relativeTime, shortID } from "./format.ts";

const now = Date.parse("2026-10-28T12:00:00Z");

test("relativeTime picks the largest whole unit", () => {
  const cases: [string, string][] = [
    ["2026-10-28T11:59:59.500Z", "just now"],
    ["2026-10-28T11:59:30Z", "30 seconds ago"],
    ["2026-10-28T11:57:00Z", "3 minutes ago"],
    ["2026-10-28T14:00:00Z", "in 2 hours"],
    ["2026-10-27T12:00:00Z", "yesterday"],
    ["2026-10-21T12:00:00Z", "7 days ago"],
  ];
  for (const [iso, want] of cases) assert.equal(relativeTime(iso, now), want, iso);
});

test("absoluteTime is in UTC", () => {
  assert.equal(absoluteTime("2026-10-28T12:00:05Z"), "Oct 28, 2026, 12:00:05 PM UTC");
});

test("duration reads naturally at every scale", () => {
  const cases: [number, string][] = [
    [0, "0 ms"],
    [42, "42 ms"],
    [1_234, "1.23 s"],
    [15_000, "15.0 s"],
    [125_000, "2 min 5 s"],
  ];
  for (const [ms, want] of cases) assert.equal(duration(ms), want, String(ms));
});

test("shortID keeps the random end of a UUIDv7", () => {
  assert.equal(shortID("01a18402-8679-7380-be9b-ed708c990248"), "8c990248");
});
