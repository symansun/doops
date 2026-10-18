package server

import (
	"net/url"
	"strconv"
	"strings"
)

// Platform admins see every event. Other users see their own events and the
// events that reference a credential they own.
func (h *GatewayHub) visibleAuditEvents(auth TokenAuth, query url.Values) ([]AuditEvent, error) {
	isAdmin := h.store.UserIsPlatformAdmin(auth.UserID)
	if strings.TrimSpace(query.Get("view")) == "global" && !isAdmin {
		return nil, ErrUnauthorized
	}
	userID := strings.TrimSpace(query.Get("user_id"))
	if userID == "self" {
		userID = auth.UserID
	}
	if !isAdmin && userID != "" && userID != auth.UserID {
		return nil, ErrUnauthorized
	}
	limit, _ := strconv.Atoi(query.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	filter := AuditFilter{
		UserID: userID, Cluster: query.Get("cluster"), Instance: query.Get("instance"),
		Session: query.Get("session"), Action: GatewayAction(query.Get("action")),
		Status: query.Get("status"), Limit: 500,
	}
	owned := map[string]bool{}
	ownsCredential := func(id string) bool {
		if id == "" {
			return false
		}
		if value, ok := owned[id]; ok {
			return value
		}
		resource, err := h.store.CredentialByID(id)
		value := err == nil && resource.OwnerID == auth.UserID
		owned[id] = value
		return value
	}
	visible := make([]AuditEvent, 0, limit)
	// Apply visibility before the requested limit so other users' events cannot
	// crowd an operator's own history out of the first database page.
	for {
		events, err := h.store.ListAuditFiltered(filter)
		if err != nil {
			return nil, err
		}
		for _, event := range events {
			if !isAdmin && event.UserID != auth.UserID && !ownsCredential(event.CredentialID) {
				continue
			}
			visible = append(visible, event)
			if len(visible) == limit {
				return visible, nil
			}
		}
		if len(events) < filter.Limit {
			return visible, nil
		}
		filter.BeforeID = events[len(events)-1].ID
	}
}
