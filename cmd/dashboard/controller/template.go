package controller

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

var tmplNameRe = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,128}$`)

type templateMeta struct {
	ID           uint64 `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	VarsSchema   string `json:"vars_schema,omitempty"`
	DetectedVars string `json:"detected_vars,omitempty"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

func toTemplateMeta(t *model.Template, includeSchema bool) templateMeta {
	m := templateMeta{
		ID:           t.ID,
		Name:         t.Name,
		Type:         t.Type,
		DetectedVars: t.DetectedVars,
		CreatedAt:    t.CreatedAt.Unix(),
		UpdatedAt:    t.UpdatedAt.Unix(),
	}
	if includeSchema {
		m.VarsSchema = t.VarsSchema
	}
	return m
}

func isAdmin(c *gin.Context) bool {
	u, ok := c.Get(model.CtxKeyAuthorizedUser)
	if !ok {
		return false
	}
	user, ok := u.(*model.User)
	if !ok || user == nil {
		return false
	}
	return user.Role.IsAdmin()
}

func currentUserID(c *gin.Context) (uint64, bool) {
	u, ok := c.Get(model.CtxKeyAuthorizedUser)
	if !ok {
		return 0, false
	}
	user, ok := u.(*model.User)
	if !ok || user == nil {
		return 0, false
	}
	return user.ID, true
}

func userHasTemplatePerm(c *gin.Context, templateID uint64, need model.TemplatePermission) (bool, error) {
	if isAdmin(c) {
		return true, nil
	}
	uid, ok := currentUserID(c)
	if !ok {
		return false, nil
	}
	var acl model.TemplateACL
	err := singleton.DB.Where("template_id = ? AND user_id = ?", templateID, uid).First(&acl).Error
	if err != nil {
		// gorm returns error for not found; treat as no permission.
		return false, nil
	}
	return model.TemplatePermission(acl.PermMask).Has(need), nil
}

func parseTemplateID(c *gin.Context) (uint64, error) {
	idStr := c.Param("id")
	if idStr == "" {
		return 0, errors.New("missing id")
	}
	var id uint64
	for _, ch := range idStr {
		if ch < '0' || ch > '9' {
			return 0, errors.New("invalid id")
		}
		id = id*10 + uint64(ch-'0')
	}
	if id == 0 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}

// GET /api/v1/templates
func listTemplates(c *gin.Context) ([]templateMeta, error) {
	admin := isAdmin(c)
	typeFilter := strings.TrimSpace(c.Query("type"))

	var templates []model.Template
	q := singleton.DB.Model(&model.Template{})
	if typeFilter != "" {
		q = q.Where("type = ?", typeFilter)
	}

	if admin {
		if err := q.Order("id DESC").Find(&templates).Error; err != nil {
			return nil, newGormError("%v", err)
		}
		out := make([]templateMeta, 0, len(templates))
		for i := range templates {
			out = append(out, toTemplateMeta(&templates[i], true))
		}
		return out, nil
	}

	uid, ok := currentUserID(c)
	if !ok {
		// optional auth: allow unauthenticated to see nothing
		return []templateMeta{}, nil
	}

	// Only list templates the user can read metadata for.
	q = q.Joins("JOIN template_acls ON template_acls.template_id = templates.id AND template_acls.user_id = ?", uid).
		Where("(template_acls.perm_mask & ?) != 0", uint32(model.TemplatePermReadMeta))
	if err := q.Order("templates.id DESC").Find(&templates).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	out := make([]templateMeta, 0, len(templates))
	for i := range templates {
		out = append(out, toTemplateMeta(&templates[i], false))
	}
	return out, nil
}

// GET /api/v1/templates/:id
func getTemplate(c *gin.Context) (*templateMeta, error) {
	id, err := parseTemplateID(c)
	if err != nil {
		return nil, err
	}

	ok, err := userHasTemplatePerm(c, id, model.TemplatePermReadMeta)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, singleton.Localizer.ErrorT("permission denied")
	}

	var t model.Template
	if err := singleton.DB.First(&t, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, singleton.Localizer.ErrorT("template id %d does not exist", id)
		}
		return nil, newGormError("%v", err)
	}
	meta := toTemplateMeta(&t, isAdmin(c))
	return &meta, nil
}

// GET /api/v1/templates/:id/raw (text/plain)
func getTemplateRaw(c *gin.Context) (any, error) {
	id, err := parseTemplateID(c)
	if err != nil {
		return nil, err
	}
	ok, err := userHasTemplatePerm(c, id, model.TemplatePermReadRaw)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, singleton.Localizer.ErrorT("permission denied")
	}

	var t model.Template
	if err := singleton.DB.First(&t, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, singleton.Localizer.ErrorT("template id %d does not exist", id)
		}
		return nil, newGormError("%v", err)
	}

	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	c.String(http.StatusOK, "%s", t.ContentRaw)
	return nil, errNoop
}

type templateUpsertForm struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	ContentRaw string `json:"content_raw"`
	VarsSchema string `json:"vars_schema"`
}

func validateTemplateForm(sf *templateUpsertForm, requireName bool) error {
	sf.Name = strings.TrimSpace(sf.Name)
	sf.Type = strings.TrimSpace(sf.Type)
	if requireName && sf.Name == "" {
		return errors.New("name is required")
	}
	if sf.Name != "" && !tmplNameRe.MatchString(sf.Name) {
		return errors.New("invalid name")
	}
	if sf.Type != "" && len(sf.Type) > 64 {
		return errors.New("type is too long")
	}
	if len(sf.ContentRaw) > 256*1024 {
		return errors.New("content_raw too large")
	}
	return nil
}

// POST /api/v1/templates (admin or ACL write)
func createTemplate(c *gin.Context) (*templateMeta, error) {
	var sf templateUpsertForm
	if err := c.ShouldBindJSON(&sf); err != nil {
		return nil, err
	}
	if err := validateTemplateForm(&sf, true); err != nil {
		return nil, err
	}
	if sf.Type == "" {
		return nil, errors.New("type is required")
	}
	if !isAdmin(c) {
		return nil, singleton.Localizer.ErrorT("permission denied")
	}
	sf.ContentRaw = strings.ReplaceAll(sf.ContentRaw, "\r\n", "\n")
	detected, err := detectTemplateVars(sf.ContentRaw)
	if err != nil {
		return nil, err
	}

	t := model.Template{
		Name:         sf.Name,
		Type:         sf.Type,
		ContentRaw:   sf.ContentRaw,
		VarsSchema:   sf.VarsSchema,
		DetectedVars: detected,
	}
	if err := singleton.DB.Create(&t).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	meta := toTemplateMeta(&t, true)
	return &meta, nil
}

// PATCH /api/v1/templates/:id (admin or ACL write)
func updateTemplate(c *gin.Context) (*templateMeta, error) {
	id, err := parseTemplateID(c)
	if err != nil {
		return nil, err
	}

	ok, err := userHasTemplatePerm(c, id, model.TemplatePermWrite)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, singleton.Localizer.ErrorT("permission denied")
	}

	var sf templateUpsertForm
	if err := c.ShouldBindJSON(&sf); err != nil {
		return nil, err
	}
	if err := validateTemplateForm(&sf, false); err != nil {
		return nil, err
	}

	updates := map[string]any{}
	if sf.Name != "" {
		updates["name"] = sf.Name
	}
	if sf.Type != "" {
		updates["type"] = sf.Type
	}
	if sf.ContentRaw != "" {
		content := strings.ReplaceAll(sf.ContentRaw, "\r\n", "\n")
		detected, err := detectTemplateVars(content)
		if err != nil {
			return nil, err
		}
		updates["content_raw"] = content
		updates["detected_vars"] = detected
	}
	if sf.VarsSchema != "" {
		updates["vars_schema"] = sf.VarsSchema
	}
	if len(updates) == 0 {
		return nil, nil
	}

	if err := singleton.DB.Model(&model.Template{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return nil, newGormError("%v", err)
	}

	var t model.Template
	if err := singleton.DB.First(&t, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, singleton.Localizer.ErrorT("template id %d does not exist", id)
		}
		return nil, newGormError("%v", err)
	}
	meta := toTemplateMeta(&t, isAdmin(c))
	return &meta, nil
}

// DELETE /api/v1/templates/:id (admin or ACL delete)
func deleteTemplate(c *gin.Context) (any, error) {
	id, err := parseTemplateID(c)
	if err != nil {
		return nil, err
	}
	ok, err := userHasTemplatePerm(c, id, model.TemplatePermDelete)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, singleton.Localizer.ErrorT("permission denied")
	}

	// delete ACLs first to avoid dangling entries
	if err := singleton.DB.Where("template_id = ?", id).Delete(&model.TemplateACL{}).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	if err := singleton.DB.Delete(&model.Template{}, id).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	return nil, nil
}

