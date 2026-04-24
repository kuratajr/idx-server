package model

import "time"

// NodeBootRun records that a given script has been dispatched for a server boot.
// Used to guarantee "run once per boot", even across disconnect/reconnect.
type NodeBootRun struct {
	ID uint64 `gorm:"primaryKey" json:"id"`

	ServerID uint64 `gorm:"not null;index:idx_node_boot_run_unique,unique" json:"server_id"`
	BootTime uint64 `gorm:"not null;index:idx_node_boot_run_unique,unique" json:"boot_time"`
	ScriptID string `gorm:"size:64;not null;index:idx_node_boot_run_unique,unique" json:"script_id"`

	DispatchedAt time.Time `json:"dispatched_at"`
	CreatedAt    time.Time `json:"created_at"`
}

