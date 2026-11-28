package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

type pluginAdminMutationRequest struct {
	Action           string `json:"action"`
	ScenarioID       string `json:"scenario_id,omitempty"`
	PluginID         string `json:"plugin_id,omitempty"`
	Enabled          *bool  `json:"enabled,omitempty"`
	ExpectedRevision *int64 `json:"expected_revision"`
	DryRun           bool   `json:"dry_run,omitempty"`
}

func writePluginHTTPError(w http.ResponseWriter, status int, code, message string, extra map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	body := map[string]any{
		"error":   code,
		"code":    code,
		"message": message,
	}
	for k, v := range extra {
		body[k] = v
	}
	_ = json.NewEncoder(w).Encode(body)
}

// HandlePlugins serves GET /v1/plugins for any authenticated user.
// It returns the resolved effective plugin state, scenario catalog, and
// advisory dependency readiness, with ETag / If-None-Match 304 support.
func (h *GatewayHub) HandlePlugins(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.store == nil {
		writePluginHTTPError(w, http.StatusServiceUnavailable, "store_unavailable", "Gateway 存储暂不可用", nil)
		return
	}

	ctx := r.Context()
	isAdmin := h.store.UserIsPlatformAdmin(auth.UserID)

	cfg, overrides, err := h.store.GetPluginConfig(ctx)
	if err != nil {
		writePluginHTTPError(w, http.StatusInternalServerError, "plugin_config_read_failed", "读取插件配置失败", nil)
		return
	}
	skills, err := h.store.ListPlatformSkills(ctx, auth, "", "", false)
	if err != nil {
		skills = nil
	}
	mcps, err := h.store.ListPlatformMCPServers(ctx, auth)
	if err != nil {
		mcps = nil
	}

	resp := buildPluginStateResponse(cfg, overrides, skills, mcps, auth.UserID, isAdmin)
	etag := computePluginStateETag(resp, isAdmin)
	w.Header().Set("ETag", etag)
	if inm := strings.TrimSpace(r.Header.Get("If-None-Match")); inm != "" && etagMatches(inm, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func etagMatches(headerVal, current string) bool {
	for _, part := range strings.Split(headerVal, ",") {
		candidate := strings.TrimSpace(part)
		candidate = strings.TrimPrefix(candidate, "W/")
		if candidate == "*" || candidate == current {
			return true
		}
	}
	return false
}

// HandleAdminPlugins serves POST /v1/admin/plugins under auditAdminMutation.
// Only platform administrators can mutate scenario/plugin state; all writes
// require expected_revision (optimistic locking) and are durably audited.
func (h *GatewayHub) HandleAdminPlugins(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	auth := internalAuditAuthFromContext(r.Context())
	if auth.UserID == "" {
		var err error
		auth, err = h.authenticateUser(r)
		if err != nil {
			h.writeUserAuthError(w, r, err)
			return
		}
	}
	if h.store == nil || !h.store.UserIsPlatformAdmin(auth.UserID) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64*1024))
	if err != nil {
		writePluginHTTPError(w, http.StatusBadRequest, "invalid_plugin_request", "请求体无效或超出大小限制", nil)
		return
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var req pluginAdminMutationRequest
	if err := dec.Decode(&req); err != nil {
		writePluginHTTPError(w, http.StatusBadRequest, "invalid_plugin_request", "插件配置请求格式不合法或包含未知字段", nil)
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writePluginHTTPError(w, http.StatusBadRequest, "invalid_plugin_request", "请求体仅允许单个 JSON 对象", nil)
		return
	}

	req.Action = strings.TrimSpace(req.Action)
	req.ScenarioID = strings.TrimSpace(req.ScenarioID)
	req.PluginID = strings.TrimSpace(req.PluginID)

	if !pluginActionPattern.MatchString(req.Action) {
		writePluginHTTPError(w, http.StatusBadRequest, "invalid_plugin_request", "不支持的插件操作类型", nil)
		return
	}
	if req.ExpectedRevision == nil || *req.ExpectedRevision < 0 {
		writePluginHTTPError(w, http.StatusBadRequest, "invalid_plugin_request", "缺少合法的 expected_revision 版本号", nil)
		return
	}

	params := pluginMutationParams{
		Action:           req.Action,
		ScenarioID:       req.ScenarioID,
		PluginID:         req.PluginID,
		ExpectedRevision: *req.ExpectedRevision,
		DryRun:           req.DryRun,
		Actor:            auth.UserID,
	}

	switch req.Action {
	case "apply_scenario":
		if !pluginScenarioPattern.MatchString(req.ScenarioID) {
			writePluginHTTPError(w, http.StatusBadRequest, "unknown_scenario", "未找到指定的场景包", nil)
			return
		}
		if _, exists := findPluginScenario(req.ScenarioID); !exists {
			writePluginHTTPError(w, http.StatusBadRequest, "unknown_scenario", "未找到指定的场景包", nil)
			return
		}
	case "set_plugin":
		if req.Enabled == nil {
			writePluginHTTPError(w, http.StatusBadRequest, "invalid_plugin_request", "缺少 enabled 开关字段", nil)
			return
		}
		if isPluginCoreEntryID(req.PluginID) || (!builtinPluginIDPattern.MatchString(req.PluginID) && !skillPluginIDPattern.MatchString(req.PluginID)) {
			writePluginHTTPError(w, http.StatusBadRequest, "invalid_plugin_id", "该插件标识不可关闭或格式不合法", nil)
			return
		}
		if builtinPluginIDPattern.MatchString(req.PluginID) {
			if _, exists := findBuiltinPlugin(req.PluginID); !exists {
				writePluginHTTPError(w, http.StatusBadRequest, "invalid_plugin_id", "未找到指定的内置插件", nil)
				return
			}
		} else if skillPluginIDPattern.MatchString(req.PluginID) {
			slug := strings.TrimPrefix(req.PluginID, "skill.")
			sk, skErr := h.store.GetPlatformSkill(r.Context(), TokenAuth{UserID: "system"}, slug)
			if skErr != nil || sk.Scope != "system" || decodeStoredSkillUIManifest(sk.UIManifestJSON) == nil || strings.TrimSpace(sk.UIManifestError) != "" {
				writePluginHTTPError(w, http.StatusBadRequest, "invalid_plugin_id", "仅支持对已存在且带有有效 plugin.yaml 的系统 Skill 插件进行平台级开关", nil)
				return
			}
		}
		params.Enabled = *req.Enabled
	case "clear_override":
		if req.PluginID == "" || isPluginCoreEntryID(req.PluginID) || (!builtinPluginIDPattern.MatchString(req.PluginID) && !skillPluginIDPattern.MatchString(req.PluginID)) {
			writePluginHTTPError(w, http.StatusBadRequest, "invalid_plugin_id", "无效的插件覆盖标识", nil)
			return
		}
	case "reset":
		// no extra fields required
	}

	result, err := h.store.MutatePluginConfig(r.Context(), params)
	if err != nil {
		var conflict *pluginRevisionConflictError
		if errors.As(err, &conflict) {
			writePluginHTTPError(w, http.StatusConflict, "plugin_revision_conflict", "插件配置已被其他管理员修改，请刷新后重试", map[string]any{
				"current_revision": conflict.CurrentRevision,
			})
			return
		}
		switch err.Error() {
		case "unknown_scenario":
			writePluginHTTPError(w, http.StatusBadRequest, "unknown_scenario", "未找到指定的场景包", nil)
		case "invalid_plugin_id":
			writePluginHTTPError(w, http.StatusBadRequest, "invalid_plugin_id", "插件标识无效或不存在可清理的覆盖项", nil)
		default:
			writePluginHTTPError(w, http.StatusInternalServerError, "plugin_config_write_failed", "保存插件配置失败", nil)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(result)
}
