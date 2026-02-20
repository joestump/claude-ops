-- +goose Up
ALTER TABLE sessions ADD COLUMN parent_session_id INTEGER REFERENCES sessions(id);
CREATE INDEX idx_sessions_parent ON sessions(parent_session_id);

-- +goose Down
DROP INDEX IF EXISTS idx_sessions_parent;
ALTER TABLE sessions DROP COLUMN parent_session_id;
