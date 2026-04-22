package model

import "time"

// Template stores a raw template (text) that can be rendered later.
type Template struct {
	ID uint64 `gorm:"primaryKey" json:"id"`

	// Name is a human-friendly unique identifier.
	Name string `gorm:"uniqueIndex;size:128;not null" json:"name"`

	// Type groups templates by use-case (agent_config, agent_install, script, yaml, ...).
	Type string `gorm:"index;size:64;not null" json:"type"`

	// ContentRaw is the raw template body (TEXT in SQLite).
	ContentRaw string `gorm:"type:text;not null" json:"content_raw,omitempty"`

	// VarsSchema optionally describes variables for UI/validation (JSON).
	VarsSchema string `gorm:"type:text" json:"vars_schema,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

