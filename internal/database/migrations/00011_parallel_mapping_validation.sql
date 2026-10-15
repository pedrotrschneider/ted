-- +goose Up
ALTER TABLE mapping_candidates ADD COLUMN validation_job_id bigint;
ALTER TABLE mapping_candidates ADD COLUMN probe_ms bigint;
ALTER TABLE mapping_operations ADD COLUMN validation_started_at timestamptz;
ALTER TABLE mapping_operations ADD COLUMN validated_at timestamptz;
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Parallel validation jobs require a data-preserving rollback'; END $$;
-- +goose StatementEnd
