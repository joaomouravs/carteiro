// Ingest latency (NFR-PERF-01: p99 below 50 ms on a developer laptop).
//
// Publishes events at a constant rate while the worker delivers them, as in
// production, and fails if the p99 latency or the error rate miss the target.
// Run it with: task loadtest:ingest (see Taskfile.yml).
import http from "k6/http";
import { check } from "k6";

const API = __ENV.API || "http://api:8080";
const ADMIN = __ENV.ADMIN_TOKEN || "dev-admin-token-change-me-0123456789";
const RATE = Number(__ENV.RATE || 200);

export const options = {
  scenarios: {
    publish: {
      // Constant arrival rate, not a fixed number of looping users: the
      // request rate stays the same even when responses slow down, so a
      // slow server shows up as latency instead of hiding it.
      executor: "constant-arrival-rate",
      rate: RATE,
      timeUnit: "1s",
      duration: __ENV.DURATION || "60s",
      preAllocatedVUs: 50,
      maxVUs: 200,
    },
  },
  thresholds: {
    "http_req_duration{name:publish}": ["p(99)<50"],
    "http_req_failed{name:publish}": ["rate<0.001"],
  },
  summaryTrendStats: ["avg", "p(50)", "p(95)", "p(99)", "max"],
};

const json = { "Content-Type": "application/json" };

export function setup() {
  const auth = { headers: { ...json, Authorization: `Bearer ${ADMIN}` } };
  const app = http.post(`${API}/v1/applications`, JSON.stringify({ name: "k6 ingest" }), auth).json();
  const key = http.post(`${API}/v1/applications/${app.id}/api-keys`, JSON.stringify({ name: "k6" }), auth).json().key;
  const ep = http.post(`${API}/v1/endpoints`, JSON.stringify({ url: "http://sink/hook" }), {
    headers: { ...json, Authorization: `Bearer ${key}` },
  });
  if (ep.status !== 201) {
    throw new Error(`creating the endpoint failed: ${ep.status} ${ep.body}`);
  }
  return { key };
}

// A payload of about 1 KiB, like a typical webhook.
const payload = {
  invoice: "in_1NfQkL2eZvKYlo2C",
  customer: { id: "cus_9s6XKzkNRiz8i3", email: "billing@example.com" },
  lines: Array.from({ length: 8 }, (_, i) => ({ id: `il_${i}`, amount: 1999 + i, currency: "brl", description: "Plano mensal" })),
  metadata: { source: "k6" },
};

export default function (data) {
  const res = http.post(`${API}/v1/events`, JSON.stringify({ event_type: "invoice.paid", payload }), {
    headers: { ...json, Authorization: `Bearer ${data.key}` },
    tags: { name: "publish" },
  });
  check(res, { "202 Accepted": (r) => r.status === 202 });
}
