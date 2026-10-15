-- +goose Up
CREATE TABLE watch_external (
 link_id uuid NOT NULL REFERENCES watch_links(id),
 media_id uuid NOT NULL REFERENCES catalog_media(id),
 sequence bigint NOT NULL,
 state jsonb NOT NULL,
 present boolean NOT NULL,
 item_present boolean NOT NULL,
 observed_at timestamptz NOT NULL,
 PRIMARY KEY(link_id,media_id)
);
CREATE TABLE watch_writes (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 user_id bigint NOT NULL REFERENCES users(id),
 media_id uuid NOT NULL REFERENCES catalog_media(id),
 link_id uuid NOT NULL REFERENCES watch_links(id),
 action text NOT NULL CHECK(action IN ('watched','unwatched','reset','restore')),
 expected_sequence bigint NOT NULL,
 expected_present boolean NOT NULL,
 expected_state jsonb NOT NULL,
 desired jsonb NOT NULL,
 phase text NOT NULL DEFAULT 'queued' CHECK(phase IN ('queued','ready','cancelling','applied','conflict','cancelled')),
 error_code text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX watch_one_pending_write ON watch_writes(user_id,media_id) WHERE phase IN ('queued','ready','cancelling');
CREATE INDEX watch_pending_link ON watch_writes(link_id) WHERE phase IN ('queued','ready','cancelling');
ALTER TABLE watch_current ADD COLUMN external_sequence bigint NOT NULL DEFAULT 0;
ALTER TABLE watch_history ADD COLUMN write_id uuid UNIQUE REFERENCES watch_writes(id);
ALTER TABLE watch_history ALTER COLUMN event_id DROP NOT NULL;
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Watch writes require a data-preserving migration'; END $$;
-- +goose StatementEnd
