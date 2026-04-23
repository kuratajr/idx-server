package model

import "time"

// NodeConfig stores a set of variable overrides bound to a Template.
// Mapping to server/group will be implemented later.
type NodeConfig struct {
	ID uint64 `gorm:"primaryKey" json:"id"`

	Name string `gorm:"index;size:128;not null" json:"name"`

	TemplateID uint64 `gorm:"not null;index" json:"template_id"`

	// VarsOverride stores JSON object: {"KEY":"value", ...}
	VarsOverride string `gorm:"type:text;not null;default:'{}'" json:"vars_override,omitempty"`

	Enabled bool `gorm:"not null;default:true" json:"enabled"`

	CreatedBy uint64 `gorm:"index;not null" json:"created_by"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

