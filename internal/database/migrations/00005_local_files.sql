-- +goose Up
CREATE TABLE local_files (
 movie_id uuid PRIMARY KEY REFERENCES streaming_items(movie_id),
 generation bigint NOT NULL,
 job_id bigint NOT NULL,
 phase text NOT NULL CHECK (phase IN ('queued','downloading','validating','publishing','ready','cancelled','failed','removing','absent')),
 completed_bytes bigint NOT NULL DEFAULT 0 CHECK (completed_bytes >= 0),
 validated_bytes bigint NOT NULL DEFAULT 0 CHECK (validated_bytes >= 0),
 total_bytes bigint NOT NULL CHECK (total_bytes > 0),
 error_code text NOT NULL DEFAULT '',
 available boolean NOT NULL DEFAULT false,
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE local_chunks (
 movie_id uuid NOT NULL REFERENCES local_files(movie_id),
 byte_offset bigint NOT NULL,
 byte_length bigint NOT NULL,
 sha256 bytea NOT NULL CHECK (octet_length(sha256)=32),
 PRIMARY KEY(movie_id,byte_offset)
);
-- +goose Down
DROP TABLE local_chunks;
DROP TABLE local_files;
