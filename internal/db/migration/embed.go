package migration

import "embed"

// FS contains versioned database migrations used by ccg migrate and local
// SQLite first-use auto migration.
// @intent keep embedded versioned SQL assets with the migration runtime that selects and executes them.
//
//go:embed sqlite/*.sql postgres/*.sql
var FS embed.FS
