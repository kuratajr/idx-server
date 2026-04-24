package model

import "time"

// RawConfig stores a single raw text config for nodes to fetch via curl.
// This is intentionally a singleton row (ID=1).
type RawConfig struct {
	ID uint64 `gorm:"primaryKey" json:"id"`

	// Content is returned as text/plain to nodes.
	Content string `gorm:"type:text;not null;default:''" json:"content"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

