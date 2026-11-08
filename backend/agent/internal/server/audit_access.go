package server

import (
	"net/url"
	"strconv"
	"strings"
)

// Platform admins see every event. Other users see their own events and the
// events that reference a credential they own. The scope is applied in SQL
// before LIMIT, so another user's events cannot fill the page.
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
		Status: query.Get("status"), Limit: limit,
	}
	if !isAdmin {
		filter.VisibleToUser = auth.UserID
	}
	return h.store.ListAuditFiltered(filter)
}
