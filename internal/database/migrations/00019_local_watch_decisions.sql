-- +goose Up
-- Deferred Jellyfin delivery can originate in TED without a Stremio identity.
ALTER TABLE watch_flag_sync ALTER COLUMN stremio_link DROP NOT NULL;
CREATE TABLE watch_title_catalog (
 title_id text NOT NULL,
 kind text NOT NULL CHECK(kind IN ('movie','series')),
 entries jsonb NOT NULL,
 regular_episode_count integer NOT NULL CHECK(regular_episode_count>=0),
 PRIMARY KEY(title_id,kind)
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Local watched decisions require a data-preserving migration'; END $$;
-- +goose StatementEnd
