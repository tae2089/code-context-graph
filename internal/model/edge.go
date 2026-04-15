package model

import "time"

// EdgeKind는 노드 간 관계의 종류를 나타낸다.
// @intent 그래프 엣지의 의미를 일관된 관계 타입으로 구분한다.
type EdgeKind string

const (
	EdgeKindCalls       EdgeKind = "calls"
	EdgeKindImportsFrom EdgeKind = "imports_from"
	EdgeKindInherits    EdgeKind = "inherits"
	EdgeKindImplements  EdgeKind = "implements"
	EdgeKindContains    EdgeKind = "contains"
	EdgeKindTestedBy    EdgeKind = "tested_by"
	EdgeKindDependsOn   EdgeKind = "depends_on"
	EdgeKindReferences  EdgeKind = "references"
)

// Edge는 두 노드 사이의 방향성 관계를 저장한다.
// @intent 코드 그래프에서 선언 간 연결과 그 출처를 영속화한다.
type Edge struct {
	ID          uint     `gorm:"primaryKey"`
	FromNodeID  uint     `gorm:"index"`
	ToNodeID    uint     `gorm:"index"`
	Kind        EdgeKind `gorm:"size:32;not null;index"`
	FilePath    string   `gorm:"size:1024;index"`
	Line        int
	Fingerprint string `gorm:"uniqueIndex;size:128;not null"`
	CreatedAt   time.Time

	FromNode Node `gorm:"foreignKey:FromNodeID;constraint:-"`
	ToNode   Node `gorm:"foreignKey:ToNodeID;constraint:-"`
}
