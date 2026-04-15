package model

import "time"

// NodeKind는 그래프 노드의 선언 분류를 나타낸다.
// @intent 파싱된 선언을 검색과 분석에 필요한 종류로 구분한다.
type NodeKind string

const (
	NodeKindFile     NodeKind = "file"
	NodeKindClass    NodeKind = "class"
	NodeKindFunction NodeKind = "function"
	NodeKindType     NodeKind = "type"
	NodeKindTest     NodeKind = "test"
)

// Node는 코드 그래프의 단일 선언 엔티티를 저장한다.
// @intent 파일 내 선언의 정체성과 위치 정보를 영속화한다.
type Node struct {
	ID            uint     `gorm:"primaryKey"`
	Namespace     string   `gorm:"size:256;not null;default:'';uniqueIndex:idx_ns_qn"`
	QualifiedName string   `gorm:"size:512;not null;uniqueIndex:idx_ns_qn"`
	Kind          NodeKind `gorm:"size:32;not null;index"`
	Name          string   `gorm:"size:256;not null"`
	FilePath      string   `gorm:"size:768;not null;index"`
	StartLine     int      `gorm:"not null"`
	EndLine       int      `gorm:"not null"`
	Hash          string   `gorm:"size:64"`
	Language      string   `gorm:"size:32;index"`
	CreatedAt     time.Time
	UpdatedAt     time.Time

	Annotation *Annotation `gorm:"foreignKey:NodeID"`
}
