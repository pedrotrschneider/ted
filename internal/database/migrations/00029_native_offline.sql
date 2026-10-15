-- +goose Up
ALTER TABLE native_playback_sessions ADD COLUMN origin text NOT NULL DEFAULT 'live' CHECK (origin IN ('live','offline'));
ALTER TABLE native_playback_sessions ADD COLUMN offline_registration jsonb;
ALTER TABLE native_playback_sessions ADD COLUMN previous_offline_session uuid REFERENCES native_playback_sessions(id);
ALTER TABLE native_playback_sessions DROP CONSTRAINT native_playback_sessions_source_check;
ALTER TABLE native_playback_sessions ADD CONSTRAINT native_playback_sessions_source_check CHECK (
 (origin='live' AND offline_registration IS NULL AND ((mapping_id IS NOT NULL)::int+(addon_source_id IS NOT NULL)::int=1)) OR
 (origin='offline' AND offline_registration IS NOT NULL AND mapping_id IS NULL AND addon_source_id IS NULL)
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'offline replay migration is forward-only; preserve registered sessions and personal history';
END $$;
-- +goose StatementEnd
