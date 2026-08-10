package graph

// SearchDocument는 노드 검색용 색인 문서를 저장한다.
// @intent 전문 검색 백엔드가 사용할 노드별 검색 본문을 유지한다.
type SearchDocument struct {
	ID        uint   `gorm:"primaryKey"`
	Namespace string `gorm:"type:text;not null;default:'default';index;uniqueIndex:idx_searchdoc_ns_node"`
	NodeID    uint   `gorm:"not null;uniqueIndex:idx_searchdoc_ns_node"`
	Content   string `gorm:"type:text;not null"`
	Language  string `gorm:"type:text;index"`
}

// SearchReason holds one recorded reason a node exists: the text of a single
// @intent or @domainRule tag.
//
// It is a row per reason, not a column on SearchDocument, because scoring reads
// each row as one document. A node that recorded three domain rules is three
// rows here, so a question matching its @intent is scored on that sentence's
// length and not on the length of three rules it never touched. It is also kept
// out of SearchDocument.Content, so a question about why the code exists is
// never scored against what the code happens to be called.
//
// NodeID stays on every row, which is what lets a caller count declarations
// rather than reasons — annotation coverage is a fraction of declarations, and
// counting rows would report a heavily annotated node several times.
// @intent give every recorded reason its own scored document while keeping the declaration it belongs to countable.
// Rows are written in the order the author wrote the tags, so reading them back
// by id restores that order.
type SearchReason struct {
	ID        uint   `gorm:"primaryKey"`
	Namespace string `gorm:"type:text;not null;default:'default';index:idx_searchreason_ns_node"`
	NodeID    uint   `gorm:"not null;index:idx_searchreason_ns_node"`
	Content   string `gorm:"type:text;not null"`
}
