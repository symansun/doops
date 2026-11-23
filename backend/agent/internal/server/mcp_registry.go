package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type platformMCPServerRecord struct {
	Slug            string            `json:"slug"`
	Name            string            `json:"name"`
	Description     string            `json:"description"`
	Scope           string            `json:"scope"`     // system | custom
	Transport       string            `json:"transport"` // stdio | http | sse
	Command         string            `json:"command,omitempty"`
	Args            []string          `json:"args,omitempty"`
	URL             string            `json:"url,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	Enabled         bool              `json:"enabled"`
	AutoStart       bool              `json:"auto_start"`
	ToolTimeoutSecs int               `json:"tool_timeout_secs"`
	LastProbeStatus string            `json:"last_probe_status,omitempty"`
	LastProbeError  string            `json:"last_probe_error,omitempty"`
	LastProbeAtMs   int64             `json:"last_probe_at_ms,omitempty"`
	LastLatencyMs   int64             `json:"last_latency_ms,omitempty"`
	Tools           []mcpToolMeta     `json:"tools"`
	OwnerID         string            `json:"owner_id"`
	OwnerName       string            `json:"owner_name"`
	CreatedAtMs     int64             `json:"created_at_ms"`
	UpdatedAtMs     int64             `json:"updated_at_ms"`
	CanEdit         bool              `json:"can_edit"`
	CanDelete       bool              `json:"can_delete"`
}

var builtinSystemMCPServers = []platformMCPServerRecord{
	{
		Slug:            "doops",
		Name:            "DoOps 内置运维 MCP",
		Description:     "Agent 原生内置 MCP Server（stdio），提供 Kubernetes 只读诊断、Helm 发布、Nginx 网站运维与目标知识库读写工具。",
		Scope:           "system",
		Transport:       "stdio",
		Command:         "/app/doops-agent",
		Args:            []string{"mcp-stdio", "--domain", "doops"},
		Enabled:         true,
		AutoStart:       true,
		ToolTimeoutSecs: doopsMCPToolTimeoutSeconds,
		OwnerID:         "system",
		OwnerName:       "system",
	},
	{
		Slug:            "dosql",
		Name:            "DoSql 数据库查询 MCP",
		Description:     "Agent 原生内置数据库 MCP Server（stdio），提供受控 MySQL / PostgreSQL / MongoDB 只读查询与审计留痕工具。",
		Scope:           "system",
		Transport:       "stdio",
		Command:         "/app/doops-agent",
		Args:            []string{"mcp-stdio", "--domain", "dosql"},
		Enabled:         true,
		AutoStart:       true,
		ToolTimeoutSecs: doopsMCPToolTimeoutSeconds,
		OwnerID:         "system",
		OwnerName:       "system",
	},
}

func (s *GatewayStore) SeedBuiltinSystemMCPServers(ctx context.Context) error {
	nowMs := time.Now().UnixMilli()
	for _, spec := range builtinSystemMCPServers {
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM platform_mcp_servers WHERE slug=?`, spec.Slug).Scan(&exists); err == nil {
			continue
		}
		argsJSON, _ := json.Marshal(spec.Args)
		_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO platform_mcp_servers (
			slug, name, description, scope, transport, command, args_json, url, env_json, headers_json,
			enabled, auto_start, tool_timeout_secs, tools_json, owner_id, owner_name, created_at_ms, updated_at_ms
		) VALUES (?, ?, ?, 'system', ?, ?, ?, '', '{}', '{}', 1, 1, ?, '[]', 'system', 'system', ?, ?)`,
			spec.Slug, spec.Name, spec.Description, spec.Transport, spec.Command, string(argsJSON), spec.ToolTimeoutSecs, nowMs, nowMs,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func scanPlatformMCPServerRow(sc skillRowScanner, userID string, isAdmin bool) (platformMCPServerRecord, error) {
	var (
		rec        platformMCPServerRecord
		argsRaw    string
		envRaw     string
		headersRaw string
		toolsRaw   string
		enabledInt int
		autoStart  int
	)
	if err := sc.Scan(
		&rec.Slug, &rec.Name, &rec.Description, &rec.Scope, &rec.Transport, &rec.Command, &argsRaw, &rec.URL,
		&envRaw, &headersRaw, &enabledInt, &autoStart, &rec.ToolTimeoutSecs,
		&rec.LastProbeStatus, &rec.LastProbeError, &rec.LastProbeAtMs, &rec.LastLatencyMs, &toolsRaw,
		&rec.OwnerID, &rec.OwnerName, &rec.CreatedAtMs, &rec.UpdatedAtMs,
	); err != nil {
		return rec, err
	}
	rec.Enabled = enabledInt != 0
	rec.AutoStart = autoStart != 0
	rec.Args = []string{}
	rec.Tools = []mcpToolMeta{}
	_ = json.Unmarshal([]byte(argsRaw), &rec.Args)
	_ = json.Unmarshal([]byte(envRaw), &rec.Env)
	_ = json.Unmarshal([]byte(headersRaw), &rec.Headers)
	_ = json.Unmarshal([]byte(toolsRaw), &rec.Tools)
	if rec.Scope == "system" {
		rec.CanEdit = false
		rec.CanDelete = false
	} else {
		rec.CanEdit = isAdmin || (userID != "" && rec.OwnerID == userID)
		rec.CanDelete = rec.CanEdit
	}
	return rec, nil
}

func (s *GatewayStore) ListPlatformMCPServers(ctx context.Context, auth TokenAuth) ([]platformMCPServerRecord, error) {
	isAdmin := auth.UserID == "system" || (auth.UserID != "" && s.UserIsPlatformAdmin(auth.UserID))
	rows, err := s.readDB().QueryContext(ctx, `SELECT slug, name, description, scope, transport, command, args_json, url,
		env_json, headers_json, enabled, auto_start, tool_timeout_secs,
		last_probe_status, last_probe_error, last_probe_at_ms, last_latency_ms, tools_json,
		owner_id, owner_name, created_at_ms, updated_at_ms
		FROM platform_mcp_servers ORDER BY (scope='system') DESC, updated_at_ms DESC, slug ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []platformMCPServerRecord{}
	for rows.Next() {
		rec, err := scanPlatformMCPServerRow(rows, auth.UserID, isAdmin)
		if err != nil {
			return nil, err
		}
		if !isAdmin && rec.Scope != "system" && !rec.Enabled && rec.OwnerID != auth.UserID {
			continue
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *GatewayStore) GetPlatformMCPServer(ctx context.Context, auth TokenAuth, slug string) (platformMCPServerRecord, error) {
	isAdmin := auth.UserID == "system" || (auth.UserID != "" && s.UserIsPlatformAdmin(auth.UserID))
	row := s.readDB().QueryRowContext(ctx, `SELECT slug, name, description, scope, transport, command, args_json, url,
		env_json, headers_json, enabled, auto_start, tool_timeout_secs,
		last_probe_status, last_probe_error, last_probe_at_ms, last_latency_ms, tools_json,
		owner_id, owner_name, created_at_ms, updated_at_ms
		FROM platform_mcp_servers WHERE slug=?`, slug)
	rec, err := scanPlatformMCPServerRow(row, auth.UserID, isAdmin)
	if err != nil {
		return rec, err
	}
	if !isAdmin && rec.Scope != "system" && !rec.Enabled && rec.OwnerID != auth.UserID {
		return platformMCPServerRecord{}, sql.ErrNoRows
	}
	return rec, nil
}

func (s *GatewayStore) UpsertPlatformMCPServer(ctx context.Context, rec platformMCPServerRecord) (platformMCPServerRecord, error) {
	nowMs := time.Now().UnixMilli()
	if rec.CreatedAtMs <= 0 {
		rec.CreatedAtMs = nowMs
	}
	rec.UpdatedAtMs = nowMs
	argsJSON, _ := json.Marshal(rec.Args)
	envJSON, _ := json.Marshal(rec.Env)
	headersJSON, _ := json.Marshal(rec.Headers)
	enabledInt := 0
	if rec.Enabled {
		enabledInt = 1
	}
	autoInt := 0
	if rec.AutoStart {
		autoInt = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO platform_mcp_servers (
		slug, name, description, scope, transport, command, args_json, url, env_json, headers_json,
		enabled, auto_start, tool_timeout_secs, tools_json, owner_id, owner_name, created_at_ms, updated_at_ms
	) VALUES (?, ?, ?, 'custom', ?, ?, ?, ?, ?, ?, ?, ?, ?, '[]', ?, ?, ?, ?)
	ON CONFLICT(slug) DO UPDATE SET
		name=excluded.name,
		description=excluded.description,
		transport=excluded.transport,
		command=excluded.command,
		args_json=excluded.args_json,
		url=excluded.url,
		env_json=excluded.env_json,
		headers_json=excluded.headers_json,
		enabled=excluded.enabled,
		auto_start=excluded.auto_start,
		tool_timeout_secs=excluded.tool_timeout_secs,
		updated_at_ms=excluded.updated_at_ms`,
		rec.Slug, rec.Name, rec.Description, rec.Transport, rec.Command, string(argsJSON), rec.URL,
		string(envJSON), string(headersJSON), enabledInt, autoInt, rec.ToolTimeoutSecs,
		rec.OwnerID, rec.OwnerName, rec.CreatedAtMs, rec.UpdatedAtMs,
	)
	return rec, err
}

func (s *GatewayStore) TogglePlatformMCPServer(ctx context.Context, slug string, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	res, err := s.db.ExecContext(ctx, `UPDATE platform_mcp_servers SET enabled=?, updated_at_ms=? WHERE slug=? AND scope='custom'`, v, time.Now().UnixMilli(), slug)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *GatewayStore) DeletePlatformMCPServer(ctx context.Context, slug string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM platform_mcp_servers WHERE slug=? AND scope='custom'`, slug)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *GatewayStore) SavePlatformMCPProbeResult(ctx context.Context, res mcpInspectServerResult) {
	toolsJSON, _ := json.Marshal(res.Tools)
	_, _ = s.db.ExecContext(ctx, `UPDATE platform_mcp_servers
		SET last_probe_status=?, last_probe_error=?, last_probe_at_ms=?, last_latency_ms=?, tools_json=?
		WHERE slug=?`,
		res.Status, trimTail(sanitizeSecretText(res.Error), 300), time.Now().UnixMilli(), res.LatencyMs, string(toolsJSON), res.Name,
	)
}

func (r platformMCPServerRecord) toAgentEntry() agentMCPServerEntry {
	probed := make([]string, 0, len(r.Tools))
	for _, t := range r.Tools {
		if t.Name != "" {
			probed = append(probed, t.Name)
		}
	}
	return agentMCPServerEntry{
		Name:            r.Slug,
		Transport:       r.Transport,
		Command:         r.Command,
		Args:            r.Args,
		URL:             r.URL,
		Env:             r.Env,
		Headers:         r.Headers,
		AutoStart:       r.AutoStart,
		ToolTimeoutSecs: r.ToolTimeoutSecs,
		ProbedTools:     probed,
	}
}

type mcpMutationRequest struct {
	Action          string             `json:"action"` // upsert | toggle | delete | sync | probe
	Slug            string             `json:"slug,omitempty"`
	Name            string             `json:"name,omitempty"`
	Description     string             `json:"description,omitempty"`
	Transport       string             `json:"transport,omitempty"`
	Command         string             `json:"command,omitempty"`
	Args            []string           `json:"args,omitempty"`
	URL             string             `json:"url,omitempty"`
	Env             map[string]string  `json:"env,omitempty"`
	Headers         map[string]string  `json:"headers,omitempty"`
	Enabled         *bool              `json:"enabled,omitempty"`
	AutoStart       *bool              `json:"auto_start,omitempty"`
	ToolTimeoutSecs int                `json:"tool_timeout_secs,omitempty"`
	Targets         []monitorTargetRef `json:"targets,omitempty"`
	Target          *monitorTargetRef  `json:"target,omitempty"`
}

func writeMCPHTTPError(w http.ResponseWriter, status int, code, msg string) {
	writeDoopsError(w, status, "", doopsErrorResponse{Error: code, Code: code, Message: msg})
}

func (h *GatewayHub) HandlePlatformMCPServers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	subPath := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/mcp-servers"), "/")

	switch r.Method {
	case http.MethodGet:
		if subPath != "" {
			if !mcpSlugPattern.MatchString(subPath) {
				writeMCPHTTPError(w, 400, "invalid_mcp_slug", "MCP 标识仅支持小写字母、数字、下划线和连字符")
				return
			}
			rec, err := h.store.GetPlatformMCPServer(r.Context(), auth, subPath)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeMCPHTTPError(w, 404, "mcp_not_found", "MCP Server 不存在或无权查看")
					return
				}
				writeMCPHTTPError(w, 503, "mcp_store_unavailable", "无法读取 MCP Server 详情")
				return
			}
			writeJSONHTTP(w, map[string]any{"server": rec})
			return
		}
		servers, err := h.store.ListPlatformMCPServers(r.Context(), auth)
		if err != nil {
			writeMCPHTTPError(w, 503, "mcp_store_unavailable", "无法读取 MCP Server 列表")
			return
		}
		resp := map[string]any{"servers": servers}
		if rawTarget := strings.TrimSpace(r.URL.Query().Get("target")); rawTarget != "" {
			cluster, instance, ok := strings.Cut(rawTarget, "/")
			if !ok || !monitorID(cluster) || !monitorID(instance) {
				writeMCPHTTPError(w, 400, "invalid_target", "目标格式应为 cluster/instance")
				return
			}
			if !h.store.UserIsPlatformAdmin(auth.UserID) && !h.store.UserCanUseTarget(auth.UserID, cluster, instance) {
				writeMCPHTTPError(w, 403, "target_forbidden", "当前账号无权访问该目标")
				return
			}
			probe := r.URL.Query().Get("probe") == "1" || r.URL.Query().Get("probe") == "true"
			live, liveErr := h.inspectAgentMCPServers(r.Context(), auth, monitorTargetRef{Cluster: cluster, Instance: instance}, "", probe, nil)
			if liveErr != nil {
				resp["agent_error"] = trimTail(sanitizeSecretText(liveErr.Error()), 240)
			} else {
				resp["agent_servers"] = live
				if probe {
					for _, item := range live {
						h.store.SavePlatformMCPProbeResult(r.Context(), item)
					}
					if refreshed, err := h.store.ListPlatformMCPServers(r.Context(), auth); err == nil {
						resp["servers"] = refreshed
					}
				}
			}
		}
		writeJSONHTTP(w, resp)

	case http.MethodPost:
		if subPath != "" {
			writeMCPHTTPError(w, 405, "method_not_allowed", "不支持对该路径 POST")
			return
		}
		var req mcpMutationRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024))
		dec.DisallowUnknownFields()
		if dec.Decode(&req) != nil || dec.Decode(new(any)) != io.EOF {
			writeMCPHTTPError(w, 400, "invalid_mcp_json", "MCP 请求格式无效")
			return
		}
		res, code, msg, status := h.applyPlatformMCPMutation(r.Context(), auth, req)
		if status != http.StatusOK {
			writeMCPHTTPError(w, status, code, msg)
			return
		}
		writeJSONHTTP(w, res)

	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func (h *GatewayHub) applyPlatformMCPMutation(ctx context.Context, auth TokenAuth, req mcpMutationRequest) (any, string, string, int) {
	isAdmin := h.store.UserIsPlatformAdmin(auth.UserID)
	slug := strings.ToLower(strings.TrimSpace(req.Slug))
	auditID, _ := h.store.StartAudit(AuditEvent{
		UserID:         auth.UserID,
		TokenID:        auth.TokenID,
		Action:         GatewayAction("mcp:" + req.Action),
		Session:        "mcp-registry",
		CommandSummary: fmt.Sprintf("action=%s slug=%s", req.Action, slug),
	})
	finish := func(errCode string) {
		if auditID <= 0 {
			return
		}
		if errCode == "" {
			_ = h.store.FinishAudit(auditID, AuditFinish{Status: "success"})
		} else {
			_ = h.store.FinishAudit(auditID, AuditFinish{Status: "error", Error: errCode})
		}
	}

	switch req.Action {
	case "upsert":
		if !mcpSlugPattern.MatchString(slug) {
			finish("invalid_mcp_slug")
			return nil, "invalid_mcp_slug", "MCP 标识须为小写字母、数字、下划线或连字符（1–48 位）", 400
		}
		if slug == "doops" || slug == "dosql" {
			finish("builtin_mcp_immutable")
			return nil, "builtin_mcp_immutable", "内置系统 MCP（doops / dosql）由 Agent 镜像托管，不可覆盖", 409
		}
		existing, existErr := h.store.GetPlatformMCPServer(ctx, TokenAuth{UserID: "system"}, slug)
		if existErr == nil {
			if existing.Scope == "system" {
				finish("builtin_mcp_immutable")
				return nil, "builtin_mcp_immutable", "内置系统 MCP 不可修改", 409
			}
			if !isAdmin && existing.OwnerID != auth.UserID {
				finish("mcp_forbidden")
				return nil, "mcp_forbidden", "仅创建者或平台管理员可编辑该 MCP Server", 403
			}
		}
		entry := agentMCPServerEntry{
			Name:            slug,
			Transport:       req.Transport,
			Command:         req.Command,
			Args:            req.Args,
			URL:             req.URL,
			Env:             req.Env,
			Headers:         req.Headers,
			AutoStart:       req.AutoStart == nil || *req.AutoStart,
			ToolTimeoutSecs: req.ToolTimeoutSecs,
		}
		if err := validateMCPServerEntry(&entry); err != nil {
			finish("invalid_mcp_config")
			return nil, "invalid_mcp_config", err.Error(), 400
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			name = slug
		}
		if len(name) > 128 {
			name = truncateUTF8Head(name, 128)
		}
		desc := strings.TrimSpace(req.Description)
		if len(desc) > 512 {
			desc = truncateUTF8Head(desc, 512)
		}
		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		ownerID := auth.UserID
		ownerName := auth.UserID
		var dbUserName string
		if h.store.readDB().QueryRowContext(ctx, `SELECT name FROM users WHERE id=?`, auth.UserID).Scan(&dbUserName) == nil && strings.TrimSpace(dbUserName) != "" {
			ownerName = strings.TrimSpace(dbUserName)
		}
		if existErr == nil && existing.OwnerID != "" {
			ownerID = existing.OwnerID
			ownerName = existing.OwnerName
		}
		rec := platformMCPServerRecord{
			Slug:            slug,
			Name:            name,
			Description:     desc,
			Scope:           "custom",
			Transport:       entry.Transport,
			Command:         entry.Command,
			Args:            entry.Args,
			URL:             entry.URL,
			Env:             entry.Env,
			Headers:         entry.Headers,
			Enabled:         enabled,
			AutoStart:       entry.AutoStart,
			ToolTimeoutSecs: entry.ToolTimeoutSecs,
			OwnerID:         ownerID,
			OwnerName:       ownerName,
		}
		if _, err := h.store.UpsertPlatformMCPServer(ctx, rec); err != nil {
			finish("mcp_save_failed")
			return nil, "mcp_save_failed", "保存 MCP Server 配置失败", 503
		}
		saved, _ := h.store.GetPlatformMCPServer(ctx, auth, slug)
		finish("")
		return map[string]any{"server": saved}, "", "", 200

	case "toggle":
		if !mcpSlugPattern.MatchString(slug) || req.Enabled == nil {
			finish("invalid_mcp_toggle")
			return nil, "invalid_mcp_toggle", "缺少 MCP 标识或启用状态", 400
		}
		existing, err := h.store.GetPlatformMCPServer(ctx, TokenAuth{UserID: "system"}, slug)
		if err != nil {
			finish("mcp_not_found")
			return nil, "mcp_not_found", "MCP Server 不存在", 404
		}
		if existing.Scope == "system" {
			finish("builtin_mcp_immutable")
			return nil, "builtin_mcp_immutable", "内置系统 MCP 不可停用", 409
		}
		if !isAdmin && existing.OwnerID != auth.UserID {
			finish("mcp_forbidden")
			return nil, "mcp_forbidden", "仅创建者或平台管理员可启停该 MCP Server", 403
		}
		if err := h.store.TogglePlatformMCPServer(ctx, slug, *req.Enabled); err != nil {
			finish("mcp_toggle_failed")
			return nil, "mcp_toggle_failed", "切换 MCP 状态失败", 503
		}
		updated, _ := h.store.GetPlatformMCPServer(ctx, auth, slug)
		finish("")
		return map[string]any{"server": updated}, "", "", 200

	case "delete":
		if !mcpSlugPattern.MatchString(slug) {
			finish("invalid_mcp_slug")
			return nil, "invalid_mcp_slug", "缺少 MCP 标识", 400
		}
		existing, err := h.store.GetPlatformMCPServer(ctx, TokenAuth{UserID: "system"}, slug)
		if err != nil {
			finish("mcp_not_found")
			return nil, "mcp_not_found", "MCP Server 不存在", 404
		}
		if existing.Scope == "system" {
			finish("builtin_mcp_immutable")
			return nil, "builtin_mcp_immutable", "内置系统 MCP 不可删除", 409
		}
		if !isAdmin && existing.OwnerID != auth.UserID {
			finish("mcp_forbidden")
			return nil, "mcp_forbidden", "仅创建者或平台管理员可删除该 MCP Server", 403
		}
		if err := h.store.DeletePlatformMCPServer(ctx, slug); err != nil {
			finish("mcp_delete_failed")
			return nil, "mcp_delete_failed", "删除 MCP Server 失败", 503
		}
		finish("")
		return map[string]any{"deleted": slug}, "", "", 200

	case "sync":
		results, syncErr := h.syncMCPServersToTargets(ctx, auth, req.Targets)
		if syncErr != nil {
			finish("mcp_sync_failed")
			return nil, "mcp_sync_failed", syncErr.Error(), 400
		}
		finish("")
		return map[string]any{"results": results}, "", "", 200

	case "probe":
		var target monitorTargetRef
		if req.Target != nil && monitorID(req.Target.Cluster) && monitorID(req.Target.Instance) {
			target = *req.Target
		} else {
			online := h.ListTargets()
			for _, t := range online {
				if isAdmin || h.store.UserCanUseTarget(auth.UserID, t.Cluster, t.Instance) {
					target = monitorTargetRef{Cluster: t.Cluster, Instance: t.Instance}
					break
				}
			}
		}
		if target.Cluster == "" || target.Instance == "" {
			finish("no_online_target")
			return nil, "no_online_target", "当前没有可用于探测的在线 Agent 目标", 400
		}
		if !isAdmin && !h.store.UserCanUseTarget(auth.UserID, target.Cluster, target.Instance) {
			finish("target_forbidden")
			return nil, "target_forbidden", "当前账号无权探测该目标", 403
		}
		var adHoc *agentMCPServerEntry
		probeName := slug
		if slug != "" {
			rec, err := h.store.GetPlatformMCPServer(ctx, auth, slug)
			if err != nil {
				finish("mcp_not_found")
				return nil, "mcp_not_found", "待探测的 MCP Server 不存在", 404
			}
			if rec.Scope == "custom" {
				e := rec.toAgentEntry()
				adHoc = &e
				probeName = ""
			}
		}
		live, probeErr := h.inspectAgentMCPServers(ctx, auth, target, probeName, true, adHoc)
		if probeErr != nil {
			finish("mcp_probe_failed")
			return nil, "mcp_probe_failed", "目标 MCP 探测失败：" + trimTail(sanitizeSecretText(probeErr.Error()), 200), 503
		}
		for _, item := range live {
			h.store.SavePlatformMCPProbeResult(ctx, item)
		}
		servers, _ := h.store.ListPlatformMCPServers(ctx, auth)
		finish("")
		return map[string]any{
			"target":        target,
			"probe_results": live,
			"servers":       servers,
		}, "", "", 200

	default:
		finish("invalid_mcp_action")
		return nil, "invalid_mcp_action", "不支持的操作类型", 400
	}
}

type mcpSyncTargetResult struct {
	Cluster      string                   `json:"cluster"`
	Instance     string                   `json:"instance"`
	Status       string                   `json:"status"` // ok | error | offline
	SyncedCount  int                      `json:"synced_count"`
	TotalServers int                      `json:"total_servers"`
	ProbeResults []mcpInspectServerResult `json:"probe_results,omitempty"`
	Error        string                   `json:"error,omitempty"`
}

func (h *GatewayHub) syncMCPServersToTargets(ctx context.Context, auth TokenAuth, requested []monitorTargetRef) ([]mcpSyncTargetResult, error) {
	isAdmin := h.store.UserIsPlatformAdmin(auth.UserID)
	allServers, err := h.store.ListPlatformMCPServers(ctx, TokenAuth{UserID: "system"})
	if err != nil {
		return nil, err
	}
	customEntries := make([]agentMCPServerEntry, 0)
	for _, s := range allServers {
		if s.Scope == "custom" && s.Enabled {
			customEntries = append(customEntries, s.toAgentEntry())
		}
	}

	targets := make([]monitorTargetRef, 0)
	if len(requested) > 0 {
		for _, t := range requested {
			if !monitorID(t.Cluster) || !monitorID(t.Instance) {
				return nil, fmt.Errorf("invalid target %s/%s", t.Cluster, t.Instance)
			}
			if !isAdmin && !h.store.UserCanUseTarget(auth.UserID, t.Cluster, t.Instance) {
				return nil, fmt.Errorf("forbidden target %s/%s", t.Cluster, t.Instance)
			}
			targets = append(targets, t)
		}
	} else {
		for _, t := range h.ListTargets() {
			if isAdmin || h.store.UserCanUseTarget(auth.UserID, t.Cluster, t.Instance) {
				targets = append(targets, monitorTargetRef{Cluster: t.Cluster, Instance: t.Instance})
			}
		}
	}

	results := make([]mcpSyncTargetResult, 0, len(targets))
	for _, tgt := range targets {
		if h.getAgent(tgt.Cluster, tgt.Instance) == nil {
			results = append(results, mcpSyncTargetResult{Cluster: tgt.Cluster, Instance: tgt.Instance, Status: "offline", Error: "target_offline"})
			continue
		}
		callCtx, cancel := context.WithTimeout(contextWithInternalAuditAuth(ctx, auth), 45*time.Second)
		rawOut, callErr := h.RunInternalToolCall(callCtx, tgt.Cluster, tgt.Instance, mcpSyncTool, map[string]any{
			"session_id": "mcp-sync-" + randomHex(4),
			"servers":    customEntries,
			"probe":      len(customEntries) > 0,
		})
		cancel()
		if callErr != nil {
			results = append(results, mcpSyncTargetResult{
				Cluster: tgt.Cluster, Instance: tgt.Instance, Status: "error",
				Error: trimTail(sanitizeSecretText(callErr.Error()), 200),
			})
			continue
		}
		var parsed struct {
			SyncedCount  int                      `json:"synced_count"`
			TotalServers int                      `json:"total_servers"`
			ProbeResults []mcpInspectServerResult `json:"probe_results"`
		}
		_ = json.Unmarshal([]byte(strings.TrimSpace(rawOut)), &parsed)
		for _, pr := range parsed.ProbeResults {
			h.store.SavePlatformMCPProbeResult(ctx, pr)
		}
		results = append(results, mcpSyncTargetResult{
			Cluster:      tgt.Cluster,
			Instance:     tgt.Instance,
			Status:       "ok",
			SyncedCount:  parsed.SyncedCount,
			TotalServers: parsed.TotalServers,
			ProbeResults: parsed.ProbeResults,
		})
	}
	return results, nil
}

func (h *GatewayHub) inspectAgentMCPServers(
	ctx context.Context,
	auth TokenAuth,
	target monitorTargetRef,
	serverName string,
	probe bool,
	adHoc *agentMCPServerEntry,
) ([]mcpInspectServerResult, error) {
	if h.getAgent(target.Cluster, target.Instance) == nil {
		return nil, fmt.Errorf("target_offline")
	}
	callCtx, cancel := context.WithTimeout(contextWithInternalAuditAuth(ctx, auth), 30*time.Second)
	defer cancel()
	args := map[string]any{
		"session_id": "mcp-inspect-" + randomHex(4),
		"probe":      probe,
	}
	if serverName != "" {
		args["server_name"] = serverName
	}
	if adHoc != nil {
		args["ad_hoc"] = adHoc
	}
	rawOut, err := h.RunInternalToolCall(callCtx, target.Cluster, target.Instance, mcpInspectTool, args)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Servers []mcpInspectServerResult `json:"servers"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(rawOut)), &parsed); err != nil {
		return nil, fmt.Errorf("invalid_mcp_inspect_response")
	}
	return parsed.Servers, nil
}
