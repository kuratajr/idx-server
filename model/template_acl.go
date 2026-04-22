package model

import "time"

// TemplatePermission is a bitmask stored in TemplateACL.PermMask.
type TemplatePermission uint32

const (
	TemplatePermReadMeta TemplatePermission = 1 << iota
	TemplatePermReadRaw
	TemplatePermWrite
	TemplatePermDelete
)

func (p TemplatePermission) Has(need TemplatePermission) bool { return p&need == need }

// TemplateACL grants a user permissions to a template.
// Admins bypass ACL checks at the API layer.
type TemplateACL struct {
	ID uint64 `gorm:"primaryKey" json:"id"`

	TemplateID uint64 `gorm:"not null;index;uniqueIndex:uidx_template_user" json:"template_id"`
	UserID     uint64 `gorm:"not null;index;uniqueIndex:uidx_template_user" json:"user_id"`

	PermMask uint32 `gorm:"not null;default:0" json:"perm_mask"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

