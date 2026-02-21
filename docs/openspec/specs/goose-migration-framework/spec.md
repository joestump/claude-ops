# SPEC-0022: Goose Migration Framework Adoption

## Overview

Replace the hand-rolled migration framework in `internal/db/db.go` with [pressly/goose v3](https://github.com/pressly/goose), converting all seven inline Go migrations to standalone SQL files embedded via `//go:embed`. This specification covers the full cutover: SQL file authoring, embedding, bootstrap from the legacy tracking table, `db.Open()` integration, and developer workflow. See ADR-0021.

## Requirements

### Requirement: SQL File-Based Migrations

All schema migrations MUST be defined as standalone `.sql` files in an `internal/db/migrations/` directory. Each file MUST follow the goose naming convention `NNNNN_short_description.sql` where `NNNNN` is a zero-padded sequential version number. Each file MUST contain both `-- +goose Up` and `-- +goose Down` sections. The system MUST NOT use inline Go function migrations for schema changes.

#### Scenario: Migration file naming

- **WHEN** a new migration is created for adding a `notifications` table
- **THEN** the file is named `00008_add_notifications.sql` and placed in `internal/db/migrations/`

#### Scenario: Up and down sections present

- **WHEN** any `.sql` migration file is inspected
- **THEN** it contains a `-- +goose Up` section with the forward DDL statements
- **THEN** it contains a `-- +goose Down` section with the reverse DDL statements

#### Scenario: Down migration reverses up migration

- **WHEN** migration `00001_initial_schema.sql` is applied and then rolled back
- **THEN** all tables and indexes created by the up section are dropped by the down section
- **THEN** the database schema is equivalent to the state before the migration was applied

### Requirement: Existing Migration Conversion

The seven existing inline Go migrations (`migrate001` through `migrate007`) MUST be converted to SQL files that produce an identical schema. The converted SQL files MUST be numbered `00001` through `00007`. The SQL statements MUST be extracted verbatim from the Go functions without modification to the DDL.

#### Scenario: Migration 001 conversion

- **WHEN** `00001_initial_schema.sql` is applied to a fresh database
- **THEN** the resulting schema contains tables `sessions`, `health_checks`, `cooldown_actions`, `service_health_streak`, and `config` with identical column definitions as produced by `migrate001()`
- **THEN** indexes `idx_health_checks_service`, `idx_health_checks_session`, `idx_cooldown_actions_service`, and `idx_sessions_status` exist

#### Scenario: Migration 002 conversion

- **WHEN** `00002_session_metadata.sql` is applied after migration 001
- **THEN** the `sessions` table has additional columns `response TEXT`, `cost_usd REAL`, `num_turns INTEGER`, and `duration_ms INTEGER`

#### Scenario: Migration 007 conversion

- **WHEN** `00007_session_summary.sql` is applied after migrations 001-006
- **THEN** the `sessions` table has an additional column `summary TEXT`

#### Scenario: Full schema equivalence

- **WHEN** all seven SQL migrations are applied to a fresh database via goose
- **THEN** the resulting schema is identical to a database created by running all seven inline Go migrations via the current hand-rolled system

### Requirement: Embedded Migrations via go:embed

Migration SQL files MUST be embedded into the compiled binary using `//go:embed`. The `internal/db/` package MUST declare an `embed.FS` variable that includes the `migrations/` directory. The application MUST NOT require migration files on disk at runtime.

#### Scenario: Binary contains embedded migrations

- **WHEN** the application is compiled with `go build`
- **THEN** the resulting binary contains all `.sql` migration files from `internal/db/migrations/`
- **THEN** the application can run migrations without access to the source directory

#### Scenario: Embed directive syntax

- **WHEN** the `internal/db/` package source is inspected
- **THEN** it contains `//go:embed migrations/*.sql` and an `embed.FS` variable named `migrations` or `migrationFS`

### Requirement: Goose Provider API Integration

The `db.Open()` function MUST use goose's `goose.NewProvider()` API to create a migration provider and apply pending migrations on startup. The provider MUST be configured with dialect `"sqlite3"`, the `*sql.DB` connection, and the embedded `fs.FS`. The system MUST call `provider.Up(ctx)` to apply all pending migrations. The system MUST NOT use the goose global functions (`goose.Up()`, `goose.SetDialect()`, etc.).

#### Scenario: Migrations run on startup

- **WHEN** `db.Open()` is called with a path to a new database
- **THEN** goose applies all migrations sequentially
- **THEN** the function returns a `*DB` with all tables created

#### Scenario: No pending migrations

- **WHEN** `db.Open()` is called on a database that is already at the latest migration version
- **THEN** `provider.Up(ctx)` returns successfully without applying any migrations
- **THEN** no error is returned

#### Scenario: Partial migration state

- **WHEN** `db.Open()` is called on a database at migration version 4 and versions 5-7 exist
- **THEN** goose applies only migrations 5, 6, and 7

### Requirement: Bootstrap from Legacy Tracking Table

For databases created with the hand-rolled system, a one-time bootstrap MUST migrate the version state from the `schema_migrations` table to goose's `goose_db_version` table. The bootstrap MUST run before goose's `provider.Up()` call. The bootstrap MUST be idempotent -- running it on a database that has already been bootstrapped MUST be a no-op. After bootstrap, the legacy `schema_migrations` table MAY be dropped or left in place.

#### Scenario: Bootstrap existing database

- **WHEN** `db.Open()` is called on a database with `schema_migrations` table showing version 7
- **THEN** the bootstrap inserts rows into `goose_db_version` for versions 1 through 7 marking them as applied
- **THEN** `provider.Up()` detects no pending migrations and applies nothing
- **THEN** the database schema is unchanged

#### Scenario: Bootstrap fresh database

- **WHEN** `db.Open()` is called on a brand-new empty database
- **THEN** the bootstrap detects no `schema_migrations` table (or the table has zero rows)
- **THEN** the bootstrap is skipped (no-op)
- **THEN** goose applies all migrations from scratch

#### Scenario: Bootstrap is idempotent

- **WHEN** `db.Open()` is called twice on the same legacy database
- **THEN** the first call performs the bootstrap and applies any new migrations
- **THEN** the second call detects that `goose_db_version` already has the correct entries and performs no bootstrap work

#### Scenario: Bootstrap with partial legacy state

- **WHEN** `db.Open()` is called on a database with `schema_migrations` showing version 5 (migrations 6 and 7 not yet applied by the old system)
- **THEN** the bootstrap inserts rows into `goose_db_version` for versions 1 through 5
- **THEN** `provider.Up()` applies migrations 6 and 7

### Requirement: Transaction Safety

Each migration MUST execute within a database transaction. If any statement within a migration fails, the entire migration MUST be rolled back. The goose provider MUST be configured to use transactional migrations (the default behavior).

#### Scenario: Failed migration rolls back

- **WHEN** a migration contains two DDL statements and the second statement fails
- **THEN** the first statement is rolled back
- **THEN** the `goose_db_version` table does not record the failed migration as applied
- **THEN** `db.Open()` returns an error

#### Scenario: Successful migration is atomic

- **WHEN** a migration with multiple DDL statements succeeds
- **THEN** all statements are committed in a single transaction
- **THEN** the `goose_db_version` table records the migration as applied

### Requirement: Legacy Code Removal

After the migration to goose is complete, the hand-rolled migration framework MUST be removed from `internal/db/db.go`. This includes the `migration` struct type, the `migrations` slice, the `migrate()` method, and all `migrateNNN()` functions. The `schema_migrations` table creation code MUST be removed. The `db.Open()` function MUST only use goose for migration management.

#### Scenario: No hand-rolled migration code remains

- **WHEN** the `internal/db/db.go` file is inspected after the goose adoption
- **THEN** the `migration` struct, `migrations` variable, `migrate()` method, and all `migrateNNN()` functions are absent
- **THEN** no references to `schema_migrations` exist in the codebase except in the bootstrap function

### Requirement: Down Migration Support

Every SQL migration file MUST include a `-- +goose Down` section that reverses the changes made by the `-- +goose Up` section. Down migrations for `CREATE TABLE` MUST use `DROP TABLE IF EXISTS`. Down migrations for `ALTER TABLE ADD COLUMN` SHOULD use a table rebuild pattern since SQLite does not support `DROP COLUMN` prior to version 3.35.0 (the pure-Go `modernc.org/sqlite` driver MAY support `DROP COLUMN` but this MUST be verified). Down migrations for `CREATE INDEX` MUST use `DROP INDEX IF EXISTS`.

#### Scenario: Down migration for table creation

- **WHEN** a migration creates a table `memories`
- **THEN** the down section contains `DROP TABLE IF EXISTS memories`

#### Scenario: Down migration for index creation

- **WHEN** a migration creates index `idx_memories_service`
- **THEN** the down section contains `DROP INDEX IF EXISTS idx_memories_service`

#### Scenario: Down migration for column addition

- **WHEN** a migration adds column `summary TEXT` to the `sessions` table
- **THEN** the down section either uses `ALTER TABLE sessions DROP COLUMN summary` (if supported by the SQLite driver) or documents that the column cannot be removed without a table rebuild

### Requirement: Existing Tests Must Pass

All existing tests in `internal/db/db_test.go` MUST continue to pass without modification after the goose adoption. The `openTestDB()` helper MUST continue to produce a fully migrated test database. The test `TestMigrateIdempotent` MUST continue to verify that opening the same database twice is safe.

#### Scenario: Test suite passes

- **WHEN** `go test ./internal/db/ -count=1 -race` is run after the goose adoption
- **THEN** all existing tests pass

#### Scenario: openTestDB produces migrated database

- **WHEN** `openTestDB(t)` is called in a test
- **THEN** the returned `*DB` has all migrations applied
- **THEN** all tables (`sessions`, `health_checks`, `cooldown_actions`, `service_health_streak`, `config`, `events`, `memories`) exist and are functional

### Requirement: Dependency Addition

The project MUST add `github.com/pressly/goose/v3` as a direct dependency in `go.mod`. The standard library `embed` package MUST be imported in the `internal/db/` package. No other new dependencies SHOULD be required for the migration framework itself.

#### Scenario: go.mod updated

- **WHEN** `go.mod` is inspected after the goose adoption
- **THEN** `github.com/pressly/goose/v3` appears in the `require` block

#### Scenario: No CGo dependency introduced

- **WHEN** the project is built with `CGO_ENABLED=0`
- **THEN** the build succeeds (goose uses `database/sql` interfaces and does not require CGo)
