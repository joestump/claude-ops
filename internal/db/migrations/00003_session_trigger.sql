-- +goose Up
ALTER TABLE sessions ADD COLUMN trigger TEXT NOT NULL DEFAULT 'scheduled';
ALTER TABLE sessions ADD COLUMN prompt_text TEXT;

-- +goose Down
ALTER TABLE sessions DROP COLUMN prompt_text;
ALTER TABLE sessions DROP COLUMN trigger;
