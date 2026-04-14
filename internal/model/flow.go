package model

import "time"

// Flow는 추적된 호출 흐름의 메타데이터를 저장한다.
// @intent 의미 있는 실행 흐름을 이름과 설명으로 식별한다.
type Flow struct {
	ID          uint   `gorm:"primaryKey"`
	Name        string `gorm:"size:256;not null"`
	Description string `gorm:"type:text"`
	CreatedAt   time.Time

	Members []FlowMembership `gorm:"foreignKey:FlowID"`
}

// FlowMembership는 흐름에 포함된 노드의 순서를 저장한다.
// @intent 특정 플로우를 구성하는 노드와 그 위치를 연결한다.
type FlowMembership struct {
	ID      uint `gorm:"primaryKey"`
	FlowID  uint `gorm:"not null;index"`
	NodeID  uint `gorm:"not null;index"`
	Ordinal int  `gorm:"not null"`
}
