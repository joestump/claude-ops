-- +goose Up
ALTER TABLE sessions ADD COLUMN summary TEXT;

-- +goose Down
ALTER TABLE sessions DROP COLUMN summary;
