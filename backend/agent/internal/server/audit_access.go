package server

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var errInvalidAuditFilter = errors.New("invalid audit filter")

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
		CorrelationID: strings.TrimSpace(query.Get("correlation_id")),
	}
	if value := query.Get("before_id"); value != "" {
		before, err := strconv.ParseInt(value, 10, 64)
		if err != nil || before < 1 {
			return nil, fmt.Errorf("%w: before_id must be a positive integer", errInvalidAuditFilter)
		}
		filter.BeforeID = before
	}
	if filter.CorrelationID != "" && !upgradeAuditIDPattern.MatchString(filter.CorrelationID) {
		return nil, fmt.Errorf("%w: invalid correlation_id", errInvalidAuditFilter)
	}
	for _, entry := range []struct {
		name string
		dest *time.Time
	}{{"since", &filter.Since}, {"until", &filter.Until}} {
		if value := query.Get(entry.name); value != "" {
			parsed, err := time.Parse(time.RFC3339, value)
			if err != nil {
				return nil, fmt.Errorf("%w: %s must be RFC3339", errInvalidAuditFilter, entry.name)
			}
			*entry.dest = parsed
		}
	}
	if !filter.Since.IsZero() && !filter.Until.IsZero() && !filter.Since.Before(filter.Until) {
		return nil, fmt.Errorf("%w: since must be before until", errInvalidAuditFilter)
	}
	if value := query.Get("hide_upgrade_observe"); value != "" {
		if value != "true" && value != "false" {
			return nil, fmt.Errorf("%w: hide_upgrade_observe must be true or false", errInvalidAuditFilter)
		}
		filter.HideUpgradeObserve = value == "true"
	}
	if !isAdmin {
		filter.VisibleToUser = auth.UserID
	}
	return h.store.ListAuditFiltered(filter)
}
