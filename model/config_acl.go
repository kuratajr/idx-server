package model

import "time"

// ConfigPermission is a bitmask stored in ConfigACL.PermMask.
type ConfigPermission uint32

const (
	ConfigPermReadMeta ConfigPermission = 1 << iota
	ConfigPermReadVars
	ConfigPermWrite
	ConfigPermDelete
)

func (p ConfigPermission) Has(need ConfigPermission) bool { return p&need == need }

// ConfigACL grants a user permissions to a config.
// Admins bypass ACL checks at the API layer.
type ConfigACL struct {
	ID uint64 `gorm:"primaryKey" json:"id"`

	ConfigID uint64 `gorm:"not null;index;uniqueIndex:uidx_config_user" json:"config_id"`
	UserID   uint64 `gorm:"not null;index;uniqueIndex:uidx_config_user" json:"user_id"`

	PermMask uint32 `gorm:"not null;default:0" json:"perm_mask"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

