package controller

import (
	"errors"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

type idxMetaEventsQuery struct {
	WorkspaceSlug string `form:"workspace_slug"`
	ServerID      uint64 `form:"server_id"`
	Limit         int    `form:"limit"`
}

// GET /api/v1/idx/meta-events (admin only)
func listIdxMetaEvents(c *gin.Context) ([]model.IdxMetaEvent, error) {
	if !isAdmin(c) {
		return nil, singleton.Localizer.ErrorT("permission denied")
	}
	var qy idxMetaEventsQuery
	if err := c.ShouldBindQuery(&qy); err != nil {
		return nil, err
	}
	limit := qy.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := singleton.DB.Model(&model.IdxMetaEvent{})
	if qy.ServerID != 0 {
		q = q.Where("server_id = ?", qy.ServerID)
	}
	if ws := strings.TrimSpace(qy.WorkspaceSlug); ws != "" {
		q = q.Where("workspace_slug = ?", strings.ToLower(ws))
	}
	var out []model.IdxMetaEvent
	if err := q.Order("id DESC").Limit(limit).Find(&out).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	return out, nil
}

// GET /api/v1/idx/meta-events/latest (admin only)
func latestIdxMetaEvent(c *gin.Context) (*model.IdxMetaEvent, error) {
	if !isAdmin(c) {
		return nil, singleton.Localizer.ErrorT("permission denied")
	}
	ws := strings.ToLower(strings.TrimSpace(c.Query("workspace_slug")))
	sidStr := strings.TrimSpace(c.Query("server_id"))
	q := singleton.DB.Model(&model.IdxMetaEvent{})
	if ws != "" {
		q = q.Where("workspace_slug = ?", ws)
	}
	if sidStr != "" {
		sid, err := strconv.ParseUint(sidStr, 10, 64)
		if err == nil && sid != 0 {
			q = q.Where("server_id = ?", sid)
		}
	}
	var ev model.IdxMetaEvent
	if err := q.Order("id DESC").First(&ev).Error; err != nil {
		return nil, errors.New("no events")
	}
	return &ev, nil
}

