package migrationfs

import "embed"

// FS contains versioned database migrations used by ccg migrate and local
// SQLite first-use auto migration.
//
//go:embed sqlite/*.sql postgres/*.sql
var FS embed.FS
