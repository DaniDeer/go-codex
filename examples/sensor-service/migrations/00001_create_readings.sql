-- +goose Up
CREATE TABLE readings (
    id          TEXT    PRIMARY KEY,
    sensor_id   TEXT    NOT NULL,
    value       REAL    NOT NULL,
    unit        TEXT    NOT NULL,
    recorded_at TEXT    NOT NULL
);

-- +goose Down
DROP TABLE readings;
