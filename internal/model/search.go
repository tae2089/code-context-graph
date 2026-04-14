package model

// SearchDocument는 노드 검색용 색인 문서를 저장한다.
// @intent 전문 검색 백엔드가 사용할 노드별 검색 본문을 유지한다.
type SearchDocument struct {
	ID       uint   `gorm:"primaryKey"`
	NodeID   uint   `gorm:"uniqueIndex;not null"`
	Content  string `gorm:"type:text;not null"`
	Language string `gorm:"size:32;index"`
}
