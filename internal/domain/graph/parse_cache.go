// @index Persisted parser-result cache entries scoped by namespace and source path.
package graph

import "time"

// ParseCacheEntry stores the latest serialized parse result for one namespace/file path.
// @intent bound cache growth per active source path while validating the complete semantic cache identity.
type ParseCacheEntry struct {
	ID            uint   `gorm:"primaryKey"`
	Namespace     string `gorm:"size:256;not null;default:'default';uniqueIndex:idx_parse_cache_ns_file"`
	FilePath      string `gorm:"size:768;not null;uniqueIndex:idx_parse_cache_ns_file"`
	SourceHash    string `gorm:"size:64;not null;index"`
	ParserVersion string `gorm:"size:160;not null"`
	ContextHash   string `gorm:"size:64;not null"`
	Payload       []byte `gorm:"not null"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
