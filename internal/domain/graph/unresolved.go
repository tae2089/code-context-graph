// @index Durable unresolved-edge reverse index for semi-naive incremental resolution.
package graph

import "time"

// UnresolvedEdgeCandidate stores one lookup key for a syntax edge that lacks a graph endpoint.
// @intent let newly added symbols select affected unchanged callers without reparsing the whole graph.
type UnresolvedEdgeCandidate struct {
	ID              uint     `gorm:"primaryKey"`
	Namespace       string   `gorm:"size:256;not null;default:'default';uniqueIndex:idx_unresolved_ns_fp_hash"`
	LookupKey       string   `gorm:"type:text;not null"`
	LookupKeyHash   string   `gorm:"size:64;not null;index:idx_unresolved_lookup_hash"`
	Fingerprint     string   `gorm:"type:text;not null"`
	FingerprintHash string   `gorm:"size:64;not null;uniqueIndex:idx_unresolved_ns_fp_hash"`
	FilePath        string   `gorm:"size:1024;not null;index"`
	Kind            EdgeKind `gorm:"size:32;not null"`
	Line            int
	CreatedAt       time.Time
}

// Edge converts the durable candidate back into resolver input.
// @intent keep unresolved storage separate from traversable graph edges while reusing the resolver contract.
func (c UnresolvedEdgeCandidate) Edge() Edge {
	return Edge{Kind: c.Kind, FilePath: c.FilePath, Line: c.Line, Fingerprint: c.Fingerprint}
}

// UnresolvedIndexState marks a namespace whose last full build populated the unresolved reverse index.
// @intent prevent upgraded databases with an empty, uninitialized index from taking an unsafe incremental shortcut.
type UnresolvedIndexState struct {
	Namespace string `gorm:"primaryKey;size:256"`
	Version   string `gorm:"size:64;not null;default:''"`
	UpdatedAt time.Time
}
