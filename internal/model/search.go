package model

type SearchDocument struct {
	ID       uint   `gorm:"primaryKey"`
	NodeID   uint   `gorm:"uniqueIndex;not null"`
	Content  string `gorm:"type:text;not null"`
	Language string `gorm:"size:32;index"`
}
