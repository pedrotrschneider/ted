-- +goose Up
-- Hashes only. A delayed SCS commit must not resurrect a signed-out browser.
CREATE TABLE browser_session_revocations (
 token_digest bytea PRIMARY KEY CHECK (octet_length(token_digest)=32),
 expires_at timestamptz NOT NULL
);
CREATE INDEX browser_session_revocations_expiry ON browser_session_revocations(expires_at);

-- +goose Down
DROP TABLE browser_session_revocations;
