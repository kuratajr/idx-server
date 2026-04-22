package controller

import (
	"errors"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

type templateACLView struct {
	ID        uint64 `json:"id"`
	TemplateID uint64 `json:"template_id"`
	UserID    uint64 `json:"user_id"`
	PermMask  uint32 `json:"perm_mask"`
}

func parseUintParam(c *gin.Context, key string) (uint64, error) {
	s := c.Param(key)
	if s == "" {
		return 0, errors.New("missing " + key)
	}
	var id uint64
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, errors.New("invalid " + key)
		}
		id = id*10 + uint64(ch-'0')
	}
	if id == 0 {
		return 0, errors.New("invalid " + key)
	}
	return id, nil
}

// GET /api/v1/templates/:id/acl (admin only)
func listTemplateACL(c *gin.Context) ([]templateACLView, error) {
	tid, err := parseTemplateID(c)
	if err != nil {
		return nil, err
	}

	var acls []model.TemplateACL
	if err := singleton.DB.Where("template_id = ?", tid).Order("id ASC").Find(&acls).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	out := make([]templateACLView, 0, len(acls))
	for i := range acls {
		out = append(out, templateACLView{
			ID:         acls[i].ID,
			TemplateID: acls[i].TemplateID,
			UserID:     acls[i].UserID,
			PermMask:   acls[i].PermMask,
		})
	}
	return out, nil
}

type templateACLUpsertForm struct {
	PermMask *uint32 `json:"perm_mask,omitempty"`

	ReadMeta *bool `json:"read_meta,omitempty"`
	ReadRaw  *bool `json:"read_raw,omitempty"`
	Write   *bool `json:"write,omitempty"`
	Delete  *bool `json:"delete,omitempty"`
}

func computePermMask(sf *templateACLUpsertForm) (uint32, error) {
	if sf.PermMask != nil {
		return *sf.PermMask, nil
	}
	var mask uint32
	if sf.ReadMeta != nil && *sf.ReadMeta {
		mask |= uint32(model.TemplatePermReadMeta)
	}
	if sf.ReadRaw != nil && *sf.ReadRaw {
		mask |= uint32(model.TemplatePermReadRaw)
	}
	if sf.Write != nil && *sf.Write {
		mask |= uint32(model.TemplatePermWrite)
	}
	if sf.Delete != nil && *sf.Delete {
		mask |= uint32(model.TemplatePermDelete)
	}
	// If all fields are nil, it's likely a caller error.
	if sf.ReadMeta == nil && sf.ReadRaw == nil && sf.Write == nil && sf.Delete == nil && sf.PermMask == nil {
		return 0, errors.New("missing permissions")
	}
	return mask, nil
}

// PUT /api/v1/templates/:id/acl/:userId (admin only)
func upsertTemplateACL(c *gin.Context) (any, error) {
	tid, err := parseTemplateID(c)
	if err != nil {
		return nil, err
	}
	uid, err := parseUintParam(c, "userId")
	if err != nil {
		return nil, err
	}

	var sf templateACLUpsertForm
	if err := c.ShouldBindJSON(&sf); err != nil {
		return nil, err
	}
	mask, err := computePermMask(&sf)
	if err != nil {
		return nil, err
	}

	var acl model.TemplateACL
	err = singleton.DB.Where("template_id = ? AND user_id = ?", tid, uid).First(&acl).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, newGormError("%v", err)
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		acl = model.TemplateACL{TemplateID: tid, UserID: uid, PermMask: mask}
		if err := singleton.DB.Create(&acl).Error; err != nil {
			return nil, newGormError("%v", err)
		}
	} else {
		if err := singleton.DB.Model(&model.TemplateACL{}).
			Where("id = ?", acl.ID).
			Update("perm_mask", mask).Error; err != nil {
			return nil, newGormError("%v", err)
		}
	}

	return nil, nil
}

// DELETE /api/v1/templates/:id/acl/:userId (admin only)
func deleteTemplateACL(c *gin.Context) (any, error) {
	tid, err := parseTemplateID(c)
	if err != nil {
		return nil, err
	}
	uid, err := parseUintParam(c, "userId")
	if err != nil {
		return nil, err
	}
	if err := singleton.DB.Where("template_id = ? AND user_id = ?", tid, uid).Delete(&model.TemplateACL{}).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	return nil, nil
}

