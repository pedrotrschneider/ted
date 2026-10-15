-- +goose Up
CREATE TABLE stremio_poll_state (
 account_id text PRIMARY KEY REFERENCES stremio_accounts(id),
 next_poll timestamptz NOT NULL DEFAULT now(),
 last_success timestamptz,
 error_code text NOT NULL DEFAULT '',
 failures integer NOT NULL DEFAULT 0
);
CREATE TABLE stremio_poll_snapshots (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 account_id text NOT NULL REFERENCES stremio_accounts(id),
 captured_at timestamptz NOT NULL DEFAULT now(),
 titles jsonb NOT NULL
);
CREATE INDEX stremio_poll_latest ON stremio_poll_snapshots(account_id,id DESC);
CREATE TABLE stremio_sync_links (
 link_id uuid PRIMARY KEY REFERENCES stremio_links(id),
 initialized_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE stremio_sync_baselines (
 link_id uuid NOT NULL REFERENCES stremio_links(id),
 title_id text NOT NULL,
 entries jsonb NOT NULL,
 issue text NOT NULL DEFAULT '',
 observed_at timestamptz NOT NULL,
 PRIMARY KEY(link_id,title_id)
);
CREATE TABLE stremio_poll_deliveries (
 snapshot_id bigint NOT NULL REFERENCES stremio_poll_snapshots(id),
 link_id uuid NOT NULL REFERENCES stremio_links(id),
 after_title text NOT NULL DEFAULT '',
 after_entry text NOT NULL DEFAULT '',
 complete boolean NOT NULL DEFAULT false,
 PRIMARY KEY(snapshot_id,link_id)
);
-- Watched-only changes awaiting an eligible Jellyfin item/observation.
CREATE TABLE watch_flag_sync (
 stremio_link uuid NOT NULL REFERENCES stremio_links(id),
 user_id bigint NOT NULL REFERENCES users(id),
 media_id uuid NOT NULL REFERENCES catalog_media(id),
 revision bigint NOT NULL,
 phase text NOT NULL DEFAULT 'pending' CHECK(phase IN ('pending','applied','superseded')),
 PRIMARY KEY(user_id,media_id)
);
ALTER TABLE watch_writes DROP CONSTRAINT watch_writes_action_check;
ALTER TABLE watch_writes ADD CONSTRAINT watch_writes_action_check CHECK(action IN ('watched','unwatched','reset','restore','flag'));
ALTER TABLE watch_writes ADD COLUMN current_revision bigint;
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Stremio polling requires a data-preserving migration'; END $$;
-- +goose StatementEnd
