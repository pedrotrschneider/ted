-- +goose Up
ALTER TABLE native_addon_sources ADD COLUMN shared_source_key text NOT NULL DEFAULT '', ADD COLUMN favorite_key text NOT NULL DEFAULT '', ADD COLUMN favorite_scope text NOT NULL DEFAULT '';
CREATE TABLE native_recent_sources (
 scope text NOT NULL, source_key text NOT NULL CHECK(length(source_key)=64),
 last_used_at timestamptz NOT NULL, event_id uuid NOT NULL REFERENCES native_watch_events(id),
 PRIMARY KEY(scope,source_key)
);
CREATE TABLE native_favorite_sources (
 scope text NOT NULL, favorite_key text NOT NULL CHECK(length(favorite_key)=64), updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(scope,favorite_key)
);
CREATE TABLE stremio_addon_policies (
 id text PRIMARY KEY CHECK(length(id)=64), name text NOT NULL, enabled boolean NOT NULL, revision bigint NOT NULL CHECK(revision>0)
);
-- Recover preferences from immutable accepted start reports. Prepared/download
-- sessions without viewing never count; later progress/replay cannot reorder a
-- viewing. This byte encoding matches addons.sharedKeys, without exposing URLs.
UPDATE native_addon_sources SET
 favorite_scope=CASE WHEN COALESCE(source->>'binge_group','')<>'' AND media_kind='episode'
   THEN 'series:'||split_part(media_identity,':',1) ELSE media_kind||':'||media_identity END,
 favorite_key=encode(sha256(
   convert_to(encode(sha256(convert_to(descriptor->'manifest'->>'id','UTF8')),'hex'),'UTF8') || decode('00','hex') ||
   convert_to(CASE WHEN COALESCE(source->>'binge_group','')<>'' THEN 'group:'||(source->>'binge_group') ELSE 'name:'||COALESCE(source->>'name','') END,'UTF8')
 ),'hex');
WITH starts AS (
 SELECT s.favorite_scope AS scope,s.favorite_key AS source_key,e.id AS event_id,(e.envelope->>'observed_at')::timestamptz AS last_used_at
 FROM native_playback_sessions p JOIN native_addon_sources s ON s.id=p.addon_source_id
 JOIN LATERAL (SELECT id,envelope FROM native_watch_events WHERE session_id=p.id ORDER BY sequence LIMIT 1) e ON true
 WHERE p.started AND s.favorite_key<>'' AND e.envelope->>'observed_at' IS NOT NULL
), distinct_sources AS (
 SELECT DISTINCT ON(scope,source_key) * FROM starts ORDER BY scope,source_key,last_used_at DESC,event_id DESC
), ranked AS (
 SELECT *,row_number() OVER(PARTITION BY scope ORDER BY last_used_at DESC,source_key DESC) AS rank FROM distinct_sources
)
INSERT INTO native_recent_sources(scope,source_key,last_used_at,event_id)
 SELECT scope,source_key,last_used_at,event_id FROM ranked WHERE rank<=3;
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Shared source preferences are durable; restore a backup instead of dropping them'; END $$;
-- +goose StatementEnd
