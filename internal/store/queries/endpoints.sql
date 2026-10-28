-- Every endpoint query filters by application_id as well as id: an endpoint
-- of another application is indistinguishable from one that does not exist.

-- name: CreateEndpoint :one
INSERT INTO endpoints (application_id, url, description, event_types)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: CreateEndpointSecret :exec
INSERT INTO endpoint_secrets (endpoint_id, secret_ciphertext)
VALUES ($1, $2);

-- name: GetEndpoint :one
SELECT * FROM endpoints
WHERE id = $1 AND application_id = $2;

-- name: ListEndpoints :many
SELECT * FROM endpoints
WHERE application_id = sqlc.arg('application_id')
  AND (sqlc.narg('before')::uuid IS NULL OR id < sqlc.narg('before')::uuid)
ORDER BY id DESC
LIMIT sqlc.arg('limit');

-- UpdateEndpoint changes only the fields passed as non-null, in a single
-- statement. Reading the row, merging in Go and writing it back would let two
-- concurrent updates silently overwrite each other's changes.
-- name: UpdateEndpoint :one
UPDATE endpoints
SET url         = COALESCE(sqlc.narg('url'), url),
    description = COALESCE(sqlc.narg('description'), description),
    event_types = COALESCE(sqlc.narg('event_types'), event_types),
    updated_at  = now()
WHERE id = sqlc.arg('id') AND application_id = sqlc.arg('application_id')
RETURNING *;

-- name: DeleteEndpoint :execrows
DELETE FROM endpoints
WHERE id = $1 AND application_id = $2;

-- EnableEndpoint re-enables an endpoint (FR-09) with a clean failure record,
-- so the next failure does not disable it again at once. Enabling an active
-- endpoint changes nothing but its failure counters.
-- name: EnableEndpoint :one
UPDATE endpoints
SET status               = 'active',
    disabled_reason      = NULL,
    consecutive_failures = 0,
    failing_since        = NULL,
    updated_at           = now()
WHERE id = $1 AND application_id = $2
RETURNING *;

-- Endpoint health (FR-08), written by the worker in the same transaction as
-- each attempt.

-- RecordEndpointResult updates the failure streak after an attempt and
-- reports whether it has lasted at least failing_for_ms. updated_at is left
-- alone: it tracks changes to the endpoint's configuration, not its health.
-- name: RecordEndpointResult :one
UPDATE endpoints
SET consecutive_failures = CASE WHEN sqlc.arg('success')::boolean THEN 0 ELSE consecutive_failures + 1 END,
    failing_since        = CASE WHEN sqlc.arg('success')::boolean THEN NULL ELSE COALESCE(failing_since, now()) END
WHERE id = sqlc.arg('id')
RETURNING status,
          consecutive_failures,
          (failing_since IS NOT NULL
           AND failing_since <= now() - sqlc.arg('failing_for_ms')::bigint * interval '1 millisecond')::boolean AS failing_long_enough;

-- name: DisableEndpoint :execrows
UPDATE endpoints
SET status = 'disabled', disabled_reason = sqlc.arg('reason'), updated_at = now()
WHERE id = sqlc.arg('id') AND status = 'active';

-- Secret rotation (FR-06).

-- LockEndpoint reads an endpoint and locks its row until the transaction
-- ends. Rotations lock it first, so two concurrent rotations of one endpoint
-- run one after the other: otherwise both would retire the same current
-- secret and both insert a new one, and the second insert would break the
-- one-current-secret index.
-- name: LockEndpoint :one
SELECT * FROM endpoints
WHERE id = $1 AND application_id = $2
FOR UPDATE;

-- RetireEndpointSecrets sets every secret of the endpoint that is still valid
-- to expire after the grace period, or keeps an earlier expiry it already
-- has. With a grace period of zero they stop being valid at once, for a
-- secret that leaked.
-- name: RetireEndpointSecrets :exec
UPDATE endpoint_secrets
SET expires_at = LEAST(COALESCE(expires_at, 'infinity'), now() + sqlc.arg('grace_ms')::bigint * interval '1 millisecond')
WHERE endpoint_id = sqlc.arg('endpoint_id')
  AND (expires_at IS NULL OR expires_at > now());
