-- +goose Up
-- Preserve current state, history, media identity, and source audit records.
-- Refuse to discard an unconfirmed explicit mutation from an older deployment.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM mapping_operations WHERE publication_mode!='native' AND phase NOT IN ('complete','failed'))
 OR EXISTS(SELECT 1 FROM library_removals WHERE publication_mode!='native' AND NOT retired AND phase!='complete')
 OR EXISTS(SELECT 1 FROM watch_writes WHERE action!='flag' AND phase IN ('queued','ready','cancelling')) THEN
   RAISE EXCEPTION 'Finish or cancel pending external publication/watch edits before upgrading';
 END IF;
 IF to_regclass('river_job') IS NOT NULL THEN
   UPDATE river_job SET state='cancelled',finalized_at=now()
   WHERE kind IN ('watch_write','watch_flag_dispatch') AND state NOT IN ('completed','cancelled','discarded');
 END IF;
END $$;
-- +goose StatementEnd

UPDATE watch_current SET source='RetainedImport' WHERE source='KeepJellyfin';
UPDATE watch_current SET needs_review=false WHERE needs_review;
UPDATE watch_history SET kind='kept_imported' WHERE kind='kept_jellyfin';
ALTER TABLE watch_history DROP COLUMN link_id, DROP COLUMN event_id, DROP COLUMN write_id;
ALTER TABLE watch_current DROP COLUMN link_id, DROP COLUMN external_sequence;
DROP TABLE watch_flag_sync, watch_writes, watch_external, watch_observations, watch_links, watch_server_users, watch_servers;

ALTER TABLE mapping_candidates DROP COLUMN jellyfin_confirmed;
ALTER TABLE mapping_operations DROP COLUMN restore_removal_id;
ALTER TABLE mapping_submissions DROP COLUMN restore_removal_id;
ALTER TABLE library_removals DROP COLUMN jellyfin_confirmed, DROP COLUMN snapshot;
ALTER TABLE library_removals ALTER COLUMN phase SET DEFAULT 'removing';
ALTER TABLE library_removals ALTER COLUMN publication_mode SET DEFAULT 'native';
ALTER TABLE mapping_operations ALTER COLUMN publication_mode SET DEFAULT 'native';
ALTER TABLE mapping_submissions ALTER COLUMN publication_mode SET DEFAULT 'native';
DROP VIEW publication_media;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Restore the pre-upgrade backup to return to the retired integration'; END $$;
-- +goose StatementEnd
