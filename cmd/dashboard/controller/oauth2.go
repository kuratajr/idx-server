package controller

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	jwt "github.com/appleboy/gin-jwt/v2"
	"github.com/gin-gonic/gin"
	"github.com/patrickmn/go-cache"
	"github.com/tidwall/gjson"
	"golang.org/x/oauth2"
	"gorm.io/gorm"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/utils"
	"github.com/nezhahq/nezha/service/singleton"
)

func getRedirectURL(c *gin.Context) string {
	scheme := "http://"
	referer := c.Request.Referer()
	if forwardedProto := c.Request.Header.Get("X-Forwarded-Proto"); forwardedProto == "https" || strings.HasPrefix(referer, "https://") {
		scheme = "https://"
	}
	return scheme + c.Request.Host + "/api/v1/oauth2/callback"
}

// @Summary Get Oauth2 Redirect URL
// @Description Get Oauth2 Redirect URL
// @Produce json
// @Param provider path string true "provider"
// @Param type query int false "type" Enums(1, 2) default(1)
// @Success 200 {object} model.Oauth2LoginResponse
// @Router /api/v1/oauth2/{provider} [get]
func oauth2redirect(c *gin.Context) (*model.Oauth2LoginResponse, error) {
	provider := c.Param("provider")
	if provider == "" {
		return nil, singleton.Localizer.ErrorT("provider is required")
	}

	rTypeInt, err := strconv.ParseUint(c.Query("type"), 10, 8)
	if err != nil {
		return nil, err
	}

	o2confRaw, has := singleton.Conf.Oauth2[provider]
	if !has {
		return nil, singleton.Localizer.ErrorT("provider not found")
	}
	redirectURL := getRedirectURL(c)
	o2conf := o2confRaw.Setup(redirectURL)

	randomString, err := utils.GenerateRandomString(32)
	if err != nil {
		return nil, err
	}
	state, stateKey := randomString[:16], randomString[16:]
	singleton.Cache.Set(fmt.Sprintf("%s%s", model.CacheKeyOauth2State, stateKey), &model.Oauth2State{
		Action:      model.Oauth2LoginType(rTypeInt),
		Provider:    provider,
		State:       state,
		RedirectURL: redirectURL,
	}, cache.DefaultExpiration)

	authOpts := []oauth2.AuthCodeOption{oauth2.AccessTypeOnline}
	if strings.EqualFold(provider, "google") {
		// Google needs offline + consent to reliably issue refresh_token.
		authOpts = []oauth2.AuthCodeOption{
			oauth2.AccessTypeOffline,
			oauth2.SetAuthURLParam("prompt", "consent"),
		}
	}
	url := o2conf.AuthCodeURL(state, authOpts...)
	// CodeQL go/cookie-secure-not-set: 根据请求协议动态设置 Secure 属性，避免 HTTP 环境下 Cookie 无法使用
	c.SetCookie("nz-o2s", stateKey, 60*5, "", "", c.Request.URL.Scheme == "https" || c.Request.TLS != nil, false)

	return &model.Oauth2LoginResponse{Redirect: url}, nil
}

// @Summary Unbind Oauth2
// @Description Unbind Oauth2
// @Accept json
// @Produce json
// @Param provider path string true "provider"
// @Success 200 {object} any
// @Router /api/v1/oauth2/{provider}/unbind [post]
func unbindOauth2(c *gin.Context) (any, error) {
	provider := c.Param("provider")
	if provider == "" {
		return nil, singleton.Localizer.ErrorT("provider is required")
	}
	_, has := singleton.Conf.Oauth2[provider]
	if !has {
		return nil, singleton.Localizer.ErrorT("provider not found")
	}
	provider = strings.ToLower(provider)

	u := c.MustGet(model.CtxKeyAuthorizedUser).(*model.User)
	query := singleton.DB.Where("provider = ? AND user_id = ?", provider, u.ID)

	var bindCount int64
	if err := query.Model(&model.Oauth2Bind{}).Count(&bindCount).Error; err != nil {
		return nil, newGormError("%v", err)
	}

	if bindCount < 2 && u.RejectPassword {
		return nil, singleton.Localizer.ErrorT("operation not permitted")
	}

	if err := query.Delete(&model.Oauth2Bind{}).Error; err != nil {
		return nil, newGormError("%v", err)
	}

	return nil, nil
}

// @Summary Oauth2 Callback
// @Description Oauth2 Callback
// @Accept json
// @Produce json
// @Param state query string true "state"
// @Param code query string true "code"
// @Success 200 {object} model.CommonResponse[any]
// @Router /api/v1/oauth2/callback [get]
func oauth2callback(jwtConfig *jwt.GinJWTMiddleware) func(c *gin.Context) (any, error) {
	return func(c *gin.Context) (any, error) {
		callbackData := &model.Oauth2Callback{
			State: c.Query("state"),
			Code:  c.Query("code"),
		}

		state, err := verifyState(c, callbackData.State)
		if err != nil {
			return nil, err
		}

		o2confRaw, has := singleton.Conf.Oauth2[state.Provider]
		if !has {
			return nil, singleton.Localizer.ErrorT("provider not found")
		}

		realip := c.GetString(model.CtxKeyRealIPStr)
		if callbackData.Code == "" {
			model.BlockIP(singleton.DB, realip, model.WAFBlockReasonTypeBruteForceOauth2, model.BlockIDToken)
			return nil, singleton.Localizer.ErrorT("code is required")
		}

		openId, otk, profileJSON, err := exchangeOpenIdAndToken(c, o2confRaw, callbackData, state.RedirectURL)
		if err != nil {
			model.BlockIP(singleton.DB, realip, model.WAFBlockReasonTypeBruteForceOauth2, model.BlockIDToken)
			return nil, err
		}

		var bind model.Oauth2Bind
		state.Provider = strings.ToLower(state.Provider)
		switch state.Action {
		case model.RTypeBind:
			u, authorized := c.Get(model.CtxKeyAuthorizedUser)
			if !authorized {
				return nil, singleton.Localizer.ErrorT("unauthorized")
			}
			user := u.(*model.User)

			result := singleton.DB.Where("provider = ? AND open_id = ?", state.Provider, openId).Limit(1).Find(&bind)
			if result.Error != nil && result.Error != gorm.ErrRecordNotFound {
				return nil, newGormError("%v", result.Error)
			}
			bind.UserID = user.ID
			bind.Provider = state.Provider
			bind.OpenID = openId

			if result.Error == gorm.ErrRecordNotFound {
				result = singleton.DB.Create(&bind)
			} else {
				result = singleton.DB.Save(&bind)
			}
			if result.Error != nil {
				return nil, newGormError("%v", result.Error)
			}

			// Upsert credential + store encrypted tokens (and auto grant "user/use" to binder).
			if err := upsertOauth2CredentialFromCallback(user.ID, state.Provider, openId, otk, profileJSON); err != nil {
				return nil, err
			}
		default:
			if err := singleton.DB.Where("provider = ? AND open_id = ?", state.Provider, openId).First(&bind).Error; err != nil {
				return nil, singleton.Localizer.ErrorT("oauth2 user not binded yet")
			}
			// Also upsert on login to keep latest refresh/access tokens.
			_ = upsertOauth2CredentialFromCallback(bind.UserID, state.Provider, openId, otk, profileJSON)
		}

		tokenString, _, err := jwtConfig.TokenGenerator(map[string]interface{}{
			"user_id": fmt.Sprintf("%d", bind.UserID),
			"ip":      realip,
		})
		if err != nil {
			return nil, err
		}

		jwtConfig.SetCookie(c, tokenString)
		c.Redirect(http.StatusFound, utils.IfOr(state.Action == model.RTypeBind, "/dashboard/profile?oauth2=true", "/dashboard/login?oauth2=true"))

		return nil, errNoop
	}
}

func exchangeOpenIdAndToken(c *gin.Context, o2confRaw *model.Oauth2Config,
	callbackData *model.Oauth2Callback, redirectURL string) (string, *oauth2.Token, []byte, error) {
	o2conf := o2confRaw.Setup(redirectURL)

	otk, err := o2conf.Exchange(c, callbackData.Code)
	if err != nil {
		return "", nil, nil, err
	}
	oauth2client := o2conf.Client(c, otk)
	resp, err := oauth2client.Get(o2confRaw.UserInfoURL)
	if err != nil {
		return "", nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, nil, err
	}

	return gjson.GetBytes(body, o2confRaw.UserIDPath).String(), otk, body, nil
}

func verifyState(c *gin.Context, state string) (*model.Oauth2State, error) {
	// 验证登录跳转时的 State
	stateKey, err := c.Cookie("nz-o2s")
	if err != nil {
		return nil, singleton.Localizer.ErrorT("invalid state key")
	}

	cacheKey := fmt.Sprintf("%s%s", model.CacheKeyOauth2State, stateKey)
	istate, ok := singleton.Cache.Get(cacheKey)
	if !ok {
		return nil, singleton.Localizer.ErrorT("invalid state key")
	}

	oauth2State, ok := istate.(*model.Oauth2State)
	if !ok || oauth2State.State != state {
		return nil, singleton.Localizer.ErrorT("invalid state key")
	}

	return oauth2State, nil
}

func upsertOauth2CredentialFromCallback(actorUserID uint64, provider string, externalAccountID string, otk *oauth2.Token, profileJSON []byte) error {
	provider = strings.ToLower(strings.TrimSpace(provider))
	externalAccountID = strings.TrimSpace(externalAccountID)
	if provider == "" || externalAccountID == "" || otk == nil {
		return nil
	}

	accessEnc, err := utils.EncryptStringAESGCM(singleton.Conf.Oauth2TokenKeys, otk.AccessToken)
	if err != nil {
		return err
	}
	refreshEnc, err := utils.EncryptStringAESGCM(singleton.Conf.Oauth2TokenKeys, otk.RefreshToken)
	if err != nil {
		return err
	}

	email := ""
	name := ""
	// Best-effort fields; providers differ.
	if len(profileJSON) > 0 {
		email = gjson.GetBytes(profileJSON, "email").String()
		if email == "" {
			email = gjson.GetBytes(profileJSON, "user.email").String()
		}
		name = gjson.GetBytes(profileJSON, "name").String()
		if name == "" {
			name = gjson.GetBytes(profileJSON, "login").String()
		}
	}

	scopes := ""
	if otk != nil && otk.Extra("scope") != nil {
		if s, ok := otk.Extra("scope").(string); ok {
			scopes = s
		}
	}

	var cred model.Oauth2Credential
	tx := singleton.DB.Where("provider = ? AND external_account_id = ?", provider, externalAccountID).First(&cred)
	if tx.Error != nil && tx.Error != gorm.ErrRecordNotFound {
		return newGormError("%v", tx.Error)
	}
	if tx.Error == gorm.ErrRecordNotFound {
		cred.Provider = provider
		cred.ExternalAccountID = externalAccountID
		cred.CreatedByUserID = actorUserID
	}

	cred.Email = email
	cred.DisplayName = name
	cred.Scopes = scopes
	cred.TokenType = otk.TokenType
	cred.AccessTokenEnc = accessEnc
	// Refresh token can be empty for some providers / subsequent logins.
	if refreshEnc != "" {
		cred.RefreshTokenEnc = refreshEnc
	}
	cred.ExpiresAt = otk.Expiry
	cred.LastRefreshedAt = time.Now()

	if cred.ID == 0 {
		if err := singleton.DB.Create(&cred).Error; err != nil {
			return newGormError("%v", err)
		}
	} else {
		if err := singleton.DB.Save(&cred).Error; err != nil {
			return newGormError("%v", err)
		}
	}

	// Auto-grant binder user "use" permission.
	g := model.Oauth2CredentialGrant{
		CredentialID:    cred.ID,
		GranteeType:     model.Oauth2GrantUser,
		GranteeID:       actorUserID,
		Perm:            model.Oauth2PermUse,
		CreatedByUserID: actorUserID,
	}
	_ = singleton.DB.FirstOrCreate(&g, model.Oauth2CredentialGrant{
		CredentialID: cred.ID,
		GranteeType:  model.Oauth2GrantUser,
		GranteeID:    actorUserID,
		Perm:         model.Oauth2PermUse,
	}).Error

	return nil
}
