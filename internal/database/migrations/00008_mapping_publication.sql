-- +goose Up
-- Active mappings remain the existing read/write contract. Versions retain both
-- staged candidates and prior provenance without exposing either as active.
ALTER TABLE media_mappings RENAME TO mapping_versions;
ALTER TABLE mapping_versions DROP CONSTRAINT movie_mappings_movie_id_key;
ALTER TABLE mapping_versions ADD COLUMN active boolean NOT NULL DEFAULT true;
CREATE UNIQUE INDEX one_active_mapping ON mapping_versions(movie_id) WHERE active;
CREATE VIEW media_mappings AS SELECT id,movie_id,origin,rd_account_id,torrent_id,info_hash,file_id,file_path,file_bytes,source_snapshot,created_by,created_at FROM mapping_versions WHERE active WITH LOCAL CHECK OPTION;

CREATE TABLE mapping_operations (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 title_id text NOT NULL,
 kind text NOT NULL CHECK(kind IN ('movie','series')),
 actor jsonb NOT NULL,
 request jsonb NOT NULL,
 phase text NOT NULL DEFAULT 'queued' CHECK(phase IN ('queued','validating','publishing','complete','failed')),
 error_code text NOT NULL DEFAULT '',
 job_id bigint,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX one_pending_mapping_operation ON mapping_operations(title_id,kind) WHERE phase NOT IN ('complete','failed');
CREATE TABLE mapping_candidates (
 operation_id uuid NOT NULL REFERENCES mapping_operations(id),
 media_id uuid NOT NULL REFERENCES catalog_media(id),
 mapping_id uuid NOT NULL UNIQUE REFERENCES mapping_versions(id),
 previous_id uuid REFERENCES mapping_versions(id),
 probe jsonb,
 PRIMARY KEY(operation_id,media_id)
);
CREATE TABLE orphan_files (
 media_id uuid NOT NULL REFERENCES catalog_media(id),
 generation bigint NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(media_id,generation)
);
-- A candidate catalog can be materialized by Jellyfin before TED activates it.
-- Failed validation never enters this view. Unknown remote outcomes stay pending.
CREATE VIEW publication_media AS
 SELECT m.movie_id,m.id AS mapping_id,m.file_bytes,s.probe FROM media_mappings m JOIN streaming_items s ON s.movie_id=m.movie_id WHERE s.published AND NOT EXISTS(SELECT 1 FROM mapping_candidates c JOIN mapping_operations o ON o.id=c.operation_id WHERE c.media_id=m.movie_id AND o.phase='publishing')
 UNION ALL
 SELECT c.media_id,c.mapping_id,m.file_bytes,c.probe FROM mapping_candidates c JOIN mapping_operations o ON o.id=c.operation_id JOIN mapping_versions m ON m.id=c.mapping_id WHERE o.phase='publishing';

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Mapping version rollback requires an explicit data-preserving migration'; END $$;
-- +goose StatementEnd
