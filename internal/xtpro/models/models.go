package models

import (
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID        uuid.UUID `json:"id" db:"id"`
	Username  string    `json:"username" db:"username"`
	Email     string    `json:"email" db:"email"`
	Password  string    `json:"-" db:"password"`
	Role      string    `json:"role" db:"role"`
	APIKey    string    `json:"api_key,omitempty" db:"api_key"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

type Tunnel struct {
	ID         uuid.UUID `json:"id" db:"id"`
	UserID     uuid.UUID `json:"user_id" db:"user_id"`
	Name       string    `json:"name" db:"name"`
	Protocol   string    `json:"protocol" db:"protocol"`
	LocalHost  string    `json:"local_host" db:"local_host"`
	LocalPort  int       `json:"local_port" db:"local_port"`
	PublicPort int       `json:"public_port" db:"public_port"`
	Status     string    `json:"status" db:"status"`
	ClientID   string    `json:"client_id" db:"client_id"`
	AuthToken  string    `json:"auth_token" db:"auth_token"`
	CreatedAt  time.Time `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time `json:"updated_at" db:"updated_at"`
	LastSeen   time.Time `json:"last_seen" db:"last_seen"`
}

type Connection struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	TunnelID       uuid.UUID  `json:"tunnel_id" db:"tunnel_id"`
	RemoteAddr     string     `json:"remote_addr" db:"remote_addr"`
	ConnectedAt    time.Time  `json:"connected_at" db:"connected_at"`
	DisconnectedAt *time.Time `json:"disconnected_at,omitempty" db:"disconnected_at"`
	BytesUp        int64      `json:"bytes_up" db:"bytes_up"`
	BytesDown      int64      `json:"bytes_down" db:"bytes_down"`
	Duration       int64      `json:"duration" db:"duration"`
}

type Metrics struct {
	ActiveTunnels    int64  `json:"active_tunnels"`
	TotalConnections int64  `json:"total_connections"`
	TotalBytesUp     int64  `json:"total_bytes_up"`
	TotalBytesDown   int64  `json:"total_bytes_down"`
	ActiveUsers      int64  `json:"active_users"`
	Uptime           string `json:"uptime"`
}

type TunnelStats struct {
	TunnelID    uuid.UUID `json:"tunnel_id"`
	Connections int64     `json:"connections"`
	BytesUp     int64     `json:"bytes_up"`
	BytesDown   int64     `json:"bytes_down"`
	LastActive  time.Time `json:"last_active"`
}

type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
	Message string      `json:"message,omitempty"`
}

const (
	TunnelStatusActive   = "active"
	TunnelStatusInactive = "inactive"
	TunnelStatusError    = "error"

	UserRoleAdmin = "admin"
	UserRoleUser  = "user"
)

