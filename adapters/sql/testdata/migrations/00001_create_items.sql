-- +goose Up
CREATE TABLE items (
    id   TEXT PRIMARY KEY,
    name TEXT NOT NULL
);

-- +goose Down
DROP TABLE items;
