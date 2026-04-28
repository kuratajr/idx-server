package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/nezhahq/nezha/internal/xtpro/config"
	"github.com/nezhahq/nezha/internal/xtpro/database"
)

type Options struct {
	ListenPort uint16
	PublicHost string

	DBPath string

	JWTSecret string
	JWTHours  int
}

type Service struct {
	srv *server
	db  *database.Database
	h   http.Handler
}

type TunnelView struct {
	ClientID          string    `json:"client_id"`
	Key               string    `json:"key"`
	Target            string    `json:"target"`
	Protocol          string    `json:"protocol"`
	PublicHost        string    `json:"public_host"`
	PublicPort        int       `json:"public_port"`
	RemoteIP          string    `json:"remote_ip"`
	LastSeen          time.Time `json:"last_seen"`
	BytesUp           uint64    `json:"bytes_up"`
	BytesDown         uint64    `json:"bytes_down"`
	ActiveConnections int64     `json:"active_connections"`
}

type DashboardSnapshot struct {
	ActiveTunnels    int          `json:"active_tunnels"`
	ActiveUsers      int          `json:"active_users"`
	TotalConnections uint64       `json:"total_connections"`
	TotalBytesUp     uint64       `json:"total_bytes_up"`
	TotalBytesDown   uint64       `json:"total_bytes_down"`
	UptimeSeconds    float64      `json:"uptime_seconds"`
	Uptime           string       `json:"uptime"`
	Tunnels          []TunnelView `json:"tunnels"`
	DatabaseUsers    int          `json:"database_users,omitempty"`
}

type UserView struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	APIKey    string    `json:"api_key,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func Start(ctx context.Context, opt Options) (*Service, error) {
	if opt.ListenPort == 0 {
		opt.ListenPort = 8881
	}
	if opt.JWTHours <= 0 {
		opt.JWTHours = 24
	}
	if opt.JWTSecret == "" {
		// A missing secret means API auth is not secure; keep it explicit.
		// Users can set xtpro.jwt_secret_key in Nezha config to override.
		opt.JWTSecret = uuid.NewString()
	}

	cfg := &config.Config{
		Server: config.ServerConfig{
			Host:            "0.0.0.0",
			Port:            int(opt.ListenPort),
			PublicPortStart: 10000,
			PublicPortEnd:   20000,
			PublicHost:      opt.PublicHost,
		},
		Database: config.DatabaseConfig{Path: opt.DBPath},
		Auth: config.AuthConfig{
			JWTSecret:   opt.JWTSecret,
			TokenExpiry: time.Duration(opt.JWTHours) * time.Hour,
		},
	}

	// Initialize database (optional)
	var db *database.Database
	var err error
	if cfg.GetDatabaseDSN() != "" {
		db, err = database.NewDatabase(cfg.GetDatabaseDSN())
		if err != nil {
			log.Printf("[xtpro/database] Failed to init: %v (running without database)", err)
			db = nil
		}
	}

	s := newServer(cfg)
	h := s.newHTTPHandler(cfg, db)

	go func() {
		if err := s.run(ctx); err != nil {
			log.Printf("[xtpro] server stopped: %v", err)
		}
	}()

	return &Service{srv: s, db: db, h: h}, nil
}

func (s *Service) Handler() http.Handler {
	if s == nil {
		return nil
	}
	return s.h
}

func (s *Service) Stop(ctx context.Context) error {
	var errs []error
	if s == nil || s.srv == nil {
		return nil
	}
	if err := s.srv.stop(ctx); err != nil {
		errs = append(errs, err)
	}
	if s.db != nil {
		if err := s.db.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("xtpro stop: %v", errs[0])
}

func (s *Service) DashboardSnapshot() DashboardSnapshot {
	if s == nil || s.srv == nil {
		return DashboardSnapshot{}
	}
	snapshot := s.srv.dashboardSnapshot()
	if s.db != nil {
		if users, err := s.db.GetAllUsers(); err == nil {
			snapshot.DatabaseUsers = len(users)
		}
	}
	return snapshot
}

func (s *Service) ListUsers() ([]UserView, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("xtpro database is not available")
	}
	users, err := s.db.GetAllUsers()
	if err != nil {
		return nil, err
	}
	res := make([]UserView, 0, len(users))
	for _, user := range users {
		res = append(res, UserView{
			ID:        user.ID.String(),
			Username:  user.Username,
			Email:     user.Email,
			Role:      user.Role,
			APIKey:    user.APIKey,
			CreatedAt: user.CreatedAt,
			UpdatedAt: user.UpdatedAt,
		})
	}
	return res, nil
}

func (s *Service) DeleteTunnel(tunnelID string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("xtpro database is not available")
	}
	return s.db.DeleteTunnelByAdmin(tunnelID)
}
