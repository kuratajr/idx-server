package controller

import (
	"errors"
	"strings"
	"time"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/utils"
	"github.com/nezhahq/nezha/service/singleton"
)

type idxDebugTokenQuery struct {
	WorkspaceSlug string `form:"workspace_slug"`
	ServerID      uint64 `form:"server_id"`
	ServerName    string `form:"server_name"`
	BootTime      uint64 `form:"boot_time"`
}

// GET /api/v1/idx/debug-token (admin only)
// Returns a short-lived signed URL that nodes will curl.
func debugIDXToken(c *gin.Context) (any, error) {
	if !isAdmin(c) {
		return nil, singleton.Localizer.ErrorT("permission denied")
	}
	var qy idxDebugTokenQuery
	if err := c.ShouldBindQuery(&qy); err != nil {
		return nil, err
	}
	workspaceSlug := normalizeSlug(qy.WorkspaceSlug)
	if workspaceSlug == "" {
		return nil, errors.New("workspace_slug is required")
	}

	serverID := qy.ServerID
	bootTime := qy.BootTime

	// Infer server_id / boot_time from online nodes when omitted.
	if serverID == 0 && strings.TrimSpace(qy.ServerName) == "" {
		for _, s := range singleton.ServerShared.Range {
			if s == nil {
				continue
			}
			if normalizeSlug(s.RuntimeWorkspaceSlug) == workspaceSlug {
				serverID = s.ID
				if bootTime == 0 && s.Host != nil && s.Host.BootTime != 0 {
					bootTime = s.Host.BootTime
				}
				break
			}
		}
	}

	// Fallback by server name if provided.
	if serverID == 0 && strings.TrimSpace(qy.ServerName) != "" {
		name := strings.TrimSpace(qy.ServerName)
		for _, s := range singleton.ServerShared.Range {
			if s != nil && strings.EqualFold(s.Name, name) {
				serverID = s.ID
				if bootTime == 0 && s.Host != nil && s.Host.BootTime != 0 {
					bootTime = s.Host.BootTime
				}
				break
			}
		}
	}

	// Fallback to DB by name == workspace_slug (common in IDX deployments).
	if serverID == 0 {
		var s model.Server
		if err := singleton.DB.Where("name = ?", workspaceSlug).First(&s).Error; err == nil {
			serverID = s.ID
		}
	}

	// If we have server_id but missing boot_time, try to fetch it from the running server.
	if serverID != 0 && bootTime == 0 {
		if s, ok := singleton.ServerShared.Get(serverID); ok && s != nil && s.Host != nil && s.Host.BootTime != 0 {
			bootTime = s.Host.BootTime
		}
	}

	if serverID == 0 {
		return nil, errors.New("server_id is required (or the node must be online to infer it)")
	}
	if bootTime == 0 {
		return nil, errors.New("boot_time is required (or the node must be online to infer it)")
	}

	payload := struct {
		ServerID      uint64 `json:"server_id"`
		WorkspaceSlug string `json:"workspace_slug"`
		BootTime      uint64 `json:"boot_time"`
		ExpiresAtUnix int64  `json:"exp"`
	}{
		ServerID:      serverID,
		WorkspaceSlug: workspaceSlug,
		BootTime:      bootTime,
		ExpiresAtUnix: time.Now().Add(2 * time.Minute).Unix(),
	}
	tok, err := utils.MakeSignedToken(singleton.Conf.AgentSecretKey, payload)
	if err != nil {
		return nil, err
	}
	signedURL := utils.BuildIDXScriptURL(singleton.Conf.InstallHost, singleton.Conf.AgentTLS, tok)
	curlCmd := "curl -fsSL " + strconv.Quote(signedURL) + " | sh"

	return gin.H{
		"workspace_slug": workspaceSlug,
		"server_id":      serverID,
		"boot_time":      bootTime,
		"expires_at":     payload.ExpiresAtUnix,
		"token":          tok,
		// Compatibility: UI may expect url/curl keys.
		"url":        signedURL,
		"curl":       curlCmd,
		"signed_url": signedURL,
	}, nil
}

