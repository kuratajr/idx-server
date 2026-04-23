package model

import "time"

type NodeConfigTargetType string

const (
	NodeConfigTargetServer NodeConfigTargetType = "server"
	NodeConfigTargetGroup  NodeConfigTargetType = "group"
	NodeConfigTargetTag    NodeConfigTargetType = "tag"
)

// NodeConfigAssignment maps a NodeConfig to a target (server/group/tag).
// Multiple assignments can exist; resolution uses priority and enabled.
type NodeConfigAssignment struct {
	ID uint64 `gorm:"primaryKey" json:"id"`

	ConfigID uint64 `gorm:"not null;index;uniqueIndex:uidx_cfg_target" json:"config_id"`

	// BatchID groups multiple rows created in one action (e.g. bulk create).
	BatchID string `gorm:"size:36;index" json:"batch_id,omitempty"`

	TargetType NodeConfigTargetType `gorm:"size:16;not null;index;uniqueIndex:uidx_cfg_target" json:"target_type"`

	ServerID uint64 `gorm:"index;uniqueIndex:uidx_cfg_target" json:"server_id"`
	GroupID  uint64 `gorm:"index;uniqueIndex:uidx_cfg_target" json:"group_id"`
	TagName  string `gorm:"size:128;index;uniqueIndex:uidx_cfg_target" json:"tag_name"`

	Priority int  `gorm:"not null;default:0;index" json:"priority"`
	Enabled  bool `gorm:"not null;default:true;index" json:"enabled"`

	CreatedBy uint64 `gorm:"index;not null" json:"created_by"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

