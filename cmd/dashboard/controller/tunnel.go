package controller

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/model"
	pb "github.com/nezhahq/nezha/proto"
	"github.com/nezhahq/nezha/service/singleton"
)

type tunnelSyncResponse struct {
	// Snapshot is returned by agent as JSON (TunnelStatusSnapshot marshaled as string).
	Snapshot string `json:"snapshot"`
}

func tunnelSync(c *gin.Context) (*tunnelSyncResponse, error) {
	idStr := c.Param("id")
	serverID, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		return nil, err
	}

	s, ok := singleton.ServerShared.Get(serverID)
	if !ok || s == nil || s.TaskStream == nil {
		return nil, singleton.Localizer.ErrorT("server not found or not connected")
	}
	if !s.HasPermission(c) {
		return nil, singleton.Localizer.ErrorT("permission denied")
	}

	var desired model.TunnelDesiredState
	if err := c.ShouldBindJSON(&desired); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(desired)
	if err != nil {
		return nil, err
	}

	if err := s.TaskStream.Send(&pb.Task{
		// Id optional; we use 0 because we gate responses via TunnelCache size=1.
		Type: model.TaskTypeTunnelSync,
		Data: string(payload),
	}); err != nil {
		return nil, err
	}

	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	select {
	case <-timeout.C:
		return nil, singleton.Localizer.ErrorT("operation timeout")
	case data := <-s.TunnelCache:
		switch v := data.(type) {
		case string:
			return &tunnelSyncResponse{Snapshot: v}, nil
		case error:
			return nil, singleton.Localizer.ErrorT("tunnel sync failed: %v", v)
		default:
			return nil, singleton.Localizer.ErrorT("tunnel sync failed")
		}
	}
}

type tunnelStatusResponse struct {
	Snapshot string `json:"snapshot"`
}

func tunnelStatus(c *gin.Context) (*tunnelStatusResponse, error) {
	idStr := c.Param("id")
	serverID, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		return nil, err
	}

	s, ok := singleton.ServerShared.Get(serverID)
	if !ok || s == nil || s.TaskStream == nil {
		return nil, singleton.Localizer.ErrorT("server not found or not connected")
	}
	if !s.HasPermission(c) {
		return nil, singleton.Localizer.ErrorT("permission denied")
	}

	if err := s.TaskStream.Send(&pb.Task{Type: model.TaskTypeTunnelReport}); err != nil {
		return nil, err
	}

	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	select {
	case <-timeout.C:
		return nil, singleton.Localizer.ErrorT("operation timeout")
	case data := <-s.TunnelCache:
		switch v := data.(type) {
		case string:
			return &tunnelStatusResponse{Snapshot: v}, nil
		case error:
			return nil, singleton.Localizer.ErrorT("tunnel report failed: %v", v)
		default:
			return nil, singleton.Localizer.ErrorT("tunnel report failed")
		}
	}
}

