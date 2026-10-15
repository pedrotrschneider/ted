-- +goose Up
-- Metadata is independent of shared library membership and user watch decisions.
CREATE TABLE metadata_entities (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 kind text NOT NULL CHECK(kind IN ('movie','series','season','episode','person')),
 identity text NOT NULL,
 parent_id uuid REFERENCES metadata_entities(id),
 provider text NOT NULL CHECK(provider IN ('Tmdb','Tvdb')),
 provider_id text NOT NULL DEFAULT '',
 item jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(item)='object'),
 field_sources jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(field_sources)='object'),
 status text NOT NULL DEFAULT 'new' CHECK(status IN ('new','queued','ready','partial','failed','missing','not_configured')),
 generation bigint NOT NULL DEFAULT 0,
 applied_generation bigint NOT NULL DEFAULT 0,
 fetched_at timestamptz,
 error_code text NOT NULL DEFAULT '',
 request jsonb NOT NULL CHECK(jsonb_typeof(request)='object'),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(kind,identity)
);
CREATE TABLE metadata_artwork (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 entity_id uuid REFERENCES metadata_entities(id),
 credit_id uuid,
 image_type text NOT NULL CHECK(image_type IN ('Primary','Backdrop','Banner','Logo','Thumb','Art','Disc','Screenshot')),
 image_index integer NOT NULL CHECK(image_index>=0),
 source_url text NOT NULL,
 user_modified boolean NOT NULL DEFAULT false,
 generation bigint NOT NULL DEFAULT 1,
 digest text NOT NULL DEFAULT '',
 content_type text NOT NULL DEFAULT '',
 bytes bigint NOT NULL DEFAULT 0 CHECK(bytes>=0),
 status text NOT NULL DEFAULT 'new' CHECK(status IN ('new','queued','ready','failed')),
 error_code text NOT NULL DEFAULT '',
 CHECK ((entity_id IS NULL) <> (credit_id IS NULL)),
 UNIQUE(entity_id,image_type,image_index),
 UNIQUE(credit_id,image_type,image_index)
);
CREATE TABLE metadata_credits (
 person_id uuid NOT NULL REFERENCES metadata_entities(id),
 provider text NOT NULL CHECK(provider IN ('Tmdb','Tvdb')),
 kind text NOT NULL CHECK(kind IN ('movie','series')),
 provider_id text NOT NULL,
 reference_id uuid NOT NULL DEFAULT gen_random_uuid(),
 summary jsonb NOT NULL CHECK(jsonb_typeof(summary)='object'),
 cinemeta_id text NOT NULL DEFAULT '',
 identity_status text NOT NULL DEFAULT 'new' CHECK(identity_status IN ('new','queued','ready','missing','failed')),
 generation bigint NOT NULL DEFAULT 1,
 roles jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(roles)='array'),
 PRIMARY KEY(person_id,provider,kind,provider_id),
 UNIQUE(reference_id)
);
ALTER TABLE metadata_artwork ADD CONSTRAINT metadata_artwork_credit_fk FOREIGN KEY(credit_id) REFERENCES metadata_credits(reference_id) ON DELETE CASCADE;
CREATE INDEX metadata_children ON metadata_entities(parent_id);
CREATE INDEX metadata_credit_identity ON metadata_credits(kind,cinemeta_id) WHERE cinemeta_id<>'';
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'metadata enrichment is forward-only; preserve metadata, artwork and person caches';
END $$;
-- +goose StatementEnd
