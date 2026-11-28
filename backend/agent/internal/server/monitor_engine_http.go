package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	Inputs          map[string]any               `json:"inputs,omitempty"`
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
		if unknown := h.unknownMonitorSkills(r.Context(), auth, req.Skills); len(unknown) > 0 {
			return nil, "unknown_skill", unknownSkillMessage(unknown), 400
		}
		if custom := h.nonSystemMonitorSkills(r.Context(), auth, req.Skills); len(custom) > 0 {
			return nil, "custom_skill_schedule_forbidden", "定时巡检计划仅支持系统 Skill，不可绑定自定义 Skill：" + strings.Join(custom, "、"), 400
		}
		skills := normalizeSkillSlugs(req.Skills)
		var cleanInputs map[string]any
		if len(req.Inputs) > 0 {
			if len(skills) != 1 {
				return nil, "inputs_require_single_skill", "传入插件参数 inputs 时必须且仅能指定 1 个系统 Skill", 400
			}
			sk, skErr := h.store.GetPlatformSkill(r.Context(), auth, skills[0])
			if skErr != nil {
				return nil, "unknown_skill", unknownSkillMessage(skills), 400
			}
			_, validatedInputs, errCode, errMsg, status := validatePluginRunInputs(
				r.Context(), h.store, auth, sk, req.Inputs, cleanTargets, scopeMode != "targets",
			)
			if status != 0 {
				return nil, errCode, errMsg, status
			}
			cleanInputs = validatedInputs
		}
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
			Inputs:          cleanInputs,
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
	Inputs      map[string]any               `json:"inputs,omitempty"`
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
		cluster := strings.TrimSpace(q.Get("cluster"))
		instance := strings.TrimSpace(q.Get("instance"))
		if (cluster != "") != (instance != "") || (cluster != "" && (!monitorID(cluster) || !monitorID(instance))) {
			writeMonitorHTTPError(w, 400, "invalid_monitor_target", "按目标筛选执行记录须同时提供有效的 cluster 与 instance")
			return
		}
		if cluster != "" && !h.store.UserIsPlatformAdmin(auth.UserID) && !h.store.UserCanUseTarget(auth.UserID, cluster, instance) {
			writeMonitorHTTPError(w, 403, "monitor_target_forbidden", "当前账号无权查看该目标的执行记录")
			return
		}
		runs, err := h.store.ListMonitorRunsFiltered(r.Context(), auth, monitorRunListFilter{
			ScheduleID: strings.TrimSpace(q.Get("schedule_id")),
			Status:     strings.TrimSpace(q.Get("status")),
			Cluster:    cluster,
			Instance:   instance,
			Limit:      limit,
		})
		if err != nil {
			writeMonitorHTTPError(w, 503, "monitor_runs_unavailable", "无法读取巡检执行记录列表")
			return
		}
		resp := map[string]any{"runs": runs}
		if cluster != "" {
			resp["target_filtered"] = true
		}
		writeJSONHTTP(w, resp)
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
		// An explicit skill selection is a promise about what will run: refuse a slug
		// the registry does not know or has disabled instead of starting a run that can
		// only report a vacuous pass (monitorEmptyRunReason in the runner).
		if h.rejectUnknownSkills(w, r, auth, req.Skills) {
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
			plan.RawInputs = sched.Inputs
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

		// PLG-02: strictly validate L2 plugin inputs on single-skill runs before dispatch.
		if len(req.Inputs) > 0 {
			if len(plan.Skills) != 1 || strings.TrimSpace(req.RetestRunID) != "" {
				writeMonitorHTTPError(w, 400, "inputs_require_single_skill", "传入插件参数 inputs 时必须且仅能指定 1 个 Skill")
				return
			}
			sk, skErr := h.store.GetPlatformSkill(r.Context(), auth, plan.Skills[0])
			if skErr != nil || !skillAllowedForMonitorRun(sk, auth, plan.TriggerKind, plan.ScheduleID != "") {
				writeMonitorHTTPError(w, 400, "unknown_skill", unknownSkillMessage(plan.Skills))
				return
			}
			resolvedTargets := h.resolveMonitorPlanTargets(r.Context(), plan)
			envInputs, _, errCode, errMsg, status := validatePluginRunInputs(
				r.Context(), h.store, auth, sk, req.Inputs, resolvedTargets, false,
			)
			if status != 0 {
				writeMonitorHTTPError(w, status, errCode, errMsg)
				return
			}
			plan.Inputs = envInputs
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

// HandleMonitorMetricsLatest serves GET /v1/monitor/metrics/latest.
// Supports both pair-scoped batch queries (`?pairs=<skill>:<key>,...`, up to 64
// exact pairs per batch call for unbounded multi-batch home widget expansion)
// and backward-compatible `?skills=<csv>&keys=<csv>` queries (up to 24 skills /
// 96 keys per batch). Always enforces per-target authorization.
func (h *GatewayHub) HandleMonitorMetricsLatest(w http.ResponseWriter, r *http.Request) {
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
	rawPairs := strings.TrimSpace(q.Get("pairs"))
	rawSkills := strings.TrimSpace(q.Get("skills"))
	rawKeys := strings.TrimSpace(q.Get("keys"))

	isAdmin := h.store.UserIsPlatformAdmin(auth.UserID)
	allow := func(cluster, instance string) bool {
		if isAdmin {
			return true
		}
		return h.store.UserCanUseTarget(auth.UserID, cluster, instance)
	}

	if rawPairs != "" {
		pairs := make([]monitorLatestMetricPair, 0, maxLatestMonitorMetricPairs)
		seenPair := make(map[string]bool)
		for _, part := range strings.Split(rawPairs, ",") {
			token := strings.TrimSpace(part)
			if token == "" {
				continue
			}
			slugRaw, keyRaw, hasColon := strings.Cut(token, ":")
			slug := strings.ToLower(strings.TrimSpace(slugRaw))
			key := strings.TrimSpace(keyRaw)
			if !hasColon || !skillSlugPattern.MatchString(slug) || !monitorMetricKeyPattern.MatchString(key) {
				writeMonitorHTTPError(w, 400, "invalid_monitor_metrics_query", "pairs 参数必须采用 <skill>:<metric_key> 格式")
				return
			}
			dedup := slug + "\x00" + key
			if !seenPair[dedup] {
				seenPair[dedup] = true
				pairs = append(pairs, monitorLatestMetricPair{Skill: slug, Key: key})
			}
		}
		if len(pairs) == 0 || len(pairs) > maxLatestMonitorMetricPairs {
			writeMonitorHTTPError(w, 400, "invalid_monitor_metrics_query", fmt.Sprintf("pairs 参数单批须包含 1 至 %d 组指标对", maxLatestMonitorMetricPairs))
			return
		}
		items, err := h.store.ListLatestInspectionMetricPairs(r.Context(), pairs, allow)
		if err != nil {
			writeMonitorHTTPError(w, 503, "monitor_metrics_unavailable", "无法读取最新聚合指标")
			return
		}
		writeJSONHTTP(w, monitorLatestMetricsResponse{
			Items: items,
			Batch: &monitorLatestMetricsBatchMeta{
				RequestedPairs: len(pairs),
				ReturnedItems:  len(items),
			},
		})
		return
	}

	if rawSkills == "" || rawKeys == "" {
		writeMonitorHTTPError(w, 400, "invalid_monitor_metrics_query", "必须提供 pairs 或 skills 与 keys 查询参数")
		return
	}
	skills := make([]string, 0, maxLatestMonitorMetricSkills)
	seenSkill := make(map[string]bool)
	for _, part := range strings.Split(rawSkills, ",") {
		slug := strings.ToLower(strings.TrimSpace(part))
		if slug == "" {
			continue
		}
		if !skillSlugPattern.MatchString(slug) {
			writeMonitorHTTPError(w, 400, "invalid_monitor_metrics_query", "skills 参数包含非法 Skill 标识")
			return
		}
		if !seenSkill[slug] {
			seenSkill[slug] = true
			skills = append(skills, slug)
		}
	}
	if len(skills) == 0 || len(skills) > maxLatestMonitorMetricSkills {
		writeMonitorHTTPError(w, 400, "invalid_monitor_metrics_query", fmt.Sprintf("skills 参数单批须包含 1 至 %d 个 Skill 标识", maxLatestMonitorMetricSkills))
		return
	}

	keys := make([]string, 0, maxLatestMonitorMetricKeys)
	seenKey := make(map[string]bool)
	for _, part := range strings.Split(rawKeys, ",") {
		k := strings.TrimSpace(part)
		if k == "" {
			continue
		}
		if !monitorMetricKeyPattern.MatchString(k) {
			writeMonitorHTTPError(w, 400, "invalid_monitor_metrics_query", "keys 参数包含非法指标键")
			return
		}
		if !seenKey[k] {
			seenKey[k] = true
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 || len(keys) > maxLatestMonitorMetricKeys {
		writeMonitorHTTPError(w, 400, "invalid_monitor_metrics_query", fmt.Sprintf("keys 参数单批须包含 1 至 %d 个指标键", maxLatestMonitorMetricKeys))
		return
	}

	items, err := h.store.ListLatestInspectionMetrics(r.Context(), skills, keys, allow)
	if err != nil {
		writeMonitorHTTPError(w, 503, "monitor_metrics_unavailable", "无法读取最新聚合指标")
		return
	}
	writeJSONHTTP(w, monitorLatestMetricsResponse{
		Items: items,
		Batch: &monitorLatestMetricsBatchMeta{
			RequestedPairs: len(skills) * len(keys),
			ReturnedItems:  len(items),
		},
	})
}

// skillAllowedForMonitorRun enforces Skill execution trust boundaries:
// scheduled runs (and manual runs of a saved schedule) may execute only
// admin-controlled system Skills so a non-admin custom Skill owner cannot
// mutate an inspection script into fleet-wide scheduled RCE; ad-hoc manual runs
// may execute system Skills or custom Skills owned by the invoking user.
func skillAllowedForMonitorRun(sk platformSkillRecord, auth TokenAuth, triggerKind string, hasSchedule bool) bool {
	if !sk.Enabled {
		return false
	}
	if triggerKind == "scheduled" || hasSchedule {
		return sk.Scope == "system"
	}
	return skillOwnedOrSystem(sk, auth)
}

// nonSystemMonitorSkills returns the labels of requested skills that resolve to
// non-system (custom) packages, which are forbidden in fleet-wide schedules.
func (h *GatewayHub) nonSystemMonitorSkills(ctx context.Context, auth TokenAuth, requested []string) []string {
	if len(requested) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(requested))
	custom := make([]string, 0, len(requested))
	for _, raw := range requested {
		label := strings.TrimSpace(raw)
		slug := strings.ToLower(label)
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		if !skillSlugPattern.MatchString(slug) {
			continue
		}
		sk, err := h.store.GetPlatformSkill(ctx, auth, slug)
		if err == nil && sk.Scope != "system" {
			custom = append(custom, label)
		}
	}
	return custom
}

// unknownMonitorSkills returns the slugs of an explicit selection that the platform
// Skill registry does not know or has disabled (or is a custom Skill owned by
// another user), in request order, deduplicated by the same normalization the
// runner and the scheduler use (lowercase, trimmed).
// The returned labels keep the caller's spelling so the 400 can quote what was sent.
func (h *GatewayHub) unknownMonitorSkills(ctx context.Context, auth TokenAuth, requested []string) []string {
	if len(requested) == 0 {
		return nil
	}
	lookupAuth := auth
	if lookupAuth.UserID == "" {
		lookupAuth.UserID = "system"
	}
	seen := make(map[string]bool, len(requested))
	unknown := make([]string, 0, len(requested))
	for _, raw := range requested {
		label := strings.TrimSpace(raw)
		slug := strings.ToLower(label)
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		if !skillSlugPattern.MatchString(slug) {
			unknown = append(unknown, label)
			continue
		}
		// Read the same registry row the scheduler resolves at run time; a disabled
		// package or another user's custom package is not executable here.
		sk, err := h.store.GetPlatformSkill(ctx, lookupAuth, slug)
		if err != nil || !skillAllowedForMonitorRun(sk, auth, "manual", false) {
			unknown = append(unknown, label)
		}
	}
	return unknown
}

func unknownSkillMessage(unknown []string) string {
	return "存在未识别或已停用的 Skill：" + strings.Join(unknown, "、")
}

func (h *GatewayHub) rejectUnknownSkills(w http.ResponseWriter, r *http.Request, auth TokenAuth, requested []string) bool {
	unknown := h.unknownMonitorSkills(r.Context(), auth, requested)
	if len(unknown) == 0 {
		return false
	}
	writeMonitorHTTPError(w, 400, "unknown_skill", unknownSkillMessage(unknown))
	return true
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
