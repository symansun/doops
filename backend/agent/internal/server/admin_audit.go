package server

import (
	"bytes"
	"encoding/json"
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
	Route      string `json:"route"`
	Method     string `json:"method"`
	ResourceID string `json:"resource_id,omitempty"`
	UserID     string `json:"subject_user_id,omitempty"`
	Disabled   *bool  `json:"disabled,omitempty"`
	Enabled    *bool  `json:"enabled,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

type adminAuditInput struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	User      string `json:"user"`
	Cluster   string `json:"cluster"`
	Instance  string `json:"instance"`
	SessionID string `json:"session_id"`
	Disabled  *bool  `json:"disabled"`
	Enabled   *bool  `json:"enabled"`
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
		isAdmin := h.store.UserIsPlatformAdmin(auth.UserID)
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			if !isAdmin {
				http.Error(w, "Forbidden", http.StatusForbidden)
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
		if enabled := r.URL.Query().Get("enabled"); enabled == "true" || enabled == "false" {
			value := enabled == "true"
			metadata.Enabled = &value
		}
		cluster, instance := input.Cluster, input.Instance
		session := "admin-" + strings.ReplaceAll(strings.TrimPrefix(route, "/v1/admin/"), "/", "-")
		if route == "/v1/admin/tokens" && r.Method == http.MethodPost {
			session = "admin-token-create"
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
