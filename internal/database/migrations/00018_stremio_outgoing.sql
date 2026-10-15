-- +goose Up
CREATE TABLE stremio_outgoing (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 link_id uuid NOT NULL REFERENCES stremio_links(id),
 account_id text NOT NULL REFERENCES stremio_accounts(id),
 user_id bigint NOT NULL REFERENCES users(id),
 media_id uuid NOT NULL REFERENCES catalog_media(id),
 identity text NOT NULL,
 title_id text NOT NULL,
 played boolean NOT NULL,
 phase text NOT NULL DEFAULT 'pending' CHECK(phase IN ('pending','prepared','applied','conflict','cancelled')),
 before_record jsonb,
 desired_record jsonb,
 verified_record jsonb,
 attempts integer NOT NULL DEFAULT 0,
 error_code text NOT NULL DEFAULT '',
 next_attempt timestamptz NOT NULL DEFAULT now(),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX stremio_outgoing_pending ON stremio_outgoing(account_id,id) WHERE phase IN ('pending','prepared');
CREATE TABLE stremio_outgoing_echoes (
 outgoing_id bigint NOT NULL REFERENCES stremio_outgoing(id),
 link_id uuid NOT NULL REFERENCES stremio_links(id),
 consumed boolean NOT NULL DEFAULT false,
 PRIMARY KEY(outgoing_id,link_id)
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Outgoing Stremio state requires a data-preserving migration'; END $$;
-- +goose StatementEnd
