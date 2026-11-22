package server

import (
	"errors"
	"net/http"
	"strings"
	"time"
)

type auditResponse struct {
	Events []auditRecord `json:"events"`
}

type auditRecord struct {
	ID              int64                 `json:"id"`
	UserID          string                `json:"user_id"`
	TokenID         string                `json:"token_id"`
	Cluster         string                `json:"cluster"`
	Instance        string                `json:"instance"`
	CredentialID    string                `json:"credential_id,omitempty"`
	VersionID       string                `json:"credential_version_id,omitempty"`
	GrantID         string                `json:"credential_grant_id,omitempty"`
	Scope           *CredentialGrantScope `json:"scope,omitempty"`
	ResourceVersion string                `json:"resource_version,omitempty"`
	Digest          string                `json:"digest,omitempty"`
	CorrelationID   string                `json:"correlation_id,omitempty"`
	Action          string                `json:"action"`
	Session         string                `json:"session"`
	CommandSummary  string                `json:"command_summary"`
	Status          string                `json:"status"`
	Error           string                `json:"error,omitempty"`
	Tail            string                `json:"tail,omitempty"`
	BytesIn         int64                 `json:"bytes_in"`
	BytesOut        int64                 `json:"bytes_out"`
	StartedAt       string                `json:"started_at"`
	EndedAt         string                `json:"ended_at,omitempty"`
}

type auditPurgeResponse struct {
	Deleted int64 `json:"deleted"`
}

func (h *GatewayHub) HandleAudit(w http.ResponseWriter, r *http.Request) {
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	isAdmin := h.store.UserIsPlatformAdmin(auth.UserID)

	switch r.Method {
	case http.MethodGet:
		events, err := h.visibleAuditEvents(auth, r.URL.Query())
		if err != nil {
			if errors.Is(err, ErrUnauthorized) {
				http.Error(w, "Forbidden", http.StatusForbidden)
			} else if errors.Is(err, errInvalidAuditFilter) {
				http.Error(w, err.Error(), http.StatusBadRequest)
			} else {
				http.Error(w, "audit_unavailable", http.StatusServiceUnavailable)
			}
			return
		}
		records := make([]auditRecord, 0, len(events))
		for _, event := range events {
			record := auditRecord{
				ID:              event.ID,
				UserID:          event.UserID,
				TokenID:         event.TokenID,
				Cluster:         event.Cluster,
				Instance:        event.Instance,
				CredentialID:    event.CredentialID,
				VersionID:       event.VersionID,
				GrantID:         event.GrantID,
				Scope:           event.Scope,
				ResourceVersion: event.ResourceVersion,
				Digest:          event.Digest,
				CorrelationID:   event.CorrelationID,
				Action:          string(event.Action),
				Session:         event.Session,
				CommandSummary:  event.CommandSummary,
				Status:          event.Status,
				Error:           event.Error,
				Tail:            event.Tail,
				BytesIn:         event.BytesIn,
				BytesOut:        event.BytesOut,
				StartedAt:       event.StartedAt.UTC().Format(time.RFC3339),
			}
			if !event.EndedAt.IsZero() {
				record.EndedAt = event.EndedAt.UTC().Format(time.RFC3339)
			}
			records = append(records, record)
		}
		writeJSONHTTP(w, auditResponse{Events: records})
	case http.MethodDelete:
		if !isAdmin {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		beforeRaw := strings.TrimSpace(r.URL.Query().Get("before"))
		if beforeRaw == "" {
			http.Error(w, "missing before", http.StatusBadRequest)
			return
		}
		before, err := time.Parse(time.RFC3339, beforeRaw)
		if err != nil {
			http.Error(w, "invalid before; use RFC3339", http.StatusBadRequest)
			return
		}
		deleted, err := h.store.PurgeAuditBefore(auth, before)
		if err != nil {
			http.Error(w, "audit_purge_failed", http.StatusServiceUnavailable)
			return
		}
		writeJSONHTTP(w, auditPurgeResponse{Deleted: deleted})
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}
