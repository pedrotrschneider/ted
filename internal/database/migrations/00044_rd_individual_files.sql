-- +goose Up
-- Individual-file fallback ownership is separate from the original all-files
-- entry. Record an add attempt before the remote call and its returned ID
-- before selecting anything; uncertain calls must not create duplicates.
CREATE TABLE rd_addon_file_torrents (
 scope text NOT NULL,
 hash text NOT NULL,
 path text NOT NULL,
 path_digest text GENERATED ALWAYS AS (md5(path)) STORED,
 bytes bigint NOT NULL CHECK(bytes>0),
 owned_id text NOT NULL DEFAULT '',
 add_attempted boolean NOT NULL DEFAULT false,
 detail jsonb,
 verified_at timestamptz,
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(scope,hash,path_digest,bytes)
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'Individual RD file ownership is forward-only; preserve remote ownership journals';
END $$;
-- +goose StatementEnd
