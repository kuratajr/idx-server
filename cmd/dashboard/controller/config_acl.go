package controller

import (
	"errors"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

type configACLView struct {
	ID       uint64 `json:"id"`
	ConfigID uint64 `json:"config_id"`
	UserID   uint64 `json:"user_id"`
	PermMask uint32 `json:"perm_mask"`
}

// GET /api/v1/configs/:id/acl (admin only)
func listConfigACL(c *gin.Context) ([]configACLView, error) {
	cid, err := parseConfigID(c)
	if err != nil {
		return nil, err
	}

	var acls []model.NodeConfigACL
	if err := singleton.DB.Where("config_id = ?", cid).Order("id ASC").Find(&acls).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	out := make([]configACLView, 0, len(acls))
	for i := range acls {
		out = append(out, configACLView{
			ID:       acls[i].ID,
			ConfigID: acls[i].ConfigID,
			UserID:   acls[i].UserID,
			PermMask: acls[i].PermMask,
		})
	}
	return out, nil
}

type configACLUpsertForm struct {
	PermMask *uint32 `json:"perm_mask,omitempty"`

	ReadMeta *bool `json:"read_meta,omitempty"`
	ReadVars *bool `json:"read_vars,omitempty"`
	Write   *bool `json:"write,omitempty"`
	Delete  *bool `json:"delete,omitempty"`
}

func computeConfigPermMask(sf *configACLUpsertForm) (uint32, error) {
	if sf.PermMask != nil {
		return *sf.PermMask, nil
	}
	var mask uint32
	if sf.ReadMeta != nil && *sf.ReadMeta {
		mask |= uint32(model.NodeConfigPermReadMeta)
	}
	if sf.ReadVars != nil && *sf.ReadVars {
		mask |= uint32(model.NodeConfigPermReadVars)
	}
	if sf.Write != nil && *sf.Write {
		mask |= uint32(model.NodeConfigPermWrite)
	}
	if sf.Delete != nil && *sf.Delete {
		mask |= uint32(model.NodeConfigPermDelete)
	}
	if sf.ReadMeta == nil && sf.ReadVars == nil && sf.Write == nil && sf.Delete == nil && sf.PermMask == nil {
		return 0, errors.New("missing permissions")
	}
	return mask, nil
}

// PUT /api/v1/configs/:id/acl/:userId (admin only)
func upsertConfigACL(c *gin.Context) (any, error) {
	cid, err := parseConfigID(c)
	if err != nil {
		return nil, err
	}
	uid, err := parseUintParam(c, "userId")
	if err != nil {
		return nil, err
	}

	var sf configACLUpsertForm
	if err := c.ShouldBindJSON(&sf); err != nil {
		return nil, err
	}
	mask, err := computeConfigPermMask(&sf)
	if err != nil {
		return nil, err
	}

	var acl model.NodeConfigACL
	err = singleton.DB.Where("config_id = ? AND user_id = ?", cid, uid).First(&acl).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, newGormError("%v", err)
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		acl = model.NodeConfigACL{ConfigID: cid, UserID: uid, PermMask: mask}
		if err := singleton.DB.Create(&acl).Error; err != nil {
			return nil, newGormError("%v", err)
		}
	} else {
		if err := singleton.DB.Model(&model.NodeConfigACL{}).
			Where("id = ?", acl.ID).
			Update("perm_mask", mask).Error; err != nil {
			return nil, newGormError("%v", err)
		}
	}

	return nil, nil
}

// DELETE /api/v1/configs/:id/acl/:userId (admin only)
func deleteConfigACL(c *gin.Context) (any, error) {
	cid, err := parseConfigID(c)
	if err != nil {
		return nil, err
	}
	uid, err := parseUintParam(c, "userId")
	if err != nil {
		return nil, err
	}
	if err := singleton.DB.Where("config_id = ? AND user_id = ?", cid, uid).Delete(&model.NodeConfigACL{}).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	return nil, nil
}

