package model

import "time"

// Oauth2CredentialUsageLog is an append-only audit log.
// Keep it small per row; store only metadata (never store tokens).
type Oauth2CredentialUsageLog struct {
	ID uint64 `gorm:"primaryKey" json:"id"`

	CredentialID uint64 `gorm:"not null;index" json:"credential_id"`
	UserID       uint64 `gorm:"not null;index" json:"user_id"`

	Action string `gorm:"size:32;not null;index" json:"action"` // e.g. "use", "refresh", "deny"
	Detail string `gorm:"type:text" json:"detail,omitempty"`

	CreatedAt time.Time `gorm:"index" json:"created_at"`
}

