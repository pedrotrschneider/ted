-- +goose Up
CREATE TABLE catalog_episode_schedules (
 title_id text PRIMARY KEY,
 episodes jsonb NOT NULL CHECK(jsonb_typeof(episodes)='array'),
 observed_at timestamptz NOT NULL,
 fetched_at timestamptz NOT NULL
);
-- +goose Down
DROP TABLE catalog_episode_schedules;
