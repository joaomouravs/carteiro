-- The delivery queue (ADR 0001, ADR 0003). Every time is computed with the
-- database's now(), never the worker's clock: workers on different machines
-- then agree on what "due" and "expired" mean even if their clocks drift.
-- Durations arrive as milliseconds and become intervals here.

-- ClaimDueDeliveries leases up to `limit` pending deliveries whose time has
-- come, oldest first. SKIP LOCKED makes concurrent workers take different
-- rows instead of waiting on each other. The transaction around this
-- statement is the statement itself, so the row locks last milliseconds; the
-- lease, not a lock, protects the row while the HTTP request runs.
--
-- attempt_count is incremented here, at claim time, and becomes the fencing
-- token for CompleteDelivery.
-- name: ClaimDueDeliveries :many
WITH due AS (
    SELECT id FROM deliveries
    WHERE status = 'pending' AND next_attempt_at <= now()
    ORDER BY next_attempt_at
    LIMIT sqlc.arg('limit')
    FOR UPDATE SKIP LOCKED
)
UPDATE deliveries d
SET status           = 'in_flight',
    lease_expires_at = now() + sqlc.arg('lease_ms')::bigint * interval '1 millisecond',
    attempt_count    = d.attempt_count + 1,
    updated_at       = now()
FROM due
WHERE d.id = due.id
RETURNING d.id, d.attempt_count;

-- ReclaimExpiredDeliveries is ClaimDueDeliveries for deliveries whose lease
-- ran out: their worker crashed, or is so slow that it must be presumed dead.
-- The new claim increments attempt_count, so if the old worker does finish,
-- its result no longer matches the token and is discarded.
-- name: ReclaimExpiredDeliveries :many
WITH expired AS (
    SELECT id FROM deliveries
    WHERE status = 'in_flight' AND lease_expires_at <= now()
    ORDER BY lease_expires_at
    LIMIT sqlc.arg('limit')
    FOR UPDATE SKIP LOCKED
)
UPDATE deliveries d
SET lease_expires_at = now() + sqlc.arg('lease_ms')::bigint * interval '1 millisecond',
    attempt_count    = d.attempt_count + 1,
    updated_at       = now()
FROM expired
WHERE d.id = expired.id
RETURNING d.id, d.attempt_count;

-- GetDeliveryJobs loads what sending needs for a batch of claimed deliveries:
-- the event, the endpoint URL, and every secret still valid for signing,
-- newest first (more than one only during a rotation, FR-06).
-- name: GetDeliveryJobs :many
SELECT d.id,
       d.event_id,
       d.endpoint_id,
       ev.event_type,
       ev.payload,
       ev.created_at AS event_created_at,
       ep.url,
       COALESCE(
           (SELECT array_agg(s.secret_ciphertext ORDER BY s.created_at DESC)
            FROM endpoint_secrets s
            WHERE s.endpoint_id = d.endpoint_id
              AND (s.expires_at IS NULL OR s.expires_at > now())),
           '{}'
       )::bytea[] AS secret_ciphertexts
FROM deliveries d
JOIN events ev ON ev.id = d.event_id
JOIN endpoints ep ON ep.id = d.endpoint_id
WHERE d.id = ANY (sqlc.arg('ids')::uuid[]);

-- CompleteDelivery records the outcome of an attempt, but only if the
-- delivery is still leased by that attempt: attempt_count is the fencing
-- token. A worker whose lease expired and was reclaimed finds a higher count,
-- updates zero rows, and its stale result cannot overwrite the newer state.
--
-- retry_in_ms is null for a final status; null times an interval is null, so
-- next_attempt_at is cleared then.
-- name: CompleteDelivery :execrows
UPDATE deliveries
SET status           = sqlc.arg('status'),
    next_attempt_at  = now() + sqlc.narg('retry_in_ms')::bigint * interval '1 millisecond',
    lease_expires_at = NULL,
    updated_at       = now()
WHERE id = sqlc.arg('id')
  AND attempt_count = sqlc.arg('attempt_count')
  AND status = 'in_flight';

-- InsertAttempt appends to the attempt log (FR-16). It is written even when
-- CompleteDelivery is fenced out: the request did happen, and the receiver
-- may have processed it.
-- name: InsertAttempt :exec
INSERT INTO attempts (delivery_id, attempt_number, started_at, duration_ms, response_status, response_body, error)
VALUES (
    sqlc.arg('delivery_id'),
    sqlc.arg('attempt_number'),
    sqlc.arg('started_at'),
    sqlc.arg('duration_ms'),
    sqlc.narg('response_status'),
    sqlc.narg('response_body'),
    sqlc.narg('error')
);
