package controller

import (
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

const rawConfigSingletonID uint64 = 1

type rawConfigMeta struct {
	UpdatedAt int64 `json:"updated_at"`
	Length    int   `json:"length"`
}

func ensureRawConfigRow() (*model.RawConfig, error) {
	var rc model.RawConfig
	err := singleton.DB.First(&rc, rawConfigSingletonID).Error
	if err == nil {
		return &rc, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	rc = model.RawConfig{ID: rawConfigSingletonID, Content: ""}
	if err := singleton.DB.Create(&rc).Error; err != nil {
		return nil, err
	}
	return &rc, nil
}

// GET /api/v1/raw-config (public)
// Returns text/plain raw config for nodes to curl.
func getRawConfig(c *gin.Context) (any, error) {
	rc, err := ensureRawConfigRow()
	if err != nil {
		return nil, newGormError("%v", err)
	}
	c.Data(200, "text/plain; charset=utf-8", []byte(rc.Content))
	return nil, errNoop
}

// GET /api/v1/raw-config/meta (admin only)
func getRawConfigMeta(c *gin.Context) (*rawConfigMeta, error) {
	if !isAdmin(c) {
		return nil, singleton.Localizer.ErrorT("permission denied")
	}
	rc, err := ensureRawConfigRow()
	if err != nil {
		return nil, newGormError("%v", err)
	}
	return &rawConfigMeta{
		UpdatedAt: rc.UpdatedAt.Unix(),
		Length:    len(rc.Content),
	}, nil
}

type rawConfigUpsertForm struct {
	Content string `json:"content"`
}

// PUT /api/v1/raw-config (admin only)
func putRawConfig(c *gin.Context) (*rawConfigMeta, error) {
	if !isAdmin(c) {
		return nil, singleton.Localizer.ErrorT("permission denied")
	}
	var sf rawConfigUpsertForm
	if err := c.ShouldBindJSON(&sf); err != nil {
		return nil, err
	}
	// Keep trailing newline optional, but normalize Windows line endings to avoid churn.
	content := strings.ReplaceAll(sf.Content, "\r\n", "\n")

	rc, err := ensureRawConfigRow()
	if err != nil {
		return nil, newGormError("%v", err)
	}
	if err := singleton.DB.Model(&model.RawConfig{}).Where("id = ?", rc.ID).Update("content", content).Error; err != nil {
		return nil, newGormError("%v", err)
	}

	var out model.RawConfig
	if err := singleton.DB.First(&out, rawConfigSingletonID).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	return &rawConfigMeta{UpdatedAt: out.UpdatedAt.Unix(), Length: len(out.Content)}, nil
}

