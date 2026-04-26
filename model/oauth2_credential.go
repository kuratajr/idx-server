package model

import "time"

type Oauth2Credential struct {
	ID uint64 `gorm:"primaryKey" json:"id"`

	Provider          string `gorm:"size:32;not null;index;uniqueIndex:uidx_provider_account" json:"provider"`
	ExternalAccountID string `gorm:"size:128;not null;index;uniqueIndex:uidx_provider_account" json:"external_account_id"`

	Email       string `gorm:"size:256;index" json:"email,omitempty"`
	DisplayName string `gorm:"size:256" json:"display_name,omitempty"`
	Scopes      string `gorm:"type:text" json:"scopes,omitempty"`

	TokenType       string `gorm:"size:32" json:"token_type,omitempty"`
	AccessTokenEnc  string `gorm:"type:text" json:"-"`
	RefreshTokenEnc string `gorm:"type:text" json:"-"`
	ExpiresAt       time.Time `gorm:"index" json:"expires_at,omitempty"`
	LastRefreshedAt time.Time `gorm:"index" json:"last_refreshed_at,omitempty"`

	Revoked         bool      `gorm:"not null;default:false;index" json:"revoked"`
	RevokedAt       time.Time `gorm:"index" json:"revoked_at,omitempty"`
	RevokedByUserID uint64    `gorm:"index" json:"revoked_by_user_id,omitempty"`
	RevokeReason    string    `gorm:"type:text" json:"revoke_reason,omitempty"`

	CreatedByUserID uint64    `gorm:"index;not null" json:"created_by_user_id"`
	CreatedAt       time.Time `gorm:"index" json:"created_at"`
	UpdatedAt       time.Time `gorm:"index" json:"updated_at"`
}

