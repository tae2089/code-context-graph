package graph

import "time"

// EdgeKind는 노드 간 관계의 종류를 나타낸다.
// @intent 그래프 엣지의 의미를 일관된 관계 타입으로 구분한다.
type EdgeKind string

const (
	EdgeKindCalls         EdgeKind = "calls"
	EdgeKindFallbackCalls EdgeKind = "fallback_calls"
	EdgeKindImportsFrom   EdgeKind = "imports_from"
	EdgeKindInherits      EdgeKind = "inherits"
	EdgeKindImplements    EdgeKind = "implements"
	EdgeKindContains      EdgeKind = "contains"
	EdgeKindTestedBy      EdgeKind = "tested_by"
	EdgeKindDependsOn     EdgeKind = "depends_on"
	EdgeKindReferences    EdgeKind = "references"
)

// CallEdgeKinds returns edge kinds that represent a callable relationship.
//
// Fallback call resolution stores low-confidence edges as EdgeKindFallbackCalls, but callers
// that want to traverse call behavior should usually include both values.
// @intent centralize call-kind handling for traversal and filtering paths.
func CallEdgeKinds() []EdgeKind {
	return []EdgeKind{EdgeKindCalls, EdgeKindFallbackCalls}
}

// IsCallKind reports whether kind represents a call edge.
//
// This helper keeps fallback call-kind handling centralized for traversal and filtering paths.
// @intent centralize call-kind handling for traversal and filtering paths.
func IsCallKind(kind EdgeKind) bool {
	return kind == EdgeKindCalls || kind == EdgeKindFallbackCalls
}

// Edge는 두 노드 사이의 방향성 관계를 저장한다.
// @intent 코드 그래프에서 선언 간 연결과 그 출처를 영속화한다.
type Edge struct {
	ID          uint     `gorm:"primaryKey"`
	Namespace   string   `gorm:"size:256;not null;default:'default';index;uniqueIndex:idx_edges_namespace_fingerprint"`
	FromNodeID  uint     `gorm:"index"`
	ToNodeID    uint     `gorm:"index"`
	Kind        EdgeKind `gorm:"size:32;not null;index"`
	FilePath    string   `gorm:"size:1024;index"`
	Line        int
	Fingerprint string `gorm:"type:text;not null;uniqueIndex:idx_edges_namespace_fingerprint"`
	CreatedAt   time.Time

	FromNode Node `gorm:"foreignKey:FromNodeID;constraint:-"`
	ToNode   Node `gorm:"foreignKey:ToNodeID;constraint:-"`
}
