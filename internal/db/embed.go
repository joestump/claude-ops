package db

import "embed"

// Governing: SPEC-0022 REQ "Embedded Migrations via go:embed"
// MigrationFS embeds all SQL migration files into the compiled binary.
// At runtime, no migration files need to exist on disk.
//
//go:embed migrations/*.sql
var MigrationFS embed.FS
