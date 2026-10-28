-- Let a replayed delivery get a full retry schedule again (FR-18).
--
-- The backoff counts attempts to decide how long to wait and when to give up.
-- A delivery that died on its 12th attempt and is replayed would otherwise
-- die again on its first failure, since 13 is past the maximum. attempt_count
-- cannot be reset: it is the fencing token (ADR 0003), and attempt numbers
-- are unique in the attempt log. So the replay records where it started, and
-- the backoff counts from there.

-- +goose Up

-- A constant default makes this a metadata-only change since PostgreSQL 11:
-- existing rows are not rewritten, however large the table.
ALTER TABLE deliveries
    ADD COLUMN replayed_at_attempt integer NOT NULL DEFAULT 0,
    ADD CONSTRAINT deliveries_replayed_at_attempt_range
        CHECK (replayed_at_attempt >= 0 AND replayed_at_attempt <= attempt_count);

-- +goose Down

ALTER TABLE deliveries DROP COLUMN replayed_at_attempt;
