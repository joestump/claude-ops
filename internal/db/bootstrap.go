package db

import (
	"database/sql"
	"fmt"
)

// Governing: SPEC-0022 REQ "Bootstrap from Legacy Tracking Table"
func bootstrapFromLegacy(conn *sql.DB) error {
	// Check if legacy table exists
	var count int
	err := conn.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`,
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check legacy table: %w", err)
	}
	if count == 0 {
		return nil // Fresh database, no bootstrap needed
	}

	// Check if goose table already exists (already bootstrapped)
	err = conn.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='goose_db_version'`,
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check goose table: %w", err)
	}
	if count > 0 {
		return nil // Already bootstrapped
	}

	// Read max version from legacy table
	var maxVersion int
	err = conn.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&maxVersion)
	if err != nil {
		return fmt.Errorf("read legacy version: %w", err)
	}
	if maxVersion == 0 {
		return nil // No migrations applied in legacy system
	}

	// Create goose tracking table
	_, err = conn.Exec(`CREATE TABLE goose_db_version (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		version_id INTEGER NOT NULL,
		is_applied INTEGER NOT NULL,
		tstamp TEXT NOT NULL DEFAULT (datetime('now'))
	)`)
	if err != nil {
		return fmt.Errorf("create goose_db_version: %w", err)
	}

	// Insert a row for each applied legacy version
	for v := 1; v <= maxVersion; v++ {
		_, err = conn.Exec(
			`INSERT INTO goose_db_version (version_id, is_applied, tstamp) VALUES (?, 1, datetime('now'))`,
			v,
		)
		if err != nil {
			return fmt.Errorf("insert goose version %d: %w", v, err)
		}
	}

	return nil
}
