package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type proxyRouteHTTPBody struct {
	ID       string `json:"id,omitempty"`
	Host     string `json:"host"`
	Cluster  string `json:"cluster"`
	Instance string `json:"instance"`
	Upstream string `json:"upstream"`
	Enabled  *bool  `json:"enabled,omitempty"`
	// UpdatedAt is the updated_at the client last read. An update that carries
	// it is refused with 409 once the route has changed; omit it to overwrite.
	UpdatedAt string `json:"updated_at,omitempty"`
}

func (h *GatewayHub) HandleAdminProxyRoutes(w http.ResponseWriter, r *http.Request) {
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	if !h.store.UserIsPlatformAdmin(auth.UserID) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	switch r.Method {
	case http.MethodGet:
		routes, err := h.store.ListProxyRoutes()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSONHTTP(w, map[string]interface{}{"routes": routes})
	case http.MethodPost:
		var body proxyRouteHTTPBody
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			http.Error(w, "invalid proxy route request", http.StatusBadRequest)
			return
		}
		enabled := true
		if body.Enabled != nil {
			enabled = *body.Enabled
		}
		input := ProxyRouteInput{
			ID: body.ID, Host: body.Host, Cluster: body.Cluster, Instance: body.Instance,
			Upstream: body.Upstream, Enabled: enabled,
		}
		if raw := strings.TrimSpace(body.UpdatedAt); raw != "" {
			expected, err := parseTime(raw)
			if err != nil {
				http.Error(w, "invalid proxy route updated_at", http.StatusBadRequest)
				return
			}
			input.ExpectUpdatedAt = &expected
		}
		route, err := h.store.UpsertProxyRoute(input)
		switch {
		case err == nil:
		case errors.Is(err, ErrProxyRouteHostInUse), errors.Is(err, ErrProxyRouteStale):
			http.Error(w, err.Error(), http.StatusConflict)
			return
		case errors.Is(err, sql.ErrNoRows):
			http.Error(w, "proxy route not found", http.StatusNotFound)
			return
		default:
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSONHTTP(w, route)
	case http.MethodDelete:
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if id == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		if err := h.store.DeleteProxyRoute(id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "proxy route not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSONHTTP(w, map[string]interface{}{"id": id, "deleted": true})
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}
