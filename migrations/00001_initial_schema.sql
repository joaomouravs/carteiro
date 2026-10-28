-- Initial schema. See docs/architecture.md for the data model.
--
-- Conventions I follow:
-- * Primary keys are UUIDv7 (native in PostgreSQL 18): globally unique like
--   random UUIDs, but time-ordered, so inserts go to the end of the B-tree
--   index instead of landing on random pages.
-- * I store statuses as text with CHECK constraints, not enum types: changing
--   an enum (removing or renaming a value) is awkward in a migration, while a
--   CHECK constraint is dropped and recreated in one statement.
-- * Every timestamp is timestamptz, stored in UTC.

-- +goose Up

CREATE TABLE applications (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    name       text        NOT NULL CHECK (name <> ''),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE api_keys (
    id             uuid        PRIMARY KEY DEFAULT uuidv7(),
    application_id uuid        NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
    name           text        NOT NULL DEFAULT '',
    -- The first characters of the key, shown in listings so a key can be
    -- identified without revealing it.
    prefix         text        NOT NULL,
    -- SHA-256 of the full key. A fast hash is enough here, unlike passwords:
    -- the key has 256 random bits, so there is nothing to brute-force.
    hash           bytea       NOT NULL UNIQUE CHECK (length(hash) = 32),
    created_at     timestamptz NOT NULL DEFAULT now(),
    last_used_at   timestamptz,
    revoked_at     timestamptz
);

CREATE INDEX api_keys_application ON api_keys (application_id);

CREATE TABLE endpoints (
    id                   uuid        PRIMARY KEY DEFAULT uuidv7(),
    application_id       uuid        NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
    url                  text        NOT NULL,
    description          text        NOT NULL DEFAULT '',
    -- Empty array means "every event type".
    event_types          text[]      NOT NULL DEFAULT '{}',
    status               text        NOT NULL DEFAULT 'active'
                                     CHECK (status IN ('active', 'disabled')),
    disabled_reason      text,
    consecutive_failures integer     NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
    -- Start of the current failure streak; null while the endpoint is healthy.
    failing_since        timestamptz,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),

    CHECK ((status = 'disabled') = (disabled_reason IS NOT NULL))
);

-- Fan-out reads the active endpoints of one application on every publish.
CREATE INDEX endpoints_active ON endpoints (application_id) WHERE status = 'active';

CREATE TABLE endpoint_secrets (
    id                uuid        PRIMARY KEY DEFAULT uuidv7(),
    endpoint_id       uuid        NOT NULL REFERENCES endpoints (id) ON DELETE CASCADE,
    -- AES-256-GCM ciphertext, nonce included.
    secret_ciphertext bytea       NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    -- Null for the current secret; set to the end of the grace period when the
    -- secret is rotated out.
    expires_at        timestamptz
);

-- An endpoint has exactly one current secret at a time. The database enforces
-- it, so a buggy or concurrent rotation cannot leave two.
CREATE UNIQUE INDEX endpoint_secrets_one_current
    ON endpoint_secrets (endpoint_id) WHERE expires_at IS NULL;

CREATE TABLE events (
    id              uuid        PRIMARY KEY DEFAULT uuidv7(),
    application_id  uuid        NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
    event_type      text        NOT NULL CHECK (event_type <> ''),
    payload         jsonb       NOT NULL,
    idempotency_key text,
    -- SHA-256 of the payload, compared when an idempotency key is reused to
    -- tell a retry (same payload) from a conflict (different payload).
    payload_hash    bytea,
    created_at      timestamptz NOT NULL DEFAULT now(),

    CHECK ((idempotency_key IS NULL) = (payload_hash IS NULL)),
    -- NULLs are distinct in a UNIQUE constraint, so events published without a
    -- key never collide with each other.
    UNIQUE (application_id, idempotency_key)
);

CREATE INDEX events_application_created ON events (application_id, created_at);
-- Retention deletes by age across all applications.
CREATE INDEX events_created ON events (created_at);

-- This table is the queue (ADR 0001).
CREATE TABLE deliveries (
    id               uuid        PRIMARY KEY DEFAULT uuidv7(),
    event_id         uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    endpoint_id      uuid        NOT NULL REFERENCES endpoints (id) ON DELETE CASCADE,
    status           text        NOT NULL DEFAULT 'pending'
                                 CHECK (status IN ('pending', 'in_flight', 'succeeded', 'dead')),
    -- Incremented on every claim; doubles as the fencing token (ADR 0003).
    attempt_count    integer     NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at  timestamptz DEFAULT now(),
    lease_expires_at timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),

    UNIQUE (event_id, endpoint_id),
    -- I let the database enforce the state invariants instead of trusting the code:
    -- a delivery holds a lease exactly while it is in flight, and a pending
    -- delivery always knows when to run.
    CHECK ((status = 'in_flight') = (lease_expires_at IS NOT NULL)),
    CHECK (status <> 'pending' OR next_attempt_at IS NOT NULL)
);

-- Partial indexes: they contain only the rows the workers look for, so they
-- stay small no matter how many succeeded deliveries accumulate.
CREATE INDEX deliveries_due    ON deliveries (next_attempt_at)  WHERE status = 'pending';
CREATE INDEX deliveries_leased ON deliveries (lease_expires_at) WHERE status = 'in_flight';
-- Replaying an endpoint's dead deliveries, and killing its pending ones when
-- it is disabled.
CREATE INDEX deliveries_endpoint_status ON deliveries (endpoint_id, status);

-- Append-only log of every HTTP attempt.
CREATE TABLE attempts (
    id              uuid        PRIMARY KEY DEFAULT uuidv7(),
    delivery_id     uuid        NOT NULL REFERENCES deliveries (id) ON DELETE CASCADE,
    attempt_number  integer     NOT NULL CHECK (attempt_number > 0),
    started_at      timestamptz NOT NULL,
    duration_ms     integer     NOT NULL CHECK (duration_ms >= 0),
    -- Null when no response arrived (timeout, connection refused, blocked IP).
    response_status integer,
    -- First 4 KiB of the response body, made valid UTF-8 before storing.
    response_body   text,
    error           text,

    UNIQUE (delivery_id, attempt_number)
);

CREATE INDEX attempts_started ON attempts (started_at);

-- +goose Down

DROP TABLE attempts;
DROP TABLE deliveries;
DROP TABLE events;
DROP TABLE endpoint_secrets;
DROP TABLE endpoints;
DROP TABLE api_keys;
DROP TABLE applications;
