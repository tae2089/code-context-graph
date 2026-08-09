package graph

import "time"

// NodeKind는 그래프 노드의 선언 분류를 나타낸다.
// @intent 파싱된 선언을 검색과 분석에 필요한 종류로 구분한다.
type NodeKind string

const (
	NodeKindFile     NodeKind = "file"
	NodeKindPackage  NodeKind = "package"
	NodeKindClass    NodeKind = "class"
	NodeKindFunction NodeKind = "function"
	NodeKindType     NodeKind = "type"
	NodeKindTest     NodeKind = "test"
)

// Node는 코드 그래프의 단일 선언 엔티티를 저장한다.
// @intent 파일 내 선언의 정체성과 위치 정보를 영속화한다.
type Node struct {
	ID            uint     `gorm:"primaryKey"`
	Namespace     string   `gorm:"type:text;not null;default:'default';uniqueIndex:idx_ns_qn_fp_sl;index:idx_nodes_ns_file_path,priority:1"`
	QualifiedName string   `gorm:"type:text;not null;uniqueIndex:idx_ns_qn_fp_sl"`
	Kind          NodeKind `gorm:"type:text;not null;index"`
	Name          string   `gorm:"type:text;not null"`
	FilePath      string   `gorm:"type:text;not null;index;uniqueIndex:idx_ns_qn_fp_sl;index:idx_nodes_ns_file_path,priority:2"`
	StartLine     int      `gorm:"not null;uniqueIndex:idx_ns_qn_fp_sl"`
	EndLine       int      `gorm:"not null"`
	Hash          string   `gorm:"type:text"`
	Language      string   `gorm:"type:text;index"`
	CreatedAt     time.Time
	UpdatedAt     time.Time

	Annotation *Annotation `gorm:"foreignKey:NodeID"`
}

// Intent returns what the author said this declaration is for, or an empty
// string when nobody wrote it down.
//
// It is the one annotation field search puts in front of a reader, because it
// carries what the identifier cannot. `Shutdown` already says it stops
// something; its intent says it exists to give in-flight webhook syncs a
// bounded way out. The summary field mostly restates the name.
//
// @requires the node's Annotation and its Tags are loaded; an unloaded association reads as absent.
// @ensures returns the first intent tag's value, so repeated calls agree.
// @intent give search one line of author-written purpose to show beside a result.
func (n Node) Intent() string {
	if n.Annotation == nil {
		return ""
	}
	for _, tag := range n.Annotation.Tags {
		if tag.Kind == TagIntent {
			return tag.Value
		}
	}
	return ""
}
