# ADR 0006: Idempotency compares normalized JSON

- **Status:** Accepted
- **Date:** 2026-10-28

## Context

`POST /v1/events` accepts an `Idempotency-Key` (FR-11). When a key is reused, Carteiro
must tell a retry of the same request (answer `200` with the original event) from a
different request that reuses the key by mistake (answer `409 Conflict`). That needs a
definition of "the same payload".

A retry does not always resend identical bytes. A client may rebuild the request from
its objects and serialize it again, with keys in another order or different whitespace.

## Decision

Two payloads are the same when they are the same JSON value, as PostgreSQL's `jsonb`
normalizes it: keys sorted, duplicate keys collapsed to the last one, insignificant
whitespace removed. Array order and number formatting still matter.

The hash is computed in SQL, `sha256(convert_to(payload::jsonb::text, 'UTF8'))`, both
when the event is stored and when a reused key is checked. The event type must also
match.

## Consequences

- A retry whose JSON was re-serialized is recognized as a retry, not a false conflict.
- There is one definition of "equal", and it is the database's, which matches the
  payload as stored. Carteiro does not implement JSON canonicalization in Go, where
  number precision and duplicate keys are easy to get wrong.
- The Go code never decodes the payload into Go values, so large integers keep their
  exact value.
- Idempotency keys stay reserved for as long as their event is retained (FR-21).

## Alternatives considered

- **Hash of the raw bytes**: simpler, but any whitespace or key-order difference in a
  legitimate retry becomes a `409`.
- **RFC 8785 (JSON Canonicalization Scheme) in Go**: a standard, but it is code to own and
  test, and it would be a second normalization, different from the one `jsonb` applies.
