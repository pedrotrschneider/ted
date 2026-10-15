-- +goose Up
ALTER TABLE catalog_movies RENAME TO catalog_media;
ALTER TABLE movie_mappings RENAME TO media_mappings;
ALTER TABLE catalog_media DROP CONSTRAINT catalog_movies_cinemeta_id_check;
ALTER TABLE catalog_media ADD COLUMN kind text NOT NULL DEFAULT 'movie' CHECK (kind IN ('movie','episode'));
ALTER TABLE catalog_media ADD CONSTRAINT media_catalog_id CHECK (
 (kind='movie' AND cinemeta_id ~ '^tt[0-9]{7,12}$') OR
 (kind='episode' AND cinemeta_id ~ '^tt[0-9]{7,12}:[0-9]{1,4}:[0-9]{1,5}$'));
CREATE TABLE catalog_series (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 cinemeta_id text NOT NULL UNIQUE CHECK (cinemeta_id ~ '^tt[0-9]{7,12}$'),
 category text NOT NULL CHECK (category IN ('shows','anime')),
 metadata jsonb NOT NULL
);
CREATE TABLE catalog_seasons (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 series_id uuid NOT NULL REFERENCES catalog_series(id),
 number integer NOT NULL CHECK (number BETWEEN 0 AND 9999),
 UNIQUE(series_id,number)
);
CREATE TABLE catalog_episodes (
 media_id uuid PRIMARY KEY REFERENCES catalog_media(id),
 season_id uuid NOT NULL REFERENCES catalog_seasons(id),
 number integer NOT NULL CHECK (number BETWEEN 0 AND 99999),
 UNIQUE(season_id,number)
);
-- Existing movie_id SQL columns and River argument keys remain stable for
-- interrupted work; they reference a playable media UUID for either type.
-- +goose Down
-- Refuse rollback with episode data instead of silently deleting mappings.
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS(SELECT 1 FROM catalog_episodes) THEN RAISE EXCEPTION 'Remove episode data explicitly before rolling back series support'; END IF; END $$;
-- +goose StatementEnd
DROP TABLE catalog_episodes;
DROP TABLE catalog_seasons;
DROP TABLE catalog_series;
ALTER TABLE catalog_media DROP CONSTRAINT media_catalog_id;
ALTER TABLE catalog_media DROP COLUMN kind;
ALTER TABLE catalog_media ADD CONSTRAINT catalog_movies_cinemeta_id_check CHECK (cinemeta_id ~ '^tt[0-9]{7,12}$');
ALTER TABLE catalog_media RENAME TO catalog_movies;
ALTER TABLE media_mappings RENAME TO movie_mappings;
