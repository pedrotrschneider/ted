-- +goose Up
ALTER TABLE local_files
 ADD COLUMN transfer_bytes_per_second double precision NOT NULL DEFAULT 0 CHECK (transfer_bytes_per_second >= 0 AND transfer_bytes_per_second < 'Infinity'::double precision),
 ADD COLUMN transfer_sampled_at timestamptz;
-- +goose Down
ALTER TABLE local_files DROP COLUMN transfer_sampled_at, DROP COLUMN transfer_bytes_per_second;
