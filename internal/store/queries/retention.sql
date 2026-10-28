-- Retention (FR-21). Each query deletes one batch, oldest first, so a purge
-- is many short transactions instead of one that holds locks and writes WAL
-- for minutes. The cutoff is now() - retention_ms, by the database clock.

-- PurgeEvents deletes old events, and with them, by ON DELETE CASCADE, their
-- deliveries and attempts. An event with a delivery still pending or in
-- flight (one replayed recently, say) is kept: retention never cancels work.
-- name: PurgeEvents :execrows
DELETE FROM events
WHERE id IN (
    SELECT e.id FROM events e
    WHERE e.created_at < now() - sqlc.arg('retention_ms')::bigint * interval '1 millisecond'
      AND NOT EXISTS (
          SELECT 1 FROM deliveries d
          WHERE d.event_id = e.id AND d.status IN ('pending', 'in_flight')
      )
    ORDER BY e.created_at
    LIMIT sqlc.arg('batch')::bigint
);

-- PurgeAttempts deletes old attempts of the events PurgeEvents keeps, such as
-- the first attempts of a delivery replayed long after it died.
-- name: PurgeAttempts :execrows
DELETE FROM attempts
WHERE id IN (
    SELECT id FROM attempts
    WHERE started_at < now() - sqlc.arg('retention_ms')::bigint * interval '1 millisecond'
    ORDER BY started_at
    LIMIT sqlc.arg('batch')::bigint
);

-- PurgeExpiredSecrets deletes secrets whose rotation grace period is over:
-- nothing is signed with them again. There are few, so I don't batch these.
-- name: PurgeExpiredSecrets :execrows
DELETE FROM endpoint_secrets WHERE expires_at <= now();

-- TryPurgeLock takes a session-level advisory lock, so only one process
-- purges at a time; the others skip the round. PostgreSQL releases it when
-- the session ends, so a crashed purger cannot hold it forever. Any fixed
-- number works as the key; I used the bytes of "purge" as an integer.
-- name: TryPurgeLock :one
SELECT pg_try_advisory_lock(483006768997) AS locked;

-- name: ReleasePurgeLock :exec
SELECT pg_advisory_unlock(483006768997);
