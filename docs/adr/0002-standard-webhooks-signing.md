# ADR 0002: Sign deliveries following the Standard Webhooks specification

- **Status:** Proposed
- **Date:** 2026-10-27

## Context

Receivers need to verify that a request came from Carteiro, was not modified, and is not
a replay of an old request. A custom HMAC scheme would work, but every receiver would
need to implement it from our documentation.

## Decision

Adopt the [Standard Webhooks](https://www.standardwebhooks.com/) specification:
HMAC-SHA256 over `id.timestamp.body`, sent in the `webhook-id`, `webhook-timestamp`, and
`webhook-signature` headers, with `whsec_`-prefixed base64 secrets.

## Consequences

- Receivers can verify signatures with existing official libraries in several languages.
  The example receiver in `examples/receiver` uses one, which also proves compatibility.
- The timestamp lets receivers reject old requests (replay protection); the id lets them
  deduplicate, which at-least-once delivery requires ([ADR 0004](0004-at-least-once-delivery.md)).
- The spec allows several signatures in one header, which gives secret rotation without
  downtime for free.
- The signing code is tested against the spec's published test vectors.
