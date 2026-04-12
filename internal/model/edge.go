package model

import "time"

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

type Edge struct {
	ID          uint     `gorm:"primaryKey"`
	FromNodeID  uint     `gorm:"not null;index"`
	ToNodeID    uint     `gorm:"not null;index"`
	Kind        EdgeKind `gorm:"size:32;not null;index"`
	FilePath    string   `gorm:"size:1024"`
	Line        int
	Fingerprint string `gorm:"uniqueIndex;size:128;not null"`
	CreatedAt   time.Time

	FromNode Node `gorm:"foreignKey:FromNodeID"`
	ToNode   Node `gorm:"foreignKey:ToNodeID"`
}
