-- +goose Up
CREATE TABLE library_removals (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 title_id text NOT NULL,
 kind text NOT NULL CHECK(kind IN ('movie','series')),
 phase text NOT NULL DEFAULT 'snapshot' CHECK(phase IN ('snapshot','removing','complete')),
 retired boolean NOT NULL DEFAULT false,
 jellyfin_confirmed boolean NOT NULL DEFAULT false,
 snapshot jsonb,
 error_code text NOT NULL DEFAULT '',
 job_id bigint,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX current_title_removal ON library_removals(title_id,kind) WHERE NOT retired;
-- Includes folder IDs, so deletion authorizes only the exact retained hierarchy.
CREATE TABLE library_removal_items (
 removal_id uuid NOT NULL REFERENCES library_removals(id),
 item_id uuid NOT NULL,
 kind text NOT NULL CHECK(kind IN ('movie','series','season','episode')),
 PRIMARY KEY(removal_id,item_id)
);
ALTER TABLE mapping_operations ADD COLUMN restore_removal_id uuid REFERENCES library_removals(id);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Library removal records preserve deletion intent and history; require an explicit data-preserving migration'; END $$;
-- +goose StatementEnd
