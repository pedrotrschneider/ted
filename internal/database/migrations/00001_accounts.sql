-- +goose Up
CREATE TABLE users (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 username text NOT NULL UNIQUE CHECK (username ~ '^[a-z0-9][a-z0-9_.-]{2,63}$'),
 password_hash text NOT NULL,
 role text NOT NULL CHECK (role IN ('admin', 'user')),
 disabled boolean NOT NULL DEFAULT false,
 session_version bigint NOT NULL DEFAULT 1 CHECK (session_version > 0),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE sessions (token text PRIMARY KEY, data bytea NOT NULL, expiry timestamptz NOT NULL);
CREATE INDEX sessions_expiry_idx ON sessions (expiry);
CREATE TABLE login_limits (key text PRIMARY KEY, attempts integer NOT NULL, expiry timestamptz NOT NULL);
CREATE INDEX login_limits_expiry_idx ON login_limits (expiry);
