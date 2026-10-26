-- +goose Up
-- Keep migration 46 for installations that ran the cache preview. Stop the old
-- application before upgrading; only disposable cache state is retired here.
-- +goose StatementBegin
DO $$ BEGIN
 IF to_regclass(format('%I.river_job', current_schema())) IS NOT NULL THEN
   EXECUTE format('UPDATE %I.river_job SET state=''cancelled'',finalized_at=now()
     WHERE kind IN (''playback_cache_fill'',''playback_cache_index'',''playback_cache_cleanup'')
     AND state NOT IN (''completed'',''cancelled'',''discarded'')', current_schema());
 END IF;
END $$;
-- +goose StatementEnd

DROP TABLE playback_cache_references, playback_cache_timeline, playback_cache_chunks,
 playback_cache_entries, playback_cache_settings;
DROP FUNCTION playback_cache_bytes_changed();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'playback cache retirement is forward-only'; END $$;
-- +goose StatementEnd
