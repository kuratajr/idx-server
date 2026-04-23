package controller

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

var cfgNameRe = regexp.MustCompile(`^[a-zA-Z0-9._ -]{1,128}$`)

type configMeta struct {
	ID           uint64 `json:"id"`
	Name         string `json:"name"`
	TemplateID   uint64 `json:"template_id"`
	Enabled      bool   `json:"enabled"`
	CreatedBy    uint64 `json:"created_by"`
	DetectedVars string `json:"detected_vars,omitempty"` // from template
	VarsOverride any    `json:"vars_override,omitempty"` // only when allowed
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

func parseConfigID(c *gin.Context) (uint64, error) { return parseUintParam(c, "id") }

func userHasConfigPerm(c *gin.Context, configID uint64, need model.NodeConfigPermission) (bool, error) {
	if isAdmin(c) {
		return true, nil
	}
	uid, ok := currentUserID(c)
	if !ok {
		return false, nil
	}
	var acl model.NodeConfigACL
	err := singleton.DB.Where("config_id = ? AND user_id = ?", configID, uid).First(&acl).Error
	if err != nil {
		return false, nil
	}
	return model.NodeConfigPermission(acl.PermMask).Has(need), nil
}

func loadTemplateForConfig(templateID uint64) (*model.Template, error) {
	var t model.Template
	if err := singleton.DB.First(&t, templateID).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

func requiredNodeVarsFromDetected(detectedJSON string) (map[string]struct{}, error) {
	if strings.TrimSpace(detectedJSON) == "" {
		return map[string]struct{}{}, nil
	}
	var dv []detectedVar
	if err := json.Unmarshal([]byte(detectedJSON), &dv); err != nil {
		return nil, err
	}
	req := map[string]struct{}{}
	for _, v := range dv {
		if v.Scope == "node" && v.Key != "" {
			req[v.Key] = struct{}{}
		}
	}
	return req, nil
}

func validateVarsOverride(vars map[string]any, detectedJSON string) error {
	req, err := requiredNodeVarsFromDetected(detectedJSON)
	if err != nil {
		return err
	}
	for k := range req {
		if _, ok := vars[k]; !ok {
			return errors.New("missing var: " + k)
		}
	}
	return nil
}

// GET /api/v1/configs
func listConfigs(c *gin.Context) ([]configMeta, error) {
	admin := isAdmin(c)

	var cfgs []model.NodeConfig
	q := singleton.DB.Model(&model.NodeConfig{})

	if admin {
		if err := q.Order("id DESC").Find(&cfgs).Error; err != nil {
			return nil, newGormError("%v", err)
		}
		out := make([]configMeta, 0, len(cfgs))
		for i := range cfgs {
			out = append(out, configMeta{
				ID:         cfgs[i].ID,
				Name:       cfgs[i].Name,
				TemplateID: cfgs[i].TemplateID,
				Enabled:    cfgs[i].Enabled,
				CreatedBy:  cfgs[i].CreatedBy,
				CreatedAt:  cfgs[i].CreatedAt.Unix(),
				UpdatedAt:  cfgs[i].UpdatedAt.Unix(),
			})
		}
		return out, nil
	}

	uid, ok := currentUserID(c)
	if !ok {
		return []configMeta{}, nil
	}

	q = q.Joins("JOIN node_config_acls ON node_config_acls.config_id = node_configs.id AND node_config_acls.user_id = ?", uid).
		Where("(node_config_acls.perm_mask & ?) != 0", uint32(model.NodeConfigPermReadMeta))
	if err := q.Order("node_configs.id DESC").Find(&cfgs).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	out := make([]configMeta, 0, len(cfgs))
	for i := range cfgs {
		out = append(out, configMeta{
			ID:         cfgs[i].ID,
			Name:       cfgs[i].Name,
			TemplateID: cfgs[i].TemplateID,
			Enabled:    cfgs[i].Enabled,
			CreatedBy:  cfgs[i].CreatedBy,
			CreatedAt:  cfgs[i].CreatedAt.Unix(),
			UpdatedAt:  cfgs[i].UpdatedAt.Unix(),
		})
	}
	return out, nil
}

// GET /api/v1/configs/:id
func getConfig(c *gin.Context) (*configMeta, error) {
	id, err := parseConfigID(c)
	if err != nil {
		return nil, err
	}

	ok, err := userHasConfigPerm(c, id, model.NodeConfigPermReadMeta)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, singleton.Localizer.ErrorT("permission denied")
	}

	var cfg model.NodeConfig
	if err := singleton.DB.First(&cfg, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, singleton.Localizer.ErrorT("config id %d does not exist", id)
		}
		return nil, newGormError("%v", err)
	}

	tpl, err := loadTemplateForConfig(cfg.TemplateID)
	if err != nil {
		return nil, newGormError("%v", err)
	}

	canReadVars, err := userHasConfigPerm(c, id, model.NodeConfigPermReadVars)
	if err != nil {
		return nil, err
	}

	var vars any
	if canReadVars || isAdmin(c) {
		var m map[string]any
		if cfg.VarsOverride != "" {
			_ = json.Unmarshal([]byte(cfg.VarsOverride), &m)
		}
		if m == nil {
			m = map[string]any{}
		}
		vars = m
	}

	return &configMeta{
		ID:           cfg.ID,
		Name:         cfg.Name,
		TemplateID:   cfg.TemplateID,
		Enabled:      cfg.Enabled,
		CreatedBy:    cfg.CreatedBy,
		DetectedVars: tpl.DetectedVars,
		VarsOverride: vars,
		CreatedAt:    cfg.CreatedAt.Unix(),
		UpdatedAt:    cfg.UpdatedAt.Unix(),
	}, nil
}

type configUpsertForm struct {
	Name        string         `json:"name"`
	TemplateID  uint64         `json:"template_id"`
	Enabled     *bool          `json:"enabled,omitempty"`
	VarsOverride map[string]any `json:"vars_override"`
}

func normalizeConfigForm(sf *configUpsertForm) {
	sf.Name = strings.TrimSpace(sf.Name)
}

// POST /api/v1/configs (admin only)
func createConfig(c *gin.Context) (*configMeta, error) {
	if !isAdmin(c) {
		return nil, singleton.Localizer.ErrorT("permission denied")
	}

	var sf configUpsertForm
	if err := c.ShouldBindJSON(&sf); err != nil {
		return nil, err
	}
	normalizeConfigForm(&sf)

	if sf.Name == "" || len(sf.Name) > 128 || !cfgNameRe.MatchString(sf.Name) {
		return nil, errors.New("invalid name")
	}
	if sf.TemplateID == 0 {
		return nil, errors.New("template_id is required")
	}
	if sf.VarsOverride == nil {
		sf.VarsOverride = map[string]any{}
	}

	tpl, err := loadTemplateForConfig(sf.TemplateID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, singleton.Localizer.ErrorT("template id %d does not exist", sf.TemplateID)
		}
		return nil, newGormError("%v", err)
	}
	if err := validateVarsOverride(sf.VarsOverride, tpl.DetectedVars); err != nil {
		return nil, err
	}

	b, err := json.Marshal(sf.VarsOverride)
	if err != nil {
		return nil, err
	}

	uid, _ := currentUserID(c)
	enabled := true
	if sf.Enabled != nil {
		enabled = *sf.Enabled
	}

	cfg := model.NodeConfig{
		Name:        sf.Name,
		TemplateID:  sf.TemplateID,
		VarsOverride: string(b),
		Enabled:     enabled,
		CreatedBy:   uid,
	}
	if err := singleton.DB.Create(&cfg).Error; err != nil {
		return nil, newGormError("%v", err)
	}

	return &configMeta{
		ID:           cfg.ID,
		Name:         cfg.Name,
		TemplateID:   cfg.TemplateID,
		Enabled:      cfg.Enabled,
		CreatedBy:    cfg.CreatedBy,
		DetectedVars: tpl.DetectedVars,
		VarsOverride: sf.VarsOverride,
		CreatedAt:    cfg.CreatedAt.Unix(),
		UpdatedAt:    cfg.UpdatedAt.Unix(),
	}, nil
}

// PATCH /api/v1/configs/:id (admin or ACL write)
func updateConfigItem(c *gin.Context) (*configMeta, error) {
	id, err := parseConfigID(c)
	if err != nil {
		return nil, err
	}
	ok, err := userHasConfigPerm(c, id, model.NodeConfigPermWrite)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, singleton.Localizer.ErrorT("permission denied")
	}

	var existing model.NodeConfig
	if err := singleton.DB.First(&existing, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, singleton.Localizer.ErrorT("config id %d does not exist", id)
		}
		return nil, newGormError("%v", err)
	}

	var sf configUpsertForm
	if err := c.ShouldBindJSON(&sf); err != nil {
		return nil, err
	}
	normalizeConfigForm(&sf)

	updates := map[string]any{}
	if sf.Name != "" {
		if len(sf.Name) > 128 || !cfgNameRe.MatchString(sf.Name) {
			return nil, errors.New("invalid name")
		}
		updates["name"] = sf.Name
	}
	if sf.TemplateID != 0 {
		updates["template_id"] = sf.TemplateID
	}
	if sf.Enabled != nil {
		updates["enabled"] = *sf.Enabled
	}

	templateID := existing.TemplateID
	if sf.TemplateID != 0 {
		templateID = sf.TemplateID
	}
	tpl, err := loadTemplateForConfig(templateID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, singleton.Localizer.ErrorT("template id %d does not exist", templateID)
		}
		return nil, newGormError("%v", err)
	}

	if sf.VarsOverride != nil {
		if err := validateVarsOverride(sf.VarsOverride, tpl.DetectedVars); err != nil {
			return nil, err
		}
		b, err := json.Marshal(sf.VarsOverride)
		if err != nil {
			return nil, err
		}
		updates["vars_override"] = string(b)
	}

	if len(updates) == 0 {
		return nil, nil
	}

	if err := singleton.DB.Model(&model.NodeConfig{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return nil, newGormError("%v", err)
	}

	var cfg model.NodeConfig
	if err := singleton.DB.First(&cfg, id).Error; err != nil {
		return nil, newGormError("%v", err)
	}

	canReadVars, _ := userHasConfigPerm(c, id, model.NodeConfigPermReadVars)
	var vars any
	if canReadVars || isAdmin(c) {
		var m map[string]any
		_ = json.Unmarshal([]byte(cfg.VarsOverride), &m)
		if m == nil {
			m = map[string]any{}
		}
		vars = m
	}

	return &configMeta{
		ID:           cfg.ID,
		Name:         cfg.Name,
		TemplateID:   cfg.TemplateID,
		Enabled:      cfg.Enabled,
		CreatedBy:    cfg.CreatedBy,
		DetectedVars: tpl.DetectedVars,
		VarsOverride: vars,
		CreatedAt:    cfg.CreatedAt.Unix(),
		UpdatedAt:    cfg.UpdatedAt.Unix(),
	}, nil
}

// DELETE /api/v1/configs/:id (admin or ACL delete)
func deleteConfigItem(c *gin.Context) (any, error) {
	id, err := parseConfigID(c)
	if err != nil {
		return nil, err
	}
	ok, err := userHasConfigPerm(c, id, model.NodeConfigPermDelete)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, singleton.Localizer.ErrorT("permission denied")
	}

	if err := singleton.DB.Where("config_id = ?", id).Delete(&model.NodeConfigACL{}).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	if err := singleton.DB.Delete(&model.NodeConfig{}, id).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	return nil, nil
}

