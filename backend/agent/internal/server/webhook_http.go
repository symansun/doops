package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

type webhookEndpointJSON struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Description       string   `json:"description,omitempty"`
	URLHost           string   `json:"url_host"`
	URLSHA256         string   `json:"url_sha256"`
	Enabled           bool     `json:"enabled"`
	ScopeCluster      string   `json:"scope_cluster,omitempty"`
	ScopeInstance     string   `json:"scope_instance,omitempty"`
	EventTypes        []string `json:"event_types"`
	CreatedBy         string   `json:"created_by,omitempty"`
	CreatedAt         string   `json:"created_at"`
	UpdatedAt         string   `json:"updated_at"`
	LastSuccessAtMs   int64    `json:"last_success_at_ms,omitempty"`
	LastFailureAtMs   int64    `json:"last_failure_at_ms,omitempty"`
	LastErrorCategory string   `json:"last_error_category,omitempty"`
	Secret            string   `json:"secret,omitempty"`
}

func webhookEndpointToJSON(ep *WebhookEndpoint) webhookEndpointJSON {
	return webhookEndpointJSON{ID: ep.ID, Name: ep.Name, Description: ep.Description, URLHost: ep.URLHost, URLSHA256: ep.URLSHA256,
		Enabled: ep.Enabled, ScopeCluster: ep.ScopeCluster, ScopeInstance: ep.ScopeInstance, EventTypes: ep.EventTypes,
		CreatedBy: ep.CreatedBy, CreatedAt: ep.CreatedAt, UpdatedAt: ep.UpdatedAt, LastSuccessAtMs: ep.LastSuccessAtMs,
		LastFailureAtMs: ep.LastFailureAtMs, LastErrorCategory: ep.LastErrorCategory}
}

type webhookCreateRequest struct {
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	URL           string   `json:"url"`
	EventTypes    []string `json:"event_types"`
	ScopeCluster  string   `json:"scope_cluster"`
	ScopeInstance string   `json:"scope_instance"`
	Enabled       *bool    `json:"enabled"`
}

type webhookUpdateRequest struct {
	Name          *string  `json:"name"`
	Description   *string  `json:"description"`
	URL           *string  `json:"url"`
	EventTypes    []string `json:"event_types"`
	ScopeCluster  *string  `json:"scope_cluster"`
	ScopeInstance *string  `json:"scope_instance"`
	Enabled       *bool    `json:"enabled"`
}

func (h *GatewayHub) HandleAdminWebhooks(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/admin/webhooks"), "/")
	if path == "" {
		switch r.Method {
		case http.MethodGet:
			eps, err := h.store.ListWebhookEndpoints(r.Context())
			if err != nil {
				writeWebhookError(w, err)
				return
			}
			out := make([]webhookEndpointJSON, 0, len(eps))
			for _, ep := range eps {
				out = append(out, webhookEndpointToJSON(ep))
			}
			writeJSONHTTP(w, map[string]any{"endpoints": out})
		case http.MethodPost:
			h.createAdminWebhook(w, r)
		default:
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	parts := strings.Split(path, "/")
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		id = parts[0]
	}
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	switch action {
	case "":
		switch r.Method {
		case http.MethodGet:
			ep, err := h.store.GetWebhookEndpoint(r.Context(), id)
			if err != nil {
				writeWebhookError(w, err)
				return
			}
			writeJSONHTTP(w, map[string]any{"endpoint": webhookEndpointToJSON(ep)})
		case http.MethodPatch, http.MethodPut:
			h.updateAdminWebhook(w, r, id)
		case http.MethodDelete:
			if err := h.store.DeleteWebhookEndpoint(r.Context(), id); err != nil {
				writeWebhookError(w, err)
				return
			}
			writeJSONHTTP(w, map[string]any{"ok": true})
		default:
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	case "rotate-secret":
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		secret, err := h.store.RotateWebhookSecret(r.Context(), id)
		if err != nil {
			writeWebhookError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSONHTTP(w, map[string]any{"id": id, "secret": secret})
	case "test":
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		auth, err := h.authenticateUser(r)
		if err != nil {
			h.writeUserAuthError(w, r, err)
			return
		}
		eventID, err := h.store.EnqueueWebhookTestEvent(r.Context(), id, auth.UserID)
		if err != nil {
			writeWebhookError(w, err)
			return
		}
		if h.webhook != nil {
			h.webhook.Wake()
		}
		writeJSONHTTP(w, map[string]any{"ok": true, "event_id": eventID})
	case "deliveries":
		if r.Method != http.MethodGet {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		deliveries, err := h.store.ListWebhookDeliveries(r.Context(), id, limit)
		if err != nil {
			writeWebhookError(w, err)
			return
		}
		writeJSONHTTP(w, map[string]any{"deliveries": deliveries})
	default:
		http.NotFound(w, r)
	}
}

func (h *GatewayHub) createAdminWebhook(w http.ResponseWriter, r *http.Request) {
	var req webhookCreateRequest
	if err := decodeWebhookJSON(w, r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	ep, secret, err := h.store.CreateWebhookEndpoint(r.Context(), WebhookCreateInput{
		Name: req.Name, Description: req.Description, URL: req.URL, EventTypes: req.EventTypes,
		ScopeCluster: req.ScopeCluster, ScopeInstance: req.ScopeInstance, Enabled: req.Enabled, CreatedBy: auth.UserID,
	})
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	resp := webhookEndpointToJSON(ep)
	resp.Secret = secret
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusCreated)
	writeJSONHTTP(w, map[string]any{"id": ep.ID, "endpoint": resp})
}

func (h *GatewayHub) updateAdminWebhook(w http.ResponseWriter, r *http.Request, id string) {
	var req webhookUpdateRequest
	if err := decodeWebhookJSON(w, r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	ep, err := h.store.UpdateWebhookEndpoint(r.Context(), id, WebhookUpdateInput{
		Name: req.Name, Description: req.Description, URL: req.URL, EventTypes: req.EventTypes,
		EventTypesSet: req.EventTypes != nil, ScopeCluster: req.ScopeCluster, ScopeInstance: req.ScopeInstance, Enabled: req.Enabled,
	})
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	writeJSONHTTP(w, map[string]any{"endpoint": webhookEndpointToJSON(ep)})
}

func decodeWebhookJSON(w http.ResponseWriter, r *http.Request, out any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	return dec.Decode(out)
}

func writeWebhookError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrWebhookNotFound):
		http.Error(w, "webhook_not_found", http.StatusNotFound)
	case errors.Is(err, ErrSecretKeyUnavailable):
		http.Error(w, "webhook_secret_unavailable", http.StatusServiceUnavailable)
	case errors.Is(err, ErrWebhookLimitExceeded):
		http.Error(w, "webhook_limit_exceeded", http.StatusConflict)
	case errors.Is(err, ErrWebhookEndpointDisabled):
		http.Error(w, "webhook_endpoint_disabled", http.StatusConflict)
	case errors.Is(err, ErrWebhookNameInvalid):
		http.Error(w, "webhook_name_invalid", http.StatusBadRequest)
	case errors.Is(err, ErrWebhookEventUnknown):
		http.Error(w, "webhook_event_unknown", http.StatusBadRequest)
	case errors.Is(err, ErrWebhookScopeInvalid):
		http.Error(w, "webhook_scope_invalid", http.StatusBadRequest)
	case errors.Is(err, ErrWebhookURLTooLong):
		http.Error(w, "webhook_url_too_long", http.StatusBadRequest)
	case errors.Is(err, ErrWebhookBlockedDestination):
		http.Error(w, "webhook_blocked_destination", http.StatusBadRequest)
	case errors.Is(err, ErrWebhookInvalidURL):
		http.Error(w, "webhook_invalid_url", http.StatusBadRequest)
	default:
		http.Error(w, "webhook_internal_error", http.StatusInternalServerError)
	}
}
