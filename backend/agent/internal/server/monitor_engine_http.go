package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/user/doops/agent/api"
)

func (h *GatewayHub) HandleMonitorOverview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	cluster := strings.TrimSpace(q.Get("cluster"))
	if cluster != "" && !monitorID(cluster) {
		writeMonitorHTTPError(w, 400, "invalid_monitor_cluster", "集群名称格式无效")
		return
	}
	kind := strings.TrimSpace(q.Get("kind"))
	if kind != "" && kind != "vm" && kind != "physical" && kind != "k8s" && kind != "container" && kind != "app" && kind != "database" {
		writeMonitorHTTPError(w, 400, "invalid_monitor_kind", "资产类型无效")
		return
	}
	tag := strings.TrimSpace(q.Get("tag"))
	resp, err := h.BuildMonitorOverview(r.Context(), auth, q.Get("range"), cluster, kind, tag)
	if err != nil {
		if err.Error() == "invalid_monitor_range" {
			writeMonitorHTTPError(w, 400, "invalid_monitor_range", "时间范围仅支持 1h、6h、24h、7d")
			return
		}
		writeMonitorHTTPError(w, 503, "monitor_overview_unavailable", "无法读取系统资源监控概览")
		return
	}
	writeJSONHTTP(w, resp)
}

func (h *GatewayHub) HandleMonitorResourceHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	resp, err := h.ReadMonitorResourceHistory(
		r.Context(), auth,
		strings.TrimSpace(q.Get("cluster")),
		strings.TrimSpace(q.Get("instance")),
		strings.TrimSpace(q.Get("asset_kind")),
		strings.TrimSpace(q.Get("asset_id")),
		strings.TrimSpace(q.Get("range")),
	)
	if err != nil {
		switch {
		case errors.Is(err, ErrForbidden):
			writeMonitorHTTPError(w, 403, "monitor_forbidden", "当前账号无权查看该目标监控历史")
		case err.Error() == "invalid_monitor_target", err.Error() == "invalid_monitor_range":
			writeMonitorHTTPError(w, 400, err.Error(), "监控历史查询参数无效")
		default:
			writeMonitorHTTPError(w, 503, "monitor_history_unavailable", "无法读取资产历史监控数据")
		}
		return
	}
	writeJSONHTTP(w, resp)
}

type monitorScheduleMutationRequest struct {
	Action          string                       `json:"action"` // create | update | toggle | delete
	ID              string                       `json:"id,omitempty"`
	Name            string                       `json:"name,omitempty"`
	Description     string                       `json:"description,omitempty"`
	Enabled         *bool                        `json:"enabled,omitempty"`
	ScopeMode       string                       `json:"scope_mode,omitempty"`
	Targets         []monitorTargetRef           `json:"targets,omitempty"`
	Tags            []string                     `json:"tags,omitempty"`
	Strategies      []string                     `json:"strategies,omitempty"`
	Skills          []string                     `json:"skills,omitempty"`
	Prompt          string                       `json:"prompt,omitempty"`
	Command         string                       `json:"command,omitempty"`
	Thresholds      api.MonitorInspectThresholds `json:"thresholds,omitempty"`
	IntervalSeconds int                          `json:"interval_seconds,omitempty"`
}

func (h *GatewayHub) HandleMonitorSchedules(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	switch r.Method {
	case http.MethodGet:
		schedules, err := h.store.ListMonitorSchedules(r.Context())
		if err != nil {
			writeMonitorHTTPError(w, 503, "monitor_schedules_unavailable", "无法读取定时巡检计划")
			return
		}
		isAdmin := h.store.UserIsPlatformAdmin(auth.UserID)
		if !isAdmin {
			visible := make([]monitorScheduleRecord, 0, len(schedules))
			for _, s := range schedules {
				if s.ScopeMode == "targets" {
					matched := false
					for _, t := range s.Targets {
						if h.store.UserCanUseTarget(auth.UserID, t.Cluster, t.Instance) {
							matched = true
							break
						}
					}
					if !matched {
						continue
					}
				}
				visible = append(visible, s)
			}
			schedules = visible
		}
		writeJSONHTTP(w, map[string]any{
			"schedules": schedules,
			"defaults":  defaultMonitorThresholds(),
		})
	case http.MethodPost:
		if !h.store.UserIsPlatformAdmin(auth.UserID) {
			writeMonitorHTTPError(w, 403, "monitor_schedule_forbidden", "仅平台管理员可维护定时巡检计划")
			return
		}
		var req monitorScheduleMutationRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
		dec.DisallowUnknownFields()
		if dec.Decode(&req) != nil || dec.Decode(new(any)) != io.EOF {
			writeMonitorHTTPError(w, 400, "invalid_monitor_schedule_json", "计划请求格式无效")
			return
		}
		if rejectLegacyStrategyParams(w, req.Strategies) {
			return
		}
		auditID, _ := h.store.StartAudit(AuditEvent{
			UserID:         auth.UserID,
			TokenID:        auth.TokenID,
			Action:         GatewayAction("monitor:schedule:" + req.Action),
			Session:        "monitor-schedule",
			CommandSummary: req.Name + " " + req.ID,
		})
		res, code, msg, status := h.applyMonitorScheduleMutation(r, auth, req)
		if status != http.StatusOK {
			if auditID > 0 {
				_ = h.store.FinishAudit(auditID, AuditFinish{Status: "error", Error: code})
			}
			writeMonitorHTTPError(w, status, code, msg)
			return
		}
		if auditID > 0 {
			_ = h.store.FinishAudit(auditID, AuditFinish{Status: "success"})
		}
		writeJSONHTTP(w, res)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func (h *GatewayHub) applyMonitorScheduleMutation(r *http.Request, auth TokenAuth, req monitorScheduleMutationRequest) (any, string, string, int) {
	ctx := r.Context()
	switch req.Action {
	case "delete":
		if strings.TrimSpace(req.ID) == "" {
			return nil, "invalid_schedule_id", "缺少计划 ID", 400
		}
		if err := h.store.DeleteMonitorSchedule(ctx, req.ID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, "schedule_not_found", "定时计划不存在", 404
			}
			return nil, "schedule_delete_failed", "删除计划失败", 503
		}
		return map[string]any{"deleted": true, "id": req.ID}, "", "", 200
	case "toggle":
		if strings.TrimSpace(req.ID) == "" || req.Enabled == nil {
			return nil, "invalid_schedule_toggle", "缺少计划 ID 或启用状态", 400
		}
		updated, err := h.store.ToggleMonitorSchedule(ctx, req.ID, *req.Enabled)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, "schedule_not_found", "定时计划不存在", 404
			}
			return nil, "schedule_toggle_failed", "切换计划状态失败", 503
		}
		return map[string]any{"schedule": updated}, "", "", 200
	case "create", "update":
		name := strings.TrimSpace(req.Name)
		if name == "" || len(name) > 128 || !inventoryMetadata(name) {
			return nil, "invalid_schedule_name", "请填写 1–128 字的有效计划名称", 400
		}
		desc := strings.TrimSpace(req.Description)
		if len(desc) > 256 || !inventoryMetadata(desc) {
			return nil, "invalid_schedule_description", "描述过长或包含非法字符", 400
		}
		scopeMode := strings.TrimSpace(req.ScopeMode)
		if scopeMode == "" {
			scopeMode = "all_online"
		}
		if scopeMode != "all_online" && scopeMode != "targets" && scopeMode != "tags" {
			return nil, "invalid_scope_mode", "范围模式仅支持 all_online、targets、tags", 400
		}
		if scopeMode == "targets" && len(req.Targets) == 0 {
			return nil, "targets_required", "指定目标模式至少需要选择一个 Agent 目标", 400
		}
		cleanTargets := make([]monitorTargetRef, 0, len(req.Targets))
		for _, t := range req.Targets {
			if !monitorID(t.Cluster) || !monitorID(t.Instance) {
				return nil, "invalid_target", "目标集群/实例格式无效", 400
			}
			cleanTargets = append(cleanTargets, t)
		}
		cleanTags, tagErr := normalizeMachineTags(req.Tags)
		if tagErr != nil {
			return nil, "invalid_tags", tagErr.Error(), 400
		}
		if scopeMode == "tags" && len(cleanTags) == 0 {
			return nil, "tags_required", "按标签筛选模式至少需要提供一个标签", 400
		}
		interval := req.IntervalSeconds
		if interval == 0 {
			interval = 300
		}
		if interval < 60 || interval > 7*86400 {
			return nil, "invalid_interval", "定时周期须在 60 秒（1 分钟）至 604800 秒（7 天）之间", 400
		}
		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		prompt, ok := validateInspectionFreeText(req.Prompt, maxInspectionPromptBytes)
		if !ok {
			return nil, "invalid_prompt", "巡检提示词过长（上限 8 KiB）或包含非法字符", 400
		}
		command, ok := validateInspectionFreeText(req.Command, maxInspectionCommandBytes)
		if !ok {
			return nil, "invalid_command", "巡检命令过长（上限 4 KiB）或包含非法字符", 400
		}
		skills := normalizeSkillSlugs(req.Skills)
		rec := monitorScheduleRecord{
			ID:              strings.TrimSpace(req.ID),
			Name:            name,
			Description:     desc,
			Enabled:         enabled,
			ScopeMode:       scopeMode,
			Targets:         cleanTargets,
			Tags:            cleanTags,
			Skills:          skills,
			Prompt:          prompt,
			Command:         command,
			Thresholds:      normalizeMonitorThresholds(req.Thresholds),
			IntervalSeconds: interval,
			CreatedBy:       auth.UserID,
		}
		if req.Action == "update" {
			if rec.ID == "" {
				return nil, "invalid_schedule_id", "更新计划需要提供 ID", 400
			}
			cur, err := h.store.GetMonitorSchedule(ctx, rec.ID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, "schedule_not_found", "定时计划不存在", 404
				}
				return nil, "schedule_load_failed", "读取计划失败", 503
			}
			rec.NextRunAtMs = cur.NextRunAtMs
			if enabled && (rec.NextRunAtMs <= 0 || cur.IntervalSeconds != interval) {
				rec.NextRunAtMs = time.Now().UTC().Add(time.Duration(interval) * time.Second).UnixMilli()
			}
		}
		saved, err := h.store.SaveMonitorSchedule(ctx, rec, req.Action == "update")
		if err != nil {
			return nil, "schedule_save_failed", "保存计划失败", 503
		}
		return map[string]any{"schedule": saved}, "", "", 200
	default:
		return nil, "invalid_schedule_action", "不支持的操作类型", 400
	}
}

type monitorRunTriggerRequest struct {
	ScheduleID  string                       `json:"schedule_id,omitempty"`
	RetestRunID string                       `json:"retest_run_id,omitempty"`
	ScopeMode   string                       `json:"scope_mode,omitempty"`
	Targets     []monitorTargetRef           `json:"targets,omitempty"`
	Tags        []string                     `json:"tags,omitempty"`
	Strategies  []string                     `json:"strategies,omitempty"`
	Skills      []string                     `json:"skills,omitempty"`
	Prompt      string                       `json:"prompt,omitempty"`
	Command     string                       `json:"command,omitempty"`
	Thresholds  api.MonitorInspectThresholds `json:"thresholds,omitempty"`
	SSHHosts    []api.MonitorInspectAsset    `json:"ssh_hosts,omitempty"`
}

func (h *GatewayHub) HandleMonitorRuns(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	subPath := strings.TrimPrefix(r.URL.Path, "/v1/monitor/runs")
	subPath = strings.Trim(subPath, "/")

	switch r.Method {
	case http.MethodGet:
		if subPath != "" {
			run, items, err := h.store.GetMonitorRunDetail(r.Context(), auth, subPath)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeMonitorHTTPError(w, 404, "monitor_run_not_found", "巡检执行记录不存在或无权查看")
					return
				}
				writeMonitorHTTPError(w, 503, "monitor_run_unavailable", "无法读取巡检执行明细")
				return
			}
			writeJSONHTTP(w, map[string]any{
				"run":   run,
				"items": items,
			})
			return
		}
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		runs, err := h.store.ListMonitorRuns(r.Context(), auth, strings.TrimSpace(q.Get("schedule_id")), strings.TrimSpace(q.Get("status")), limit)
		if err != nil {
			writeMonitorHTTPError(w, 503, "monitor_runs_unavailable", "无法读取巡检执行记录列表")
			return
		}
		writeJSONHTTP(w, map[string]any{"runs": runs})
	case http.MethodPost:
		if subPath != "" {
			writeMonitorHTTPError(w, 405, "method_not_allowed", "不支持对该路径 POST")
			return
		}
		var req monitorRunTriggerRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
		dec.DisallowUnknownFields()
		if dec.Decode(&req) != nil || dec.Decode(new(any)) != io.EOF {
			writeMonitorHTTPError(w, 400, "invalid_monitor_run_json", "巡检触发参数格式无效")
			return
		}
		if rejectLegacyStrategyParams(w, req.Strategies) || rejectLegacySSHHosts(w, req.SSHHosts) {
			return
		}

		cleanPrompt, ok := validateInspectionFreeText(req.Prompt, maxInspectionPromptBytes)
		if !ok {
			writeMonitorHTTPError(w, 400, "invalid_prompt", "巡检提示词过长（上限 8 KiB）或包含非法字符")
			return
		}
		cleanCommand, ok := validateInspectionFreeText(req.Command, maxInspectionCommandBytes)
		if !ok {
			writeMonitorHTTPError(w, 400, "invalid_command", "巡检命令过长（上限 4 KiB）或包含非法字符")
			return
		}

		plan := monitorRunPlan{
			TriggerKind: "manual",
			TriggeredBy: auth.UserID,
			Auth:        auth,
			ScopeMode:   req.ScopeMode,
			Targets:     req.Targets,
			Tags:        req.Tags,
			Skills:      normalizeSkillSlugs(req.Skills),
			Prompt:      cleanPrompt,
			Command:     cleanCommand,
			Thresholds:  req.Thresholds,
		}

		if strings.TrimSpace(req.RetestRunID) != "" {
			prevRun, prevItems, err := h.store.GetMonitorRunDetail(r.Context(), auth, strings.TrimSpace(req.RetestRunID))
			if err != nil {
				writeMonitorHTTPError(w, 404, "retest_run_not_found", "待复检的历史运行记录不存在")
				return
			}
			retestMap := map[string][]api.MonitorInspectAsset{}
			for _, it := range prevItems {
				if it.Status == "warning" || it.Status == "critical" || it.Status == "error" || it.Status == "offline" {
					k := tunnelKey(it.Cluster, it.Instance)
					retestMap[k] = append(retestMap[k], api.MonitorInspectAsset{
						AssetID:   it.AssetID,
						AssetName: it.AssetName,
						AssetKind: it.AssetKind,
						Strategy:  it.Strategy,
					})
				}
			}
			if len(retestMap) == 0 {
				writeMonitorHTTPError(w, 409, "no_failed_items_to_retest", "该次执行没有异常或离线项需要复检")
				return
			}
			plan.TriggerKind = "retest"
			plan.ScheduleID = prevRun.ScheduleID
			plan.ScheduleName = prevRun.ScheduleName
			plan.RetestItems = retestMap
		} else if strings.TrimSpace(req.ScheduleID) != "" {
			sched, err := h.store.GetMonitorSchedule(r.Context(), strings.TrimSpace(req.ScheduleID))
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeMonitorHTTPError(w, 404, "schedule_not_found", "定时巡检计划不存在")
					return
				}
				writeMonitorHTTPError(w, 503, "schedule_load_failed", "读取计划失败")
				return
			}
			plan.ScheduleID = sched.ID
			plan.ScheduleName = sched.Name
			plan.ScopeMode = sched.ScopeMode
			plan.Targets = sched.Targets
			plan.Tags = sched.Tags
			plan.Skills = sched.Skills
			plan.Prompt = sched.Prompt
			plan.Command = sched.Command
			plan.Thresholds = sched.Thresholds
		} else {
			if plan.ScopeMode == "" {
				if len(plan.Targets) > 0 {
					plan.ScopeMode = "targets"
				} else if len(plan.Tags) > 0 {
					plan.ScopeMode = "tags"
				} else {
					plan.ScopeMode = "all_online"
				}
			}
			for _, t := range plan.Targets {
				if !monitorID(t.Cluster) || !monitorID(t.Instance) {
					writeMonitorHTTPError(w, 400, "invalid_target", "目标集群/实例格式无效")
					return
				}
				if !h.store.UserCanUseTarget(auth.UserID, t.Cluster, t.Instance) {
					writeMonitorHTTPError(w, 403, "monitor_target_forbidden", "当前账号无权巡检所选目标")
					return
				}
			}
		}

		run, items, err := h.ExecuteMonitorRun(r.Context(), plan)
		if err != nil {
			writeMonitorHTTPError(w, 503, "monitor_run_failed", "巡检任务下发或持久化失败")
			return
		}
		writeJSONHTTP(w, map[string]any{"run": run, "items": items})
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

// HandleMonitorMetrics serves the read-only inspection metric time series
// (SKILL_FIRST_INSPECTION_SPEC.md §4.2). Metrics are projected into
// inspection_metric_points at persistence time; this endpoint only reads them.
func (h *GatewayHub) HandleMonitorMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	skill := strings.ToLower(strings.TrimSpace(q.Get("skill")))
	if !skillSlugPattern.MatchString(skill) {
		writeMonitorHTTPError(w, 400, "invalid_monitor_metrics_query", "skill 参数无效")
		return
	}
	cluster, instance, ok := strings.Cut(strings.TrimSpace(q.Get("target")), "/")
	cluster = strings.TrimSpace(cluster)
	instance = strings.TrimSpace(instance)
	if !ok || cluster == "" || instance == "" || !monitorID(cluster) || !monitorID(instance) {
		writeMonitorHTTPError(w, 400, "invalid_monitor_metrics_query", "target 参数需为 cluster/instance")
		return
	}
	rangeKey, window, ok := monitorMetricRangeDuration(q.Get("range"))
	if !ok {
		writeMonitorHTTPError(w, 400, "invalid_monitor_metrics_range", "时间范围仅支持 1h、6h、24h、7d、30d")
		return
	}
	if !h.store.UserIsPlatformAdmin(auth.UserID) && !h.store.UserCanUseTarget(auth.UserID, cluster, instance) {
		writeMonitorHTTPError(w, 403, "monitor_metrics_forbidden", "无权查看该目标的指标时序")
		return
	}
	now := time.Now().UTC()
	toMs := now.UnixMilli()
	fromMs := now.Add(-window).UnixMilli()
	series, err := h.store.ListInspectionMetricSeries(r.Context(), cluster, instance, skill, fromMs, toMs)
	if err != nil {
		writeMonitorHTTPError(w, 503, "monitor_metrics_unavailable", "无法读取巡检指标时序")
		return
	}
	writeJSONHTTP(w, monitorMetricSeriesResponse{
		Skill:    skill,
		Cluster:  cluster,
		Instance: instance,
		Range:    rangeKey,
		FromMs:   fromMs,
		ToMs:     toMs,
		Series:   series,
	})
}

func writeMonitorHTTPError(w http.ResponseWriter, status int, code, message string) {
	writeDoopsError(w, status, "", doopsErrorResponse{Error: code, Code: code, Message: message})
}

// rejectLegacyStrategyParams refuses the retired native strategy selector.
// Inspection capability comes only from Skill packages
// (SKILL_FIRST_INSPECTION_SPEC.md 1.1), so the parameter must fail loudly
// instead of being silently ignored by an older client.
func rejectLegacyStrategyParams(w http.ResponseWriter, strategies []string) bool {
	if len(strategies) == 0 {
		return false
	}
	writeMonitorHTTPError(w, 400, "strategies_removed", "strategies 已移除，请改用 skill/actions：巡检能力由 Skill 包提供")
	return true
}

// rejectLegacySSHHosts refuses the retired inline SSH host list; SSH inspection
// is delivered by the inspect-ssh-host Skill package now.
func rejectLegacySSHHosts(w http.ResponseWriter, hosts []api.MonitorInspectAsset) bool {
	if len(hosts) == 0 {
		return false
	}
	writeMonitorHTTPError(w, 400, "ssh_hosts_removed", "ssh_hosts 已移除，请改用 inspect-ssh-host Skill 包")
	return true
}
