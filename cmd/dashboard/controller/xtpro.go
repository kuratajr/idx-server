package controller

import (
	"fmt"

	"github.com/gin-gonic/gin"

	xtproserver "github.com/nezhahq/nezha/internal/xtpro/server"
	"github.com/nezhahq/nezha/service/singleton"
)

type xtproOverview struct {
	Enabled  bool                          `json:"enabled"`
	Snapshot xtproserver.DashboardSnapshot `json:"snapshot"`
}

func requireXTPro() (*xtproserver.Service, error) {
	if !singleton.Conf.XTPRO.Enabled {
		return nil, fmt.Errorf("xtpro is disabled")
	}
	if singleton.XTProShared == nil {
		return nil, fmt.Errorf("xtpro service is not ready")
	}
	return singleton.XTProShared, nil
}

func getXTProOverview(c *gin.Context) (*xtproOverview, error) {
	svc, err := requireXTPro()
	if err != nil {
		return nil, err
	}
	return &xtproOverview{
		Enabled:  true,
		Snapshot: svc.DashboardSnapshot(),
	}, nil
}

func listXTProTunnels(c *gin.Context) ([]xtproserver.TunnelView, error) {
	svc, err := requireXTPro()
	if err != nil {
		return nil, err
	}
	return svc.DashboardSnapshot().Tunnels, nil
}

func listXTProUsers(c *gin.Context) ([]xtproserver.UserView, error) {
	svc, err := requireXTPro()
	if err != nil {
		return nil, err
	}
	return svc.ListUsers()
}

func deleteXTProTunnel(c *gin.Context) (map[string]bool, error) {
	svc, err := requireXTPro()
	if err != nil {
		return nil, err
	}
	if err := svc.DeleteTunnel(c.Param("id")); err != nil {
		return nil, err
	}
	return map[string]bool{"deleted": true}, nil
}
