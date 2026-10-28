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
