-- +goose Up
ALTER TABLE stremio_sync_baselines ADD COLUMN completion jsonb NOT NULL DEFAULT 'null';
CREATE TABLE stremio_episode_recency (
    user_id bigint NOT NULL REFERENCES users(id),
    media_id uuid NOT NULL REFERENCES catalog_media(id),
    stremio_account text NOT NULL REFERENCES stremio_accounts(id),
    watched_at timestamptz NOT NULL,
    PRIMARY KEY(user_id,media_id)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'episode recency is forward-only; preserve watch state and integration observations';
END $$;
-- +goose StatementEnd
