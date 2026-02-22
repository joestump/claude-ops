-- Governing: SPEC-0013 REQ "Events Table" (id, session_id, level, service, message, created_at)
-- +goose Up
CREATE TABLE events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id INTEGER REFERENCES sessions(id),
    level TEXT NOT NULL,
    service TEXT,
    message TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX idx_events_created ON events(created_at);
CREATE INDEX idx_events_session ON events(session_id);
CREATE INDEX idx_events_level ON events(level, created_at);

-- +goose Down
DROP INDEX IF EXISTS idx_events_level;
DROP INDEX IF EXISTS idx_events_session;
DROP INDEX IF EXISTS idx_events_created;
DROP TABLE IF EXISTS events;
