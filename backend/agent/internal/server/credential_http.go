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
	GranteeID string               `json:"grantee_id"`
	Scope     CredentialGrantScope `json:"scope"`
}

type credentialGrantRevokeHTTPBody struct {
	GrantID string `json:"grant_id"`
}

type credentialTargetListEntry struct {
	ID        string                      `json:"id"`
	Name      string                      `json:"name"`
	Scope     CredentialScope             `json:"scope"`
	Type      CredentialType              `json:"type"`
	State     CredentialState             `json:"state"`
	UpdatedAt time.Time                   `json:"updated_at"`
	Grants    []credentialTargetListGrant `json:"grants"`
}

type credentialTargetListGrant struct {
	ID           string               `json:"id"`
	CredentialID string               `json:"credential_id"`
	Scope        CredentialGrantScope `json:"scope"`
	State        CredentialState      `json:"state"`
	CreatedAt    time.Time            `json:"created_at"`
}

func (h *GatewayHub) HandleCredentials(w http.ResponseWriter, r *http.Request) {
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleCredentialList(w, r, auth)
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
			!h.canViewCredential(
				auth.UserID,
				resource,
				r.URL.Query().Get("cluster"),
				r.URL.Query().Get("instance"),
			) {
			http.NotFound(w, r)
			return
		}
		if !h.store.UserIsPlatformAdmin(auth.UserID) &&
			(resource.Scope != CredentialScopePersonal || resource.OwnerID != auth.UserID) {
			writeJSONHTTP(w, credentialTargetListEntry{
				ID:        resource.ID,
				Name:      resource.Name,
				Scope:     resource.Scope,
				Type:      resource.Type,
				State:     resource.State,
				UpdatedAt: resource.UpdatedAt,
				Grants:    []credentialTargetListGrant{},
			})
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
	body.GranteeID = strings.TrimSpace(body.GranteeID)
	if body.GranteeID == "" {
		body.GranteeID = auth.UserID
	}
	if resource.Scope == CredentialScopePersonal {
		if resource.OwnerID == auth.UserID {
			if !h.store.UserCan(auth.UserID, body.Scope.Cluster, body.Scope.Instance, ActionAsk) {
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
		fmt.Sprintf("credential grant %s/%s",
			strings.TrimSpace(body.Scope.Cluster), strings.TrimSpace(body.Scope.Instance)))
	if !ok {
		return
	}
	grant, err := h.store.CreateCredentialGrantAudited(CredentialGrantCreateRequest{
		CredentialID: resource.ID,
		GranteeID:    body.GranteeID,
		Scope:        body.Scope,
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
	required, verified := activeCredentialVerifications(grants, verifications)
	if !verified {
		_ = h.finishCredentialAudit(auditID, "error", "credential_verification_required")
		http.Error(w, "credential_verification_required", http.StatusConflict)
		return
	}
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
		expectedPreviousVersionID := ""
		if previousErr == nil {
			expectedPreviousVersionID = previous.ID
		}
		evidence, refreshedRequest, err := h.materializeCredentialVersion(
			r.Context(), request.Cluster, request.Instance, request.SessionID,
			resource, version, expectedPreviousVersionID, request.GrantID, ref,
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

func activeCredentialVerifications(
	grants []CredentialGrant,
	verifications []CredentialVerification,
) ([]CredentialVerification, bool) {
	if len(grants) == 0 {
		return nil, false
	}
	active := make(map[string]struct{}, len(grants))
	for _, grant := range grants {
		active[grant.ID+"\x00"+string(grant.Scope.Use)] = struct{}{}
	}
	required := make([]CredentialVerification, 0, len(grants))
	verified := make(map[string]struct{}, len(grants))
	for _, verification := range verifications {
		key := verification.GrantID + "\x00" + string(verification.Use)
		if _, ok := active[key]; !ok {
			continue
		}
		required = append(required, verification)
		verified[key] = struct{}{}
	}
	return required, len(verified) == len(active)
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

func (h *GatewayHub) canViewCredential(
	actorID string,
	resource CredentialResource,
	cluster string,
	instance string,
) bool {
	if h.store.UserIsPlatformAdmin(actorID) {
		return true
	}
	if resource.Scope == CredentialScopePersonal && resource.OwnerID == actorID {
		return true
	}
	cluster = strings.TrimSpace(cluster)
	instance = strings.TrimSpace(instance)
	if cluster == "" || instance == "" ||
		!h.store.UserCan(actorID, cluster, instance, ActionCredentialMetadata) {
		return false
	}
	entries, err := h.store.ListCredentialAccess(actorID, cluster, instance, false)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.ID == resource.ID {
			return true
		}
	}
	return false
}

func (h *GatewayHub) handleCredentialList(w http.ResponseWriter, r *http.Request, auth TokenAuth) {
	view := strings.TrimSpace(r.URL.Query().Get("view"))
	cluster := strings.TrimSpace(r.URL.Query().Get("cluster"))
	instance := strings.TrimSpace(r.URL.Query().Get("instance"))
	if view == "target" || cluster != "" || instance != "" {
		if view != "" && view != "target" {
			http.Error(w, "invalid credential list view", http.StatusBadRequest)
			return
		}
		if cluster == "" || instance == "" {
			http.Error(w, "credential target list requires cluster and instance", http.StatusBadRequest)
			return
		}
		if !h.store.UserCan(auth.UserID, cluster, instance, ActionCredentialMetadata) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		entries, err := h.store.ListCredentialAccess(
			auth.UserID, cluster, instance, h.store.UserIsPlatformAdmin(auth.UserID),
		)
		if err != nil {
			http.Error(w, "failed to list target credentials", http.StatusInternalServerError)
			return
		}
		if entries == nil {
			entries = []CredentialListEntry{}
		}
		responseEntries := make([]credentialTargetListEntry, 0, len(entries))
		for _, entry := range entries {
			grants := make([]credentialTargetListGrant, 0, len(entry.Grants))
			for _, grant := range entry.Grants {
				grants = append(grants, credentialTargetListGrant{
					ID:           grant.ID,
					CredentialID: grant.CredentialID,
					Scope:        grant.Scope,
					State:        grant.State,
					CreatedAt:    grant.CreatedAt,
				})
			}
			responseEntries = append(responseEntries, credentialTargetListEntry{
				ID:        entry.ID,
				Name:      entry.Name,
				Scope:     entry.Scope,
				Type:      entry.Type,
				State:     entry.State,
				UpdatedAt: entry.UpdatedAt,
				Grants:    grants,
			})
		}
		writeJSONHTTP(w, map[string]any{
			"view":        "target",
			"cluster":     cluster,
			"instance":    instance,
			"credentials": responseEntries,
		})
		return
	}
	if view != "" && view != "managed" {
		http.Error(w, "invalid credential list view", http.StatusBadRequest)
		return
	}
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
		http.Error(w, "failed to list managed credentials", http.StatusInternalServerError)
		return
	}
	if resources == nil {
		resources = []CredentialResource{}
	}
	writeJSONHTTP(w, map[string]any{"view": "managed", "credentials": resources})
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
