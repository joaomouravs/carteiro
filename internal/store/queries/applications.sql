-- name: CreateApplication :one
INSERT INTO applications (name)
VALUES ($1)
RETURNING *;

-- name: GetApplication :one
SELECT * FROM applications
WHERE id = $1;

-- ListApplications returns the newest applications first. UUIDv7 ids are
-- ordered by creation time, so the id alone is both the sort key and the
-- cursor: the next page starts below the last id of the previous one.
-- name: ListApplications :many
SELECT * FROM applications
WHERE sqlc.narg('before')::uuid IS NULL OR id < sqlc.narg('before')::uuid
ORDER BY id DESC
LIMIT sqlc.arg('limit');
