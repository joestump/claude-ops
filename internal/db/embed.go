package db

import "embed"

// Governing: SPEC-0022 REQ "Embedded Migrations via go:embed"
// migrationFS embeds all SQL migration files into the compiled binary.
// At runtime, no migration files need to exist on disk.
//
//go:embed migrations/*.sql
var migrationFS embed.FS //nolint:unused // used by goose provider in #214
