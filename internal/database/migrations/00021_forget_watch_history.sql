-- +goose Up
ALTER TABLE watch_writes ADD COLUMN forget_history boolean NOT NULL DEFAULT false;
-- +goose Down
ALTER TABLE watch_writes DROP COLUMN forget_history;
