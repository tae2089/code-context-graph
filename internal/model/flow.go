package model

import "time"

type Flow struct {
	ID          uint   `gorm:"primaryKey"`
	Name        string `gorm:"size:256;not null"`
	Description string `gorm:"type:text"`
	CreatedAt   time.Time

	Members []FlowMembership `gorm:"foreignKey:FlowID"`
}

type FlowMembership struct {
	ID      uint `gorm:"primaryKey"`
	FlowID  uint `gorm:"not null;index"`
	NodeID  uint `gorm:"not null;index"`
	Ordinal int  `gorm:"not null"`
}
