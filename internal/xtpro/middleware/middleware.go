package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/internal/xtpro/auth"
	"github.com/nezhahq/nezha/internal/xtpro/models"
)

func AuthMiddleware(authService *auth.AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		tokenString := ""
		if authHeader != "" {
			tokenString = strings.TrimPrefix(authHeader, "Bearer ")
		}
		if tokenString == "" {
			tokenString = c.Query("token")
		}
		if tokenString == "" {
			c.JSON(http.StatusUnauthorized, models.APIResponse{Success: false, Error: "Authorization required"})
			c.Abort()
			return
		}

		claims, err := authService.ValidateToken(tokenString)
		if err != nil {
			c.JSON(http.StatusUnauthorized, models.APIResponse{Success: false, Error: "Invalid token"})
			c.Abort()
			return
		}

		c.Set("user_id", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("role", claims.Role)
		c.Next()
	}
}

func AdminMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, exists := c.Get("role")
		if !exists || role != models.UserRoleAdmin {
			c.JSON(http.StatusForbidden, models.APIResponse{Success: false, Error: "Admin access required"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		if origin != "" {
			c.Header("Access-Control-Allow-Origin", origin)
		} else {
			c.Header("Access-Control-Allow-Origin", "*")
		}

		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Authorization, X-API-Key, X-Requested-With")
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

func LoggingMiddleware() gin.HandlerFunc {
	return gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		return fmt.Sprintf("[%s] %s %s %d %s %s %s\n",
			param.TimeStamp.Format("2006-01-02 15:04:05"),
			param.Method,
			param.Path,
			param.StatusCode,
			param.Latency,
			param.ClientIP,
			param.ErrorMessage,
		)
	})
}

func RecoveryMiddleware() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, _ interface{}) {
		c.JSON(http.StatusInternalServerError, models.APIResponse{Success: false, Error: "Internal server error"})
	})
}

