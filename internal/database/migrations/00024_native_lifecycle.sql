-- +goose Up
-- Existing work belongs to the Jellyfin workflow, even when a later process
-- starts in native mode. Never reinterpret an unknown publication outcome.
ALTER TABLE mapping_submissions ADD COLUMN publication_mode text NOT NULL DEFAULT 'jellyfin' CHECK(publication_mode IN ('native','jellyfin'));
ALTER TABLE mapping_operations ADD COLUMN publication_mode text NOT NULL DEFAULT 'jellyfin' CHECK(publication_mode IN ('native','jellyfin'));
ALTER TABLE library_removals ADD COLUMN publication_mode text NOT NULL DEFAULT 'jellyfin' CHECK(publication_mode IN ('native','jellyfin'));
CREATE TABLE library_lifecycle (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 publication_mode text NOT NULL CHECK(publication_mode IN ('native','jellyfin'))
);
-- Native candidates must never leak into the bridge's pre-activation catalog.
CREATE OR REPLACE VIEW publication_media AS
 SELECT m.movie_id,m.id AS mapping_id,m.file_bytes,s.probe FROM media_mappings m JOIN streaming_items s ON s.movie_id=m.movie_id WHERE s.published AND NOT EXISTS(SELECT 1 FROM mapping_candidates c JOIN mapping_operations o ON o.id=c.operation_id WHERE c.media_id=m.movie_id AND o.phase='publishing' AND o.publication_mode='jellyfin')
 UNION ALL
 SELECT c.media_id,c.mapping_id,m.file_bytes,c.probe FROM mapping_candidates c JOIN mapping_operations o ON o.id=c.operation_id JOIN mapping_versions m ON m.id=c.mapping_id WHERE o.phase='publishing' AND o.publication_mode='jellyfin';
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Native lifecycle ownership requires a data-preserving rollback'; END $$;
-- +goose StatementEnd
