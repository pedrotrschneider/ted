-- +goose Up
CREATE TABLE metadata_series_providers (
 title_id text PRIMARY KEY CHECK (title_id ~ '^tt[0-9]{7,12}$'),
 provider text NOT NULL CHECK (provider IN ('Tmdb','Tvdb')),
 revision bigint NOT NULL DEFAULT 1,
 provider_ids jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(provider_ids)='object'),
 changed_by bigint NOT NULL REFERENCES users(id),
 changed_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE catalog_episode_schedules ADD COLUMN canonical_episodes jsonb;
ALTER TABLE catalog_episode_schedules ADD COLUMN identity_version integer NOT NULL DEFAULT 0;
UPDATE catalog_episode_schedules SET canonical_episodes=episodes;
CREATE TABLE metadata_series_layouts (
 title_id text PRIMARY KEY REFERENCES metadata_series_providers(title_id),
 provider text NOT NULL CHECK(provider IN ('Tmdb','Tvdb')),
 generation bigint NOT NULL,
 episodes jsonb NOT NULL CHECK(jsonb_typeof(episodes)='array')
);
CREATE TABLE metadata_episode_layout (
 episode_id text PRIMARY KEY,
 title_id text NOT NULL REFERENCES metadata_series_layouts(title_id) ON DELETE CASCADE,
 season integer NOT NULL CHECK(season>=0),
 number integer NOT NULL CHECK(number>=0),
 UNIQUE(title_id,season,number)
);
-- +goose StatementBegin
CREATE FUNCTION ted_episode_season(episode text) RETURNS integer LANGUAGE sql STABLE AS $$
 SELECT COALESCE((SELECT season FROM metadata_episode_layout WHERE episode_id=episode),NULLIF(split_part(episode,':',2),'')::int)
$$;
CREATE FUNCTION ted_episode_number(episode text) RETURNS integer LANGUAGE sql STABLE AS $$
 SELECT COALESCE((SELECT number FROM metadata_episode_layout WHERE episode_id=episode),NULLIF(split_part(episode,':',3),'')::int)
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'series metadata choices are forward-only; preserve server preferences';
END $$;
-- +goose StatementEnd
