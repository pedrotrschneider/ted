-- +goose Up
CREATE TABLE watch_servers (
 id text PRIMARY KEY CHECK(length(id) BETWEEN 1 AND 128),
 stream_id uuid NOT NULL UNIQUE,
 name text NOT NULL,
 last_sequence bigint NOT NULL DEFAULT 0 CHECK(last_sequence>=0),
 last_seen timestamptz NOT NULL DEFAULT now(),
 pending bigint NOT NULL DEFAULT 0,
 error_code text NOT NULL DEFAULT ''
);
CREATE TABLE watch_server_users (
 server_id text NOT NULL REFERENCES watch_servers(id),
 jellyfin_user_id uuid NOT NULL,
 name text NOT NULL,
 eligible boolean NOT NULL,
 PRIMARY KEY(server_id,jellyfin_user_id)
);
CREATE TABLE watch_links (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 user_id bigint NOT NULL REFERENCES users(id),
 server_id text NOT NULL,
 jellyfin_user_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 retired_at timestamptz,
 FOREIGN KEY(server_id,jellyfin_user_id) REFERENCES watch_server_users(server_id,jellyfin_user_id)
);
CREATE UNIQUE INDEX watch_active_user ON watch_links(user_id) WHERE retired_at IS NULL;
CREATE UNIQUE INDEX watch_active_profile ON watch_links(server_id,jellyfin_user_id) WHERE retired_at IS NULL;
CREATE TABLE watch_observations (
 server_id text NOT NULL REFERENCES watch_servers(id),
 sequence bigint NOT NULL,
 event_id uuid NOT NULL UNIQUE,
 envelope jsonb NOT NULL,
 received_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(server_id,sequence)
);
CREATE TABLE watch_current (
 user_id bigint NOT NULL REFERENCES users(id),
 media_id uuid NOT NULL REFERENCES catalog_media(id),
 link_id uuid NOT NULL REFERENCES watch_links(id),
 state jsonb NOT NULL,
 revision bigint NOT NULL DEFAULT 1,
 source text NOT NULL,
 observed_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT now(),
 needs_review boolean NOT NULL DEFAULT false,
 PRIMARY KEY(user_id,media_id)
);
CREATE TABLE watch_history (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 user_id bigint NOT NULL REFERENCES users(id),
 media_id uuid NOT NULL REFERENCES catalog_media(id),
 link_id uuid NOT NULL REFERENCES watch_links(id),
 event_id uuid NOT NULL UNIQUE REFERENCES watch_observations(event_id),
 kind text NOT NULL,
 state jsonb NOT NULL,
 observed_at timestamptz NOT NULL,
 received_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX watch_history_user ON watch_history(user_id,id DESC);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Watch history and observations require a data-preserving migration'; END $$;
-- +goose StatementEnd
