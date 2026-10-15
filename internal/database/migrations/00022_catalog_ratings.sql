-- +goose Up
CREATE TABLE catalog_ratings (
 title_id text NOT NULL,
 kind text NOT NULL CHECK(kind IN ('movie','series')),
 rating double precision CHECK(rating>0 AND rating<=10),
 checked_at timestamptz NOT NULL,
 PRIMARY KEY(title_id,kind)
);
-- +goose Down
DROP TABLE catalog_ratings;
