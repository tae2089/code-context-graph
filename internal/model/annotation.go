package model

import "time"

// TagKind는 DocTag의 종류를 나타낸다.
// @intent 구조화된 문서 태그의 의미 분류를 표준화한다.
type TagKind string

const (
	TagParam      TagKind = "param"
	TagReturn     TagKind = "return"
	TagSee        TagKind = "see"
	TagIntent     TagKind = "intent"
	TagDomainRule TagKind = "domainRule"
	TagSideEffect TagKind = "sideEffect"
	TagMutates    TagKind = "mutates"
	TagRequires   TagKind = "requires"
	TagEnsures    TagKind = "ensures"
	TagIndex      TagKind = "index"
	TagThrows     TagKind = "throws"
	TagTypedef    TagKind = "typedef"
)

// Annotation은 코드 선언에 연결된 구조화된 주석이다.
// @intent 노드에 연결된 요약과 태그 메타데이터를 영속화한다.
type Annotation struct {
	ID        uint   `gorm:"primaryKey"`
	NodeID    uint   `gorm:"uniqueIndex;not null"`
	Summary   string `gorm:"size:1024"`
	Context   string `gorm:"size:2048"`
	RawText   string `gorm:"type:text"`
	CreatedAt time.Time
	UpdatedAt time.Time

	Tags []DocTag `gorm:"foreignKey:AnnotationID"`
}

// DocTag는 Annotation 내의 개별 태그이다.
// @intent 어노테이션의 단일 구조화 태그 항목을 표현한다.
type DocTag struct {
	ID           uint    `gorm:"primaryKey"`
	AnnotationID uint    `gorm:"not null;index"`
	Kind         TagKind `gorm:"size:32;not null;index"`
	Name         string  `gorm:"size:128"`
	Value        string  `gorm:"type:text;not null"`
	Ordinal      int     `gorm:"not null"`
	CreatedAt    time.Time
}
