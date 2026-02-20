-- +goose Up
CREATE TABLE sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tier INTEGER NOT NULL,
    model TEXT NOT NULL,
    prompt_file TEXT NOT NULL,
    status TEXT NOT NULL,
    started_at TEXT NOT NULL,
    ended_at TEXT,
    exit_code INTEGER,
    log_file TEXT,
    context TEXT
);

CREATE TABLE health_checks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id INTEGER REFERENCES sessions(id),
    service TEXT NOT NULL,
    check_type TEXT NOT NULL,
    status TEXT NOT NULL,
    response_time_ms INTEGER,
    error_detail TEXT,
    checked_at TEXT NOT NULL
);

CREATE TABLE cooldown_actions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    service TEXT NOT NULL,
    action_type TEXT NOT NULL,
    timestamp TEXT NOT NULL,
    success INTEGER NOT NULL,
    tier INTEGER NOT NULL,
    error TEXT,
    session_id INTEGER REFERENCES sessions(id)
);

CREATE TABLE service_health_streak (
    service TEXT PRIMARY KEY,
    consecutive_healthy INTEGER NOT NULL DEFAULT 0,
    last_checked_at TEXT
);

CREATE TABLE config (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_health_checks_service ON health_checks(service, checked_at);
CREATE INDEX idx_health_checks_session ON health_checks(session_id);
CREATE INDEX idx_cooldown_actions_service ON cooldown_actions(service, action_type, timestamp);
CREATE INDEX idx_sessions_status ON sessions(status, started_at);

-- +goose Down
DROP INDEX IF EXISTS idx_sessions_status;
DROP INDEX IF EXISTS idx_cooldown_actions_service;
DROP INDEX IF EXISTS idx_health_checks_session;
DROP INDEX IF EXISTS idx_health_checks_service;
DROP TABLE IF EXISTS config;
DROP TABLE IF EXISTS service_health_streak;
DROP TABLE IF EXISTS cooldown_actions;
DROP TABLE IF EXISTS health_checks;
DROP TABLE IF EXISTS sessions;
