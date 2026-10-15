-- +goose Up
CREATE TABLE stremio_addon_collections (
 account_id text PRIMARY KEY REFERENCES stremio_accounts(id),
 account_updated_at timestamptz NOT NULL,
 payload jsonb NOT NULL CHECK (jsonb_typeof(payload)='array'),
 observed_at timestamptz NOT NULL,
 error_code text NOT NULL DEFAULT ''
);
CREATE TABLE native_addon_sources (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 user_id bigint NOT NULL REFERENCES users(id),
 link_id uuid NOT NULL REFERENCES stremio_links(id),
 account_id text NOT NULL REFERENCES stremio_accounts(id),
 account_updated_at timestamptz NOT NULL,
 media_kind text NOT NULL CHECK (media_kind IN ('movie','episode')),
 media_identity text NOT NULL,
 addon_key text NOT NULL,
 descriptor jsonb NOT NULL,
 source jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz NOT NULL DEFAULT now()+interval '15 minutes'
);
CREATE INDEX native_addon_sources_owner ON native_addon_sources(user_id,link_id);
ALTER TABLE native_playback_sessions ALTER COLUMN mapping_id DROP NOT NULL;
ALTER TABLE native_playback_sessions DROP CONSTRAINT native_playback_sessions_duration_ms_check;
ALTER TABLE native_playback_sessions ADD CONSTRAINT native_playback_sessions_duration_ms_check CHECK (duration_ms>=0 AND duration_ms<=604800000);
ALTER TABLE native_playback_sessions ADD COLUMN addon_source_id uuid REFERENCES native_addon_sources(id);
ALTER TABLE native_playback_sessions ADD CONSTRAINT native_playback_sessions_source_check CHECK ((mapping_id IS NOT NULL)::int+(addon_source_id IS NOT NULL)::int=1);
CREATE TABLE native_addon_playback (
 session_id uuid PRIMARY KEY REFERENCES native_playback_sessions(id),
 source jsonb NOT NULL,
 file_size bigint NOT NULL CHECK (file_size>0),
 fingerprint text NOT NULL,
 etag text NOT NULL DEFAULT '',
 updated_at timestamptz NOT NULL DEFAULT now()
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'native add-on playback migration is forward-only; preserve session history and source provenance';
END $$;
-- +goose StatementEnd
