-- InsertEvent stores an event. With an idempotency key, it also stores the
-- SHA-256 of the payload as PostgreSQL normalizes it: casting to jsonb sorts
-- the keys, drops duplicate keys and whitespace, so two encodings of the same
-- JSON object hash the same (ADR 0006).
--
-- If the application already used the key, ON CONFLICT DO NOTHING makes the
-- statement return no row instead of failing. A concurrent insert with the
-- same key waits for the first transaction to finish, so exactly one wins.
-- name: InsertEvent :one
INSERT INTO events (application_id, event_type, payload, idempotency_key, payload_hash)
VALUES (
    sqlc.arg('application_id'),
    sqlc.arg('event_type'),
    sqlc.arg('payload')::jsonb,
    sqlc.narg('idempotency_key')::text,
    CASE WHEN sqlc.narg('idempotency_key')::text IS NULL THEN NULL
         ELSE sha256(convert_to(sqlc.arg('payload')::jsonb::text, 'UTF8'))
    END
)
ON CONFLICT (application_id, idempotency_key) DO NOTHING
RETURNING *;

-- GetEventByIdempotencyKey loads the event that already holds a key, and
-- whether its payload equals the new one under the same normalization.
-- name: GetEventByIdempotencyKey :one
SELECT sqlc.embed(events),
       (events.payload_hash = sha256(convert_to(sqlc.arg('payload')::jsonb::text, 'UTF8')))::boolean AS same_payload
FROM events
WHERE application_id = sqlc.arg('application_id')
  AND idempotency_key = sqlc.arg('idempotency_key');

-- CreateDeliveries fans an event out to every active endpoint of the
-- application subscribed to its type; an empty event_types list subscribes
-- to all types (FR-12). Each delivery is due immediately.
-- name: CreateDeliveries :execrows
INSERT INTO deliveries (event_id, endpoint_id)
SELECT sqlc.arg('event_id'), e.id
FROM endpoints e
WHERE e.application_id = sqlc.arg('application_id')
  AND e.status = 'active'
  AND (cardinality(e.event_types) = 0 OR sqlc.arg('event_type')::text = ANY (e.event_types));
