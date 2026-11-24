package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// HandleAdminSensitiveFilter serves the administrator-managed prompt and
// command filter. Every route is wrapped by auditAdminMutation in tunnel_hub.go.
func (h *GatewayHub) HandleAdminSensitiveFilter(w http.ResponseWriter, r *http.Request) {
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	if !h.store.UserIsPlatformAdmin(auth.UserID) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case path == "/v1/admin/sensitive-filter" && r.Method == http.MethodGet:
		view, err := h.store.ListSensitiveFilter()
		if err != nil {
			writeSensitiveFilterError(w, err)
			return
		}
		writeJSONHTTP(w, view)
	case path == "/v1/admin/sensitive-filter" && r.Method == http.MethodPatch:
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			writeSensitiveFilterError(w, errSensitiveInvalid)
			return
		}
		if body.Enabled == nil {
			writeSensitiveFilterError(w, errSensitiveInvalid)
			return
		}
		if err := h.store.SetSensitiveFilterEnabled(*body.Enabled); err != nil {
			writeSensitiveFilterError(w, err)
			return
		}
		writeJSONHTTP(w, map[string]interface{}{"enabled": *body.Enabled})
	case path == "/v1/admin/sensitive-filter/rules":
		h.handleAdminSensitiveRules(w, r)
	case path == "/v1/admin/sensitive-filter/whitelist":
		h.handleAdminSensitiveWhitelist(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *GatewayHub) handleAdminSensitiveRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Rules []SensitiveRuleInput `json:"rules"`
			Text  string               `json:"text"`
			Scope string               `json:"scope"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			writeSensitiveFilterError(w, errSensitiveInvalid)
			return
		}
		if len(body.Rules) == 0 && strings.TrimSpace(body.Text) != "" {
			body.Rules = []SensitiveRuleInput{{Text: body.Text, Scope: body.Scope}}
		}
		created, skipped, err := h.store.AddSensitiveRules(body.Rules)
		if err != nil {
			writeSensitiveFilterError(w, err)
			return
		}
		writeJSONHTTP(w, map[string]interface{}{"rules": created, "skipped": skipped})
	case http.MethodPatch:
		var body struct {
			ID      string `json:"id"`
			Enabled *bool  `json:"enabled"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			writeSensitiveFilterError(w, errSensitiveInvalid)
			return
		}
		if strings.TrimSpace(body.ID) == "" || body.Enabled == nil {
			writeSensitiveFilterError(w, errSensitiveInvalid)
			return
		}
		rule, err := h.store.SetSensitiveRuleEnabled(body.ID, *body.Enabled)
		if err != nil {
			writeSensitiveFilterError(w, err)
			return
		}
		writeJSONHTTP(w, rule)
	case http.MethodDelete:
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if id == "" {
			writeSensitiveFilterError(w, errSensitiveInvalid)
			return
		}
		if err := h.store.DeleteSensitiveRule(id); err != nil {
			writeSensitiveFilterError(w, err)
			return
		}
		writeJSONHTTP(w, map[string]interface{}{"ok": true})
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func (h *GatewayHub) handleAdminSensitiveWhitelist(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body SensitiveWhitelistInput
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			writeSensitiveFilterError(w, errSensitiveInvalid)
			return
		}
		entry, err := h.store.AddSensitiveWhitelist(body)
		if err != nil {
			writeSensitiveFilterError(w, err)
			return
		}
		writeJSONHTTP(w, entry)
	case http.MethodDelete:
		input := SensitiveWhitelistInput{
			Kind:     strings.TrimSpace(r.URL.Query().Get("kind")),
			UserID:   strings.TrimSpace(r.URL.Query().Get("user_id")),
			Cluster:  strings.TrimSpace(r.URL.Query().Get("cluster")),
			Instance: strings.TrimSpace(r.URL.Query().Get("instance")),
		}
		if err := h.store.DeleteSensitiveWhitelist(input); err != nil {
			writeSensitiveFilterError(w, err)
			return
		}
		writeJSONHTTP(w, map[string]interface{}{"ok": true})
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func writeSensitiveFilterError(w http.ResponseWriter, err error) {
	status, code, message := sensitiveFilterErrorView(err)
	writeDoopsError(w, status, "", doopsErrorResponse{Error: code, Code: code, Message: message})
}

// sensitiveFilterErrorView keeps the public error category stable for the
// console while giving the operator a readable Chinese explanation.
func sensitiveFilterErrorView(err error) (int, string, string) {
	switch {
	case errors.Is(err, errSensitiveInvalid):
		return http.StatusBadRequest, "invalid_sensitive_filter_input", "敏感词规则或白名单格式无效"
	case errors.Is(err, errSensitiveRuleNotFound):
		return http.StatusNotFound, "sensitive_rule_not_found", "敏感词规则不存在，可能已被其他管理员删除"
	case errors.Is(err, errSensitiveWhitelistNotFound):
		return http.StatusNotFound, "sensitive_whitelist_not_found", "白名单条目不存在，可能已被其他管理员删除"
	case errors.Is(err, errSensitiveWhitelistExists):
		return http.StatusConflict, "sensitive_whitelist_exists", "该白名单条目已存在"
	case errors.Is(err, errSensitiveRuleLimit):
		return http.StatusConflict, "sensitive_rule_limit_reached", "敏感词规则数量已达上限"
	case errors.Is(err, errSensitiveWhitelistLimit):
		return http.StatusConflict, "sensitive_whitelist_limit_reached", "白名单数量已达上限"
	default:
		return http.StatusInternalServerError, "sensitive_filter_storage_unavailable", "敏感词配置存储不可用，请稍后重试"
	}
}
