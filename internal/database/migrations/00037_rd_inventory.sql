-- +goose Up
CREATE TABLE rd_inventory_state (
 scope text PRIMARY KEY,
 account_id text NOT NULL DEFAULT '',
 ready boolean NOT NULL DEFAULT false,
 total integer NOT NULL DEFAULT 0 CHECK(total>=0),
 generation bigint NOT NULL DEFAULT 0,
 phase text NOT NULL DEFAULT 'new',
 checked_at timestamptz,
 full_scan_at timestamptz,
 full_rebuilt_at timestamptz,
 error_code text NOT NULL DEFAULT ''
);
CREATE TABLE rd_inventory_torrents (
 scope text NOT NULL REFERENCES rd_inventory_state(scope),
 id text NOT NULL,
 ordinal integer NOT NULL CHECK(ordinal>=0),
 hash text NOT NULL,
 summary jsonb NOT NULL,
 detail jsonb,
 generation bigint NOT NULL,
 detail_generation bigint NOT NULL DEFAULT 0,
 observed_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(scope,id)
);
CREATE INDEX rd_inventory_hash ON rd_inventory_torrents(scope,hash);
CREATE INDEX rd_inventory_order ON rd_inventory_torrents(scope,ordinal);
-- Durable ownership is recorded before selecting all files, independently of a
-- playback session. A cancelled caller must not lose an already added torrent.
CREATE TABLE rd_addon_torrents (
 scope text NOT NULL,
 hash text NOT NULL,
 owned_id text NOT NULL DEFAULT '',
 add_attempted boolean NOT NULL DEFAULT false,
 detail jsonb,
 verified_at timestamptz,
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(scope,hash)
);
ALTER TABLE native_playback_sessions ADD COLUMN expected_duration_ms bigint NOT NULL DEFAULT 0 CHECK(expected_duration_ms>=0 AND expected_duration_ms<=604800000);
ALTER TABLE native_addon_playback ADD COLUMN placeholder boolean NOT NULL DEFAULT false;
ALTER TABLE native_addon_playback ADD COLUMN observed_duration_ms bigint NOT NULL DEFAULT 0;
ALTER TABLE native_watch_events DROP CONSTRAINT native_watch_events_outcome_check;
ALTER TABLE native_watch_events ADD CONSTRAINT native_watch_events_outcome_check CHECK(outcome IN ('applied','stale','ignored'));
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'RD cache/source migration is forward-only; preserve source ownership and playback history';
END $$;
-- +goose StatementEnd
