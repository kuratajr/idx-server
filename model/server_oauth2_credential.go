package model

import "time"

// ServerOauth2Credential is a join table mapping servers to oauth2 credentials.
// This enables 1 server -> many accounts, and 1 account -> many servers.
type ServerOauth2Credential struct {
	ID uint64 `gorm:"primaryKey" json:"id"`

	ServerID     uint64 `gorm:"not null;index;uniqueIndex:uidx_server_cred" json:"server_id"`
	CredentialID uint64 `gorm:"not null;index;uniqueIndex:uidx_server_cred" json:"credential_id"`

	// Provider is redundant (Credential has it) but makes filtering/queries easier.
	Provider string `gorm:"size:32;not null;index" json:"provider"`

	CreatedAt time.Time `gorm:"index" json:"created_at"`
	UpdatedAt time.Time `gorm:"index" json:"updated_at"`
}

