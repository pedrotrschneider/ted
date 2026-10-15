-- +goose Up
-- Keep the original account-addition date separate from an episode watch date.
CREATE TABLE watch_account_title_dates (
 user_id bigint NOT NULL REFERENCES users(id),
 title_id text NOT NULL,
 added_at timestamptz NOT NULL,
 provider_dated boolean NOT NULL DEFAULT false,
 PRIMARY KEY(user_id,title_id)
);
INSERT INTO watch_account_title_dates(user_id,title_id,added_at)
 SELECT h.user_id,split_part(c.cinemeta_id,':',1),min(h.observed_at)
 FROM watch_history h JOIN catalog_media c ON c.id=h.media_id
 WHERE h.kind IN ('stremio_import','stremio_observed')
 GROUP BY h.user_id,split_part(c.cinemeta_id,':',1);
-- Provider choices are public image descriptors; credentials remain in settings.
CREATE TABLE metadata_artwork_choices (
 entity_id uuid PRIMARY KEY REFERENCES metadata_entities(id),
 generation bigint NOT NULL DEFAULT 0,
 status text NOT NULL DEFAULT 'new' CHECK(status IN ('new','queued','ready','failed')),
 images jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(images)='array'),
 fetched_at timestamptz,
 error_code text NOT NULL DEFAULT ''
);
CREATE TABLE metadata_artwork_edits (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 artwork_id uuid NOT NULL REFERENCES metadata_artwork(id),
 generation bigint NOT NULL,
 user_id bigint NOT NULL REFERENCES users(id),
 previous_digest text NOT NULL,
 digest text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(artwork_id,generation)
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'artwork choices are forward-only; preserve user artwork and edit history';
END $$;
-- +goose StatementEnd
