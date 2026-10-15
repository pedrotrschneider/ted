-- +goose Up
CREATE TABLE provider_settings (
 id boolean PRIMARY KEY DEFAULT true CHECK(id),
 rd_key text NOT NULL DEFAULT '',
 tmdb_token text NOT NULL DEFAULT '',
 tvdb_key text NOT NULL DEFAULT '',
 tvdb_pin text NOT NULL DEFAULT '',
 revision bigint NOT NULL DEFAULT 1,
 updated_at timestamptz NOT NULL DEFAULT now()
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'provider settings are forward-only; preserve saved credentials'; END $$;
-- +goose StatementEnd
