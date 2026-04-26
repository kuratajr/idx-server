package controller

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

type idxMetaRunsQuery struct {
	WorkspaceSlug string `form:"workspace_slug"`
	ServerID      uint64 `form:"server_id"`
	Status        string `form:"status"`
	From          int64  `form:"from"`
	To            int64  `form:"to"`
	Limit         int    `form:"limit"`
	Offset        int    `form:"offset"`
}

// GET /api/v1/idx/meta-runs (admin only)
func listIdxMetaRuns(c *gin.Context) ([]model.IdxMetaRun, error) {
	if !isAdmin(c) {
		return nil, singleton.Localizer.ErrorT("permission denied")
	}
	var qy idxMetaRunsQuery
	if err := c.ShouldBindQuery(&qy); err != nil {
		return nil, err
	}
	limit := qy.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if qy.Offset < 0 {
		qy.Offset = 0
	}
	q := singleton.DB.Model(&model.IdxMetaRun{})
	if ws := normalizeSlug(qy.WorkspaceSlug); ws != "" {
		q = q.Where("workspace_slug = ?", ws)
	}
	if qy.ServerID != 0 {
		q = q.Where("server_id = ?", qy.ServerID)
	}
	if st := strings.TrimSpace(qy.Status); st != "" {
		q = q.Where("status = ?", st)
	}
	if qy.From > 0 {
		q = q.Where("dispatched_at >= ?", time.Unix(qy.From, 0))
	}
	if qy.To > 0 {
		q = q.Where("dispatched_at <= ?", time.Unix(qy.To, 0))
	}
	var out []model.IdxMetaRun
	if err := q.Order("id DESC").Limit(limit).Offset(qy.Offset).Find(&out).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	return out, nil
}

// GET /api/v1/idx/meta-runs/latest (admin only)
func latestIdxMetaRun(c *gin.Context) (*model.IdxMetaRun, error) {
	if !isAdmin(c) {
		return nil, singleton.Localizer.ErrorT("permission denied")
	}
	ws := normalizeSlug(c.Query("workspace_slug"))
	var sid uint64
	if s := strings.TrimSpace(c.Query("server_id")); s != "" {
		if v, err := strconv.ParseUint(s, 10, 64); err == nil {
			sid = v
		}
	}
	q := singleton.DB.Model(&model.IdxMetaRun{})
	if ws != "" {
		q = q.Where("workspace_slug = ?", ws)
	}
	if sid != 0 {
		q = q.Where("server_id = ?", sid)
	}
	var run model.IdxMetaRun
	if err := q.Order("id DESC").First(&run).Error; err != nil {
		return nil, errors.New("no runs")
	}
	return &run, nil
}

// GET /api/v1/idx/meta-runs/:id (admin only)
func getIdxMetaRun(c *gin.Context) (*model.IdxMetaRun, error) {
	if !isAdmin(c) {
		return nil, singleton.Localizer.ErrorT("permission denied")
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		return nil, err
	}
	var run model.IdxMetaRun
	if err := singleton.DB.First(&run, id).Error; err != nil {
		return nil, errors.New("not found")
	}
	return &run, nil
}

type idxMetaRunsClearForm struct {
	ServerID      uint64 `json:"server_id,omitempty"`
	WorkspaceSlug string `json:"workspace_slug,omitempty"`
	BeforeUnix    int64  `json:"before,omitempty"`
	Status        string `json:"status,omitempty"`
	ClearAll      bool   `json:"clear_all,omitempty"`
}

// POST /api/v1/idx/meta-runs/clear (admin only)
func clearIdxMetaRuns(c *gin.Context) (any, error) {
	if !isAdmin(c) {
		return nil, singleton.Localizer.ErrorT("permission denied")
	}
	var sf idxMetaRunsClearForm
	if err := c.ShouldBindJSON(&sf); err != nil {
		return nil, err
	}
	if !sf.ClearAll && sf.ServerID == 0 && strings.TrimSpace(sf.WorkspaceSlug) == "" && sf.BeforeUnix == 0 && strings.TrimSpace(sf.Status) == "" {
		return nil, errors.New("refusing to clear without filters; set clear_all=true to wipe everything")
	}
	q := singleton.DB.Model(&model.IdxMetaRun{})
	if sf.ServerID != 0 {
		q = q.Where("server_id = ?", sf.ServerID)
	}
	if ws := normalizeSlug(sf.WorkspaceSlug); ws != "" {
		q = q.Where("workspace_slug = ?", ws)
	}
	if sf.BeforeUnix > 0 {
		q = q.Where("dispatched_at < ?", time.Unix(sf.BeforeUnix, 0))
	}
	if st := strings.TrimSpace(sf.Status); st != "" {
		q = q.Where("status = ?", st)
	}
	if err := q.Delete(&model.IdxMetaRun{}).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	return nil, nil
}

