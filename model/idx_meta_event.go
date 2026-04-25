package model

import "time"

type IdxMetaEventStage string

const (
	IdxMetaStageDispatchAttempt IdxMetaEventStage = "dispatch_attempt"
	IdxMetaStageDispatched      IdxMetaEventStage = "dispatched"
	IdxMetaStageSendFailed      IdxMetaEventStage = "send_failed"
	IdxMetaStageScriptFetched   IdxMetaEventStage = "script_fetched"
	IdxMetaStageResult          IdxMetaEventStage = "result"
)

// IdxMetaEvent is a lightweight, time-ordered audit log for IDX meta bootstrap.
// This is intended for debugging "did it run?" questions.
type IdxMetaEvent struct {
	ID uint64 `gorm:"primaryKey" json:"id"`

	ServerID      uint64 `gorm:"not null;index" json:"server_id"`
	WorkspaceSlug string `gorm:"size:128;not null;index" json:"workspace_slug"`
	BootTime      uint64 `gorm:"not null;index" json:"boot_time"`

	Stage IdxMetaEventStage `gorm:"size:32;not null;index" json:"stage"`

	AssignmentID uint64 `gorm:"index" json:"assignment_id,omitempty"`
	TargetType   string `gorm:"size:16;index" json:"target_type,omitempty"`
	Priority     int    `json:"priority,omitempty"`
	ConfigID     uint64 `gorm:"index" json:"config_id,omitempty"`
	TemplateID   uint64 `gorm:"index" json:"template_id,omitempty"`

	Successful *bool  `json:"successful,omitempty"`
	Message    string `gorm:"type:text" json:"message,omitempty"`

	CreatedAt time.Time `gorm:"index" json:"created_at"`
}

