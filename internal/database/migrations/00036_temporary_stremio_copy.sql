-- +goose Up
CREATE TABLE temporary_stremio_copies (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 user_id bigint NOT NULL REFERENCES users(id),
 source_id text NOT NULL,
 source_email text NOT NULL,
 destination_id text NOT NULL REFERENCES stremio_accounts(id),
 destination_email text NOT NULL,
 source_key text NOT NULL,
 destination_key text NOT NULL,
 phase text NOT NULL DEFAULT 'queued' CHECK(phase IN ('queued','resolving','copying','retrying','failed','complete','cancelled')),
 resume_phase text NOT NULL DEFAULT 'queued',
 error_code text NOT NULL DEFAULT '',
 error_title text NOT NULL DEFAULT '',
 attempts integer NOT NULL DEFAULT 0,
 next_attempt timestamptz NOT NULL DEFAULT now(),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK(source_id <> destination_id),
 CHECK(phase NOT IN ('complete','cancelled') OR (source_key='' AND destination_key=''))
);
CREATE UNIQUE INDEX temporary_stremio_copy_destination ON temporary_stremio_copies(destination_id) WHERE phase NOT IN ('complete','cancelled');
CREATE UNIQUE INDEX temporary_stremio_copy_user ON temporary_stremio_copies(user_id) WHERE phase NOT IN ('complete','cancelled');
CREATE TABLE temporary_stremio_copy_titles (
 copy_id uuid NOT NULL REFERENCES temporary_stremio_copies(id),
 title_id text NOT NULL,
 source_record jsonb NOT NULL,
 resolved boolean NOT NULL DEFAULT false,
 entries jsonb NOT NULL DEFAULT '[]',
 episode_ids jsonb NOT NULL DEFAULT '[]',
 done boolean NOT NULL DEFAULT false,
 skip_reason text NOT NULL DEFAULT '',
 before_record jsonb,
 desired_record jsonb,
 PRIMARY KEY(copy_id,title_id)
);
-- +goose Down
DROP TABLE temporary_stremio_copy_titles;
DROP TABLE temporary_stremio_copies;
