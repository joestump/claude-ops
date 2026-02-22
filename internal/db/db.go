package db

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

// DB wraps a sql.DB connection to the SQLite database.
// Governing: SPEC-0008 REQ-8 — SQLite State Storage (pure-Go driver via modernc.org/sqlite)
type DB struct {
	conn *sql.DB
}

// Session represents a Claude CLI session record.
type Session struct {
	ID         int64
	Tier       int
	Model      string
	PromptFile string
	Status     string // running, completed, failed, timed_out, escalated
	StartedAt  string
	EndedAt    *string
	ExitCode   *int
	LogFile    *string
	Context    *string // JSON blob
	Response   *string // final markdown response from Claude
	CostUSD    *float64
	NumTurns   *int
	DurationMs *int64
	Trigger         string  // "scheduled" or "manual"
	PromptText      *string // custom prompt text for ad-hoc sessions
	ParentSessionID *int64  // links to parent session for escalation chains
	Summary         *string // LLM-generated summary of session response — Governing: SPEC-0021 REQ "Summary Persistence"
}

// HealthCheck represents a parsed health check result.
// Governing: SPEC-0008 REQ-9 — Health Check History (service, check_type, status, timestamp, response_time, error)
type HealthCheck struct {
	ID             int64
	SessionID      *int64
	Service        string
	CheckType      string // http, dns, container, database, service
	Status         string // healthy, degraded, down
	ResponseTimeMs *int
	ErrorDetail    *string
	CheckedAt      string
}

// Event represents a parsed event marker from an LLM session.
type Event struct {
	ID        int64
	SessionID *int64
	Level     string
	Service   *string
	Message   string
	CreatedAt string
}

// Memory represents a persistent operational knowledge record.
type Memory struct {
	ID          int64
	Service     *string
	Category    string
	Observation string
	Confidence  float64
	Active      bool
	CreatedAt   string
	UpdatedAt   string
	SessionID   *int64
	Tier        int
}

// CooldownAction represents a remediation action record.
type CooldownAction struct {
	ID         int64
	Service    string
	ActionType string // restart, redeployment
	Timestamp  string
	Success    bool
	Tier       int
	Error      *string
	SessionID  *int64
}

// Open creates a new DB connection and runs all pending migrations.
// Governing: SPEC-0008 REQ-8 — SQLite State Storage (database init and schema migration on startup)
// Governing: SPEC-0022 REQ "Goose Provider API Integration"
func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	conn.SetMaxOpenConns(1)

	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	// Bootstrap: migrate legacy schema_migrations -> goose_db_version
	if err := bootstrapFromLegacy(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("bootstrap legacy migrations: %w", err)
	}

	// Extract the migrations subdirectory from the embedded FS
	migrationsFS, err := fs.Sub(MigrationFS, "migrations")
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("migrations sub-fs: %w", err)
	}

	// Create goose provider with embedded migrations (MUST NOT use global functions).
	// Governing: SPEC-0022 REQ "Transaction Safety"
	// Goose runs each SQL migration in a transaction by default (useTx=true).
	// No WithDisableTransaction option is passed, so failed statements cause
	// full rollback and goose_db_version is not updated on failure.
	provider, err := goose.NewProvider(goose.DialectSQLite3, conn, migrationsFS)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("create migration provider: %w", err)
	}

	// Apply all pending migrations
	if _, err := provider.Up(context.Background()); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("apply migrations: %w", err)
	}

	return &DB{conn: conn}, nil
}

// Close closes the database connection.
func (d *DB) Close() error {
	return d.conn.Close()
}

// Conn returns the underlying *sql.DB for use by other packages if needed.
func (d *DB) Conn() *sql.DB {
	return d.conn
}

// --- Session Methods ---

const sessionColumns = `id, tier, model, prompt_file, status, started_at, ended_at, exit_code, log_file, context, response, cost_usd, num_turns, duration_ms, trigger, prompt_text, parent_session_id, summary`

func scanSession(scanner interface{ Scan(...any) error }, s *Session) error {
	return scanner.Scan(&s.ID, &s.Tier, &s.Model, &s.PromptFile, &s.Status, &s.StartedAt, &s.EndedAt, &s.ExitCode, &s.LogFile, &s.Context, &s.Response, &s.CostUSD, &s.NumTurns, &s.DurationMs, &s.Trigger, &s.PromptText, &s.ParentSessionID, &s.Summary)
}

// InsertSession creates a new session record and returns its ID.
func (d *DB) InsertSession(s *Session) (int64, error) {
	res, err := d.conn.Exec(
		`INSERT INTO sessions (tier, model, prompt_file, status, started_at, ended_at, exit_code, log_file, context, trigger, prompt_text, parent_session_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.Tier, s.Model, s.PromptFile, s.Status, s.StartedAt, s.EndedAt, s.ExitCode, s.LogFile, s.Context, s.Trigger, s.PromptText, s.ParentSessionID,
	)
	if err != nil {
		return 0, fmt.Errorf("insert session: %w", err)
	}
	return res.LastInsertId()
}

// UpdateSession updates a session's mutable fields (status, ended_at, exit_code, log_file).
func (d *DB) UpdateSession(id int64, status string, endedAt *string, exitCode *int, logFile *string) error {
	_, err := d.conn.Exec(
		`UPDATE sessions SET status = ?, ended_at = ?, exit_code = ?, log_file = ? WHERE id = ?`,
		status, endedAt, exitCode, logFile, id,
	)
	if err != nil {
		return fmt.Errorf("update session %d: %w", id, err)
	}
	return nil
}

// UpdateSessionStatus updates only the status of a session (e.g. to "escalated").
func (d *DB) UpdateSessionStatus(id int64, status string) error {
	_, err := d.conn.Exec(`UPDATE sessions SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		return fmt.Errorf("update session status %d: %w", id, err)
	}
	return nil
}

// UpdateSessionResult stores the final response and metadata from a completed session.
func (d *DB) UpdateSessionResult(id int64, response string, costUSD float64, numTurns int, durationMs int64) error {
	_, err := d.conn.Exec(
		`UPDATE sessions SET response = ?, cost_usd = ?, num_turns = ?, duration_ms = ? WHERE id = ?`,
		response, costUSD, numTurns, durationMs, id,
	)
	if err != nil {
		return fmt.Errorf("update session result %d: %w", id, err)
	}
	return nil
}

// GetSession retrieves a single session by ID.
func (d *DB) GetSession(id int64) (*Session, error) {
	s := &Session{}
	row := d.conn.QueryRow(`SELECT `+sessionColumns+` FROM sessions WHERE id = ?`, id)
	if err := scanSession(row, s); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("get session %d: %w", id, err)
	}
	return s, nil
}

// ListSessions returns sessions ordered by started_at descending, with a limit and offset.
func (d *DB) ListSessions(limit, offset int) ([]Session, error) {
	rows, err := d.conn.Query(
		`SELECT `+sessionColumns+` FROM sessions ORDER BY started_at DESC LIMIT ? OFFSET ?`, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var sessions []Session
	for rows.Next() {
		var s Session
		if err := scanSession(rows, &s); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// --- Health Check Methods ---
// Governing: SPEC-0008 REQ-9 — Health Check History (store and query health check results)

// InsertHealthCheck stores a health check result.
func (d *DB) InsertHealthCheck(h *HealthCheck) (int64, error) {
	res, err := d.conn.Exec(
		`INSERT INTO health_checks (session_id, service, check_type, status, response_time_ms, error_detail, checked_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		h.SessionID, h.Service, h.CheckType, h.Status, h.ResponseTimeMs, h.ErrorDetail, h.CheckedAt,
	)
	if err != nil {
		return 0, fmt.Errorf("insert health check: %w", err)
	}
	return res.LastInsertId()
}

// QueryHealthChecks returns health checks for a service within a time range,
// ordered by checked_at descending.
func (d *DB) QueryHealthChecks(service string, since, until string, limit int) ([]HealthCheck, error) {
	rows, err := d.conn.Query(
		`SELECT id, session_id, service, check_type, status, response_time_ms, error_detail, checked_at
		 FROM health_checks
		 WHERE service = ? AND checked_at >= ? AND checked_at <= ?
		 ORDER BY checked_at DESC LIMIT ?`,
		service, since, until, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query health checks: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var checks []HealthCheck
	for rows.Next() {
		var h HealthCheck
		if err := rows.Scan(&h.ID, &h.SessionID, &h.Service, &h.CheckType, &h.Status, &h.ResponseTimeMs, &h.ErrorDetail, &h.CheckedAt); err != nil {
			return nil, fmt.Errorf("scan health check: %w", err)
		}
		checks = append(checks, h)
	}
	return checks, rows.Err()
}

// --- Event Methods ---

// InsertEvent stores an event record.
func (d *DB) InsertEvent(e *Event) (int64, error) {
	res, err := d.conn.Exec(
		`INSERT INTO events (session_id, level, service, message, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		e.SessionID, e.Level, e.Service, e.Message, e.CreatedAt,
	)
	if err != nil {
		return 0, fmt.Errorf("insert event: %w", err)
	}
	return res.LastInsertId()
}

// ListEvents returns events ordered by created_at descending, with a limit, offset,
// and optional level/service filters.
func (d *DB) ListEvents(limit, offset int, level, service *string) ([]Event, error) {
	query := `SELECT id, session_id, level, service, message, created_at FROM events WHERE 1=1`
	var args []any
	if level != nil {
		query += ` AND level = ?`
		args = append(args, *level)
	}
	if service != nil {
		query += ` AND service = ?`
		args = append(args, *service)
	}
	query += ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := d.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var events []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.SessionID, &e.Level, &e.Service, &e.Message, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// --- Cooldown Methods ---
// Governing: SPEC-0008 REQ-8 — SQLite State Storage (cooldown enforcement via SQLite replaces cooldown.json)

// CheckCooldown returns the count of actions of the given type for a service
// within the specified window.
func (d *DB) CheckCooldown(service, actionType string, window time.Duration) (int, error) {
	since := time.Now().UTC().Add(-window).Format(time.RFC3339)
	var count int
	err := d.conn.QueryRow(
		`SELECT COUNT(*) FROM cooldown_actions
		 WHERE service = ? AND action_type = ? AND timestamp > ?`,
		service, actionType, since,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("check cooldown: %w", err)
	}
	return count, nil
}

// InsertCooldownAction records a remediation action.
func (d *DB) InsertCooldownAction(a *CooldownAction) (int64, error) {
	res, err := d.conn.Exec(
		`INSERT INTO cooldown_actions (service, action_type, timestamp, success, tier, error, session_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.Service, a.ActionType, a.Timestamp, boolToInt(a.Success), a.Tier, a.Error, a.SessionID,
	)
	if err != nil {
		return 0, fmt.Errorf("insert cooldown action: %w", err)
	}
	return res.LastInsertId()
}

// --- Health Streak Methods ---

// GetHealthStreak returns the consecutive healthy count for a service.
func (d *DB) GetHealthStreak(service string) (int, error) {
	var count int
	err := d.conn.QueryRow(
		`SELECT consecutive_healthy FROM service_health_streak WHERE service = ?`, service,
	).Scan(&count)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get health streak: %w", err)
	}
	return count, nil
}

// SetHealthStreak upserts the consecutive healthy count for a service.
func (d *DB) SetHealthStreak(service string, count int) error {
	_, err := d.conn.Exec(
		`INSERT INTO service_health_streak (service, consecutive_healthy, last_checked_at)
		 VALUES (?, ?, datetime('now'))
		 ON CONFLICT(service) DO UPDATE SET consecutive_healthy = ?, last_checked_at = datetime('now')`,
		service, count, count,
	)
	if err != nil {
		return fmt.Errorf("set health streak: %w", err)
	}
	return nil
}

// --- Config Methods ---

// GetConfig returns the value for a configuration key, or the fallback if not set.
func (d *DB) GetConfig(key, fallback string) (string, error) {
	var value string
	err := d.conn.QueryRow(`SELECT value FROM config WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return fallback, nil
	}
	if err != nil {
		return "", fmt.Errorf("get config %q: %w", key, err)
	}
	return value, nil
}

// SetConfig upserts a configuration key-value pair.
func (d *DB) SetConfig(key, value string) error {
	_, err := d.conn.Exec(
		`INSERT INTO config (key, value, updated_at) VALUES (?, ?, datetime('now'))
		 ON CONFLICT(key) DO UPDATE SET value = ?, updated_at = datetime('now')`,
		key, value, value,
	)
	if err != nil {
		return fmt.Errorf("set config %q: %w", key, err)
	}
	return nil
}

// ServiceStatus summarizes the current state of a service from recent health checks.
type ServiceStatus struct {
	Service    string
	Status     string
	LastCheck  *string
	CheckCount int
}

// ListServiceStatuses returns an aggregate status for each service based on
// the most recent health check per service and total check count.
func (d *DB) ListServiceStatuses() ([]ServiceStatus, error) {
	rows, err := d.conn.Query(`
		SELECT h.service,
		       h.status,
		       h.checked_at,
		       counts.cnt
		FROM health_checks h
		INNER JOIN (
			SELECT service, MAX(id) AS max_id, COUNT(*) AS cnt
			FROM health_checks
			GROUP BY service
		) counts ON h.id = counts.max_id
		ORDER BY h.service`)
	if err != nil {
		return nil, fmt.Errorf("list service statuses: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var statuses []ServiceStatus
	for rows.Next() {
		var s ServiceStatus
		if err := rows.Scan(&s.Service, &s.Status, &s.LastCheck, &s.CheckCount); err != nil {
			return nil, fmt.Errorf("scan service status: %w", err)
		}
		statuses = append(statuses, s)
	}
	return statuses, rows.Err()
}

// RecentCooldown represents an aggregated cooldown view for the dashboard.
type RecentCooldown struct {
	Service    string
	ActionType string
	Count      int
	LastAction string
}

// ListRecentCooldowns returns cooldown action counts per service/action_type
// within the given window.
func (d *DB) ListRecentCooldowns(window time.Duration) ([]RecentCooldown, error) {
	since := time.Now().UTC().Add(-window).Format(time.RFC3339)
	rows, err := d.conn.Query(`
		SELECT service, action_type, COUNT(*) AS cnt, MAX(timestamp) AS last_action
		FROM cooldown_actions
		WHERE timestamp > ?
		GROUP BY service, action_type
		ORDER BY last_action DESC`, since)
	if err != nil {
		return nil, fmt.Errorf("list recent cooldowns: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var cooldowns []RecentCooldown
	for rows.Next() {
		var c RecentCooldown
		if err := rows.Scan(&c.Service, &c.ActionType, &c.Count, &c.LastAction); err != nil {
			return nil, fmt.Errorf("scan cooldown: %w", err)
		}
		cooldowns = append(cooldowns, c)
	}
	return cooldowns, rows.Err()
}

// LatestSession returns the most recent session, or nil if none exist.
func (d *DB) LatestSession() (*Session, error) {
	s := &Session{}
	row := d.conn.QueryRow(`SELECT ` + sessionColumns + ` FROM sessions ORDER BY started_at DESC LIMIT 1`)
	if err := scanSession(row, s); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("latest session: %w", err)
	}
	return s, nil
}

// GetEscalationChain walks parent_session_id links from the given session
// to the root, then returns the chain ordered from root to leaf.
func (d *DB) GetEscalationChain(sessionID int64) ([]Session, error) {
	rows, err := d.conn.Query(`
		WITH RECURSIVE chain(id) AS (
			SELECT id FROM sessions WHERE id = ?
			UNION ALL
			SELECT s.parent_session_id FROM sessions s
			JOIN chain c ON s.id = c.id
			WHERE s.parent_session_id IS NOT NULL
		)
		SELECT `+sessionColumns+` FROM sessions
		WHERE id IN (SELECT id FROM chain)
		ORDER BY id ASC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("get escalation chain: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var sessions []Session
	for rows.Next() {
		var s Session
		if err := scanSession(rows, &s); err != nil {
			return nil, fmt.Errorf("scan escalation chain session: %w", err)
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// GetChildSessions returns direct child sessions of the given session.
func (d *DB) GetChildSessions(sessionID int64) ([]Session, error) {
	rows, err := d.conn.Query(
		`SELECT `+sessionColumns+` FROM sessions WHERE parent_session_id = ? ORDER BY id ASC`, sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("get child sessions: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var sessions []Session
	for rows.Next() {
		var s Session
		if err := scanSession(rows, &s); err != nil {
			return nil, fmt.Errorf("scan child session: %w", err)
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// --- Memory Methods ---

// InsertMemory stores a memory record and returns its ID.
func (d *DB) InsertMemory(m *Memory) (int64, error) {
	res, err := d.conn.Exec(
		`INSERT INTO memories (service, category, observation, confidence, active, created_at, updated_at, session_id, tier)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Service, m.Category, m.Observation, m.Confidence, boolToInt(m.Active), m.CreatedAt, m.UpdatedAt, m.SessionID, m.Tier,
	)
	if err != nil {
		return 0, fmt.Errorf("insert memory: %w", err)
	}
	return res.LastInsertId()
}

// GetMemory retrieves a single memory by ID.
func (d *DB) GetMemory(id int64) (*Memory, error) {
	m := &Memory{}
	var active int
	err := d.conn.QueryRow(
		`SELECT id, service, category, observation, confidence, active, created_at, updated_at, session_id, tier
		 FROM memories WHERE id = ?`, id,
	).Scan(&m.ID, &m.Service, &m.Category, &m.Observation, &m.Confidence, &active, &m.CreatedAt, &m.UpdatedAt, &m.SessionID, &m.Tier)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get memory %d: %w", id, err)
	}
	m.Active = active == 1
	return m, nil
}

// UpdateMemory updates a memory's observation, confidence, and active flag.
func (d *DB) UpdateMemory(id int64, observation string, confidence float64, active bool) error {
	_, err := d.conn.Exec(
		`UPDATE memories SET observation = ?, confidence = ?, active = ?, updated_at = datetime('now') WHERE id = ?`,
		observation, confidence, boolToInt(active), id,
	)
	if err != nil {
		return fmt.Errorf("update memory %d: %w", id, err)
	}
	return nil
}

// DeleteMemory removes a memory by ID.
func (d *DB) DeleteMemory(id int64) error {
	_, err := d.conn.Exec(`DELETE FROM memories WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete memory %d: %w", id, err)
	}
	return nil
}

// ListMemories returns memories with optional service and category filters,
// ordered by confidence descending.
func (d *DB) ListMemories(service *string, category *string, limit, offset int) ([]Memory, error) {
	query := `SELECT id, service, category, observation, confidence, active, created_at, updated_at, session_id, tier FROM memories WHERE 1=1`
	var args []any

	if service != nil {
		query += ` AND service = ?`
		args = append(args, *service)
	}
	if category != nil {
		query += ` AND category = ?`
		args = append(args, *category)
	}
	query += ` ORDER BY confidence DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := d.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var memories []Memory
	for rows.Next() {
		var m Memory
		var active int
		if err := rows.Scan(&m.ID, &m.Service, &m.Category, &m.Observation, &m.Confidence, &active, &m.CreatedAt, &m.UpdatedAt, &m.SessionID, &m.Tier); err != nil {
			return nil, fmt.Errorf("scan memory: %w", err)
		}
		m.Active = active == 1
		memories = append(memories, m)
	}
	return memories, rows.Err()
}

// GetActiveMemories returns active memories with confidence >= 0.3,
// ordered by confidence descending.
func (d *DB) GetActiveMemories(limit int) ([]Memory, error) {
	rows, err := d.conn.Query(
		`SELECT id, service, category, observation, confidence, active, created_at, updated_at, session_id, tier
		 FROM memories WHERE active = 1 AND confidence >= 0.3
		 ORDER BY confidence DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get active memories: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var memories []Memory
	for rows.Next() {
		var m Memory
		var active int
		if err := rows.Scan(&m.ID, &m.Service, &m.Category, &m.Observation, &m.Confidence, &active, &m.CreatedAt, &m.UpdatedAt, &m.SessionID, &m.Tier); err != nil {
			return nil, fmt.Errorf("scan active memory: %w", err)
		}
		m.Active = active == 1
		memories = append(memories, m)
	}
	return memories, rows.Err()
}

// FindSimilarMemory finds an existing memory matching the given service and category.
func (d *DB) FindSimilarMemory(service *string, category string) (*Memory, error) {
	var query string
	var args []any

	if service != nil {
		query = `SELECT id, service, category, observation, confidence, active, created_at, updated_at, session_id, tier
			 FROM memories WHERE service = ? AND category = ? ORDER BY confidence DESC LIMIT 1`
		args = []any{*service, category}
	} else {
		query = `SELECT id, service, category, observation, confidence, active, created_at, updated_at, session_id, tier
			 FROM memories WHERE service IS NULL AND category = ? ORDER BY confidence DESC LIMIT 1`
		args = []any{category}
	}

	m := &Memory{}
	var active int
	err := d.conn.QueryRow(query, args...).Scan(&m.ID, &m.Service, &m.Category, &m.Observation, &m.Confidence, &active, &m.CreatedAt, &m.UpdatedAt, &m.SessionID, &m.Tier)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find similar memory: %w", err)
	}
	m.Active = active == 1
	return m, nil
}

// DecayStaleMemories reduces confidence for memories not updated within graceDays,
// then deactivates any that fall below 0.3.
func (d *DB) DecayStaleMemories(graceDays int, decayRate float64) error {
	cutoff := time.Now().UTC().AddDate(0, 0, -graceDays).Format(time.RFC3339)
	_, err := d.conn.Exec(
		`UPDATE memories SET confidence = confidence - ? WHERE active = 1 AND updated_at < ?`,
		decayRate, cutoff,
	)
	if err != nil {
		return fmt.Errorf("decay stale memories: %w", err)
	}

	_, err = d.conn.Exec(`UPDATE memories SET active = 0 WHERE confidence < 0.3`)
	if err != nil {
		return fmt.Errorf("deactivate low-confidence memories: %w", err)
	}
	return nil
}

// UpdateSessionSummary stores an LLM-generated summary for a session.
// Governing: SPEC-0021 REQ "Session Summary Generation"
func (d *DB) UpdateSessionSummary(id int64, summary string) error {
	_, err := d.conn.Exec(`UPDATE sessions SET summary = ? WHERE id = ?`, summary, id)
	if err != nil {
		return fmt.Errorf("update session summary %d: %w", id, err)
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
