package model

import "time"

type NodeKind string

const (
	NodeKindFile     NodeKind = "file"
	NodeKindClass    NodeKind = "class"
	NodeKindFunction NodeKind = "function"
	NodeKindType     NodeKind = "type"
	NodeKindTest     NodeKind = "test"
)

type Node struct {
	ID            uint     `gorm:"primaryKey"`
	QualifiedName string   `gorm:"uniqueIndex;size:512;not null"`
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
