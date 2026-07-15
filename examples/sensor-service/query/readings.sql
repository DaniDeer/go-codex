-- name: InsertReading :exec
INSERT INTO readings (id, sensor_id, value, unit, recorded_at)
VALUES (?, ?, ?, ?, ?);

-- name: GetReading :one
SELECT id, sensor_id, value, unit, recorded_at
FROM readings
WHERE id = ?;

-- name: ListReadings :many
SELECT id, sensor_id, value, unit, recorded_at
FROM readings
ORDER BY recorded_at DESC;

-- name: ListReadingsBySensor :many
SELECT id, sensor_id, value, unit, recorded_at
FROM readings
WHERE sensor_id = ?
ORDER BY recorded_at ASC, id ASC;
