package rpc

import (
	"context"
	// boot-script dispatch runs once per boot (boot_time)
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"log"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/jinzhu/copier"
	geoipx "github.com/nezhahq/nezha/pkg/geoip"
	"github.com/nezhahq/nezha/pkg/grpcx"
	"github.com/nezhahq/nezha/pkg/tsdb"
	"github.com/nezhahq/nezha/pkg/utils"

	"github.com/nezhahq/nezha/model"
	pb "github.com/nezhahq/nezha/proto"
	"github.com/nezhahq/nezha/service/singleton"
	"gorm.io/gorm/clause"
)

func idxTemplateFuncMap(vars map[string]any) template.FuncMap {
	// Minimal helpers to support templates using {{ v "KEY" }} style.
	return template.FuncMap{
		"v": func(key string) any {
			key = strings.TrimSpace(key)
			if key == "" {
				return ""
			}
			if vars == nil {
				return ""
			}
			if val, ok := vars[key]; ok {
				return val
			}
			// also try uppercase key for convenience
			if val, ok := vars[strings.ToUpper(key)]; ok {
				return val
			}
			return ""
		},
	}
}

var _ pb.NezhaServiceServer = (*NezhaHandler)(nil)

var NezhaHandlerSingleton *NezhaHandler

type NezhaHandler struct {
	Auth          *authHandler
	ioStreams     map[string]*ioStreamContext
	ioStreamMutex *sync.RWMutex
}

func NewNezhaHandler() *NezhaHandler {
	return &NezhaHandler{
		Auth:          &authHandler{},
		ioStreamMutex: new(sync.RWMutex),
		ioStreams:     make(map[string]*ioStreamContext),
	}
}

func (s *NezhaHandler) RequestTask(stream pb.NezhaService_RequestTaskServer) error {
	var clientID uint64
	var err error
	if clientID, err = s.Auth.Check(stream.Context()); err != nil {
		return err
	}

	server, _ := singleton.ServerShared.Get(clientID)
	server.TaskStream = stream
	// Best-effort: dispatch IDX meta script once per boot after task stream is ready.
	s.dispatchIDXMetaOnce(server)
	var result *pb.TaskResult
	for {
		result, err = stream.Recv()
		if err != nil {
			log.Printf("NEZHA>> RequestTask error: %v, clientID: %d\n", err, clientID)
			return err
		}
		switch result.GetType() {
		case model.TaskTypeCommand:
			// IDX meta run result tracking (best-effort).
			{
				var run model.IdxMetaRun
				if err := singleton.DB.First(&run, result.GetId()).Error; err == nil &&
					(run.Status == model.IdxMetaRunDispatched || run.Status == model.IdxMetaRunFetched) {
					out := result.GetData()
					const maxOut = 32 * 1024
					if len(out) > maxOut {
						out = out[:maxOut]
					}
					succ := result.GetSuccessful()
					status := model.IdxMetaRunFailed
					if succ {
						status = model.IdxMetaRunSuccess
					}
					_ = singleton.DB.Model(&model.IdxMetaRun{}).Where("id = ?", run.ID).Updates(map[string]any{
						"status":      status,
						"successful":  succ,
						"output":      out,
						"finished_at": time.Now(),
					}).Error
				}
			}
			// IDX meta bootstrap result tracking (best-effort).
			var ev model.IdxMetaEvent
			if err := singleton.DB.First(&ev, result.GetId()).Error; err == nil && ev.Stage == model.IdxMetaStageDispatched {
				out := result.GetData()
				const maxOut = 8192
				if len(out) > maxOut {
					out = out[:maxOut]
				}
				succ := result.GetSuccessful()
				_ = singleton.DB.Model(&model.IdxMetaEvent{}).Where("id = ?", ev.ID).Updates(map[string]any{
					"stage":      model.IdxMetaStageResult,
					"successful": succ,
					"message":    out,
				}).Error
			}
			// 处理上报的计划任务
			cr, _ := singleton.CronShared.Get(result.GetId())
			if cr != nil {
				// 保存当前服务器状态信息
				var curServer model.Server
				copier.Copy(&curServer, server)
				if cr.PushSuccessful && result.GetSuccessful() {
					singleton.NotificationShared.SendNotification(cr.NotificationGroupID, fmt.Sprintf("[%s] %s, %s\n%s", singleton.Localizer.T("Scheduled Task Executed Successfully"),
						cr.Name, server.Name, result.GetData()), "", &curServer)
				}
				if !result.GetSuccessful() {
					singleton.NotificationShared.SendNotification(cr.NotificationGroupID, fmt.Sprintf("[%s] %s, %s\n%s", singleton.Localizer.T("Scheduled Task Executed Failed"),
						cr.Name, server.Name, result.GetData()), "", &curServer)
				}
				singleton.DB.Model(cr).Updates(model.Cron{
					LastExecutedAt: time.Now().Add(time.Second * -1 * time.Duration(result.GetDelay())),
					LastResult:     result.GetSuccessful(),
				})
			}
		case model.TaskTypeReportConfig:
			if len(server.ConfigCache) < 1 {
				if !result.GetSuccessful() {
					server.ConfigCache <- errors.New(result.Data)
					continue
				}
				server.ConfigCache <- result.Data
			}
		default:
			if model.IsServiceSentinelNeeded(result.GetType()) {
				singleton.ServiceSentinelShared.Dispatch(singleton.ReportData{
					Data:     result,
					Reporter: clientID,
				})
			}
		}
	}
}

func normalizeTag(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	return s
}

func (s *NezhaHandler) dispatchIDXMetaOnce(server *model.Server) {
	if server == nil || server.TaskStream == nil || server.Host == nil {
		if singleton.Conf.Debug {
			log.Printf("NEZHA>> idx-meta skip: missing server/taskstream/host (server=nil=%v)", server == nil)
		}
		return
	}
	bootTime := server.Host.BootTime
	if bootTime == 0 {
		if singleton.Conf.Debug {
			log.Printf("NEZHA>> idx-meta skip: boot_time=0 server_id=%d", server.ID)
		}
		return
	}

	// Only IDX nodes participate.
	if !server.RuntimeIDX {
		if singleton.Conf.Debug {
			log.Printf("NEZHA>> idx-meta skip: idx=false server_id=%d name=%q", server.ID, server.Name)
		}
		return
	}
	workspaceSlug := normalizeTag(server.RuntimeWorkspaceSlug)
	if workspaceSlug == "" {
		if singleton.Conf.Debug {
			log.Printf("NEZHA>> idx-meta skip: missing workspace_slug server_id=%d name=%q", server.ID, server.Name)
		}
		return
	}

	// Create a run record for timeline tracking.
	run := model.IdxMetaRun{
		ServerID:      server.ID,
		WorkspaceSlug: workspaceSlug,
		BootTime:      bootTime,
		Status:        model.IdxMetaRunDispatched,
		DispatchedAt:  time.Now(),
	}
	_ = singleton.DB.Create(&run).Error

	// Start an event record so we can track whether it ran.
	ev := model.IdxMetaEvent{
		ServerID:      server.ID,
		WorkspaceSlug: workspaceSlug,
		BootTime:      bootTime,
		Stage:         model.IdxMetaStageDispatchAttempt,
	}
	_ = singleton.DB.Create(&ev).Error

	// Resolve effective assignment for this server by (server_id, group_id, tag_name=workspace_slug).
	var groupIDs []uint64
	_ = singleton.DB.Model(&model.ServerGroupServer{}).
		Select("server_group_id").
		Where("server_id = ?", server.ID).
		Scan(&groupIDs).Error

	q := singleton.DB.Model(&model.NodeConfigAssignment{}).
		Where("enabled = ?", true).
		Where(singleton.DB.Where("target_type = ? AND server_id = ?", model.NodeConfigTargetServer, server.ID).
			Or("target_type = ? AND tag_name = ?", model.NodeConfigTargetTag, workspaceSlug))
	if len(groupIDs) > 0 {
		q = q.Or("target_type = ? AND group_id IN ?", model.NodeConfigTargetGroup, groupIDs)
	}

	var asg model.NodeConfigAssignment
	if err := q.Order("priority DESC, updated_at DESC, id DESC").First(&asg).Error; err != nil {
		_ = singleton.DB.Model(&model.IdxMetaRun{}).Where("id = ?", run.ID).Updates(map[string]any{
			"status": model.IdxMetaRunSkipped,
			"error":  "no matching assignment",
		}).Error
		_ = singleton.DB.Model(&model.IdxMetaEvent{}).Where("id = ?", ev.ID).Updates(map[string]any{
			"stage":   model.IdxMetaStageSendFailed,
			"message": "no matching assignment",
		}).Error
		if singleton.Conf.Debug {
			log.Printf("NEZHA>> idx-meta no assignment server_id=%d slug=%q boot_time=%d groups=%v", server.ID, workspaceSlug, bootTime, groupIDs)
		}
		return
	}
	if singleton.Conf.Debug {
		log.Printf("NEZHA>> idx-meta selected assignment id=%d target=%s priority=%d server_id=%d slug=%q boot_time=%d",
			asg.ID, asg.TargetType, asg.Priority, server.ID, workspaceSlug, bootTime)
	}

	var cfg model.NodeConfig
	if err := singleton.DB.First(&cfg, asg.ConfigID).Error; err != nil {
		if singleton.Conf.Debug {
			log.Printf("NEZHA>> idx-meta skip: config load failed config_id=%d err=%v", asg.ConfigID, err)
		}
		return
	}
	if !cfg.Enabled {
		if singleton.Conf.Debug {
			log.Printf("NEZHA>> idx-meta skip: config disabled config_id=%d", cfg.ID)
		}
		return
	}

	var tplRow model.Template
	if err := singleton.DB.First(&tplRow, cfg.TemplateID).Error; err != nil {
		if singleton.Conf.Debug {
			log.Printf("NEZHA>> idx-meta skip: template load failed template_id=%d err=%v", cfg.TemplateID, err)
		}
		return
	}
	// Only render templates intended as scripts.
	if strings.TrimSpace(strings.ToLower(tplRow.Type)) != "script" {
		_ = singleton.DB.Model(&model.IdxMetaRun{}).Where("id = ?", run.ID).Updates(map[string]any{
			"status": model.IdxMetaRunSkipped,
			"error":  "template type is not script",
		}).Error
		_ = singleton.DB.Model(&model.IdxMetaEvent{}).Where("id = ?", ev.ID).Updates(map[string]any{
			"stage":   model.IdxMetaStageSendFailed,
			"message": "template type is not script",
		}).Error
		if singleton.Conf.Debug {
			log.Printf("NEZHA>> idx-meta skip: template type=%q (need script) template_id=%d", tplRow.Type, tplRow.ID)
		}
		return
	}

	var vars map[string]any
	if cfg.VarsOverride != "" {
		_ = json.Unmarshal([]byte(cfg.VarsOverride), &vars)
	}
	if vars == nil {
		vars = map[string]any{}
	}
	// Inject runtime values.
	vars["WORKSPACE_SLUG"] = workspaceSlug
	vars["SERVER_ID"] = server.ID
	vars["BOOT_TIME"] = bootTime

	t, err := template.New("idx-script").
		Funcs(idxTemplateFuncMap(vars)).
		Option("missingkey=error").
		Parse(tplRow.ContentRaw)
	if err != nil {
		return
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, vars); err != nil {
		return
	}
	rendered := buf.String()
	if strings.TrimSpace(rendered) == "" {
		return
	}
	// Wrap for sh compatibility if template didn't specify a shebang.
	if !strings.HasPrefix(rendered, "#!") {
		rendered = "#!/bin/sh\nset -eu\n\n" + rendered
	}

	// Intentionally constant: updating config/template won't retrigger within the same boot.
	// If you need to rerun without reboot, delete related rows in node_boot_runs.
	const scriptID = "idx-meta"

	bootRun := model.NodeBootRun{
		ServerID:      server.ID,
		BootTime:      bootTime,
		ScriptID:      scriptID,
		DispatchedAt:  time.Now(),
	}
	// Insert-if-not-exists (unique by server_id+boot_time+script_id).
	res := singleton.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&bootRun)
	if res.Error != nil {
		return
	}
	if res.RowsAffected == 0 {
		// Already dispatched for this boot.
		_ = singleton.DB.Model(&model.IdxMetaRun{}).Where("id = ?", run.ID).Updates(map[string]any{
			"status": model.IdxMetaRunSkipped,
			"error":  "already dispatched in this boot",
		}).Error
		_ = singleton.DB.Model(&model.IdxMetaEvent{}).Where("id = ?", ev.ID).Updates(map[string]any{
			"stage":   model.IdxMetaStageResult,
			"message": "skipped: already dispatched in this boot",
		}).Error
		if singleton.Conf.Debug {
			log.Printf("NEZHA>> idx-meta skip: already dispatched server_id=%d boot_time=%d", server.ID, bootTime)
		}
		return
	}

	// Dispatch a short bootstrap command: curl rendered script (signed) and pipe to sh.
	// This avoids sending a huge script over gRPC and makes debugging easier.
	type payload struct {
		RunID         uint64 `json:"run_id"`
		ServerID      uint64 `json:"server_id"`
		WorkspaceSlug string `json:"workspace_slug"`
		BootTime      uint64 `json:"boot_time"`
		ExpiresAtUnix int64  `json:"exp"`
	}
	tok, err := utils.MakeSignedToken(singleton.Conf.AgentSecretKey, payload{
		RunID:         run.ID,
		ServerID:      server.ID,
		WorkspaceSlug: workspaceSlug,
		BootTime:      bootTime,
		ExpiresAtUnix: time.Now().Add(2 * time.Minute).Unix(),
	})
	if err != nil {
		singleton.DB.Where("server_id = ? AND boot_time = ? AND script_id = ?", server.ID, bootTime, scriptID).Delete(&model.NodeBootRun{})
		_ = singleton.DB.Model(&model.IdxMetaRun{}).Where("id = ?", run.ID).Updates(map[string]any{
			"status": model.IdxMetaRunFailed,
			"error":  "token build failed",
		}).Error
		return
	}
	url := utils.BuildIDXScriptURL(singleton.Conf.InstallHost, singleton.Conf.AgentTLS, tok)
	cmd := fmt.Sprintf("curl -fsSL %q | sh", url)
	if len(cmd) > 2048 {
		cmd = cmd[:2048]
	}

	_ = singleton.DB.Model(&model.IdxMetaRun{}).Where("id = ?", run.ID).Updates(map[string]any{
		"assignment_id": asg.ID,
		"target_type":   string(asg.TargetType),
		"priority":      asg.Priority,
		"config_id":     asg.ConfigID,
		"template_id":   tplRow.ID,
		"command":       cmd,
	}).Error

	_ = singleton.DB.Model(&model.IdxMetaEvent{}).Where("id = ?", ev.ID).Updates(map[string]any{
		"stage":        model.IdxMetaStageDispatched,
		"assignment_id": asg.ID,
		"target_type":   string(asg.TargetType),
		"priority":      asg.Priority,
		"config_id":     asg.ConfigID,
		"template_id":   tplRow.ID,
		"message":       cmd,
	}).Error
	if singleton.Conf.Debug {
		log.Printf("NEZHA>> idx-meta dispatched server_id=%d slug=%q boot_time=%d assignment_id=%d config_id=%d template_id=%d",
			server.ID, workspaceSlug, bootTime, asg.ID, asg.ConfigID, tplRow.ID)
	}

	// If send fails, delete the record so it can be retried.
	if err := server.TaskStream.Send(&pb.Task{
		Id:   run.ID,
		Type: model.TaskTypeCommand,
		Data: cmd,
	}); err != nil {
		singleton.DB.Where("server_id = ? AND boot_time = ? AND script_id = ?", server.ID, bootTime, scriptID).Delete(&model.NodeBootRun{})
		_ = singleton.DB.Model(&model.IdxMetaRun{}).Where("id = ?", run.ID).Updates(map[string]any{
			"status": model.IdxMetaRunFailed,
			"error":  fmt.Sprintf("send failed: %v", err),
		}).Error
		_ = singleton.DB.Model(&model.IdxMetaEvent{}).Where("id = ?", ev.ID).Updates(map[string]any{
			"stage":   model.IdxMetaStageSendFailed,
			"message": fmt.Sprintf("send failed: %v", err),
		}).Error
		if singleton.Conf.Debug {
			log.Printf("NEZHA>> idx-meta send failed server_id=%d err=%v", server.ID, err)
		}
	}
}

func (s *NezhaHandler) ReportSystemState(stream pb.NezhaService_ReportSystemStateServer) error {
	clientID, err := s.Auth.Check(stream.Context())
	if err != nil {
		return err
	}
	var state *pb.State
	for {
		state, err = stream.Recv()
		if err != nil {
			log.Printf("NEZHA>> ReportSystemState error: %v, clientID: %d\n", err, clientID)
			return err
		}
		innerState := model.PB2State(state)

		server, ok := singleton.ServerShared.Get(clientID)
		if !ok || server == nil {
			return errors.New("server not found")
		}

		server.LastActive = time.Now()
		server.State = &innerState

		if singleton.TSDBEnabled() {
			maxTemp := 0.0
			for _, t := range innerState.Temperatures {
				if t.Temperature > maxTemp {
					maxTemp = t.Temperature
				}
			}
			maxGPU := 0.0
			for _, g := range innerState.GPU {
				if g > maxGPU {
					maxGPU = g
				}
			}
			if err := singleton.TSDBShared.WriteServerMetrics(&tsdb.ServerMetrics{
				ServerID:       clientID,
				Timestamp:      time.Now(),
				CPU:            innerState.CPU,
				MemUsed:        innerState.MemUsed,
				SwapUsed:       innerState.SwapUsed,
				DiskUsed:       innerState.DiskUsed,
				NetInSpeed:     innerState.NetInSpeed,
				NetOutSpeed:    innerState.NetOutSpeed,
				NetInTransfer:  innerState.NetInTransfer,
				NetOutTransfer: innerState.NetOutTransfer,
				Load1:          innerState.Load1,
				Load5:          innerState.Load5,
				Load15:         innerState.Load15,
				TCPConnCount:   innerState.TcpConnCount,
				UDPConnCount:   innerState.UdpConnCount,
				ProcessCount:   innerState.ProcessCount,
				Temperature:    maxTemp,
				Uptime:         innerState.Uptime,
				GPU:            maxGPU,
			}); err != nil {
				log.Printf("NEZHA>> Failed to write server metrics to TSDB: %v", err)
			}
		}

		// 应对 dashboard / agent 重启的情况，如果从未记录过，先打点，等到小时时间点时入库
		if server.PrevTransferInSnapshot == 0 || server.PrevTransferOutSnapshot == 0 {
			server.PrevTransferInSnapshot = state.NetInTransfer
			server.PrevTransferOutSnapshot = state.NetOutTransfer
		}

		if err = stream.Send(&pb.Receipt{Proced: true}); err != nil {
			return err
		}
	}
}

func (s *NezhaHandler) onReportSystemInfo(c context.Context, r *pb.Host) error {
	var clientID uint64
	var err error
	if clientID, err = s.Auth.Check(c); err != nil {
		return err
	}
	host := model.PB2Host(r)

	server, ok := singleton.ServerShared.Get(clientID)
	if !ok || server == nil {
		return errors.New("server not found")
	}

	/**
	 * 这里的 singleton 中的数据都是关机前的旧数据
	 * 当 agent 重启时，bootTime 变大，agent 端会先上报 host 信息，然后上报 state 信息
	 * 这时可以借助上报顺序的空档，立即记录停机前的数据并重置 Prev* 数据，并由接下来的 state 方法重新赋值
	 */
	if !server.LastActive.IsZero() && host.BootTime > server.Host.BootTime {
		singleton.RecordTransferHourlyUsage(server)
		server.PrevTransferInSnapshot = 0
		server.PrevTransferOutSnapshot = 0
	}

	server.Host = &host
	return nil
}

func (s *NezhaHandler) ReportSystemInfo(c context.Context, r *pb.Host) (*pb.Receipt, error) {
	if err := s.onReportSystemInfo(c, r); err != nil {
		return nil, err
	}
	return &pb.Receipt{Proced: true}, nil
}

func (s *NezhaHandler) ReportSystemInfo2(c context.Context, r *pb.Host) (*pb.Uint64Receipt, error) {
	if err := s.onReportSystemInfo(c, r); err != nil {
		return nil, err
	}
	return &pb.Uint64Receipt{Data: singleton.DashboardBootTime}, nil
}

func (s *NezhaHandler) IOStream(stream pb.NezhaService_IOStreamServer) error {
	if _, err := s.Auth.Check(stream.Context()); err != nil {
		return err
	}
	id, err := stream.Recv()
	if err != nil {
		return err
	}

	// ff05ff05 是 Nezha 的魔数，用于标识流 ID
	if id == nil || len(id.Data) < 4 || (id.Data[0] != 0xff && id.Data[1] != 0x05 && id.Data[2] != 0xff && id.Data[3] == 0x05) {
		return fmt.Errorf("invalid stream id")
	}

	go func() {
		for {
			if err := stream.Send(&pb.IOStreamData{Data: []byte{}}); err != nil {
				log.Printf("NEZHA>> IOStream keepAlive error: %v\n", err)
				return
			}
			time.Sleep(time.Second * 30)
		}
	}()

	streamId := string(id.Data[4:])

	if _, err := s.GetStream(streamId); err != nil {
		return err
	}
	iw := grpcx.NewIOStreamWrapper(stream)
	if err := s.AgentConnected(streamId, iw); err != nil {
		return err
	}
	iw.Wait()
	return nil
}

func (s *NezhaHandler) ReportGeoIP(c context.Context, r *pb.GeoIP) (*pb.GeoIP, error) {
	var clientID uint64
	var err error
	if clientID, err = s.Auth.Check(c); err != nil {
		return nil, err
	}

	geoip := model.PB2GeoIP(r)
	use6 := r.GetUse6()

	if geoip.IP.IPv4Addr == "" && geoip.IP.IPv6Addr == "" {
		ip, _ := c.Value(model.CtxKeyRealIP{}).(string)
		if ip == "" {
			ip, _ = c.Value(model.CtxKeyConnectingIP{}).(string)
		}
		geoip.IP.IPv4Addr = ip
	}

	joinedIP := geoip.IP.Join()

	server, ok := singleton.ServerShared.Get(clientID)
	if !ok || server == nil {
		return nil, fmt.Errorf("server not found")
	}

	// 检查并更新DDNS
	if server.EnableDDNS && joinedIP != "" &&
		(server.GeoIP == nil || server.GeoIP.IP != geoip.IP) {
		ipv4 := geoip.IP.IPv4Addr
		ipv6 := geoip.IP.IPv6Addr

		if err := singleton.ServerShared.UpdateDDNS(server, &model.IP{IPv4Addr: ipv4, IPv6Addr: ipv6}); err != nil {
			log.Printf("NEZHA>> Failed to update DDNS for server %d: %v", err, server.ID)
		}
	}

	// 发送IP变动通知
	if server.GeoIP != nil && singleton.Conf.EnableIPChangeNotification &&
		((singleton.Conf.Cover == model.ConfigCoverAll && !singleton.Conf.IgnoredIPNotificationServerIDs[clientID]) ||
			(singleton.Conf.Cover == model.ConfigCoverIgnoreAll && singleton.Conf.IgnoredIPNotificationServerIDs[clientID])) &&
		server.GeoIP.IP.Join() != "" &&
		joinedIP != "" &&
		server.GeoIP.IP != geoip.IP {

		singleton.NotificationShared.SendNotification(singleton.Conf.IPChangeNotificationGroupID,
			fmt.Sprintf(
				"[%s] %s, %s => %s",
				singleton.Localizer.T("IP Changed"),
				server.Name, singleton.IPDesensitize(server.GeoIP.IP.Join()),
				singleton.IPDesensitize(joinedIP),
			),
			"")
	}

	// 根据内置数据库查询 IP 地理位置
	var ip string
	if geoip.IP.IPv6Addr != "" && (use6 || geoip.IP.IPv4Addr == "") {
		ip = geoip.IP.IPv6Addr
	} else {
		ip = geoip.IP.IPv4Addr
	}

	netIP := net.ParseIP(ip)
	location, err := geoipx.Lookup(netIP)
	if err != nil {
		log.Printf("NEZHA>> geoip.Lookup: %v", err)
	}
	geoip.CountryCode = location

	// 将地区码写入到 Host
	server.GeoIP = &geoip

	return &pb.GeoIP{Ip: nil, CountryCode: location, DashboardBootTime: singleton.DashboardBootTime}, nil
}
