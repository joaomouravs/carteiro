# ADR 0006: Idempotency compares normalized JSON

- **Status:** Accepted
- **Date:** 2026-10-28

## Context

`POST /v1/events` takes an `Idempotency-Key` (FR-11). When a key comes back, Carteiro has
to tell a retry of the same request (answer `200` with the original event) from a
different request that reused the key by mistake (answer `409 Conflict`). For that I
needed to define what "the same payload" means.

A retry does not always send the same bytes. A client may rebuild the request from its
objects and serialize it again, with the keys in another order or different whitespace.

## Decision

Two payloads are the same when they are the same JSON value as PostgreSQL's `jsonb`
normalizes it: keys sorted, duplicate keys collapsed to the last one, insignificant
whitespace removed. Array order and how numbers are written still count.

I compute the hash in SQL, `sha256(convert_to(payload::jsonb::text, 'UTF8'))`, both when I
store the event and when I check a reused key. The event type has to match too.

## Consequences

- A retry whose JSON was serialized again is treated as a retry, not as a false conflict.
- There is a single definition of "equal", and it is the database's, which matches the
  payload as it is stored. I did not write JSON canonicalization in Go, where number
  precision and duplicate keys are easy to get wrong.
- The Go code never decodes the payload into Go values, so large integers keep their
  exact value.
- An idempotency key stays taken for as long as its event is kept (FR-21).

## Alternatives I considered

- **A hash of the raw bytes**: simpler, but any whitespace or key order difference in an
  honest retry would turn into a `409`.
- **RFC 8785 (JSON Canonicalization Scheme) in Go**: it is a standard, but it is more code
  to own and test, and it would be a second normalization, different from the one `jsonb`
  applies.
