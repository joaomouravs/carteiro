-- name: CreateAPIKey :one
INSERT INTO api_keys (application_id, name, prefix, hash)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListAPIKeys :many
SELECT * FROM api_keys
WHERE application_id = $1
ORDER BY id DESC;

-- RevokeAPIKey is idempotent: revoking a revoked key keeps the original
-- revocation time and still matches, so only a missing key returns no row.
-- name: RevokeAPIKey :one
UPDATE api_keys
SET revoked_at = COALESCE(revoked_at, now())
WHERE id = $1 AND application_id = $2
RETURNING *;

-- name: GetActiveAPIKeyByHash :one
SELECT * FROM api_keys
WHERE hash = $1 AND revoked_at IS NULL;

-- name: TouchAPIKey :exec
UPDATE api_keys SET last_used_at = now()
WHERE id = $1;
