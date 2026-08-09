package graph

// SearchDocument는 노드 검색용 색인 문서를 저장한다.
// @intent 전문 검색 백엔드가 사용할 노드별 검색 본문을 유지한다.
type SearchDocument struct {
	ID        uint   `gorm:"primaryKey"`
	Namespace string `gorm:"type:text;not null;default:'default';index;uniqueIndex:idx_searchdoc_ns_node"`
	NodeID    uint   `gorm:"not null;uniqueIndex:idx_searchdoc_ns_node"`
	Content   string `gorm:"type:text;not null"`
	// IntentContent holds only the reason the node exists (@intent, @domainRule).
	// It is indexed separately from Content so an intent question is scored on the
	// reason rather than on whatever the node happens to be called.
	IntentContent string `gorm:"type:text;not null;default:''"`
	Language      string `gorm:"type:text;index"`
}
