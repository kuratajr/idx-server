package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"text/template"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/utils"
	"github.com/nezhahq/nezha/service/singleton"
)

type idxScriptTokenPayload struct {
	RunID         uint64 `json:"run_id"`
	ServerID       uint64 `json:"server_id"`
	WorkspaceSlug  string `json:"workspace_slug"`
	BootTime       uint64 `json:"boot_time"`
	ExpiresAtUnix  int64  `json:"exp"`
}

func parseIDXToken(tok string) (*idxScriptTokenPayload, error) {
	var p idxScriptTokenPayload
	if err := utils.VerifySignedToken(singleton.Conf.AgentSecretKey, tok, &p); err != nil {
		return nil, err
	}
	if p.ExpiresAtUnix <= time.Now().Unix() {
		return nil, errors.New("token expired")
	}
	p.WorkspaceSlug = normalizeSlug(p.WorkspaceSlug)
	if p.ServerID == 0 || p.WorkspaceSlug == "" || p.BootTime == 0 {
		return nil, errors.New("invalid token fields")
	}
	return &p, nil
}

// GET /api/v1/idx/script (public via signed token)
// Returns rendered sh script for IDX node: curl -fsSL "...?token=..." | sh
func getIDXRenderedScript(c *gin.Context) (any, error) {
	tok := strings.TrimSpace(c.Query("token"))
	if tok == "" {
		return nil, errors.New("token is required")
	}
	p, err := parseIDXToken(tok)
	if err != nil {
		if singleton.Conf.Debug {
			singleton.DB.Logger.Info(c, "NEZHA>> idx-script token invalid err=%v", err)
		}
		_ = singleton.DB.Create(&model.IdxMetaEvent{
			ServerID:      0,
			WorkspaceSlug: "",
			BootTime:      0,
			Stage:         model.IdxMetaStageScriptFetched,
			Message:       "token invalid: " + err.Error(),
		}).Error
		return nil, err
	}

	if singleton.Conf.Debug {
		singleton.DB.Logger.Info(c, "NEZHA>> idx-script fetched server_id=%d slug=%q boot_time=%d", p.ServerID, p.WorkspaceSlug, p.BootTime)
	}
	_ = singleton.DB.Create(&model.IdxMetaEvent{
		ServerID:      p.ServerID,
		WorkspaceSlug: p.WorkspaceSlug,
		BootTime:      p.BootTime,
		Stage:         model.IdxMetaStageScriptFetched,
		Message:       "ok",
	}).Error

	// Update run timeline if we have run_id.
	if p.RunID != 0 {
		_ = singleton.DB.Model(&model.IdxMetaRun{}).Where("id = ?", p.RunID).Updates(map[string]any{
			"status":     model.IdxMetaRunFetched,
			"fetched_at": time.Now(),
		}).Error
	}

	// Resolve effective assignment by (server_id, group_ids, tag_name=workspace_slug).
	var groupIDs []uint64
	_ = singleton.DB.Model(&model.ServerGroupServer{}).
		Select("server_group_id").
		Where("server_id = ?", p.ServerID).
		Scan(&groupIDs).Error

	q := singleton.DB.Model(&model.NodeConfigAssignment{}).
		Where("enabled = ?", true)

	sub := singleton.DB.Where("target_type = ? AND tag_name = ?", model.NodeConfigTargetTag, p.WorkspaceSlug).
		Or("target_type = ? AND server_id = ?", model.NodeConfigTargetServer, p.ServerID)
	if len(groupIDs) > 0 {
		sub = sub.Or("target_type = ? AND group_id IN ?", model.NodeConfigTargetGroup, groupIDs)
	}

	var asg model.NodeConfigAssignment
	if err := q.Where(sub).Order("priority DESC, updated_at DESC, id DESC").First(&asg).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("no matching assignment")
		}
		return nil, newGormError("%v", err)
	}

	var cfg model.NodeConfig
	if err := singleton.DB.First(&cfg, asg.ConfigID).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	if !cfg.Enabled {
		return nil, errors.New("config is disabled")
	}

	var tplRow model.Template
	if err := singleton.DB.First(&tplRow, cfg.TemplateID).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	if strings.TrimSpace(strings.ToLower(tplRow.Type)) != "script" {
		return nil, errors.New("template type is not script")
	}

	var vars map[string]any
	if cfg.VarsOverride != "" {
		_ = json.Unmarshal([]byte(cfg.VarsOverride), &vars)
	}
	if vars == nil {
		vars = map[string]any{}
	}
	vars["WORKSPACE_SLUG"] = p.WorkspaceSlug
	vars["SERVER_ID"] = p.ServerID
	vars["BOOT_TIME"] = p.BootTime

	tpl, err := template.New("idx-script-token").
		Funcs(template.FuncMap{
			"v": func(key string) any {
				key = strings.TrimSpace(key)
				if key == "" {
					return ""
				}
				if val, ok := vars[key]; ok {
					return val
				}
				if val, ok := vars[strings.ToUpper(key)]; ok {
					return val
				}
				return ""
			},
		}).
		Option("missingkey=error").
		Parse(tplRow.ContentRaw)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, vars); err != nil {
		return nil, err
	}
	rendered := buf.String()
	if strings.TrimSpace(rendered) == "" {
		return nil, errors.New("rendered script is empty")
	}
	if !strings.HasPrefix(rendered, "#!") {
		rendered = "#!/bin/sh\nset -eu\n\n" + rendered
	}

	c.Data(200, "text/plain; charset=utf-8", []byte(rendered))
	return nil, errNoop
}

// Note: URL/token building for dispatch lives in pkg/utils to avoid import cycles.

