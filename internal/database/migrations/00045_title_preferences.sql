-- +goose Up
CREATE TABLE account_title_preferences (
 user_id bigint NOT NULL REFERENCES users(id),
 kind text NOT NULL CHECK(kind IN ('movie','series')),
 title_id text NOT NULL CHECK(title_id ~ '^tt[0-9]{7,12}$'),
 watchlist boolean NOT NULL DEFAULT false,
 rating smallint CHECK(rating BETWEEN 1 AND 7),
 metadata jsonb NOT NULL,
 saved_at timestamptz NOT NULL DEFAULT now(),
 rated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(user_id,kind,title_id)
);
CREATE INDEX account_watchlist ON account_title_preferences(user_id,kind,saved_at DESC,title_id DESC) WHERE watchlist;
CREATE INDEX account_ratings ON account_title_preferences(user_id,rating,rated_at DESC,title_id DESC) WHERE rating IS NOT NULL;
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Personal watch lists and ratings require a data-preserving migration'; END $$;
-- +goose StatementEnd
