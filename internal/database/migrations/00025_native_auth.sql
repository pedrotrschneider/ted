-- +goose Up
CREATE TABLE native_token_families (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id bigint NOT NULL REFERENCES users(id),
    account_version bigint NOT NULL CHECK (account_version > 0),
    device_id text NOT NULL CHECK (length(device_id) BETWEEN 1 AND 128),
    device_name text NOT NULL CHECK (length(device_name) BETWEEN 1 AND 128),
    created_at timestamptz NOT NULL,
    absolute_expires_at timestamptz NOT NULL,
    idle_expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    revocation_reason text CHECK (revocation_reason IN ('logout', 'refresh_reuse')),
    CHECK (created_at < idle_expires_at AND idle_expires_at <= absolute_expires_at),
    CHECK ((revoked_at IS NULL) = (revocation_reason IS NULL))
);
CREATE INDEX native_token_families_user ON native_token_families(user_id);

CREATE TABLE native_refresh_tokens (
    digest bytea PRIMARY KEY CHECK (octet_length(digest) = 32),
    family_id uuid NOT NULL REFERENCES native_token_families(id),
    generation bigint NOT NULL CHECK (generation > 0),
    previous_digest bytea,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at > created_at),
    consumed_at timestamptz,
    UNIQUE (family_id, generation),
    UNIQUE (family_id, digest),
    FOREIGN KEY (family_id, previous_digest) REFERENCES native_refresh_tokens(family_id, digest),
    CHECK ((generation = 1) = (previous_digest IS NULL))
);
-- Expiry/revocation can only reduce validity. Rotation must consume the current
-- row before inserting its successor; a transaction cannot leave two current rows.
CREATE UNIQUE INDEX native_refresh_one_current ON native_refresh_tokens(family_id) WHERE consumed_at IS NULL;

CREATE TABLE native_access_tokens (
    digest bytea PRIMARY KEY CHECK (octet_length(digest) = 32),
    family_id uuid NOT NULL REFERENCES native_token_families(id),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at > created_at)
);
CREATE INDEX native_access_tokens_family ON native_access_tokens(family_id);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'native authentication migration is forward-only; preserve token lineage and revocations';
END $$;
-- +goose StatementEnd
