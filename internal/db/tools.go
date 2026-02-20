//go:build tools

// Governing: SPEC-0022 REQ "Dependency Addition"
// This file pins github.com/pressly/goose/v3 as a direct dependency.
// The actual goose integration follows in subsequent issues (#210, #214).
package db

import _ "github.com/pressly/goose/v3"
