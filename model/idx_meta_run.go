package model

import "time"

type IdxMetaRunStatus string

const (
	IdxMetaRunSkipped    IdxMetaRunStatus = "skipped"
	IdxMetaRunDispatched IdxMetaRunStatus = "dispatched"
	IdxMetaRunFetched    IdxMetaRunStatus = "fetched"
	IdxMetaRunSuccess    IdxMetaRunStatus = "success"
	IdxMetaRunFailed     IdxMetaRunStatus = "failed"
)

// IdxMetaRun is a high-level timeline record for one meta bootstrap run.
// ID is used as Task.Id for correlation with TaskResult.
type IdxMetaRun struct {
	ID uint64 `gorm:"primaryKey" json:"id"`

	ServerID      uint64 `gorm:"not null;index" json:"server_id"`
	WorkspaceSlug string `gorm:"size:128;not null;index" json:"workspace_slug"`
	BootTime      uint64 `gorm:"not null;index" json:"boot_time"`

	AssignmentID uint64 `gorm:"index" json:"assignment_id,omitempty"`
	TargetType   string `gorm:"size:16;index" json:"target_type,omitempty"`
	Priority     int    `json:"priority,omitempty"`
	ConfigID     uint64 `gorm:"index" json:"config_id,omitempty"`
	TemplateID   uint64 `gorm:"index" json:"template_id,omitempty"`

	Status     IdxMetaRunStatus `gorm:"size:16;not null;index" json:"status"`
	Successful *bool            `json:"successful,omitempty"`

	Command string `gorm:"type:text" json:"command,omitempty"`
	Output  string `gorm:"type:text" json:"output,omitempty"`
	Error   string `gorm:"type:text" json:"error,omitempty"`

	DispatchedAt time.Time `gorm:"index" json:"dispatched_at"`
	FetchedAt    time.Time `gorm:"index" json:"fetched_at"`
	FinishedAt   time.Time `gorm:"index" json:"finished_at"`

	CreatedAt time.Time `gorm:"index" json:"created_at"`
	UpdatedAt time.Time `gorm:"index" json:"updated_at"`
}

