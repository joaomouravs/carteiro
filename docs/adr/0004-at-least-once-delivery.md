# ADR 0004: Guarantee at-least-once delivery, without ordering

- **Status:** Proposed
- **Date:** 2026-10-27

## Context

A sender cannot know whether a receiver processed a request when the connection drops
after the request was sent but before the response arrived. Retrying risks a duplicate;
not retrying risks a loss. Exactly-once delivery over HTTP is not achievable by the
sender alone.

Ordering has a similar cost: delivering events to an endpoint strictly in order means
one failing event blocks every later event for that endpoint.

## Decision

- Carteiro guarantees **at-least-once** delivery: it retries until it gets a `2xx` or
  exhausts attempts. Receivers deduplicate using `webhook-id`.
- Carteiro does **not** guarantee ordering. Events carry their creation timestamp, and
  receivers that care about order should treat webhooks as notifications and fetch the
  current state from the producer's API.

## Consequences

- Workers stay independent and horizontally scalable; no per-endpoint serialization.
- One bad event never blocks other events to the same endpoint.
- The guarantee must be stated clearly in the README, along with how to deduplicate,
  because receivers carry part of the contract.
