-- +goose Up
CREATE TABLE playback_cache_settings (
 id boolean PRIMARY KEY DEFAULT true CHECK(id),
 enabled boolean NOT NULL DEFAULT true,
 max_bytes bigint NOT NULL DEFAULT 214748364800 CHECK(max_bytes BETWEEN 1073741824 AND 109951162777600),
 retention_minutes integer NOT NULL DEFAULT 10 CHECK(retention_minutes BETWEEN 1 AND 1440),
 used_bytes bigint NOT NULL DEFAULT 0 CHECK(used_bytes>=0),
 revision bigint NOT NULL DEFAULT 1
);
INSERT INTO playback_cache_settings(id) VALUES(true);
CREATE TABLE playback_cache_entries (
 cache_key text PRIMARY KEY CHECK(cache_key ~ '^[a-f0-9]{64}$'),
 file_bytes bigint NOT NULL CHECK(file_bytes>0),
 duration_ms bigint NOT NULL DEFAULT 0 CHECK(duration_ms BETWEEN 0 AND 604800000),
 cursor_bytes bigint NOT NULL DEFAULT 0,
 demand_until timestamptz NOT NULL DEFAULT '-infinity',
 unused_since timestamptz DEFAULT now(),
 deleting boolean NOT NULL DEFAULT false,
 job_id bigint,
 index_job_id bigint,
 indexed boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE playback_cache_references (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 session_id uuid UNIQUE REFERENCES native_playback_sessions(id),
 source_id uuid REFERENCES native_addon_sources(id),
 parent_session_id uuid REFERENCES native_playback_sessions(id),
 user_id bigint NOT NULL REFERENCES users(id),
 family_id uuid NOT NULL REFERENCES native_token_families(id),
 device_id text NOT NULL,
 cache_key text NOT NULL REFERENCES playback_cache_entries(cache_key) ON DELETE CASCADE,
 active_until timestamptz NOT NULL DEFAULT now(),
 released boolean NOT NULL DEFAULT false,
 CHECK((session_id IS NOT NULL AND source_id IS NULL AND parent_session_id IS NULL) OR
       (session_id IS NULL AND source_id IS NOT NULL AND parent_session_id IS NOT NULL)),
 UNIQUE(source_id,parent_session_id)
);
CREATE INDEX playback_cache_references_entry ON playback_cache_references(cache_key);
CREATE TABLE playback_cache_chunks (
 cache_key text NOT NULL REFERENCES playback_cache_entries(cache_key) ON DELETE CASCADE,
 chunk_index bigint NOT NULL CHECK(chunk_index>=0),
 bytes bigint NOT NULL CHECK(bytes>0),
 ready boolean NOT NULL DEFAULT false,
 PRIMARY KEY(cache_key,chunk_index)
);
CREATE TABLE playback_cache_timeline (
 cache_key text NOT NULL REFERENCES playback_cache_entries(cache_key) ON DELETE CASCADE,
 byte_offset bigint NOT NULL CHECK(byte_offset>=0),
 time_ms bigint NOT NULL CHECK(time_ms BETWEEN 0 AND 604800000),
 PRIMARY KEY(cache_key,byte_offset)
);
-- +goose StatementBegin
CREATE FUNCTION playback_cache_bytes_changed() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE delta bigint;
BEGIN
 IF TG_OP='INSERT' THEN delta:=NEW.bytes;
 ELSIF TG_OP='DELETE' THEN delta:=-OLD.bytes;
 ELSE delta:=NEW.bytes-OLD.bytes;
 END IF;
 IF delta<>0 THEN UPDATE playback_cache_settings SET used_bytes=used_bytes+delta WHERE id; END IF;
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER playback_cache_bytes AFTER INSERT OR UPDATE OR DELETE ON playback_cache_chunks
FOR EACH ROW EXECUTE FUNCTION playback_cache_bytes_changed();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'playback cache migration is forward-only'; END $$;
-- +goose StatementEnd
