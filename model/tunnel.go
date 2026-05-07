package model

import (
	"strconv"
	"strings"
	"time"
)

type TunnelProtocol string

const (
	TunnelProtocolTCP  TunnelProtocol = "tcp"
	TunnelProtocolUDP  TunnelProtocol = "udp"
	TunnelProtocolHTTP TunnelProtocol = "http"
	TunnelProtocolFile TunnelProtocol = "file"
)

type TunnelState string

const (
	TunnelStatePending    TunnelState = "pending"
	TunnelStateConnecting TunnelState = "connecting"
	TunnelStateActive     TunnelState = "active"
	TunnelStateDegraded   TunnelState = "degraded"
	TunnelStateError      TunnelState = "error"
	TunnelStateStopped    TunnelState = "stopped"
)

type TunnelFileShareSpec struct {
	Path        string `json:"path"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	Permissions string `json:"permissions"`
}

type TunnelSpec struct {
	ID                 string               `json:"id"`
	Name               string               `json:"name,omitempty"`
	ServerAddr         string               `json:"server_addr"`
	ClientID           string               `json:"client_id,omitempty"`
	Protocol           TunnelProtocol       `json:"protocol"`
	LocalHost          string               `json:"local_host,omitempty"`
	LocalPort          int                  `json:"local_port,omitempty"`
	RequestedPort      int                  `json:"requested_port,omitempty"`
	Enabled            bool                 `json:"enabled"`
	InsecureSkipVerify bool                 `json:"insecure_skip_verify,omitempty"`
	CertFingerprint    string               `json:"cert_fingerprint,omitempty"`
	FileShare          *TunnelFileShareSpec `json:"file_share,omitempty"`
}

func (s TunnelSpec) LocalAddr() string {
	host := strings.TrimSpace(s.LocalHost)
	if host == "" {
		host = "localhost"
	}
	if s.LocalPort > 0 {
		return host + ":" + itoa(s.LocalPort)
	}
	return host
}

type TunnelDesiredState struct {
	Version string       `json:"version,omitempty"`
	Replace bool         `json:"replace"`
	Tunnels []TunnelSpec `json:"tunnels"`
}

type TunnelStatus struct {
	ID                 string         `json:"id"`
	Name               string         `json:"name,omitempty"`
	Protocol           TunnelProtocol `json:"protocol"`
	State              TunnelState    `json:"state"`
	LocalAddr          string         `json:"local_addr,omitempty"`
	RequestedPort      int            `json:"requested_port,omitempty"`
	AssignedRemotePort int            `json:"assigned_remote_port,omitempty"`
	PublicAddr         string         `json:"public_addr,omitempty"`
	Subdomain          string         `json:"subdomain,omitempty"`
	BaseDomain         string         `json:"base_domain,omitempty"`
	Error              string         `json:"error,omitempty"`
	BytesUp            uint64         `json:"bytes_up,omitempty"`
	BytesDown          uint64         `json:"bytes_down,omitempty"`
	ActiveSessions     int64          `json:"active_sessions,omitempty"`
	TotalSessions      uint64         `json:"total_sessions,omitempty"`
	LastHeartbeat      *time.Time     `json:"last_heartbeat,omitempty"`
	UpdatedAt          time.Time      `json:"updated_at"`
}

type TunnelStatusSnapshot struct {
	Version string         `json:"version,omitempty"`
	Tunnels []TunnelStatus `json:"tunnels"`
}

func itoa(v int) string { return strconv.Itoa(v) }

