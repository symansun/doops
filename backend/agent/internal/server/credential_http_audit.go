package server

import (
	"net/http"
	"time"
)

func (h *GatewayHub) startCredentialAudit(auth TokenAuth, action GatewayAction, credentialID, summary string) (int64, error) {
	return h.store.StartAudit(AuditEvent{
		UserID:         auth.UserID,
		TokenID:        auth.TokenID,
		CredentialID:   credentialID,
		CorrelationID:  "credop_" + randomHex(12),
		Action:         action,
		Session:        credentialID,
		CommandSummary: summary,
		StartedAt:      time.Now().UTC(),
	})
}

func (h *GatewayHub) finishCredentialAudit(id int64, status, category string) error {
	return h.store.FinishAudit(id, AuditFinish{
		Status:  status,
		Error:   category,
		EndedAt: time.Now().UTC(),
	})
}

// credentialTargetAudit is the target part of the audit of an operation that
// runs on one target: its cluster/instance and, once a grant is known, the
// grant and its stored scope. Only a pair of plain identifiers may reach the
// audit columns; anything else is request text, and the scope that holds it is
// left out too, so no audit column carries it.
func credentialTargetAudit(scope CredentialGrantScope, grantID string) AuditFinish {
	scope = normalizeCredentialGrantScope(scope)
	var finish AuditFinish
	if grantID != "" {
		finish.GrantID = grantID
	}
	if !validCredentialMetadataToken(scope.Cluster, 253) || !validCredentialMetadataToken(scope.Instance, 253) {
		return finish
	}
	finish.Cluster, finish.Instance = scope.Cluster, scope.Instance
	if grantID != "" && validateCredentialGrantScope(scope) == nil {
		finish.Scope = &scope
	}
	return finish
}

// finishCredentialTargetAudit finishes the audit of a single-target operation,
// keeping its target on failure as well as on success.
func (h *GatewayHub) finishCredentialTargetAudit(id int64, target AuditFinish, status, category string) error {
	target.Status = status
	target.Error = category
	target.EndedAt = time.Now().UTC()
	return h.store.FinishAudit(id, target)
}

func (h *GatewayHub) requireCredentialAudit(
	w http.ResponseWriter,
	auth TokenAuth,
	action GatewayAction,
	credentialID string,
	summary string,
) (int64, bool) {
	id, err := h.startCredentialAudit(auth, action, credentialID, summary)
	if err != nil {
		http.Error(w, "credential_audit_unavailable", http.StatusInternalServerError)
		return 0, false
	}
	return id, true
}

func (h *GatewayHub) completeCredentialAudit(
	w http.ResponseWriter,
	id int64,
	status string,
	category string,
) bool {
	if err := h.finishCredentialAudit(id, status, category); err != nil {
		http.Error(w, "credential_audit_unavailable", http.StatusInternalServerError)
		return false
	}
	return true
}
