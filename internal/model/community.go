package model

import "time"

type Community struct {
	ID          uint   `gorm:"primaryKey"`
	Key         string `gorm:"uniqueIndex;size:512;not null"`
	Label       string `gorm:"size:256;not null"`
	Strategy    string `gorm:"size:32;not null;index"`
	Description string `gorm:"type:text"`
	CreatedAt   time.Time
	UpdatedAt   time.Time

	Members []CommunityMembership `gorm:"foreignKey:CommunityID"`
}

type CommunityMembership struct {
	ID          uint `gorm:"primaryKey"`
	CommunityID uint `gorm:"not null;uniqueIndex:idx_community_node"`
	NodeID      uint `gorm:"not null;uniqueIndex:idx_community_node;index"`
	CreatedAt   time.Time
}
