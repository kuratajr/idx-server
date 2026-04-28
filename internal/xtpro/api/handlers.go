package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/nezhahq/nezha/internal/xtpro/auth"
	"github.com/nezhahq/nezha/internal/xtpro/database"
	"github.com/nezhahq/nezha/internal/xtpro/models"
)

var startTime = time.Now()

type Handler struct {
	db          *database.Database
	authService *auth.AuthService
}

func NewHandler(db *database.Database, authService *auth.AuthService) *Handler {
	return &Handler{db: db, authService: authService}
}

func (h *Handler) Login(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse{Success: false, Error: "Invalid request"})
		return
	}

	user, err := h.db.GetUserByUsername(req.Username)
	if err != nil {
		c.JSON(http.StatusUnauthorized, models.APIResponse{Success: false, Error: "Invalid credentials"})
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, models.APIResponse{Success: false, Error: "Invalid credentials"})
		return
	}

	token, err := h.authService.GenerateToken(user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse{Success: false, Error: "Failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, models.APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"token":    token,
			"username": user.Username,
			"role":     user.Role,
		},
	})
}

func (h *Handler) Register(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required,min=6"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse{Success: false, Error: "Invalid request: " + err.Error()})
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse{Success: false, Error: "Failed to process password"})
		return
	}

	user := &models.User{
		ID:       uuid.New(),
		Username: req.Username,
		Email:    req.Email,
		Password: string(hashedPassword),
		Role:     models.UserRoleUser,
		APIKey:   uuid.New().String(),
	}

	if err := h.db.CreateUser(user); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse{Success: false, Error: "Username or email already exists"})
		return
	}

	c.JSON(http.StatusCreated, models.APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"user_id":  user.ID,
			"username": user.Username,
			"api_key":  user.APIKey,
		},
	})
}

func (h *Handler) GetMetrics(c *gin.Context) {
	metrics, err := h.db.GetMetrics()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse{Success: false, Error: "Failed to fetch metrics"})
		return
	}
	uptime := time.Since(startTime)
	c.JSON(http.StatusOK, models.APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"active_tunnels":    metrics.ActiveTunnels,
			"total_connections": metrics.TotalConnections,
			"total_bytes_up":    metrics.TotalBytesUp,
			"total_bytes_down":  metrics.TotalBytesDown,
			"active_users":      metrics.ActiveUsers,
			"uptime_seconds":    uptime.Seconds(),
			"uptime_formatted":  uptime.String(),
		},
	})
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, models.APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"status": "healthy",
			"uptime": time.Since(startTime).String(),
		},
	})
}

