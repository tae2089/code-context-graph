// @index Persisted parser-result cache entries scoped by namespace and source path.
package graph

import "time"

// ParseCacheEntry stores the latest serialized parse result for one namespace/file path.
// @intent bound cache growth per active source path while validating the complete semantic cache identity.
type ParseCacheEntry struct {
	ID            uint   `gorm:"primaryKey"`
	Namespace     string `gorm:"type:text;not null;default:'default';uniqueIndex:idx_parse_cache_ns_file"`
	FilePath      string `gorm:"type:text;not null;uniqueIndex:idx_parse_cache_ns_file"`
	SourceHash    string `gorm:"type:text;not null;index"`
	ParserVersion string `gorm:"type:text;not null"`
	ContextHash   string `gorm:"type:text;not null"`
	Payload       []byte `gorm:"not null"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
