-- +goose Up
ALTER TABLE users ADD COLUMN avatar_version text NOT NULL DEFAULT '';
CREATE TABLE account_avatars (
    user_id bigint PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    image bytea NOT NULL CHECK (octet_length(image) BETWEEN 1 AND 524288)
);

-- +goose Down
DROP TABLE account_avatars;
ALTER TABLE users DROP COLUMN avatar_version;
