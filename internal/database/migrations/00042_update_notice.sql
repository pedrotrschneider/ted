-- +goose Up
ALTER TABLE users ADD COLUMN update_notice_after timestamptz NOT NULL DEFAULT '-infinity';

-- +goose Down
ALTER TABLE users DROP COLUMN update_notice_after;
