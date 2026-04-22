package rpc

import (
	"context"
	"log"
	"strings"

	petname "github.com/dustinkirkland/golang-petname"
	"github.com/hashicorp/go-uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

type authHandler struct {
	ClientSecret string
	ClientUUID   string
}

func mdKeys(md metadata.MD) []string {
	keys := make([]string, 0, len(md))
	for k := range md {
		keys = append(keys, k)
	}
	return keys
}

func normalizeClientName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Collapse whitespace (also strips newlines/tabs) to avoid log injection & keep names readable.
	s = strings.Join(strings.Fields(s), " ")
	const maxRunes = 128
	r := []rune(s)
	if len(r) > maxRunes {
		s = string(r[:maxRunes])
	}
	return s
}

func (a *authHandler) Check(ctx context.Context) (uint64, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return 0, status.Errorf(codes.Unauthenticated, "获取 metaData 失败")
	}

	clientName := ""
	if v, ok := md["client_name"]; ok && len(v) > 0 {
		clientName = normalizeClientName(v[0])
	}

	gcpWorkstation := "<missing>"
	if v, ok := md["gcp_workstation"]; ok && len(v) > 0 {
		gcpWorkstation = strings.TrimSpace(v[0])
	}

	log.Printf("NEZHA>> auth incoming md keys=%v client_name=%q gcp_workstation=%q", mdKeys(md), clientName, gcpWorkstation)

	var clientSecret string
	if value, ok := md["client_secret"]; ok {
		clientSecret = strings.TrimSpace(value[0])
	}

	if clientSecret == "" {
		return 0, status.Error(codes.Unauthenticated, "客户端认证失败")
	}

	ip, _ := ctx.Value(model.CtxKeyRealIP{}).(string)

	singleton.UserLock.RLock()
	userId, ok := singleton.AgentSecretToUserId[clientSecret]
	if !ok {
		singleton.UserLock.RUnlock()
		model.BlockIP(singleton.DB, ip, model.WAFBlockReasonTypeAgentAuthFail, model.BlockIDgRPC)
		return 0, status.Error(codes.Unauthenticated, "客户端认证失败")
	}
	singleton.UserLock.RUnlock()

	model.UnblockIP(singleton.DB, ip, model.BlockIDgRPC)

	var clientUUID string
	if value, ok := md["client_uuid"]; ok {
		clientUUID = value[0]
	}

	if _, err := uuid.ParseUUID(clientUUID); err != nil {
		return 0, status.Error(codes.Unauthenticated, "客户端 UUID 不合法")
	}

	clientID, hasID := singleton.ServerShared.UUIDToID(clientUUID)
	if !hasID {
		name := petname.Generate(2, "-")
		if clientName != "" {
			name = clientName
		}
		s := model.Server{UUID: clientUUID, Name: name, Common: model.Common{
			UserID: userId,
		}}
		if err := singleton.DB.Create(&s).Error; err != nil {
			return 0, status.Error(codes.Unauthenticated, err.Error())
		}

		model.InitServer(&s)
		singleton.ServerShared.Update(&s, clientUUID)

		clientID = s.ID
	} else if clientName != "" {
		// If server exists but name is empty, set it from client_name.
		if server, ok := singleton.ServerShared.Get(clientID); ok && server != nil && server.Name == "" {
			if err := singleton.DB.Model(&model.Server{}).
				Where("id = ? AND name = ''", clientID).
				Update("name", clientName).Error; err != nil {
				return 0, status.Error(codes.Internal, err.Error())
			}

			server.Name = clientName
			singleton.ServerShared.Update(server, "")
		}
	}

	return clientID, nil
}
