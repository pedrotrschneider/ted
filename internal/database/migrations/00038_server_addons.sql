-- +goose Up
-- Discovery credentials are independent of personal watch-history credentials.
CREATE TABLE server_addon_accounts (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 account_id text NOT NULL,
 email text NOT NULL,
 auth_key text NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 active boolean NOT NULL DEFAULT true
);
CREATE UNIQUE INDEX server_addon_one_active ON server_addon_accounts(active) WHERE active;
CREATE TABLE server_addon_collections (
 provider_id uuid PRIMARY KEY REFERENCES server_addon_accounts(id),
 account_updated_at timestamptz NOT NULL,
 payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='array'),
 observed_at timestamptz NOT NULL,
 error_code text NOT NULL DEFAULT ''
);
ALTER TABLE native_addon_sources ALTER COLUMN link_id DROP NOT NULL;
ALTER TABLE native_addon_sources DROP CONSTRAINT native_addon_sources_account_id_fkey;
ALTER TABLE native_addon_sources ADD COLUMN provider_id uuid REFERENCES server_addon_accounts(id);
CREATE INDEX native_addon_sources_provider ON native_addon_sources(user_id,provider_id);

-- Only the explicitly approved single-user deployment is automatically migrated.
INSERT INTO server_addon_accounts(account_id,email,auth_key,updated_at)
 SELECT a.id,a.email,a.auth_key,a.updated_at FROM stremio_accounts a
 JOIN stremio_links l ON l.account_id=a.id JOIN users u ON u.id=l.user_id
 WHERE u.username='native_tester' AND l.retired_at IS NULL AND a.auth_key!=''
 AND (SELECT count(*) FROM users)=1;
INSERT INTO server_addon_collections(provider_id,account_updated_at,payload,observed_at,error_code)
 SELECT p.id,c.account_updated_at,c.payload,c.observed_at,c.error_code
 FROM server_addon_accounts p JOIN stremio_addon_collections c ON c.account_id=p.account_id;
UPDATE native_addon_sources s SET provider_id=p.id FROM server_addon_accounts p
 WHERE s.account_id=p.account_id AND s.account_updated_at=p.updated_at;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'server add-on credentials are independent; restore a complete backup to roll back';
END $$;
-- +goose StatementEnd
