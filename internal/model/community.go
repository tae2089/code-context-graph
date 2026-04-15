package model

import "time"

// Community는 커뮤니티 분석 결과의 그룹 메타데이터를 저장한다.
// @intent 연관된 노드 집합을 전략별 커뮤니티 단위로 표현한다.
type Community struct {
	ID          uint   `gorm:"primaryKey"`
	Namespace   string `gorm:"size:256;not null;default:'';uniqueIndex:idx_community_ns_key"`
	Key         string `gorm:"size:512;not null;uniqueIndex:idx_community_ns_key"`
	Label       string `gorm:"size:256;not null"`
	Strategy    string `gorm:"size:32;not null;index"`
	Description string `gorm:"type:text"`
	CreatedAt   time.Time
	UpdatedAt   time.Time

	Members []CommunityMembership `gorm:"foreignKey:CommunityID"`
}

// CommunityMembership는 노드와 커뮤니티의 소속 관계를 저장한다.
// @intent 특정 노드가 어떤 커뮤니티에 속하는지 연결한다.
type CommunityMembership struct {
	ID          uint `gorm:"primaryKey"`
	CommunityID uint `gorm:"not null;uniqueIndex:idx_community_node"`
	NodeID      uint `gorm:"not null;uniqueIndex:idx_community_node;index"`
	CreatedAt   time.Time
}
