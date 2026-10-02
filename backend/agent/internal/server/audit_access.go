package server

import (
	"net/url"
	"strconv"
	"strings"
)

func (h *GatewayHub) visibleAuditEvents(auth TokenAuth, query url.Values) ([]AuditEvent, error) {
	isAdmin := h.store.UserIsPlatformAdmin(auth.UserID)
	credentialAuditor := h.store.UserHasAction(auth.UserID, ActionCredentialAudit)
	unscopedCredentialAuditor := h.store.UserCan(auth.UserID, "", "", ActionCredentialAudit)
	if strings.TrimSpace(query.Get("view")) == "global" && !isAdmin {
		return nil, ErrUnauthorized
	}
	userID := strings.TrimSpace(query.Get("user_id"))
	if userID == "self" {
		userID = auth.UserID
	}
	if !isAdmin && !credentialAuditor {
		if userID != "" && userID != auth.UserID {
			return nil, ErrUnauthorized
		}
		userID = auth.UserID
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
	visible := make([]AuditEvent, 0, limit)
	// Apply visibility before the requested limit. Other users' events must not
	// crowd an operator's own history out of the first database page.
	for {
		events, err := h.store.ListAuditFiltered(filter)
		if err != nil {
			return nil, err
		}
		for _, event := range events {
			if !isAdmin && event.UserID != auth.UserID {
				if !strings.HasPrefix(string(event.Action), "credential:") || !credentialAuditor {
					continue
				}
				if event.Cluster == "" || event.Instance == "" {
					if !unscopedCredentialAuditor {
						continue
					}
				} else if !h.store.UserCan(auth.UserID, event.Cluster, event.Instance, ActionCredentialAudit) {
					continue
				}
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
