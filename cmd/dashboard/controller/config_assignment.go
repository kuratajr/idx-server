package controller

import (
	"errors"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/hashicorp/go-uuid"
	"gorm.io/gorm/clause"
	"gorm.io/gorm"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

var tagNameRe = regexp.MustCompile(`^[a-zA-Z0-9._:-]{1,128}$`)

func normalizeTagName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	return s
}

type assignmentForm struct {
	ConfigID    uint64 `json:"config_id"`
	TargetType  string `json:"target_type"`
	ServerID    uint64 `json:"server_id,omitempty"`
	GroupID     uint64 `json:"group_id,omitempty"`
	TagName     string `json:"tag_name,omitempty"`
	Priority    *int   `json:"priority,omitempty"`
	Enabled     *bool  `json:"enabled,omitempty"`
}

type assignmentBulkForm struct {
	ConfigID uint64 `json:"config_id"`
	Servers  []uint64 `json:"servers,omitempty"`
	Groups   []uint64 `json:"groups,omitempty"`
	Tags     []string `json:"tags,omitempty"`

	Priority *int  `json:"priority,omitempty"`
	Enabled  *bool `json:"enabled,omitempty"`
}

func validateAssignment(sf *assignmentForm) error {
	if sf.ConfigID == 0 {
		return errors.New("config_id is required")
	}
	sf.TargetType = strings.TrimSpace(sf.TargetType)
	switch model.NodeConfigTargetType(sf.TargetType) {
	case model.NodeConfigTargetServer:
		if sf.ServerID == 0 {
			return errors.New("server_id is required")
		}
	case model.NodeConfigTargetGroup:
		if sf.GroupID == 0 {
			return errors.New("group_id is required")
		}
	case model.NodeConfigTargetTag:
		sf.TagName = normalizeTagName(sf.TagName)
		if sf.TagName == "" || !tagNameRe.MatchString(sf.TagName) {
			return errors.New("invalid tag_name")
		}
	default:
		return errors.New("invalid target_type")
	}
	return nil
}

type assignmentView struct {
	ID         uint64 `json:"id"`
	ConfigID   uint64 `json:"config_id"`
	BatchID    string `json:"batch_id,omitempty"`
	TargetType string `json:"target_type"`
	ServerID   uint64 `json:"server_id,omitempty"`
	GroupID    uint64 `json:"group_id,omitempty"`
	TagName    string `json:"tag_name,omitempty"`
	Priority   int    `json:"priority"`
	Enabled    bool   `json:"enabled"`
	CreatedBy  uint64 `json:"created_by"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

func toAssignmentView(a *model.NodeConfigAssignment) assignmentView {
	return assignmentView{
		ID:         a.ID,
		ConfigID:   a.ConfigID,
		BatchID:    a.BatchID,
		TargetType: string(a.TargetType),
		ServerID:   a.ServerID,
		GroupID:    a.GroupID,
		TagName:    a.TagName,
		Priority:   a.Priority,
		Enabled:    a.Enabled,
		CreatedBy:  a.CreatedBy,
		CreatedAt:  a.CreatedAt.Unix(),
		UpdatedAt:  a.UpdatedAt.Unix(),
	}
}

// GET /api/v1/config-assignments (admin only)
func listConfigAssignments(c *gin.Context) ([]assignmentView, error) {
	var list []model.NodeConfigAssignment
	q := singleton.DB.Model(&model.NodeConfigAssignment{})
	if v := strings.TrimSpace(c.Query("target_type")); v != "" {
		q = q.Where("target_type = ?", v)
	}
	if v := strings.TrimSpace(c.Query("tag_name")); v != "" {
		q = q.Where("tag_name = ?", normalizeTagName(v))
	}
	if v := strings.TrimSpace(c.Query("config_id")); v != "" {
		// best-effort parse
		var id uint64
		for _, ch := range v {
			if ch < '0' || ch > '9' {
				id = 0
				break
			}
			id = id*10 + uint64(ch-'0')
		}
		if id != 0 {
			q = q.Where("config_id = ?", id)
		}
	}
	if v := strings.TrimSpace(c.Query("server_id")); v != "" {
		var id uint64
		for _, ch := range v {
			if ch < '0' || ch > '9' {
				id = 0
				break
			}
			id = id*10 + uint64(ch-'0')
		}
		if id != 0 {
			q = q.Where("server_id = ?", id)
		}
	}
	if v := strings.TrimSpace(c.Query("group_id")); v != "" {
		var id uint64
		for _, ch := range v {
			if ch < '0' || ch > '9' {
				id = 0
				break
			}
			id = id*10 + uint64(ch-'0')
		}
		if id != 0 {
			q = q.Where("group_id = ?", id)
		}
	}
	if err := q.Order("id DESC").Find(&list).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	out := make([]assignmentView, 0, len(list))
	for i := range list {
		out = append(out, toAssignmentView(&list[i]))
	}
	return out, nil
}

type effectiveAssignmentQuery struct {
	ServerID uint64 `form:"server_id"`
	GroupID  uint64 `form:"group_id"`
	TagName  string `form:"tag_name"`
}

// GET /api/v1/config-assignments/effective (admin only)
// Cho mục đích debug: trả assignment được chọn theo enabled+priority.
func effectiveConfigAssignment(c *gin.Context) (*assignmentView, error) {
	var qy effectiveAssignmentQuery
	if err := c.ShouldBindQuery(&qy); err != nil {
		return nil, err
	}
	q := singleton.DB.Model(&model.NodeConfigAssignment{}).Where("enabled = ?", true)

	tag := normalizeTagName(qy.TagName)
	if qy.ServerID == 0 && qy.GroupID == 0 && tag == "" {
		return nil, errors.New("server_id or group_id or tag_name is required")
	}

	// Build OR conditions for different target types.
	sub := singleton.DB.Where("1 = 0")
	if qy.ServerID != 0 {
		sub = sub.Or("target_type = ? AND server_id = ?", model.NodeConfigTargetServer, qy.ServerID)
	}
	if qy.GroupID != 0 {
		sub = sub.Or("target_type = ? AND group_id = ?", model.NodeConfigTargetGroup, qy.GroupID)
	}
	if tag != "" {
		sub = sub.Or("target_type = ? AND tag_name = ?", model.NodeConfigTargetTag, tag)
	}

	var a model.NodeConfigAssignment
	if err := q.Where(sub).Order("priority DESC, updated_at DESC, id DESC").First(&a).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("no matching assignment")
		}
		return nil, newGormError("%v", err)
	}
	v := toAssignmentView(&a)
	return &v, nil
}

// POST /api/v1/config-assignments (admin only)
func createConfigAssignment(c *gin.Context) (*assignmentView, error) {
	var sf assignmentForm
	if err := c.ShouldBindJSON(&sf); err != nil {
		return nil, err
	}
	if err := validateAssignment(&sf); err != nil {
		return nil, err
	}

	uid, _ := currentUserID(c)
	enabled := true
	if sf.Enabled != nil {
		enabled = *sf.Enabled
	}
	priority := 0
	if sf.Priority != nil {
		priority = *sf.Priority
	}

	batchID, _ := uuid.GenerateUUID()
	a := model.NodeConfigAssignment{
		ConfigID:    sf.ConfigID,
		BatchID:     batchID,
		TargetType:  model.NodeConfigTargetType(sf.TargetType),
		ServerID:    sf.ServerID,
		GroupID:     sf.GroupID,
		TagName:     sf.TagName,
		Priority:    priority,
		Enabled:     enabled,
		CreatedBy:   uid,
	}
	// Upsert: if target already exists, update priority/enabled/batch_id.
	if err := singleton.DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "config_id"},
			{Name: "target_type"},
			{Name: "server_id"},
			{Name: "group_id"},
			{Name: "tag_name"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"batch_id", "priority", "enabled", "updated_at"}),
	}).Create(&a).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	// Ensure we return the row (including ID) even if it was updated.
	var outRow model.NodeConfigAssignment
	if err := singleton.DB.Where("config_id = ? AND target_type = ? AND server_id = ? AND group_id = ? AND tag_name = ?",
		a.ConfigID, a.TargetType, a.ServerID, a.GroupID, a.TagName).First(&outRow).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	v := toAssignmentView(&outRow)
	return &v, nil
}

// POST /api/v1/config-assignments/bulk (admin only)
// Create multiple assignments in one request.
func createConfigAssignmentsBulk(c *gin.Context) ([]assignmentView, error) {
	var sf assignmentBulkForm
	if err := c.ShouldBindJSON(&sf); err != nil {
		return nil, err
	}
	if sf.ConfigID == 0 {
		return nil, errors.New("config_id is required")
	}
	enabled := true
	if sf.Enabled != nil {
		enabled = *sf.Enabled
	}
	priority := 0
	if sf.Priority != nil {
		priority = *sf.Priority
	}

	if len(sf.Servers) == 0 && len(sf.Groups) == 0 && len(sf.Tags) == 0 {
		return nil, errors.New("servers or groups or tags is required")
	}

	uid, _ := currentUserID(c)
	toCreate := make([]model.NodeConfigAssignment, 0, len(sf.Servers)+len(sf.Groups)+len(sf.Tags))
	batchID, _ := uuid.GenerateUUID()

	for _, sid := range sf.Servers {
		if sid == 0 {
			continue
		}
		toCreate = append(toCreate, model.NodeConfigAssignment{
			ConfigID:   sf.ConfigID,
			BatchID:    batchID,
			TargetType: model.NodeConfigTargetServer,
			ServerID:   sid,
			Priority:   priority,
			Enabled:    enabled,
			CreatedBy:  uid,
		})
	}
	for _, gid := range sf.Groups {
		if gid == 0 {
			continue
		}
		toCreate = append(toCreate, model.NodeConfigAssignment{
			ConfigID:   sf.ConfigID,
			BatchID:    batchID,
			TargetType: model.NodeConfigTargetGroup,
			GroupID:    gid,
			Priority:   priority,
			Enabled:    enabled,
			CreatedBy:  uid,
		})
	}
	for _, t := range sf.Tags {
		tag := normalizeTagName(t)
		if tag == "" {
			continue
		}
		if !tagNameRe.MatchString(tag) {
			return nil, errors.New("invalid tag_name")
		}
		toCreate = append(toCreate, model.NodeConfigAssignment{
			ConfigID:   sf.ConfigID,
			BatchID:    batchID,
			TargetType: model.NodeConfigTargetTag,
			TagName:    tag,
			Priority:   priority,
			Enabled:    enabled,
			CreatedBy:  uid,
		})
	}
	if len(toCreate) == 0 {
		return nil, errors.New("no valid targets")
	}

	// Upsert in a single batch: avoid UNIQUE errors on repeated apply.
	if err := singleton.DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "config_id"},
			{Name: "target_type"},
			{Name: "server_id"},
			{Name: "group_id"},
			{Name: "tag_name"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"batch_id", "priority", "enabled", "updated_at"}),
	}).Create(&toCreate).Error; err != nil {
		return nil, newGormError("%v", err)
	}

	// Query back all rows for this batch_id (includes both inserted and updated).
	var list []model.NodeConfigAssignment
	if err := singleton.DB.Where("batch_id = ?", batchID).Order("id DESC").Find(&list).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	out := make([]assignmentView, 0, len(list))
	for i := range list {
		out = append(out, toAssignmentView(&list[i]))
	}
	return out, nil
}

type assignmentBatchView struct {
	BatchID    string `json:"batch_id"`
	ConfigID   uint64 `json:"config_id"`
	Priority   int    `json:"priority"`
	Enabled    bool   `json:"enabled"`
	Count      int64  `json:"count"`
	UpdatedAt  int64  `json:"updated_at"`
}

// GET /api/v1/config-assignment-batches (admin only)
// Returns grouped rows by batch_id so UI can show "one item" per create action.
func listAssignmentBatches(c *gin.Context) ([]assignmentBatchView, error) {
	type row struct {
		BatchID   string
		ConfigID  uint64
		Priority  int
		Enabled   bool
		Count     int64
		UpdatedAt int64
	}
	var rows []row
	if err := singleton.DB.Table("node_config_assignments").
		Select("batch_id as batch_id, config_id as config_id, priority as priority, enabled as enabled, COUNT(*) as count, MAX(strftime('%s', updated_at)) as updated_at").
		Where("batch_id <> ''").
		Group("batch_id, config_id, priority, enabled").
		Order("updated_at DESC").
		Scan(&rows).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	out := make([]assignmentBatchView, 0, len(rows))
	for _, r := range rows {
		out = append(out, assignmentBatchView{
			BatchID:   r.BatchID,
			ConfigID:  r.ConfigID,
			Priority:  r.Priority,
			Enabled:   r.Enabled,
			Count:     r.Count,
			UpdatedAt: r.UpdatedAt,
		})
	}
	return out, nil
}

// GET /api/v1/config-assignment-batches/:batchId (admin only)
func getAssignmentBatch(c *gin.Context) ([]assignmentView, error) {
	bid := strings.TrimSpace(c.Param("batchId"))
	if bid == "" {
		return nil, errors.New("missing batchId")
	}
	var list []model.NodeConfigAssignment
	if err := singleton.DB.Where("batch_id = ?", bid).Order("id DESC").Find(&list).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	out := make([]assignmentView, 0, len(list))
	for i := range list {
		out = append(out, toAssignmentView(&list[i]))
	}
	return out, nil
}

// PATCH /api/v1/config-assignments/:id (admin only)
func updateConfigAssignment(c *gin.Context) (*assignmentView, error) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		return nil, err
	}

	var sf assignmentForm
	if err := c.ShouldBindJSON(&sf); err != nil {
		return nil, err
	}

	updates := map[string]any{}
	if sf.Priority != nil {
		updates["priority"] = *sf.Priority
	}
	if sf.Enabled != nil {
		updates["enabled"] = *sf.Enabled
	}
	if sf.TagName != "" {
		tag := normalizeTagName(sf.TagName)
		if !tagNameRe.MatchString(tag) {
			return nil, errors.New("invalid tag_name")
		}
		updates["tag_name"] = tag
	}
	if len(updates) == 0 {
		return nil, nil
	}

	if err := singleton.DB.Model(&model.NodeConfigAssignment{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return nil, newGormError("%v", err)
	}

	var a model.NodeConfigAssignment
	if err := singleton.DB.First(&a, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, singleton.Localizer.ErrorT("assignment id %d does not exist", id)
		}
		return nil, newGormError("%v", err)
	}
	v := toAssignmentView(&a)
	return &v, nil
}

// DELETE /api/v1/config-assignments/:id (admin only)
func deleteConfigAssignment(c *gin.Context) (any, error) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		return nil, err
	}
	if err := singleton.DB.Delete(&model.NodeConfigAssignment{}, id).Error; err != nil {
		return nil, newGormError("%v", err)
	}
	return nil, nil
}

