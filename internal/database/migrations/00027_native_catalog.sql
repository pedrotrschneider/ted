-- +goose Up
-- Discovery metadata is independent of shared library membership and mappings.
CREATE TABLE native_catalog_titles (
    kind text NOT NULL CHECK (kind IN ('movie','series')),
    title_id text NOT NULL,
    metadata jsonb NOT NULL CHECK (jsonb_typeof(metadata)='object'),
    observed_at timestamptz NOT NULL,
    PRIMARY KEY(kind,title_id)
);
CREATE INDEX watch_current_personal_recent ON watch_current(user_id,updated_at DESC,media_id DESC);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'native catalog migration is forward-only; preserve retained discovery metadata';
END $$;
-- +goose StatementEnd
