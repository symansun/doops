package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

type credentialVerifyHTTPBody struct {
	VersionID string               `json:"version_id"`
	SessionID string               `json:"session_id"`
	Scope     CredentialGrantScope `json:"scope"`
}

type credentialVerificationEvidence struct {
	Status          string `json:"status"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
	Digest          string `json:"digest,omitempty"`
	ErrorCategory   string `json:"errorCategory,omitempty"`
}

func (h *GatewayHub) handleCredentialVerify(
	w http.ResponseWriter,
	r *http.Request,
	auth TokenAuth,
	resource CredentialResource,
) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.canVerifyCredential(auth.UserID, resource) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	var body credentialVerifyHTTPBody
	if err := decodeCredentialJSON(w, r, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body.VersionID = strings.TrimSpace(body.VersionID)
	body.SessionID = strings.TrimSpace(body.SessionID)
	// Verification only selects the target; each grant is verified with the scope
	// stored when it was bound, so callers never restate namespace, use or keys.
	body.Scope = CredentialGrantScope{
		Cluster: strings.TrimSpace(body.Scope.Cluster), Instance: strings.TrimSpace(body.Scope.Instance),
	}
	if body.VersionID == "" || validateSession(body.SessionID) != nil ||
		body.Scope.Cluster == "" || body.Scope.Instance == "" {
		http.Error(w, "invalid credential verify request", http.StatusBadRequest)
		return
	}
	if !h.store.UserCanUseTarget(auth.UserID, body.Scope.Cluster, body.Scope.Instance) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	unlock, err := h.acquireCredentialMutationLocks(r.Context(), []string{resource.ID})
	if err != nil {
		http.Error(w, "operation canceled", http.StatusConflict)
		return
	}
	defer unlock()
	if h.store.validateUserTokenAuth(auth) != nil || !h.canVerifyCredential(auth.UserID, resource) ||
		!h.store.UserCanUseTarget(auth.UserID, body.Scope.Cluster, body.Scope.Instance) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	version, err := h.store.CredentialVersionByID(resource.ID, body.VersionID)
	if err != nil {
		http.Error(w, credentialErrorCategory(err), credentialHTTPStatus(err))
		return
	}
	if version.State != CredentialVersionStaged {
		http.Error(w, "credential_version_not_staged", http.StatusConflict)
		return
	}
	grants, err := h.store.CredentialGrantsForContext(resource.ID, body.Scope)
	if err != nil {
		http.Error(w, credentialErrorCategory(err), credentialHTTPStatus(err))
		return
	}
	active, activeErr := h.store.ActiveCredentialVersion(resource.ID)
	if activeErr != nil && !errors.Is(activeErr, ErrCredentialVersionNotFound) {
		http.Error(w, credentialErrorCategory(activeErr), credentialHTTPStatus(activeErr))
		return
	}
	auditID, ok := h.requireCredentialAudit(w, auth, ActionCredentialVerify, resource.ID, "credential verify")
	if !ok {
		return
	}
	// A verification runs on one target, so a failure is audited against that
	// target the same way a success is.
	failure := credentialTargetAudit(body.Scope, "")
	failure.VersionID = version.ID
	evidence := make([]credentialVerificationEvidence, 0, len(grants))
	verifications := make([]CredentialVerification, 0, len(grants))
	var auditScope CredentialGrantScope
	for index, grant := range grants {
		scope := grant.Scope
		if scope.Use == "" {
			scope.Use = credentialUseForType(resource.Type)
		}
		if index == 0 {
			auditScope = scope
		}
		failure = credentialTargetAudit(scope, grant.ID)
		failure.VersionID = version.ID
		ref := CredentialPlanReference{Name: resource.Name, Scope: scope}
		expectedPreviousVersionID := ""
		if activeErr == nil {
			expectedPreviousVersionID = active.ID
		}
		observed, request, err := h.materializeCredentialVersion(
			r.Context(), body.Scope.Cluster, body.Scope.Instance, body.SessionID,
			resource, version, expectedPreviousVersionID, grant.ID, ref,
		)
		if err != nil {
			h.writeCredentialVerificationError(w, auditID, failure, credentialOperationErrorCategory(err), http.StatusBadGateway)
			return
		}
		if activeErr == nil {
			if _, _, err := h.materializeCredentialVersion(
				r.Context(), body.Scope.Cluster, body.Scope.Instance, body.SessionID,
				resource, active, version.ID, grant.ID, ref,
			); err != nil {
				h.writeCredentialVerificationError(w, auditID, failure, "credential_verification_outcome_unknown", http.StatusBadGateway)
				return
			}
			if err := h.store.MarkCredentialMaterializationCleaned(request); err != nil {
				h.writeCredentialVerificationError(w, auditID, failure, "credential_verification_outcome_unknown", http.StatusBadGateway)
				return
			}
		} else if err := h.cleanupCredentialVersion(r.Context(), request); err != nil {
			h.writeCredentialVerificationError(w, auditID, failure, "credential_verification_outcome_unknown", http.StatusBadGateway)
			return
		}
		verifications = append(verifications, CredentialVerification{
			CredentialID: resource.ID, VersionID: version.ID, GrantID: grant.ID,
			Use: scope.Use, Request: request, Evidence: observed,
		})
		evidence = append(evidence, credentialVerificationEvidence{
			Status: observed.Status, ResourceVersion: observed.ResourceVersion,
			Digest: observed.Digest, ErrorCategory: observed.ErrorCategory,
		})
	}
	// The success audit keeps only validated identifiers, like the failure
	// target above: a verify whose request target is request text writes no
	// cluster, instance or scope column.
	success := credentialTargetAudit(body.Scope, "")
	success.Status = "success"
	success.VersionID = version.ID
	if success.Cluster != "" && success.Instance != "" && validateCredentialGrantScope(auditScope) == nil {
		success.Scope = &auditScope
	}
	success.EndedAt = time.Now().UTC()
	if err := h.store.RecordCredentialVerificationsAudited(verifications, auditID, success); err != nil {
		h.writeCredentialVerificationError(w, auditID, failure, "credential_verification_persist_failed", http.StatusInternalServerError)
		return
	}
	writeJSONHTTP(w, map[string]any{"status": "verified", "evidence": evidence})
}

func (h *GatewayHub) writeCredentialVerificationError(
	w http.ResponseWriter,
	auditID int64,
	target AuditFinish,
	category string,
	status int,
) {
	if err := h.finishCredentialTargetAudit(auditID, target, "error", category); err != nil {
		http.Error(w, "credential_audit_unavailable", http.StatusInternalServerError)
		return
	}
	http.Error(w, category, status)
}

func (h *GatewayHub) rollbackCredentialPromotion(
	ctx context.Context,
	resource CredentialResource,
	previous CredentialVersion,
	previousErr error,
	applied []CredentialVerification,
) error {
	var rollbackErr error
	for index := len(applied) - 1; index >= 0; index-- {
		verification := applied[index]
		if previousErr == nil {
			ref := credentialReferenceFromRequest(verification.Request)
			if _, _, err := h.materializeCredentialVersion(
				ctx, verification.Request.Cluster, verification.Request.Instance,
				verification.Request.SessionID, resource, previous,
				verification.Request.VersionID, verification.Request.GrantID, ref,
			); err != nil {
				rollbackErr = err
			}
			continue
		}
		if err := h.cleanupCredentialVersion(ctx, verification.Request); err != nil {
			rollbackErr = err
		}
	}
	return rollbackErr
}
