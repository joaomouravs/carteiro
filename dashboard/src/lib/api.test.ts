import assert from "node:assert/strict";
import { afterEach, test } from "node:test";

import { ApiError, createClient, isID } from "./api.ts";

type Call = { url: string; method: string; headers: Record<string, string>; body: string | undefined; cache: unknown };

const realFetch = globalThis.fetch;
afterEach(() => {
  globalThis.fetch = realFetch;
});

// respond replaces fetch with one that records each request and answers with
// status and body.
function respond(status: number, body: unknown): Call[] {
  const calls: Call[] = [];
  globalThis.fetch = (async (input: URL | string, init: RequestInit = {}) => {
    calls.push({
      url: input.toString(),
      method: init.method ?? "GET",
      headers: init.headers as Record<string, string>,
      body: init.body as string | undefined,
      cache: init.cache,
    });
    return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
  }) as typeof fetch;
  return calls;
}

const ID = "01a18402-8679-7380-be9b-ed708c990248";
const client = createClient("http://carteiro:8080", "crt_key");

test("requests carry the API key and are never cached", async () => {
  const calls = respond(200, { id: ID, name: "Acme", created_at: "2026-10-28T12:00:00Z" });
  const app = await client.me();
  assert.equal(app.name, "Acme");
  assert.equal(calls[0]?.url, "http://carteiro:8080/v1/me");
  assert.equal(calls[0]?.headers.Authorization, "Bearer crt_key");
  assert.equal(calls[0]?.cache, "no-store");
});

test("query parameters are sent only when set", async () => {
  const calls = respond(200, { data: [], next_cursor: null });
  await client.listEvents({ eventType: "invoice.paid", cursor: "abc" });
  await client.listEvents();
  assert.equal(calls[0]?.url, "http://carteiro:8080/v1/events?cursor=abc&event_type=invoice.paid");
  assert.equal(calls[1]?.url, "http://carteiro:8080/v1/events");
});

test("bodies are JSON, and absent when there is nothing to send", async () => {
  const calls = respond(200, {});
  await client.rotateSecret(ID, 0);
  await client.rotateSecret(ID);
  assert.equal(calls[0]?.body, '{"grace_period_seconds":0}');
  assert.equal(calls[0]?.headers["Content-Type"], "application/json");
  assert.equal(calls[1]?.body, undefined, "an omitted grace period must keep Carteiro's default");
});

test("errors carry Carteiro's problem details", async () => {
  respond(409, { type: "about:blank", title: "Conflict", status: 409, detail: "the delivery is already queued" });
  await assert.rejects(client.replayDelivery(ID), (err: unknown) => {
    assert.ok(err instanceof ApiError);
    assert.equal(err.status, 409);
    assert.equal(err.message, "the delivery is already queued");
    return true;
  });
});

test("ids that are not UUIDs never reach Carteiro", async () => {
  const calls = respond(200, {});
  for (const bad of ["../applications", "x/../../v1/me", "", ID + "/attempts"]) {
    assert.equal(isID(bad), false, bad);
    await assert.rejects(client.getDelivery(bad), (err: unknown) => err instanceof ApiError && err.status === 404);
  }
  assert.equal(calls.length, 0);
});
