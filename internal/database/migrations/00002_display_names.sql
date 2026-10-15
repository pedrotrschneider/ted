-- +goose Up
ALTER TABLE users ADD COLUMN display_name text;
UPDATE users SET display_name=username;
ALTER TABLE users ALTER COLUMN display_name SET NOT NULL;
ALTER TABLE users ADD CONSTRAINT users_display_name_length CHECK (char_length(display_name) BETWEEN 1 AND 100);
