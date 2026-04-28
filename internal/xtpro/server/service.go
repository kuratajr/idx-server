package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/nezhahq/nezha/internal/xtpro/config"
	"github.com/nezhahq/nezha/internal/xtpro/database"
	"github.com/nezhahq/nezha/internal/xtpro/models"
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
		} else {
			// Seed default admin if no users exist
			users, _ := db.GetAllUsers()
			if len(users) == 0 {
				hashedPassword, _ := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
				admin := &models.User{
					ID:       uuid.New(),
					Username: "admin",
					Email:    "admin@xtpro.local",
					Password: string(hashedPassword),
					Role:     models.UserRoleAdmin,
					APIKey:   uuid.New().String(),
				}
				if err := db.CreateUser(admin); err == nil {
					log.Printf("[xtpro/database] Created default admin: admin / admin123")
				}
			}
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

