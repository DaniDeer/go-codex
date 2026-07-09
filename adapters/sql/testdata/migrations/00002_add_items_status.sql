-- +goose Up
ALTER TABLE items ADD COLUMN status TEXT NOT NULL DEFAULT 'active';

-- +goose Down
ALTER TABLE items DROP COLUMN status;
