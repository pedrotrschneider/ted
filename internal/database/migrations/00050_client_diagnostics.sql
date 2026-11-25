-- +goose Up
CREATE TABLE client_log_settings (
    singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
    retention_days integer NOT NULL DEFAULT 90 CHECK(retention_days BETWEEN 1 AND 3650)
);
INSERT INTO client_log_settings(singleton) VALUES(true);
CREATE TABLE client_log_batches (
    seq bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    batch_id uuid NOT NULL,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id text NOT NULL,
    device_name text NOT NULL,
    platform text NOT NULL,
    app_version text NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    first_at timestamptz NOT NULL,
    last_at timestamptz NOT NULL,
    levels text[] NOT NULL,
    components text[] NOT NULL,
    session_ids text[] NOT NULL,
    search_text text NOT NULL,
    payload bytea,
    payload_hash text NOT NULL,
    entry_count integer NOT NULL CHECK(entry_count BETWEEN 1 AND 100),
    stored_bytes bigint NOT NULL CHECK(stored_bytes >= 0),
    UNIQUE(user_id,device_id,batch_id)
);
CREATE INDEX client_log_received ON client_log_batches(received_at);
CREATE INDEX client_log_device ON client_log_batches(user_id,device_id,seq DESC);
CREATE INDEX client_log_sessions ON client_log_batches USING gin(session_ids);

-- +goose Down
DROP TABLE client_log_batches;
DROP TABLE client_log_settings;
