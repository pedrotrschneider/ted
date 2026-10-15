-- +goose Up
CREATE TABLE streaming_items (
 movie_id uuid PRIMARY KEY REFERENCES catalog_movies(id),
 mapping_id uuid NOT NULL UNIQUE REFERENCES movie_mappings(id),
 phase text NOT NULL CHECK (phase IN ('queued','preparing','probing','ready','failed','cancelled')),
 job_id bigint,
 error_code text NOT NULL DEFAULT '',
 probe jsonb,
 published boolean NOT NULL DEFAULT false,
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK (NOT published OR (phase='ready' AND probe IS NOT NULL))
);
CREATE TABLE streaming_links (
 mapping_id uuid PRIMARY KEY REFERENCES movie_mappings(id),
 credential_scope text NOT NULL,
 download_url text NOT NULL,
 expires_at timestamptz NOT NULL
);
-- Operational recovery references never replace the original source provenance.
CREATE TABLE streaming_recovery (
 mapping_id uuid PRIMARY KEY REFERENCES movie_mappings(id),
 torrent_id text NOT NULL,
 rd_account_id text NOT NULL
);
-- +goose Down
DROP TABLE streaming_recovery;
DROP TABLE streaming_links;
DROP TABLE streaming_items;
