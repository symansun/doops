package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Only identifiers and typed control metadata may enter the admin audit.
// Request/response bodies, names, URLs, configuration and error text must not.
type adminAuditMetadata struct {
	Route      string          `json:"route"`
	Method     string          `json:"method"`
	ResourceID string          `json:"resource_id,omitempty"`
	UserID     string          `json:"subject_user_id,omitempty"`
	Actions    []GatewayAction `json:"actions,omitempty"`
	Disabled   *bool           `json:"disabled,omitempty"`
	Enabled    *bool           `json:"enabled,omitempty"`
	HTTPStatus int             `json:"http_status,omitempty"`
}

type adminAuditInput struct {
	ID        string   `json:"id"`
	UserID    string   `json:"user_id"`
	User      string   `json:"user"`
	Cluster   string   `json:"cluster"`
	Instance  string   `json:"instance"`
	SessionID string   `json:"session_id"`
	Actions   []string `json:"actions"`
	Disabled  *bool    `json:"disabled"`
	Enabled   *bool    `json:"enabled"`
}

var adminAuditIDPattern = regexp.MustCompile(`^(usr_[a-f0-9]{24}|tok_[a-f0-9]{24}|repo_[a-f0-9]{24}|route_[a-f0-9]{24}|job_[a-f0-9]{20}|op-[a-f0-9]{24}|audit-[1-9][0-9]*|[0-9]+)$`)
var adminAuditLabelPattern = regexp.MustCompile(`^[a-zA-Z0-9_.*-]{1,128}$`)

func adminAuditID(value string) string {
	if adminAuditIDPattern.MatchString(value) {
		return value
	}
	return ""
}

func adminAuditLabel(value string) string {
	if adminAuditLabelPattern.MatchString(value) {
		return value
	}
	return ""
}

func (m adminAuditMetadata) summary() string {
	data, _ := json.Marshal(m)
	return string(data)
}

// Probe the same completion write before dispatch, without publishing a false
// success. The pending event remains durable if execution or completion fails.
func (s *GatewayStore) startMutationAudit(event AuditEvent) (int64, error) {
	id, err := s.StartAudit(event)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := finishAuditWith(tx, id, AuditFinish{Status: "success"}); err != nil {
		return 0, err
	}
	if err := tx.Rollback(); err != nil {
		return 0, err
	}
	return id, nil
}

type adminAuditResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (w *adminAuditResponse) Header() http.Header { return w.header }

func (w *adminAuditResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *adminAuditResponse) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.body.Write(data)
}

func (h *GatewayHub) auditAdminMutation(route string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth, err := h.authenticateUser(r)
		if err != nil {
			h.writeUserAuthError(w, r)
			return
		}
		isAdmin := h.store.UserHasAction(auth.UserID, ActionAdmin)
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			if !isAdmin {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			if err := h.authorizeAdminMutationTarget(auth.UserID, route, r, adminAuditInput{},
				r.URL.Query().Get("cluster"), r.URL.Query().Get("instance")); err != nil {
				status := http.StatusInternalServerError
				if errors.Is(err, ErrUnauthorized) {
					status = http.StatusForbidden
				}
				http.Error(w, http.StatusText(status), status)
				return
			}
			next(w, r)
			return
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		var input adminAuditInput
		payload, bodyErr := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		r.Body = io.NopCloser(bytes.NewReader(payload))
		if json.Unmarshal(payload, &input) != nil {
			input = adminAuditInput{}
		}
		metadata := adminAuditMetadata{Route: route, Method: r.Method,
			ResourceID: adminAuditID(strings.TrimSpace(r.URL.Query().Get("id"))),
			UserID:     adminAuditID(input.UserID), Disabled: input.Disabled, Enabled: input.Enabled}
		if metadata.ResourceID == "" {
			metadata.ResourceID = adminAuditID(input.ID)
		}
		if isAdmin && metadata.UserID == "" && input.User != "" {
			if user, err := h.store.FindUserByName(input.User); err == nil {
				metadata.UserID = user.ID
			}
		}
		for _, raw := range input.Actions {
			if action, ok := knownActions[strings.ToLower(strings.TrimSpace(raw))]; ok {
				metadata.Actions = append(metadata.Actions, action)
			}
		}
		if enabled := r.URL.Query().Get("enabled"); enabled == "true" || enabled == "false" {
			value := enabled == "true"
			metadata.Enabled = &value
		}
		cluster, instance := input.Cluster, input.Instance
		if route == "/v1/targets/unlock" {
			cluster, instance = r.URL.Query().Get("cluster"), r.URL.Query().Get("instance")
		}
		if isAdmin && route == "/v1/admin/operations" {
			for _, op := range h.listActiveOperations() {
				if op.ID == strings.TrimSpace(r.URL.Query().Get("id")) {
					cluster, instance = op.Cluster, op.Instance
					break
				}
			}
		}
		session := "admin-" + strings.ReplaceAll(strings.TrimPrefix(route, "/v1/admin/"), "/", "-")
		if route == "/v1/admin/tokens" && r.Method == http.MethodPost {
			session = "admin-token-create"
		}
		if route == "/v1/admin/audit/optimize" && validateSession(input.SessionID) == nil {
			session = input.SessionID
		}
		id, err := h.store.startMutationAudit(AuditEvent{
			UserID: auth.UserID, TokenID: auth.TokenID, Action: ActionAdmin,
			Cluster: adminAuditLabel(cluster), Instance: adminAuditLabel(instance),
			Session: session, CommandSummary: metadata.summary(),
		})
		if err != nil {
			http.Error(w, "audit_unavailable", http.StatusServiceUnavailable)
			return
		}
		response := &adminAuditResponse{header: make(http.Header)}
		completed := false
		defer func() {
			if !completed {
				_ = h.store.FinishAudit(id, AuditFinish{Status: "error", Error: "handler_interrupted"})
			}
		}()
		if bodyErr != nil {
			http.Error(response, "invalid request body", http.StatusBadRequest)
		} else if !isAdmin {
			http.Error(response, "Forbidden", http.StatusForbidden)
		} else if err := h.authorizeAdminMutationTarget(auth.UserID, route, r, input, cluster, instance); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, ErrUnauthorized) {
				status = http.StatusForbidden
			}
			http.Error(response, http.StatusText(status), status)
		} else {
			r = r.WithContext(contextWithInternalAuditAuth(r.Context(), auth))
			next(response, r)
		}
		if response.status == 0 {
			response.status = http.StatusOK
		}
		var result struct {
			ID      json.RawMessage `json:"id"`
			UserID  string          `json:"user_id"`
			TokenID string          `json:"token_id"`
			OK      *bool           `json:"ok"`
			IsError bool            `json:"isError"`
			Error   json.RawMessage `json:"error"`
		}
		if response.status < 400 && json.Unmarshal(response.body.Bytes(), &result) == nil {
			if result.OK != nil && !*result.OK || result.IsError ||
				len(result.Error) != 0 && string(result.Error) != "null" && string(result.Error) != `""` {
				response.status = http.StatusBadGateway
			} else {
				var resourceID string
				if json.Unmarshal(result.ID, &resourceID) != nil {
					resourceID = string(result.ID)
				}
				if value := adminAuditID(resourceID); value != "" {
					metadata.ResourceID = value
				}
				if value := adminAuditID(result.TokenID); value != "" {
					metadata.ResourceID = value
				}
				if value := adminAuditID(result.UserID); value != "" {
					metadata.UserID = value
				}
			}
		}
		metadata.HTTPStatus = response.status
		status, category := "success", ""
		if response.status >= 300 {
			status, category = "error", fmt.Sprintf("http_%d", response.status)
			if response.status == http.StatusForbidden {
				status = "forbidden"
			}
		}
		completed = true
		if err := h.store.FinishAudit(id, AuditFinish{
			Status: status, Error: category, CommandSummary: metadata.summary(),
			BytesIn: int64(len(payload)), BytesOut: int64(response.body.Len()), EndedAt: time.Now().UTC(),
		}); err != nil {
			http.Error(w, "audit_completion_failed; mutation outcome requires verification", http.StatusServiceUnavailable)
			return
		}
		for key, values := range response.header {
			w.Header()[key] = values
		}
		w.WriteHeader(response.status)
		_, _ = w.Write(response.body.Bytes())
	}
}

// Global control-plane access requires platform admin; target operations retain
// their own action grants. An admin grant on one target cannot elevate a user.
func (h *GatewayHub) authorizeAdminMutationTarget(userID, route string, r *http.Request, input adminAuditInput, cluster, instance string) error {
	check := func(cluster, instance string, action GatewayAction) error {
		if !h.store.UserCan(userID, strings.TrimSpace(cluster), strings.TrimSpace(instance), action) {
			return ErrUnauthorized
		}
		return nil
	}
	switch route {
	case "/v1/admin/users", "/v1/admin/users/password", "/v1/admin/users/disable",
		"/v1/admin/grants", "/v1/admin/tokens", "/v1/admin/repos", "/v1/admin/repos/test",
		"/v1/admin/jobs", "/v1/admin/jobs/issues", "/v1/admin/audit/optimize":
		if !h.store.UserIsPlatformAdmin(userID) {
			return ErrUnauthorized
		}
	case "/v1/admin/repos/clone":
		return check(cluster, instance, ActionPull)
	case "/v1/targets/unlock", "/v1/admin/operations":
		if route == "/v1/admin/operations" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			return check(cluster, instance, ActionAdmin)
		}
		if route == "/v1/admin/operations" && r.Method == http.MethodPost && !h.store.UserIsPlatformAdmin(userID) {
			return ErrUnauthorized
		}
		if cluster != "" && instance != "" {
			return check(cluster, instance, ActionAdmin)
		}
	case "/v1/admin/proxy-routes":
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if r.Method == http.MethodPost {
			id = strings.TrimSpace(input.ID)
			if err := check(cluster, instance, ActionAdmin); err != nil {
				return err
			}
		}
		if id != "" {
			var oldCluster, oldInstance string
			err := h.store.db.QueryRow(`SELECT cluster, instance FROM proxy_routes WHERE id = ?`, id).Scan(&oldCluster, &oldInstance)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil {
				return check(oldCluster, oldInstance, ActionAdmin)
			}
		}
	case "/v1/admin/jobs/run":
		if strings.TrimSpace(r.URL.Query().Get("enabled")) != "" {
			if !h.store.UserIsPlatformAdmin(userID) {
				return ErrUnauthorized
			}
			return nil
		}
		if h.scheduler == nil {
			return nil
		}
		job, err := h.store.GetSchedulerJob(r.URL.Query().Get("id"))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		mode := strings.ToLower(job.ScanMode)
		if mode == "audit" {
			if !h.store.UserIsPlatformAdmin(userID) {
				return ErrUnauthorized
			}
			return nil
		}
		action := ActionAsk
		if mode == "exec" {
			action = ActionExec
		}
		for _, target := range h.scheduler.matchTargets(job) {
			if err := check(target.Cluster, target.Instance, action); err != nil {
				return err
			}
		}
	}
	return nil
}
