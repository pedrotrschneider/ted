-- +goose Up
CREATE TABLE catalog_movies (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 cinemeta_id text NOT NULL UNIQUE CHECK (cinemeta_id ~ '^tt[0-9]{7,12}$'),
 metadata jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE movie_mappings (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 movie_id uuid NOT NULL UNIQUE REFERENCES catalog_movies(id),
 origin text NOT NULL DEFAULT 'rd_account' CHECK (origin = 'rd_account'),
 rd_account_id text NOT NULL,
 torrent_id text NOT NULL,
 info_hash text NOT NULL CHECK (info_hash ~ '^[a-f0-9]{40}$'),
 file_id bigint NOT NULL CHECK (file_id > 0),
 file_path text NOT NULL,
 file_bytes bigint NOT NULL CHECK (file_bytes > 0),
 source_snapshot jsonb NOT NULL,
 created_by bigint NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT now()
);
-- Draft mappings have no library membership, playback availability, or job side effects.

-- +goose Down
DROP TABLE movie_mappings;
DROP TABLE catalog_movies;
