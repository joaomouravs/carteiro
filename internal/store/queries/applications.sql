-- name: CreateApplication :one
INSERT INTO applications (name)
VALUES ($1)
RETURNING *;

-- name: GetApplication :one
SELECT * FROM applications
WHERE id = $1;
