// @index GORM models for runtime schema compatibility checks.
package model

import "time"

// SchemaVersion records the database schema level expected by the binary.
// @intent let runtime commands fail fast when explicit migrations were not run.
type SchemaVersion struct {
	Key       string `gorm:"primaryKey;size:64"`
	Version   int    `gorm:"not null"`
	UpdatedAt time.Time
}

// TableName pins SchemaVersion to the migration-managed schema version table.
// @intent keep runtime schema checks aligned with explicit migration bookkeeping.
func (SchemaVersion) TableName() string {
	return "ccg_schema_versions"
}
