package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type credentialCreateHTTPBody struct {
	Name  string          `json:"name"`
	Scope CredentialScope `json:"scope"`
	Type  CredentialType  `json:"type"`
}

type credentialGrantHTTPBody struct {
	GranteeID   string          `json:"grantee_id"`
	Cluster     string          `json:"cluster"`
	Instance    string          `json:"instance"`
	Project     string          `json:"project"`
	Environment string          `json:"environment"`
	Template    string          `json:"template"`
	Namespace   string          `json:"namespace"`
	Uses        []CredentialUse `json:"uses"`
}

type credentialGrantRevokeHTTPBody struct {
	GrantID string `json:"grant_id"`
}

func (h *GatewayHub) HandleCredentials(w http.ResponseWriter, r *http.Request) {
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !h.store.UserHasAction(auth.UserID, ActionCredentialMetadata) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		filter := CredentialListFilter{ActorID: auth.UserID}
		if h.store.UserIsPlatformAdmin(auth.UserID) {
			filter.ActorID = ""
		}
		resources, err := h.store.ListCredentials(filter)
		if err != nil {
			http.Error(w, "failed to list credentials", http.StatusInternalServerError)
			return
		}
		if resources == nil {
			resources = []CredentialResource{}
		}
		writeJSONHTTP(w, map[string]any{"credentials": resources})
	case http.MethodPost:
		if !h.store.UserHasAction(auth.UserID, ActionCredentialCreate) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		var body credentialCreateHTTPBody
		if err := decodeCredentialJSON(w, r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Scope == CredentialScopePlatform && !h.store.UserIsPlatformAdmin(auth.UserID) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		ownerID := ""
		if body.Scope == CredentialScopePersonal {
			ownerID = auth.UserID
		}
		auditID, ok := h.requireCredentialAudit(
			w, auth, ActionCredentialCreate, "", "credential create "+strings.TrimSpace(body.Name),
		)
		if !ok {
			return
		}
		resource, err := h.store.CreateCredentialAudited(CredentialCreateRequest{
			Name: body.Name, Scope: body.Scope, Type: body.Type, OwnerID: ownerID, CreatedBy: auth.UserID,
		}, auditID)
		if err != nil {
			_ = h.finishCredentialAudit(auditID, "error", credentialErrorCategory(err))
			http.Error(w, credentialErrorCategory(err), credentialHTTPStatus(err))
			return
		}
		writeJSONHTTP(w, resource)
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func (h *GatewayHub) HandleCredential(w http.ResponseWriter, r *http.Request) {
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r)
		return
	}
	id, operation, ok := credentialPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	resource, err := h.store.CredentialByID(id)
	if err != nil {
		http.Error(w, credentialErrorCategory(err), credentialHTTPStatus(err))
		return
	}
	switch operation {
	case "":
		if r.Method != http.MethodGet {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		if !h.store.UserHasAction(auth.UserID, ActionCredentialMetadata) ||
			!h.canViewCredential(auth.UserID, resource) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		writeJSONHTTP(w, resource)
	case "payload":
		h.handleCredentialPayload(w, r, auth, resource)
	case "grants":
		h.handleCredentialGrants(w, r, auth, resource)
	case "verify":
		h.handleCredentialVerify(w, r, auth, resource)
	case "promote":
		h.handleCredentialPromote(w, r, auth, resource)
	case "revoke":
		h.handleCredentialRevoke(w, r, auth, resource)
	default:
		http.NotFound(w, r)
	}
}

func (h *GatewayHub) handleCredentialPayload(w http.ResponseWriter, r *http.Request, auth TokenAuth, resource CredentialResource) {
	if r.Method != http.MethodPut {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.canRotateCredential(auth.UserID, resource) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if r.URL.Query().Has("activate") {
		http.Error(w, "credential versions must be verified before promotion", http.StatusBadRequest)
		return
	}
	payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil || !json.Valid(payload) {
		http.Error(w, "credential payload must be valid JSON", http.StatusBadRequest)
		return
	}
	auditID, ok := h.requireCredentialAudit(w, auth, ActionCredentialRotate, resource.ID, "credential payload put")
	if !ok {
		clear(payload)
		return
	}
	version, err := h.store.PutCredentialVersionAudited(CredentialVersionPutRequest{
		CredentialID: resource.ID, Payload: payload, CreatedBy: auth.UserID,
	}, auditID)
	clear(payload)
	if err != nil {
		_ = h.finishCredentialAudit(auditID, "error", credentialErrorCategory(err))
		http.Error(w, credentialErrorCategory(err), credentialHTTPStatus(err))
		return
	}
	writeJSONHTTP(w, version)
}

func (h *GatewayHub) handleCredentialGrants(w http.ResponseWriter, r *http.Request, auth TokenAuth, resource CredentialResource) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	h.credentialMutationMu.Lock()
	defer h.credentialMutationMu.Unlock()
	if r.Method == http.MethodDelete {
		var body credentialGrantRevokeHTTPBody
		if err := decodeCredentialJSON(w, r, &body); err != nil || strings.TrimSpace(body.GrantID) == "" {
			http.Error(w, "invalid credential grant revoke request", http.StatusBadRequest)
			return
		}
		if resource.Scope == CredentialScopePersonal {
			if resource.OwnerID != auth.UserID && !h.store.UserIsPlatformAdmin(auth.UserID) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
		} else if !h.store.UserIsPlatformAdmin(auth.UserID) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		auditID, ok := h.requireCredentialAudit(w, auth, ActionCredentialGrant, resource.ID, "credential grant revoke")
		if !ok {
			return
		}
		grant, err := h.store.BeginCredentialGrantRevocation(resource.ID, body.GrantID, auth.UserID)
		if err != nil {
			_ = h.finishCredentialAudit(auditID, "error", credentialErrorCategory(err))
			http.Error(w, credentialErrorCategory(err), credentialHTTPStatus(err))
			return
		}
		if err := h.cleanupCredentialGrantMaterializations(r.Context(), resource.ID, grant.ID); err != nil {
			_ = h.finishCredentialAudit(auditID, "error", "credential_grant_revocation_outcome_unknown")
			http.Error(w, "credential_grant_revocation_outcome_unknown", http.StatusBadGateway)
			return
		}
		grant, err = h.store.FinalizeCredentialGrantRevocation(resource.ID, grant.ID, auditID)
		if err != nil {
			_ = h.finishCredentialAudit(auditID, "error", credentialErrorCategory(err))
			http.Error(w, credentialErrorCategory(err), credentialHTTPStatus(err))
			return
		}
		writeJSONHTTP(w, grant)
		return
	}
	var body credentialGrantHTTPBody
	if err := decodeCredentialJSON(w, r, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if resource.Scope == CredentialScopePersonal {
		if resource.OwnerID == auth.UserID {
			if !h.store.UserCan(auth.UserID, body.Cluster, body.Instance, ActionAsk) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
		} else if !h.store.UserIsPlatformAdmin(auth.UserID) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	} else if !h.store.UserIsPlatformAdmin(auth.UserID) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	auditID, ok := h.requireCredentialAudit(w, auth, ActionCredentialGrant, resource.ID,
		fmt.Sprintf("credential grant %s/%s", strings.TrimSpace(body.Cluster), strings.TrimSpace(body.Instance)))
	if !ok {
		return
	}
	grant, err := h.store.CreateCredentialGrantAudited(CredentialGrantCreateRequest{
		CredentialID: resource.ID,
		GranteeID:    body.GranteeID,
		Cluster:      body.Cluster,
		Instance:     body.Instance,
		Project:      body.Project,
		Environment:  body.Environment,
		Template:     body.Template,
		Namespace:    body.Namespace,
		Uses:         body.Uses,
		CreatedBy:    auth.UserID,
	}, auditID)
	if err != nil {
		_ = h.finishCredentialAudit(auditID, "error", credentialErrorCategory(err))
		http.Error(w, credentialErrorCategory(err), credentialHTTPStatus(err))
		return
	}
	writeJSONHTTP(w, grant)
}

func (h *GatewayHub) handleCredentialPromote(w http.ResponseWriter, r *http.Request, auth TokenAuth, resource CredentialResource) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.canRotateCredential(auth.UserID, resource) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	h.credentialMutationMu.Lock()
	defer h.credentialMutationMu.Unlock()
	var body struct {
		VersionID string `json:"version_id"`
	}
	if err := decodeCredentialJSON(w, r, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	auditID, ok := h.requireCredentialAudit(w, auth, ActionCredentialRotate, resource.ID, "credential version promote")
	if !ok {
		return
	}
	version, err := h.store.CredentialVersionByID(resource.ID, body.VersionID)
	if err != nil {
		_ = h.finishCredentialAudit(auditID, "error", credentialErrorCategory(err))
		http.Error(w, credentialErrorCategory(err), credentialHTTPStatus(err))
		return
	}
	if version.State != CredentialVersionStaged {
		_ = h.finishCredentialAudit(auditID, "error", "credential_version_not_staged")
		http.Error(w, "credential_version_not_staged", http.StatusConflict)
		return
	}
	grants, err := h.store.ActiveCredentialGrants(resource.ID)
	if err != nil {
		_ = h.finishCredentialAudit(auditID, "error", "credential_grants_unavailable")
		http.Error(w, "credential_grants_unavailable", http.StatusInternalServerError)
		return
	}
	verifications, err := h.store.CredentialVerifications(resource.ID, version.ID)
	if err != nil {
		_ = h.finishCredentialAudit(auditID, "error", "credential_verifications_unavailable")
		http.Error(w, "credential_verifications_unavailable", http.StatusInternalServerError)
		return
	}
	verificationCountByGrantUse := make(map[string]int, len(verifications))
	for _, verification := range verifications {
		verificationCountByGrantUse[verification.GrantID+"\x00"+string(verification.Use)]++
	}
	for _, grant := range grants {
		for _, use := range grant.Uses {
			if verificationCountByGrantUse[grant.ID+"\x00"+string(use)] == 0 {
				_ = h.finishCredentialAudit(auditID, "error", "credential_verification_required")
				http.Error(w, "credential_verification_required", http.StatusConflict)
				return
			}
		}
	}
	required := verifications
	previous, previousErr := h.store.CredentialVersionByState(resource.ID, CredentialVersionActive)
	if previousErr != nil && !errors.Is(previousErr, ErrCredentialVersionNotFound) {
		_ = h.finishCredentialAudit(auditID, "error", credentialErrorCategory(previousErr))
		http.Error(w, credentialErrorCategory(previousErr), credentialHTTPStatus(previousErr))
		return
	}
	applied := make([]CredentialVerification, 0, len(required))
	seen := make(map[string]struct{})
	for _, verification := range required {
		request := verification.Request
		key := credentialMaterializationContextKey(request)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		ref := credentialReferenceFromRequest(request)
		evidence, refreshedRequest, err := h.materializeCredentialVersion(
			r.Context(), request.Cluster, request.Instance, request.SessionID,
			resource, version, request.GrantID, ref,
		)
		if err != nil {
			if rollbackErr := h.rollbackCredentialPromotion(r.Context(), resource, previous, previousErr, applied); rollbackErr != nil {
				_ = h.finishCredentialAudit(auditID, "error", "credential_promotion_outcome_unknown")
				http.Error(w, "credential_promotion_outcome_unknown", http.StatusBadGateway)
				return
			}
			_ = h.finishCredentialAudit(auditID, "error", "credential_promotion_materialization_failed")
			http.Error(w, "credential_promotion_materialization_failed", http.StatusBadGateway)
			return
		}
		verification.Request = refreshedRequest
		verification.Evidence = evidence
		applied = append(applied, verification)
	}
	if err := h.store.PromoteCredentialVersionAudited(resource.ID, body.VersionID, auth.UserID, auditID); err != nil {
		if rollbackErr := h.rollbackCredentialPromotion(r.Context(), resource, previous, previousErr, applied); rollbackErr != nil {
			_ = h.finishCredentialAudit(auditID, "error", "credential_promotion_outcome_unknown")
			http.Error(w, "credential_promotion_outcome_unknown", http.StatusBadGateway)
			return
		}
		_ = h.finishCredentialAudit(auditID, "error", credentialErrorCategory(err))
		http.Error(w, credentialErrorCategory(err), credentialHTTPStatus(err))
		return
	}
	writeJSONHTTP(w, map[string]any{"credential_id": resource.ID, "version_id": body.VersionID, "promoted": true})
}

func (h *GatewayHub) handleCredentialRevoke(w http.ResponseWriter, r *http.Request, auth TokenAuth, resource CredentialResource) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if resource.Scope == CredentialScopePersonal {
		if resource.OwnerID != auth.UserID && !h.store.UserIsPlatformAdmin(auth.UserID) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	} else if !h.store.UserIsPlatformAdmin(auth.UserID) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	h.credentialMutationMu.Lock()
	defer h.credentialMutationMu.Unlock()
	auditID, ok := h.requireCredentialAudit(w, auth, ActionCredentialRevoke, resource.ID, "credential revoke")
	if !ok {
		return
	}
	if _, err := h.store.BeginCredentialRevocation(resource.ID, auth.UserID); err != nil {
		_ = h.finishCredentialAudit(auditID, "error", credentialErrorCategory(err))
		http.Error(w, credentialErrorCategory(err), credentialHTTPStatus(err))
		return
	}
	materializations, err := h.store.CredentialMaterializations(resource.ID, "", "")
	if err != nil {
		_ = h.finishCredentialAudit(auditID, "error", "credential_cleanup_context_unavailable")
		http.Error(w, "credential_cleanup_context_unavailable", http.StatusInternalServerError)
		return
	}
	seen := make(map[string]struct{})
	for _, materialization := range materializations {
		key := credentialMaterializationContextKey(materialization.Request)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if err := h.cleanupCredentialMaterializationContext(r.Context(), materialization.Request); err != nil {
			_ = h.finishCredentialAudit(auditID, "error", "credential_cleanup_failed")
			http.Error(w, "credential_cleanup_failed", http.StatusBadGateway)
			return
		}
	}
	if err := h.store.FinalizeCredentialRevocationAudited(resource.ID, auditID); err != nil {
		_ = h.finishCredentialAudit(auditID, "error", credentialErrorCategory(err))
		http.Error(w, credentialErrorCategory(err), credentialHTTPStatus(err))
		return
	}
	writeJSONHTTP(w, map[string]any{"credential_id": resource.ID, "revoked": true})
}

func (h *GatewayHub) canViewCredential(actorID string, resource CredentialResource) bool {
	if h.store.UserIsPlatformAdmin(actorID) {
		return true
	}
	if resource.Scope == CredentialScopePersonal && resource.OwnerID == actorID {
		return true
	}
	resources, err := h.store.ListCredentials(CredentialListFilter{ActorID: actorID})
	if err != nil {
		return false
	}
	for _, candidate := range resources {
		if candidate.ID == resource.ID {
			return true
		}
	}
	return false
}

func (h *GatewayHub) canRotateCredential(actorID string, resource CredentialResource) bool {
	if resource.Scope == CredentialScopePersonal && resource.OwnerID == actorID {
		return true
	}
	return h.store.UserIsPlatformAdmin(actorID)
}

func (h *GatewayHub) canVerifyCredential(actorID string, resource CredentialResource) bool {
	if resource.Scope == CredentialScopePersonal && resource.OwnerID == actorID {
		return true
	}
	return h.store.UserIsPlatformAdmin(actorID)
}

func credentialPath(path string) (id, operation string, ok bool) {
	const prefix = "/v1/credentials/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if rest == "" {
		return "", "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) > 2 || strings.TrimSpace(parts[0]) == "" {
		return "", "", false
	}
	id = strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		operation = strings.TrimSpace(parts[1])
	}
	return id, operation, true
}

func decodeCredentialJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid credential request")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("credential request must contain exactly one JSON object")
	}
	return nil
}

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

func credentialErrorCategory(err error) string {
	switch {
	case errors.Is(err, ErrCredentialNotFound), errors.Is(err, sql.ErrNoRows):
		return "credential_not_found"
	case errors.Is(err, ErrCredentialBundleNotFound):
		return "credential_bundle_not_found"
	case errors.Is(err, ErrCredentialAmbiguous):
		return "credential_reference_ambiguous"
	case errors.Is(err, ErrCredentialVersionNotFound):
		return "credential_version_not_found"
	case errors.Is(err, ErrCredentialRevoked):
		return "credential_revoked"
	case errors.Is(err, ErrCredentialGrantDenied):
		return "credential_grant_denied"
	case errors.Is(err, ErrCredentialPrepareBindingInvalid):
		return "credential_prepare_binding_invalid"
	case errors.Is(err, ErrCredentialForbidden):
		return "credential_forbidden"
	case errors.Is(err, ErrSecretKeyUnavailable):
		return "credential_key_unavailable"
	case errors.Is(err, ErrCredentialPayloadInvalid):
		return "credential_payload_invalid"
	case errors.Is(err, ErrCredentialAuditUnavailable):
		return "credential_audit_unavailable"
	default:
		return "credential_invalid"
	}
}

func credentialHTTPStatus(err error) int {
	switch {
	case errors.Is(err, ErrCredentialNotFound), errors.Is(err, ErrCredentialVersionNotFound),
		errors.Is(err, ErrCredentialBundleNotFound), errors.Is(err, sql.ErrNoRows):
		return http.StatusNotFound
	case errors.Is(err, ErrCredentialAmbiguous):
		return http.StatusConflict
	case errors.Is(err, ErrCredentialForbidden), errors.Is(err, ErrCredentialGrantDenied):
		return http.StatusForbidden
	case errors.Is(err, ErrSecretKeyUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, ErrCredentialAuditUnavailable):
		return http.StatusInternalServerError
	default:
		return http.StatusBadRequest
	}
}
