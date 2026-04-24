package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"text/template"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

type idxPreviewQuery struct {
	WorkspaceSlug string `form:"workspace_slug"`
	ServerID      uint64 `form:"server_id"`
	ServerName    string `form:"server_name"`
}

func normalizeSlug(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	return s
}

// GET /api/v1/idx/preview-script (admin only)
// Returns rendered text/plain script using the same resolution algorithm as IDX boot dispatch.
func previewIDXScript(c *gin.Context) (any, error) {
	if !isAdmin(c) {
		return nil, singleton.Localizer.ErrorT("permission denied")
	}

	var qy idxPreviewQuery
	if err := c.ShouldBindQuery(&qy); err != nil {
		return nil, err
	}
	workspaceSlug := normalizeSlug(qy.WorkspaceSlug)
	if workspaceSlug == "" {
		return nil, errors.New("workspace_slug is required")
	}

	// Optional server context (for server/group assignment resolution).
	var serverID uint64
	if qy.ServerID != 0 {
		serverID = qy.ServerID
	} else if strings.TrimSpace(qy.ServerName) != "" {
		var s model.Server
		if err := singleton.DB.Where("name = ?", strings.TrimSpace(qy.ServerName)).First(&s).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, errors.New("server not found")
			}
			return nil, newGormError("%v", err)
		}
		serverID = s.ID
	} else {
		// Best-effort: infer server_id by workspace_slug.
		// 1) Prefer an online server whose runtime slug matches.
		for _, s := range singleton.ServerShared.Range {
			if s == nil {
				continue
			}
			if normalizeSlug(s.RuntimeWorkspaceSlug) == workspaceSlug {
				serverID = s.ID
				break
			}
		}
		// 2) Fallback to DB lookup by server name (in IDX deployments, name often equals slug).
		if serverID == 0 {
			var s model.Server
			if err := singleton.DB.Where("name = ?", workspaceSlug).First(&s).Error; err == nil {
				serverID = s.ID
			}
		}
	}

	// Resolve effective assignment by (server_id, group_ids, tag_name=workspace_slug).
	var groupIDs []uint64
	if serverID != 0 {
		_ = singleton.DB.Model(&model.ServerGroupServer{}).
			Select("server_group_id").
			Where("server_id = ?", serverID).
			Scan(&groupIDs).Error
	}

	q := singleton.DB.Model(&model.NodeConfigAssignment{}).
		Where("enabled = ?", true)

	sub := singleton.DB.Where("target_type = ? AND tag_name = ?", model.NodeConfigTargetTag, workspaceSlug)
	if serverID != 0 {
		sub = sub.Or("target_type = ? AND server_id = ?", model.NodeConfigTargetServer, serverID)
		if len(groupIDs) > 0 {
			sub = sub.Or("target_type = ? AND group_id IN ?", model.NodeConfigTargetGroup, groupIDs)
		}
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
	vars["WORKSPACE_SLUG"] = workspaceSlug
	if serverID != 0 {
		vars["SERVER_ID"] = serverID
	}

	tpl, err := template.New("idx-preview-script").
		Funcs(template.FuncMap{
			"v": func(key string) any {
				key = strings.TrimSpace(key)
				if key == "" {
					return ""
				}
				if vars == nil {
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

