-- +goose Up
ALTER TABLE mapping_candidates ADD COLUMN jellyfin_confirmed boolean NOT NULL DEFAULT false;
-- +goose Down
ALTER TABLE mapping_candidates DROP COLUMN jellyfin_confirmed;
