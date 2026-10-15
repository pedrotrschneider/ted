-- +goose Up
ALTER TABLE stremio_sync_links ADD COLUMN enrolled_at timestamptz;
UPDATE stremio_sync_links SET enrolled_at=initialized_at;
-- Enroll previously excluded entries on the next full snapshot without resetting
-- accepted baselines for the rest of the account or deleting historical evidence.
UPDATE stremio_sync_baselines b SET entries=COALESCE((
 SELECT jsonb_agg(e.value) FROM jsonb_array_elements(b.entries) e
 WHERE NOT EXISTS(SELECT 1 FROM stremio_import_exclusions x JOIN stremio_links l ON l.user_id=x.user_id AND l.account_id=x.account_id
 WHERE l.id=b.link_id AND x.excluded AND x.entry_id=e.value->>'id')
),'[]'::jsonb);
UPDATE stremio_import_exclusions SET excluded=false,updated_at=now() WHERE excluded;
UPDATE stremio_previews SET phase='cancelled',updated_at=now() WHERE phase IN('queued','resolving');
UPDATE stremio_imports SET phase='cancelled' WHERE phase IN('queued','running');
-- River is installed after application migrations on a fresh database.
-- +goose StatementBegin
DO $$ BEGIN
 IF to_regclass('river_job') IS NOT NULL THEN
  UPDATE river_job SET state='cancelled',finalized_at=now() WHERE kind IN('stremio_preview','stremio_import') AND state NOT IN('completed','cancelled','discarded');
 END IF;
END $$;
-- +goose StatementEnd
UPDATE stremio_poll_state SET next_poll=now();
-- Resolution progress is not an accepted remote snapshot. It lets large first
-- reads resume after cancellation without exposing partial watched history.
CREATE TABLE stremio_resolved_titles (
 account_id text NOT NULL REFERENCES stremio_accounts(id),
 title_id text NOT NULL,
 value jsonb NOT NULL,
 PRIMARY KEY(account_id,title_id)
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Full-account sync requires a data-preserving forward migration'; END $$;
-- +goose StatementEnd
