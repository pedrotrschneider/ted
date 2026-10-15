-- +goose Up
CREATE TABLE stremio_accounts (
 id text PRIMARY KEY,
 email text NOT NULL,
 auth_key text NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE stremio_links (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 user_id bigint NOT NULL REFERENCES users(id),
 account_id text NOT NULL REFERENCES stremio_accounts(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 retired_at timestamptz
);
CREATE UNIQUE INDEX stremio_active_user ON stremio_links(user_id) WHERE retired_at IS NULL;
CREATE TABLE stremio_previews (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 link_id uuid NOT NULL REFERENCES stremio_links(id),
 phase text NOT NULL DEFAULT 'queued' CHECK(phase IN ('queued','resolving','ready','failed','cancelled')),
 error_code text NOT NULL DEFAULT '',
 captured_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX stremio_pending_preview ON stremio_previews(link_id) WHERE phase IN ('queued','resolving');
CREATE TABLE stremio_preview_titles (
 preview_id uuid NOT NULL REFERENCES stremio_previews(id),
 title_id text NOT NULL,
 raw jsonb NOT NULL,
 resolved boolean NOT NULL DEFAULT false,
 issue text NOT NULL DEFAULT '',
 entries jsonb NOT NULL DEFAULT '[]',
 episode_ids jsonb NOT NULL DEFAULT '[]',
 PRIMARY KEY(preview_id,title_id)
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Stremio snapshots and links require a data-preserving migration'; END $$;
-- +goose StatementEnd
