# ADR 0004: Guarantee at-least-once delivery, without ordering

- **Status:** Accepted
- **Date:** 2026-10-27

## Context

When the connection drops after a request was sent but before the response came back,
the sender cannot know whether the receiver processed it. Retrying risks a duplicate; not
retrying risks losing it. No sender can get exactly-once delivery over HTTP on its own.

Ordering has a similar cost. Delivering an endpoint's events strictly in order means one
failing event holds up every event after it.

## Decision

- Carteiro delivers **at least once**: it retries until it gets a `2xx` or runs out of
  attempts. Receivers drop duplicates using `webhook-id`.
- Carteiro does **not** guarantee order. Every event carries its creation time, and a
  receiver that cares about order should treat a webhook as a notification and fetch the
  current state from the producer's API.

## Consequences

- Workers stay independent and I can add more of them freely; there is no per-endpoint
  serialization to coordinate.
- One bad event never blocks the others going to the same endpoint.
- Receivers carry part of this contract, so the README states it plainly and the example
  receiver shows how to deduplicate.
