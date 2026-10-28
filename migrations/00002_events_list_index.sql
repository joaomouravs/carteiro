-- Index the event listing by the column it paginates on.
--
-- GET /v1/events pages by id: WHERE application_id = $1 AND id < $cursor
-- ORDER BY id DESC LIMIT n. The initial schema indexed (application_id,
-- created_at), which matches the filter but not the order. PostgreSQL then
-- walks the primary key backwards and throws away other applications' events.
-- When I measured it with 200,000 events spread over 50 applications, one page
-- of 51 read 2,545 rows. A small application among large ones pays for most of
-- the table.
-- With (application_id, id) the same page reads exactly 51 rows, a backward
-- range scan. UUIDv7 ids are time-ordered, so id order is creation order.

-- +goose Up

DROP INDEX events_application_created;
CREATE INDEX events_application_id ON events (application_id, id);

-- +goose Down

DROP INDEX events_application_id;
CREATE INDEX events_application_created ON events (application_id, created_at);
