package controller

import (
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

type oauth2CredentialDTO struct {
	ID                uint64    `json:"id"`
	Provider          string    `json:"provider"`
	ExternalAccountID string    `json:"external_account_id"`
	Email             string    `json:"email,omitempty"`
	DisplayName       string    `json:"display_name,omitempty"`
	Scopes            string    `json:"scopes,omitempty"`
	TokenType         string    `json:"token_type,omitempty"`
	ExpiresAt         time.Time `json:"expires_at,omitempty"`
	LastRefreshedAt   time.Time `json:"last_refreshed_at,omitempty"`

	Revoked         bool      `json:"revoked"`
	RevokedAt       time.Time `json:"revoked_at,omitempty"`
	RevokedByUserID uint64    `json:"revoked_by_user_id,omitempty"`
	RevokeReason    string    `json:"revoke_reason,omitempty"`

	CreatedByUserID uint64    `json:"created_by_user_id"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func toOauth2CredentialDTO(c model.Oauth2Credential) oauth2CredentialDTO {
	return oauth2CredentialDTO{
		ID:                c.ID,
		Provider:          c.Provider,
		ExternalAccountID: c.ExternalAccountID,
		Email:             c.Email,
		DisplayName:       c.DisplayName,
		Scopes:            c.Scopes,
		TokenType:         c.TokenType,
		ExpiresAt:         c.ExpiresAt,
		LastRefreshedAt:   c.LastRefreshedAt,
		Revoked:           c.Revoked,
		RevokedAt:         c.RevokedAt,
		RevokedByUserID:   c.RevokedByUserID,
		RevokeReason:      c.RevokeReason,
		CreatedByUserID:   c.CreatedByUserID,
		CreatedAt:         c.CreatedAt,
		UpdatedAt:         c.UpdatedAt,
	}
}

func listOauth2Credentials(c *gin.Context) ([]oauth2CredentialDTO, error) {
	var rows []model.Oauth2Credential
	q := singleton.DB.Model(&model.Oauth2Credential{})

	if provider := strings.TrimSpace(c.Query("provider")); provider != "" {
		q = q.Where("provider = ?", strings.ToLower(provider))
	}
	if v := strings.TrimSpace(c.Query("revoked")); v != "" {
		if v == "1" || strings.EqualFold(v, "true") {
			q = q.Where("revoked = ?", true)
		} else {
			q = q.Where("revoked = ?", false)
		}
	}

	if err := q.Order("id desc").Limit(500).Find(&rows).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	out := make([]oauth2CredentialDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, toOauth2CredentialDTO(r))
	}
	return out, nil
}

func getOauth2Credential(c *gin.Context) (*oauth2CredentialDTO, error) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		return nil, singleton.Localizer.ErrorT("invalid id")
	}
	var row model.Oauth2Credential
	if err := singleton.DB.First(&row, id).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	dto := toOauth2CredentialDTO(row)
	return &dto, nil
}

type revokeOauth2CredentialForm struct {
	Reason string `json:"reason"`
}

func revokeOauth2Credential(c *gin.Context) (any, error) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		return nil, singleton.Localizer.ErrorT("invalid id")
	}
	var form revokeOauth2CredentialForm
	_ = c.ShouldBindJSON(&form)

	auth := c.MustGet(model.CtxKeyAuthorizedUser).(*model.User)
	if err := singleton.DB.Model(&model.Oauth2Credential{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"revoked":           true,
			"revoked_at":        time.Now(),
			"revoked_by_user_id": auth.ID,
			"revoke_reason":     strings.TrimSpace(form.Reason),
		}).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	return nil, nil
}

func listOauth2CredentialGrants(c *gin.Context) ([]model.Oauth2CredentialGrant, error) {
	credID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || credID == 0 {
		return nil, singleton.Localizer.ErrorT("invalid id")
	}
	var rows []model.Oauth2CredentialGrant
	if err := singleton.DB.Where("credential_id = ?", credID).Order("id desc").Limit(1000).Find(&rows).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	return rows, nil
}

type upsertOauth2CredentialGrantForm struct {
	GranteeType model.Oauth2GrantGranteeType `json:"grantee_type"`
	GranteeID   uint64                      `json:"grantee_id"`
	Perm        model.Oauth2GrantPerm       `json:"perm"`
}

func upsertOauth2CredentialGrant(c *gin.Context) (any, error) {
	credID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || credID == 0 {
		return nil, singleton.Localizer.ErrorT("invalid id")
	}
	var form upsertOauth2CredentialGrantForm
	if err := c.ShouldBindJSON(&form); err != nil {
		return nil, err
	}
	if form.GranteeType != model.Oauth2GrantUser && form.GranteeType != model.Oauth2GrantRole && form.GranteeType != model.Oauth2GrantAll {
		return nil, singleton.Localizer.ErrorT("invalid grantee_type")
	}
	if form.Perm != model.Oauth2PermUse && form.Perm != model.Oauth2PermManage {
		return nil, singleton.Localizer.ErrorT("invalid perm")
	}
	if form.GranteeType == model.Oauth2GrantAll {
		form.GranteeID = 0
	}

	auth := c.MustGet(model.CtxKeyAuthorizedUser).(*model.User)
	g := model.Oauth2CredentialGrant{
		CredentialID:    credID,
		GranteeType:     form.GranteeType,
		GranteeID:       form.GranteeID,
		Perm:            form.Perm,
		CreatedByUserID: auth.ID,
	}

	if err := singleton.DB.FirstOrCreate(&g, model.Oauth2CredentialGrant{
		CredentialID: credID,
		GranteeType:  form.GranteeType,
		GranteeID:    form.GranteeID,
		Perm:         form.Perm,
	}).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	return nil, nil
}

type deleteOauth2CredentialGrantForm struct {
	GrantID uint64 `json:"grant_id"`
}

func deleteOauth2CredentialGrant(c *gin.Context) (any, error) {
	var form deleteOauth2CredentialGrantForm
	if err := c.ShouldBindJSON(&form); err != nil {
		return nil, err
	}
	if form.GrantID == 0 {
		return nil, singleton.Localizer.ErrorT("grant_id is required")
	}
	if err := singleton.DB.Delete(&model.Oauth2CredentialGrant{}, form.GrantID).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	return nil, nil
}

// listMyOauth2Credentials lists credentials the user can "use" (direct user grant, role grant, or all).
func listMyOauth2Credentials(c *gin.Context) ([]oauth2CredentialDTO, error) {
	auth := c.MustGet(model.CtxKeyAuthorizedUser).(*model.User)

	var grants []model.Oauth2CredentialGrant
	if err := singleton.DB.Where("(grantee_type = ? AND grantee_id = ?) OR (grantee_type = ? AND grantee_id = ?) OR (grantee_type = ? AND grantee_id = 0)",
		model.Oauth2GrantUser, auth.ID,
		model.Oauth2GrantRole, uint64(auth.Role),
		model.Oauth2GrantAll,
	).Where("perm = ?", model.Oauth2PermUse).Find(&grants).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	if len(grants) == 0 {
		return []oauth2CredentialDTO{}, nil
	}
	ids := make([]uint64, 0, len(grants))
	seen := map[uint64]struct{}{}
	for _, g := range grants {
		if _, ok := seen[g.CredentialID]; ok {
			continue
		}
		seen[g.CredentialID] = struct{}{}
		ids = append(ids, g.CredentialID)
	}

	var rows []model.Oauth2Credential
	if err := singleton.DB.Where("id IN ?", ids).Order("id desc").Find(&rows).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	out := make([]oauth2CredentialDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, toOauth2CredentialDTO(r))
	}
	return out, nil
}

var _ = gorm.ErrRecordNotFound

