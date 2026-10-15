-- +goose Up
ALTER TABLE stremio_previews ADD COLUMN selection_revision bigint NOT NULL DEFAULT 1;
ALTER TABLE stremio_preview_titles ADD COLUMN selected jsonb NOT NULL DEFAULT '[]';
CREATE TABLE stremio_imports (
 preview_id uuid PRIMARY KEY REFERENCES stremio_previews(id),
 link_id uuid NOT NULL UNIQUE REFERENCES stremio_links(id),
 phase text NOT NULL DEFAULT 'queued' CHECK(phase IN ('queued','running','complete','cancelled')),
 created_at timestamptz NOT NULL DEFAULT now(),
 completed_at timestamptz
);
CREATE TABLE stremio_import_entries (
 preview_id uuid NOT NULL REFERENCES stremio_imports(preview_id),
 title_id text NOT NULL,
 entry_id text NOT NULL,
 metadata jsonb NOT NULL,
 selected boolean NOT NULL,
 done boolean NOT NULL DEFAULT false,
 PRIMARY KEY(preview_id,entry_id)
);
CREATE TABLE stremio_import_exclusions (
 user_id bigint NOT NULL REFERENCES users(id),
 account_id text NOT NULL REFERENCES stremio_accounts(id),
 entry_id text NOT NULL,
 preview_id uuid NOT NULL REFERENCES stremio_previews(id),
 excluded boolean NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(user_id,account_id,entry_id)
);
-- History/state can exist before the user links a Jellyfin profile.
ALTER TABLE watch_current ALTER COLUMN link_id DROP NOT NULL;
ALTER TABLE watch_history ALTER COLUMN link_id DROP NOT NULL;
ALTER TABLE watch_history ADD COLUMN stremio_account text REFERENCES stremio_accounts(id);
ALTER TABLE watch_history ADD COLUMN stremio_preview uuid REFERENCES stremio_previews(id);
CREATE UNIQUE INDEX watch_stremio_initial_import ON watch_history(user_id,media_id,stremio_account) WHERE kind='stremio_import';
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Stremio imports require a data-preserving migration'; END $$;
-- +goose StatementEnd
