-- +goose Up
CREATE TABLE native_playback_sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id bigint NOT NULL REFERENCES users(id),
    family_id uuid NOT NULL REFERENCES native_token_families(id),
    device_id text NOT NULL,
    media_id uuid NOT NULL REFERENCES catalog_media(id),
    mapping_id uuid NOT NULL REFERENCES mapping_versions(id),
    duration_ms bigint NOT NULL CHECK (duration_ms > 0 AND duration_ms <= 604800000),
    applied_revision bigint NOT NULL DEFAULT 0,
    last_sequence bigint NOT NULL DEFAULT 0,
    last_event_at timestamptz,
    completed boolean NOT NULL DEFAULT false,
    started boolean NOT NULL DEFAULT false,
    stopped boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX native_playback_sessions_owner ON native_playback_sessions(user_id,device_id);

CREATE TABLE native_watch_events (
    id uuid PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id),
    session_id uuid NOT NULL REFERENCES native_playback_sessions(id),
    sequence bigint NOT NULL CHECK (sequence > 0),
    envelope jsonb NOT NULL,
    outcome text NOT NULL CHECK (outcome IN ('applied','stale')),
    received_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(session_id,sequence)
);
ALTER TABLE watch_history ADD COLUMN native_event_id uuid UNIQUE REFERENCES native_watch_events(id);
ALTER TABLE watch_current ADD COLUMN duration_ms bigint CHECK (duration_ms > 0 AND duration_ms <= 604800000);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'native playback migration is forward-only; preserve personal history and replay protection';
END $$;
-- +goose StatementEnd
