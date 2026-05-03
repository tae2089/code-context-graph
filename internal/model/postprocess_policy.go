package model

import "time"

// PostprocessPolicyState stores the latest effective postprocess policy per namespace and tool.
// @intent 자동 정책 엔진의 최신 판단 상태를 namespace/tool 단위로 유지한다.
type PostprocessPolicyState struct {
	Namespace string    `gorm:"primaryKey;size:256"`
	Tool      string    `gorm:"primaryKey;size:64"`
	Policy    string    `gorm:"size:32;not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

func (PostprocessPolicyState) TableName() string {
	return "ccg_postprocess_policy_state"
}

// PostprocessRunLog appends the effective policy and outcome of each run.
// @intent 자동 정책 엔진의 실행 이력과 결과를 추적해 후속 판단 근거로 사용한다.
type PostprocessRunLog struct {
	ID           uint      `gorm:"primaryKey"`
	Namespace    string    `gorm:"size:256;not null;index:idx_pp_log_ns_tool_time,priority:1"`
	Tool         string    `gorm:"size:64;not null;index:idx_pp_log_ns_tool_time,priority:2"`
	Policy       string    `gorm:"size:32;not null"`
	Source       string    `gorm:"size:16;not null"`
	Status       string    `gorm:"size:16;not null"`
	FailedSteps  string    `gorm:"type:text;not null;default:'[]'"`
	SkippedSteps string    `gorm:"type:text;not null;default:'[]'"`
	CreatedAt    time.Time `gorm:"not null;index:idx_pp_log_ns_tool_time,priority:3,sort:desc"`
}

func (PostprocessRunLog) TableName() string {
	return "ccg_postprocess_run_logs"
}
