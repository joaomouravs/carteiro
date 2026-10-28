// The client for Carteiro's public API. It is the only thing that sends the
// API key anywhere, and "server-only" makes the build fail if a client
// component ever imports it, so the key cannot end up in browser JavaScript.
import "server-only";

import type { Application, Attempt, Delivery, Endpoint, Event, Page, Problem } from "./types.ts";

export class ApiError extends Error {
  readonly status: number;
  readonly problem: Problem | null;

  constructor(status: number, problem: Problem | null) {
    super(problem?.detail ?? problem?.title ?? `Carteiro answered ${status}`);
    this.name = "ApiError";
    this.status = status;
    this.problem = problem;
  }
}

// Ids come from URLs the user can edit. Checking their shape before they go
// into a path keeps "../" and friends out of requests to Carteiro.
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function isID(value: string): boolean {
  return UUID.test(value);
}

function id(value: string): string {
  if (!isID(value)) {
    throw new ApiError(404, null);
  }
  return value;
}

type Query = Record<string, string | undefined>;

export type Client = ReturnType<typeof createClient>;

export function createClient(baseURL: string, apiKey: string) {
  async function request<T>(method: string, path: string, opts: { query?: Query; body?: unknown } = {}): Promise<T> {
    const url = new URL(path, baseURL);
    for (const [name, value] of Object.entries(opts.query ?? {})) {
      if (value) url.searchParams.set(name, value);
    }
    const res = await fetch(url, {
      method,
      headers: {
        Authorization: `Bearer ${apiKey}`,
        ...(opts.body === undefined ? {} : { "Content-Type": "application/json" }),
      },
      body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
      // Every page shows live state; nothing from Carteiro is cached.
      cache: "no-store",
    });
    if (!res.ok) {
      const problem = (await res.json().catch(() => null)) as Problem | null;
      throw new ApiError(res.status, problem);
    }
    return (await res.json()) as T;
  }

  // Every method is async, so a bad id rejects the promise like any other
  // error instead of throwing before there is one.
  return {
    me: async () => request<Application>("GET", "/v1/me"),

    listEvents: async (q: { cursor?: string; eventType?: string; limit?: number } = {}) =>
      request<Page<Event>>("GET", "/v1/events", {
        query: { cursor: q.cursor, event_type: q.eventType, limit: q.limit?.toString() },
      }),
    getEvent: async (eventID: string) => request<Event>("GET", `/v1/events/${id(eventID)}`),
    listEventDeliveries: async (eventID: string, cursor?: string) =>
      request<Page<Delivery>>("GET", `/v1/events/${id(eventID)}/deliveries`, { query: { cursor } }),

    getDelivery: async (deliveryID: string) => request<Delivery>("GET", `/v1/deliveries/${id(deliveryID)}`),
    listAttempts: async (deliveryID: string, cursor?: string) =>
      request<Page<Attempt>>("GET", `/v1/deliveries/${id(deliveryID)}/attempts`, { query: { cursor } }),
    replayDelivery: async (deliveryID: string) => request<Delivery>("POST", `/v1/deliveries/${id(deliveryID)}/replay`),

    listEndpoints: async (cursor?: string) => request<Page<Endpoint>>("GET", "/v1/endpoints", { query: { cursor } }),
    getEndpoint: async (endpointID: string) => request<Endpoint>("GET", `/v1/endpoints/${id(endpointID)}`),
    enableEndpoint: async (endpointID: string) => request<Endpoint>("POST", `/v1/endpoints/${id(endpointID)}/enable`),
    rotateSecret: async (endpointID: string, gracePeriodSeconds?: number) =>
      request<Endpoint>("POST", `/v1/endpoints/${id(endpointID)}/rotate-secret`, {
        body: gracePeriodSeconds === undefined ? undefined : { grace_period_seconds: gracePeriodSeconds },
      }),
    replayEndpoint: async (endpointID: string, since: string) =>
      request<{ replayed: number }>("POST", `/v1/endpoints/${id(endpointID)}/replay`, { body: { since } }),
  };
}
