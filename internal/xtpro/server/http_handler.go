package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/nezhahq/nezha/internal/xtpro/api"
	"github.com/nezhahq/nezha/internal/xtpro/auth"
	"github.com/nezhahq/nezha/internal/xtpro/config"
	"github.com/nezhahq/nezha/internal/xtpro/database"
	"github.com/nezhahq/nezha/internal/xtpro/middleware"
	"github.com/nezhahq/nezha/internal/xtpro/uiembed"
)

func (s *server) newHTTPHandler(cfg *config.Config, db *database.Database) http.Handler {
	var handlers *api.Handler
	var authService *auth.AuthService

	if db != nil {
		authService = auth.NewAuthService(cfg.Auth.JWTSecret, cfg.Auth.TokenExpiry)
		handlers = api.NewHandler(db, authService)
	}

	gin.SetMode(gin.ReleaseMode)
	gin.DisableConsoleColor()
	router := gin.New()
	router.Use(middleware.LoggingMiddleware())
	router.Use(middleware.RecoveryMiddleware())
	router.Use(middleware.CORSMiddleware())

	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"server":  "xtpro embedded",
			"version": "7.5.0",
		})
	})

	// Serve UI (prefer embedded assets; fallback to disk in dev)
	if _, err := os.Stat("frontend"); err == nil {
		dashboardDir := "./frontend"
		router.Static("/dashboard", dashboardDir)

		landingDir := filepath.Join(dashboardDir, "landing")
		router.Static("/assets", dashboardDir)

		router.StaticFile("/", filepath.Join(landingDir, "index.html"))
		router.GET("/style.css", func(c *gin.Context) {
			c.Header("Content-Type", "text/css")
			c.File(filepath.Join(landingDir, "style.css"))
		})
		router.GET("/script.js", func(c *gin.Context) {
			c.Header("Content-Type", "application/javascript")
			c.File(filepath.Join(landingDir, "script.js"))
		})
	} else {
		uiRoot := uiembed.Root()
		router.StaticFS("/dashboard", uiRoot)
		router.StaticFS("/assets", uiRoot)
	}

	// WebSocket endpoint for dashboard updates
	router.GET("/api/v1/dashboard/ws", func(c *gin.Context) {
		upgrader := websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		}

		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		if err := s.sendDashboardUpdate(conn); err != nil {
			return
		}

		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			if err := s.sendDashboardUpdate(conn); err != nil {
				return
			}
		}
	})

	if handlers != nil {
		apiRouter := router.Group("/api")
		{
			apiRouter.POST("/auth/login", handlers.Login)
			apiRouter.POST("/auth/register", handlers.Register)
			apiRouter.GET("/metrics", handlers.GetMetrics)
			apiRouter.GET("/health", handlers.Health)
		}
	}
	return router
}

// Small helpers to keep the old dashboard links working.
func safeAssetName(asset string) bool {
	if asset == "" || strings.Contains(asset, "/") || strings.Contains(asset, "\\") {
		return false
	}
	return true
}

