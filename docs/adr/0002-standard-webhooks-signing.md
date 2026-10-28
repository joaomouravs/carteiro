# ADR 0002: Sign deliveries following the Standard Webhooks specification

- **Status:** Accepted
- **Date:** 2026-10-27

## Context

A receiver needs to know that a request really came from Carteiro, that nobody changed
it on the way, and that it is not an old request being replayed. I could have invented my
own HMAC scheme, but then every receiver would have to implement it from my docs.

## Decision

I follow the [Standard Webhooks](https://www.standardwebhooks.com/) specification:
HMAC-SHA256 over `id.timestamp.body`, sent in the `webhook-id`, `webhook-timestamp` and
`webhook-signature` headers, with secrets shown as `whsec_` plus base64.

## Consequences

- Receivers can verify with the official libraries, which exist for several languages.
  The example receiver in `examples/receiver` uses the Go one, and its tests double as the
  proof that my signatures are compatible.
- The timestamp lets receivers reject old requests, and the id lets them drop duplicates,
  which at-least-once delivery requires ([ADR 0004](0004-at-least-once-delivery.md)).
- The header can carry several signatures, so secret rotation without downtime came for
  free.
- I test the signing code against the test vector of the official Go library (the spec
  itself publishes none), so it produces exactly what the official libraries verify.
