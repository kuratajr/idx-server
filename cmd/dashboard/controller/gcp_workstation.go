package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/utils"
	"github.com/nezhahq/nezha/service/oauth2vault"
	"github.com/nezhahq/nezha/service/singleton"
)

type generateGCPAccessTokenForm struct {
	// Optional override; if empty, use server.workstation_gcp.
	Workstation string `json:"workstation,omitempty"`
	// Optional; default 86400s.
	TTL string `json:"ttl,omitempty"`
	// Optional override: which google credential id to use (must be mapped to server).
	Oauth2GGID uint64 `json:"oauth2_gg_id,omitempty"`
}

type gcpGenerateAccessTokenRequest struct {
	TTL string `json:"ttl"`
}

type gcpGenerateAccessTokenResponse struct {
	AccessToken string `json:"accessToken"`
	ExpireTime  string `json:"expireTime"`
}

type googleAPIErrorEnvelope struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

type generateGCPAccessTokenResult struct {
	Workstation string    `json:"workstation"`
	AccessToken string    `json:"access_token"`
	ExpireTime  time.Time `json:"expire_time,omitempty"`
}

// @Summary Generate GCP Workstations access token
// @Security BearerAuth
// @Schemes
// @Description Calls Google Workstations :generateAccessToken using the server's mapped Google OAuth2 credential.
// @Tags auth required
// @Accept json
// @Produce json
// @Param id path uint true "Server ID"
// @Param body body generateGCPAccessTokenForm false "optional overrides"
// @Success 200 {object} model.CommonResponse[generateGCPAccessTokenResult]
// @Router /server/{id}/gcp/access-token [post]
func generateServerGCPAccessToken(c *gin.Context) (*generateGCPAccessTokenResult, error) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(strings.TrimSpace(idStr), 10, 64)
	if err != nil || id == 0 {
		return nil, errors.New("invalid server id")
	}

	// Load server row for persisted fields and permission check.
	var srow model.Server
	if err := singleton.DB.First(&srow, id).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	if !srow.HasPermission(c) {
		return nil, singleton.Localizer.ErrorT("permission denied")
	}

	var form generateGCPAccessTokenForm
	// Body is optional; ignore bind error if empty body.
	_ = c.ShouldBindJSON(&form)

	workstation := strings.TrimSpace(form.Workstation)
	if workstation == "" && srow.WorkstationGCP != nil {
		workstation = strings.TrimSpace(*srow.WorkstationGCP)
	}
	if workstation == "" {
		return nil, errors.New("workstation is required")
	}

	ttl := strings.TrimSpace(form.TTL)
	if ttl == "" {
		ttl = "86400s"
	}

	if srow.Oauth2GGID == nil || *srow.Oauth2GGID == 0 {
		// not fatal yet if server has multiple mappings and caller specifies one
	}

	actor := c.MustGet(model.CtxKeyAuthorizedUser).(*model.User)

	credID := uint64(0)
	if form.Oauth2GGID != 0 {
		credID = form.Oauth2GGID
		// Ensure this credential is mapped to the server.
		var cnt int64
		if err := singleton.DB.Model(&model.ServerOauth2Credential{}).
			Where("server_id = ? AND credential_id = ? AND provider = ?", id, credID, "google").
			Count(&cnt).Error; err != nil {
			return nil, newGormError("%v", err)
		}
		if cnt == 0 {
			return nil, errors.New("oauth2_gg_id is not mapped to this server")
		}
	} else if srow.Oauth2GGID != nil && *srow.Oauth2GGID != 0 {
		credID = *srow.Oauth2GGID
	} else {
		// fallback to first mapped credential
		var row model.ServerOauth2Credential
		if err := singleton.DB.Where("server_id = ? AND provider = ?", id, "google").
			Order("id asc").First(&row).Error; err != nil {
			return nil, errors.New("oauth2_gg_id is not configured for this server")
		}
		credID = row.CredentialID
	}

	tok, err := oauth2vault.GetToken(c, actor, credID)
	if err != nil {
		if err == oauth2vault.ErrPermissionDenied {
			return nil, singleton.Localizer.ErrorT("permission denied")
		}
		return nil, err
	}
	if tok == nil || strings.TrimSpace(tok.AccessToken) == "" {
		return nil, errors.New("missing oauth2 access token")
	}

	reqBody, _ := json.Marshal(gcpGenerateAccessTokenRequest{TTL: ttl})
	url := fmt.Sprintf("https://workstations.googleapis.com/v1/%s:generateAccessToken", strings.TrimPrefix(workstation, "/"))
	req, err := http.NewRequestWithContext(c, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(tok.AccessToken))
	req.Header.Set("Content-Type", "application/json")

	resp, err := utils.HttpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Try to parse Google API error envelope for a cleaner message.
		msg := strings.TrimSpace(string(body))
		var env googleAPIErrorEnvelope
		if err := json.Unmarshal(body, &env); err == nil {
			if m := strings.TrimSpace(env.Error.Message); m != "" {
				msg = m
			} else if st := strings.TrimSpace(env.Error.Status); st != "" {
				msg = st
			}
		}
		if msg == "" {
			msg = resp.Status
		}
		return nil, fmt.Errorf("gcp generateAccessToken failed (http %d): %s", resp.StatusCode, msg)
	}

	var out gcpGenerateAccessTokenResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if strings.TrimSpace(out.AccessToken) == "" {
		return nil, errors.New("gcp generateAccessToken returned empty accessToken")
	}

	var exp time.Time
	if strings.TrimSpace(out.ExpireTime) != "" {
		// RFC3339 expected from Google APIs.
		exp, _ = time.Parse(time.RFC3339Nano, strings.TrimSpace(out.ExpireTime))
	}

	// Persist token + workstation on the server row (best-effort).
	access := strings.TrimSpace(out.AccessToken)
	ws := workstation
	updates := map[string]any{
		"token_gcp":       &access,
		"workstation_gcp": &ws,
	}
	_ = singleton.DB.Model(&model.Server{}).Where("id = ?", id).Updates(updates).Error

	// Keep in-memory server object updated for immediate use in UI.
	if s, ok := singleton.ServerShared.Get(id); ok && s != nil {
		s.TokenGCP = &access
		s.WorkstationGCP = &ws
		singleton.ServerShared.Update(s, "")
	}

	return &generateGCPAccessTokenResult{
		Workstation: workstation,
		AccessToken: access,
		ExpireTime:  exp,
	}, nil
}

