-- +goose Up
CREATE TABLE memories (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    service TEXT,
    category TEXT NOT NULL,
    observation TEXT NOT NULL,
    confidence REAL NOT NULL DEFAULT 0.7,
    active INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    session_id INTEGER REFERENCES sessions(id),
    tier INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX idx_memories_service ON memories(service, active);
CREATE INDEX idx_memories_confidence ON memories(confidence, active);
CREATE INDEX idx_memories_category ON memories(category);

-- +goose Down
DROP INDEX IF EXISTS idx_memories_category;
DROP INDEX IF EXISTS idx_memories_confidence;
DROP INDEX IF EXISTS idx_memories_service;
DROP TABLE IF EXISTS memories;
