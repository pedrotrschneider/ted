-- +goose Up
-- A short renewable owner lease fences durable RD effects after a device
-- disappears. Disposable warmed bytes remain outside durable storage.
CREATE TABLE native_next_preparations (
    operation_id uuid PRIMARY KEY,
    source_id uuid NOT NULL REFERENCES native_addon_sources(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    family_id uuid NOT NULL REFERENCES native_token_families(id) ON DELETE CASCADE,
    device_id text NOT NULL,
    expires_at timestamptz NOT NULL,
    cancelled_at timestamptz
);
CREATE INDEX native_next_preparations_expiry ON native_next_preparations(expires_at);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'cancel and retire next-episode preparation jobs before removing their ownership leases';
END $$;
-- +goose StatementEnd
