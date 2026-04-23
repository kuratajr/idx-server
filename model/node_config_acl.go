package model

import "time"

// NodeConfigPermission is a bitmask stored in NodeConfigACL.PermMask.
type NodeConfigPermission uint32

const (
	NodeConfigPermReadMeta NodeConfigPermission = 1 << iota
	NodeConfigPermReadVars
	NodeConfigPermWrite
	NodeConfigPermDelete
)

func (p NodeConfigPermission) Has(need NodeConfigPermission) bool { return p&need == need }

// NodeConfigACL grants a user permissions to a node config.
// Admins bypass ACL checks at the API layer.
type NodeConfigACL struct {
	ID uint64 `gorm:"primaryKey" json:"id"`

	ConfigID uint64 `gorm:"not null;index;uniqueIndex:uidx_node_config_user" json:"config_id"`
	UserID   uint64 `gorm:"not null;index;uniqueIndex:uidx_node_config_user" json:"user_id"`

	PermMask uint32 `gorm:"not null;default:0" json:"perm_mask"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

