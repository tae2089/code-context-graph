// @index Materialized cross-namespace annotation references for federated graph analysis.
package graph

import "time"

// CrossRefStatus describes whether a cross-namespace reference currently resolves to a node.
// @intent distinguish navigable references from dangling ones without deleting authored links.
type CrossRefStatus string

const (
	CrossRefStatusResolved CrossRefStatus = "resolved"
	CrossRefStatusDead     CrossRefStatus = "dead"
)

// CrossRefSource records which signal produced a cross-namespace reference.
// @intent keep room for future non-annotation signals (e.g. import mapping) without schema rework.
type CrossRefSource string

const CrossRefSourceAnnotation CrossRefSource = "annotation"

// CrossRef materializes one @see ccg:// annotation tag as queryable cross-namespace graph state.
// @intent make annotation-declared repository links traversable and listable instead of plain tag text.
// @domainRule target identity is symbolic (namespace, path, symbol); resolved_node_id is derived state that rebuilds change.
// @domainRule rows for one source namespace are fully replaced on each build, so no uniqueness constraint is required.
type CrossRef struct {
	ID             uint           `gorm:"primaryKey"`
	FromNamespace  string         `gorm:"type:text;not null;index:idx_crossref_from_ns"`
	FromNodeID     uint           `gorm:"not null;index:idx_crossref_from_node"`
	Raw            string         `gorm:"type:text;not null"`
	ToNamespace    string         `gorm:"type:text;not null;index:idx_crossref_to_ns"`
	ToPath         string         `gorm:"type:text;not null;default:''"`
	ToSymbol       string         `gorm:"type:text;not null;default:''"`
	ResolvedNodeID *uint          `gorm:"index:idx_crossref_resolved_node"`
	Status         CrossRefStatus `gorm:"type:text;not null"`
	Source         CrossRefSource `gorm:"type:text;not null;default:'annotation'"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
