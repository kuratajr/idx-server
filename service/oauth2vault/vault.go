package oauth2vault

import (
	"context"
	"errors"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"gorm.io/gorm"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/utils"
	"github.com/nezhahq/nezha/service/singleton"
)

var ErrPermissionDenied = errors.New("permission denied")

func canUserUseCredential(u *model.User, credID uint64) (bool, error) {
	if u == nil {
		return false, ErrPermissionDenied
	}
	if u.Role.IsAdmin() {
		return true, nil
	}

	var cnt int64
	if err := singleton.DB.Model(&model.Oauth2CredentialGrant{}).
		Where("credential_id = ? AND perm = ? AND ((grantee_type = ? AND grantee_id = ?) OR (grantee_type = ? AND grantee_id = ?) OR (grantee_type = ? AND grantee_id = 0))",
			credID, model.Oauth2PermUse,
			model.Oauth2GrantUser, u.ID,
			model.Oauth2GrantRole, uint64(u.Role),
			model.Oauth2GrantAll,
		).Count(&cnt).Error; err != nil {
		return false, err
	}
	return cnt > 0, nil
}

// GetToken returns a valid oauth2.Token for a credential (refreshing if necessary).
// It also records a usage log entry (best-effort).
func GetToken(ctx context.Context, actor *model.User, credID uint64) (*oauth2.Token, error) {
	ok, err := canUserUseCredential(actor, credID)
	if err != nil {
		return nil, err
	}
	if !ok {
		_ = singleton.DB.Create(&model.Oauth2CredentialUsageLog{
			CredentialID: credID,
			UserID:       actor.ID,
			Action:       "deny",
			Detail:       "no grant",
		}).Error
		return nil, ErrPermissionDenied
	}

	var cred model.Oauth2Credential
	if err := singleton.DB.First(&cred, credID).Error; err != nil {
		return nil, err
	}
	if cred.Revoked {
		return nil, errors.New("credential revoked")
	}

	access, err := utils.DecryptStringAESGCM(singleton.Conf.Oauth2TokenKeys, cred.AccessTokenEnc)
	if err != nil {
		return nil, err
	}
	refresh, err := utils.DecryptStringAESGCM(singleton.Conf.Oauth2TokenKeys, cred.RefreshTokenEnc)
	if err != nil {
		return nil, err
	}

	tok := &oauth2.Token{
		AccessToken:  access,
		TokenType:    strings.TrimSpace(cred.TokenType),
		RefreshToken: refresh,
		Expiry:       cred.ExpiresAt,
	}

	// If token is still valid enough, return it.
	if tok.Valid() && time.Until(tok.Expiry) > 30*time.Second {
		_ = singleton.DB.Create(&model.Oauth2CredentialUsageLog{
			CredentialID: credID,
			UserID:       actor.ID,
			Action:       "use",
		}).Error
		return tok, nil
	}

	// Refresh on-demand (requires provider config present).
	conf, has := singleton.Conf.Oauth2[cred.Provider]
	if !has {
		return nil, errors.New("provider oauth2 config missing")
	}

	o2 := conf.Setup("") // redirectURL not required for refresh
	src := o2.TokenSource(ctx, tok)
	newTok, err := src.Token()
	if err != nil {
		_ = singleton.DB.Create(&model.Oauth2CredentialUsageLog{
			CredentialID: credID,
			UserID:       actor.ID,
			Action:       "refresh",
			Detail:       "failed: " + err.Error(),
		}).Error
		return nil, err
	}

	accessEnc, err := utils.EncryptStringAESGCM(singleton.Conf.Oauth2TokenKeys, newTok.AccessToken)
	if err != nil {
		return nil, err
	}
	refreshEnc, err := utils.EncryptStringAESGCM(singleton.Conf.Oauth2TokenKeys, newTok.RefreshToken)
	if err != nil {
		return nil, err
	}

	updates := map[string]any{
		"access_token_enc":   accessEnc,
		"expires_at":         newTok.Expiry,
		"token_type":         newTok.TokenType,
		"last_refreshed_at":  time.Now(),
	}
	if refreshEnc != "" {
		updates["refresh_token_enc"] = refreshEnc
	}
	if err := singleton.DB.Model(&model.Oauth2Credential{}).Where("id = ?", credID).Updates(updates).Error; err != nil {
		return nil, err
	}
	_ = singleton.DB.Create(&model.Oauth2CredentialUsageLog{
		CredentialID: credID,
		UserID:       actor.ID,
		Action:       "refresh",
	}).Error

	return newTok, nil
}

var _ = gorm.ErrRecordNotFound

