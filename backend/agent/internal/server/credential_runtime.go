package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/user/doops/agent/api"
)

type CredentialMaterialization struct {
	CredentialID      string   `json:"credentialId"`
	GrantID           string   `json:"grantId,omitempty"`
	VersionID         string   `json:"versionId"`
	ResourceName      string   `json:"resourceName,omitempty"`
	Namespace         string   `json:"namespace"`
	SecretType        string   `json:"secretType,omitempty"`
	Keys              []string `json:"keys,omitempty"`
	ResourceVersion   string   `json:"resourceVersion,omitempty"`
	Digest            string   `json:"digest,omitempty"`
	Fingerprint       string   `json:"fingerprint,omitempty"`
	ExpiresAt         string   `json:"expiresAt,omitempty"`
	Status            string   `json:"status"`
	ErrorCategory     string   `json:"errorCategory,omitempty"`
	PreviousVersionID string   `json:"previousVersionId,omitempty"`
	ContextKind       string   `json:"contextKind,omitempty"`
	ContextSocket     string   `json:"contextSocket,omitempty"`
}

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

type authorizedCredentialMaterialization struct {
	Reference  CredentialPlanReference
	Authorized AuthorizedCredentialUse
}

type completedCredentialMaterialization struct {
	Materialization CredentialMaterialization
	Item            authorizedCredentialMaterialization
	SessionID       string
}

type credentialApplyPreparation struct {
	Bindings  []api.AgentCredentialBinding
	completed []completedCredentialMaterialization
}

func validateCredentialMaterializationMetadata(materialization CredentialMaterialization) error {
	if !validCredentialMetadataToken(materialization.CredentialID, 128) ||
		!validCredentialMetadataToken(materialization.GrantID, 128) ||
		!validCredentialMetadataToken(materialization.VersionID, 128) ||
		strings.TrimSpace(materialization.Namespace) != materialization.Namespace ||
		!validKubernetesCredentialName(materialization.Namespace) {
		return errors.New("credential_materialization_metadata_invalid")
	}
	if materialization.ResourceName != "" &&
		(strings.TrimSpace(materialization.ResourceName) != materialization.ResourceName ||
			!validKubernetesCredentialName(materialization.ResourceName)) {
		return errors.New("credential_materialization_metadata_invalid")
	}
	switch materialization.SecretType {
	case "", "Opaque", "kubernetes.io/dockerconfigjson", "kubernetes.io/tls":
	default:
		return errors.New("credential_materialization_metadata_invalid")
	}
	switch materialization.Status {
	case "planned", "verified", "outcome_unknown":
	default:
		return errors.New("credential_materialization_metadata_invalid")
	}
	if materialization.ResourceVersion != "" &&
		(!validCredentialMetadataToken(materialization.ResourceVersion, 256) ||
			credentialScalarContainsInlineMaterial(materialization.ResourceVersion)) {
		return errors.New("credential_materialization_metadata_invalid")
	}
	if materialization.Digest != "" &&
		(strings.TrimSpace(materialization.Digest) != materialization.Digest ||
			!validCredentialSHA256Digest(materialization.Digest)) {
		return errors.New("credential_materialization_metadata_invalid")
	}
	if materialization.Fingerprint != "" &&
		(strings.TrimSpace(materialization.Fingerprint) != materialization.Fingerprint ||
			!validCredentialSHA256Digest(materialization.Fingerprint)) {
		return errors.New("credential_materialization_metadata_invalid")
	}
	if materialization.ExpiresAt != "" {
		if _, err := time.Parse(time.RFC3339, materialization.ExpiresAt); err != nil {
			return errors.New("credential_materialization_metadata_invalid")
		}
	}
	if materialization.ContextKind != "" {
		if materialization.ContextKind != "helm-native" || !validNativeHelmSocketPath(materialization.ContextSocket) || materialization.SecretType != "" {
			return errors.New("credential_materialization_metadata_invalid")
		}
	}
	if materialization.ErrorCategory != "" && !validCredentialErrorCategory(materialization.ErrorCategory) {
		return errors.New("credential_materialization_metadata_invalid")
	}
	for _, key := range materialization.Keys {
		if !validCredentialMetadataToken(key, 253) || credentialScalarContainsInlineMaterial(key) {
			return errors.New("credential_materialization_metadata_invalid")
		}
	}
	if materialization.PreviousVersionID != "" &&
		(!strings.HasPrefix(materialization.PreviousVersionID, "credver_") ||
			!validLowerHex(strings.TrimPrefix(materialization.PreviousVersionID, "credver_"), 24)) {
		return errors.New("credential_materialization_metadata_invalid")
	}
	return nil
}

func validCredentialMetadataToken(value string, maxLength int) bool {
	if value == "" || len(value) > maxLength || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '_' || char == '-' ||
			char == '.' || char == ':' || char == '/' {
			continue
		}
		return false
	}
	return true
}

func validCredentialErrorCategory(category string) bool {
	if category == "" || len(category) > 128 {
		return false
	}
	for _, char := range category {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

func (h *GatewayHub) resolveExplicitCredentials(
	auth TokenAuth,
	cluster string,
	instance string,
	instruction string,
	names []string,
	action GatewayAction,
	skill string,
) ([]authorizedCredentialMaterialization, error) {
	if len(names) == 0 {
		return nil, nil
	}
	if !h.store.UserCan(auth.UserID, cluster, instance, action) {
		return nil, ErrCredentialForbidden
	}
	if credentialScalarContainsInlineMaterial(instruction) {
		return nil, ErrInlineCredentialMaterial
	}
	use := CredentialUse("")
	if skill == "dosql" {
		use = CredentialUseDatabase
	}
	authorized := make([]authorizedCredentialMaterialization, 0, len(names))
	for _, name := range names {
		resolved, err := h.store.AuthorizeCredentialUse(CredentialUseRequest{
			ActorID: auth.UserID, Credential: name,
			Scope: CredentialGrantScope{Cluster: cluster, Instance: instance, Use: use},
		})
		if err != nil {
			return nil, err
		}
		if err := credentialTypeAllowsUse(resolved.Resource.Type, resolved.Grant.Scope.Use); err != nil {
			return nil, err
		}
		authorized = append(authorized, authorizedCredentialMaterialization{
			Reference:  CredentialPlanReference{Name: name, Scope: normalizeCredentialGrantScope(resolved.Grant.Scope)},
			Authorized: resolved,
		})
	}
	return authorized, nil
}

func (h *GatewayHub) materializeExplicitCredentialApply(
	ctx context.Context,
	auth TokenAuth,
	sessionID string,
	authorized []authorizedCredentialMaterialization,
) (credentialApplyPreparation, error) {
	preparation := credentialApplyPreparation{
		Bindings:  make([]api.AgentCredentialBinding, 0, len(authorized)),
		completed: make([]completedCredentialMaterialization, 0, len(authorized)),
	}
	for _, item := range authorized {
		cluster := item.Reference.Scope.Cluster
		instance := item.Reference.Scope.Instance
		auditID, err := h.store.StartAudit(AuditEvent{
			UserID: auth.UserID, TokenID: auth.TokenID, Cluster: cluster, Instance: instance,
			CredentialID: item.Authorized.Resource.ID, VersionID: item.Authorized.Version.ID,
			GrantID: item.Authorized.Grant.ID, Scope: &item.Reference.Scope,
			CorrelationID: "credapply_" + randomHex(12), Action: ActionCredentialUse,
			Session: sessionID, CommandSummary: "credential materialize use=" + string(item.Reference.Scope.Use),
			StartedAt: time.Now().UTC(),
		})
		if err != nil {
			_ = h.rollbackCredentialPrepare(ctx, preparation.completed)
			return credentialApplyPreparation{}, ErrCredentialAuditUnavailable
		}
		materialization, request, err := h.materializeCredentialVersion(
			ctx, cluster, instance, sessionID, item.Authorized.Resource, item.Authorized.Version,
			item.Authorized.Version.ID, item.Authorized.Grant.ID, item.Reference,
		)
		if err != nil {
			_ = h.store.FinishAudit(auditID, AuditFinish{
				Status: "error", Error: credentialOperationErrorCategory(err), EndedAt: time.Now().UTC(),
			})
			_ = h.rollbackCredentialPrepare(ctx, preparation.completed)
			return credentialApplyPreparation{}, err
		}
		completed := completedCredentialMaterialization{
			Materialization: materialization, Item: item, SessionID: sessionID,
		}
		preparation.completed = append(preparation.completed, completed)
		if err := h.store.RecordCredentialVerification(CredentialVerification{
			CredentialID: item.Authorized.Resource.ID, VersionID: item.Authorized.Version.ID,
			GrantID: item.Authorized.Grant.ID, Use: item.Reference.Scope.Use,
			Request: request, Evidence: materialization,
		}); err != nil {
			_ = h.rollbackCredentialPrepare(ctx, preparation.completed)
			return credentialApplyPreparation{}, err
		}
		if err := h.store.FinishAudit(auditID, AuditFinish{
			Status: "success", Scope: &item.Reference.Scope, ResourceVersion: materialization.ResourceVersion,
			Digest: materialization.Digest, EndedAt: time.Now().UTC(),
		}); err != nil {
			_ = h.rollbackCredentialPrepare(ctx, preparation.completed)
			return credentialApplyPreparation{}, ErrCredentialAuditUnavailable
		}
		preparation.Bindings = append(preparation.Bindings, api.AgentCredentialBinding{
			Name: referenceName(item.Reference), Use: string(item.Reference.Scope.Use),
			Namespace: item.Reference.Scope.Namespace, ResourceName: materialization.ResourceName,
			DatabaseAssetID: item.Reference.Scope.DatabaseAssetID, EnvironmentID: item.Reference.Scope.EnvironmentID,
			DatabaseEngine: item.Reference.Scope.DatabaseEngine, DatabaseName: item.Reference.Scope.DatabaseName,
			SecretType: materialization.SecretType, Keys: append([]string(nil), materialization.Keys...),
			ResourceVersion: materialization.ResourceVersion, Digest: materialization.Digest,
			CredentialID: item.Authorized.Resource.ID, GrantID: item.Authorized.Grant.ID,
			VersionID: item.Authorized.Version.ID, HelmRelease: item.Reference.Scope.HelmRelease,
			HelmChart:   item.Reference.Scope.HelmChart,
			ContextKind: materialization.ContextKind, ContextSocket: materialization.ContextSocket,
		})
	}
	return preparation, nil
}

func referenceName(reference CredentialPlanReference) string {
	return strings.TrimSpace(reference.Name)
}

func credentialTypeAllowsUse(credentialType CredentialType, use CredentialUse) error {
	valid := (credentialType == CredentialTypeRegistry && use == CredentialUseImagePull) ||
		(credentialType == CredentialTypeTLS && use == CredentialUseTLS) ||
		(credentialType == CredentialTypeOpaque && (use == CredentialUseOpaqueSecret || use == CredentialUseDatabase)) ||
		(credentialType == CredentialTypeHelmRepository && use == CredentialUseHelmPull) ||
		(credentialType == CredentialTypeGitToken && use == CredentialUseGitCheckout)
	if !valid {
		return errors.New("credential_use_mismatch")
	}
	return nil
}

func (h *GatewayHub) rollbackCredentialPrepare(ctx context.Context, completed []completedCredentialMaterialization) error {
	var rollbackErr error
	for index := len(completed) - 1; index >= 0; index-- {
		entry := completed[index]
		request := credentialMaterializeRequest(entry.SessionID, entry.Item.Authorized, entry.Item.Reference)
		request.VersionID = entry.Materialization.VersionID
		if entry.Materialization.PreviousVersionID == "" {
			if err := h.cleanupCredentialVersion(ctx, request); err != nil {
				rollbackErr = err
			}
			continue
		}
		previous, err := h.store.CredentialVersionByID(request.CredentialID, entry.Materialization.PreviousVersionID)
		if err != nil {
			rollbackErr = err
			continue
		}
		if _, _, err := h.materializeCredentialVersion(
			ctx, request.Cluster, request.Instance, request.SessionID,
			entry.Item.Authorized.Resource, previous, request.VersionID,
			request.GrantID, entry.Item.Reference,
		); err != nil {
			rollbackErr = err
		}
	}
	return rollbackErr
}

func (h *GatewayHub) materializeCredentialVersion(
	ctx context.Context,
	cluster string,
	instance string,
	sessionID string,
	resource CredentialResource,
	version CredentialVersion,
	expectedPreviousVersionID string,
	grantID string,
	ref CredentialPlanReference,
) (CredentialMaterialization, CredentialMaterializeRequest, error) {
	return h.materializeCredentialVersionAttempt(
		ctx, cluster, instance, sessionID, resource, version,
		expectedPreviousVersionID, grantID, ref, true,
	)
}

func (h *GatewayHub) materializeCredentialVersionAttempt(
	ctx context.Context,
	cluster string,
	instance string,
	sessionID string,
	resource CredentialResource,
	version CredentialVersion,
	expectedPreviousVersionID string,
	grantID string,
	ref CredentialPlanReference,
	recoverBindingFailure bool,
) (CredentialMaterialization, CredentialMaterializeRequest, error) {
	payload, err := h.store.ResolveCredentialPayload(resource.ID, version.ID)
	if err != nil {
		return CredentialMaterialization{}, CredentialMaterializeRequest{}, err
	}
	defer clear(payload)
	requiredKeys := append([]string(nil), ref.Scope.RequiredKeys...)
	if len(requiredKeys) == 0 {
		requiredKeys, err = credentialRequiredKeys(resource.Type, payload)
	}
	if err != nil {
		return CredentialMaterialization{}, CredentialMaterializeRequest{}, err
	}
	request := CredentialMaterializeRequest{
		SessionID: sessionID, Cluster: cluster, Instance: instance,
		CredentialID: resource.ID, GrantID: grantID, VersionID: version.ID,
		CredentialType: resource.Type, Scope: ref.Scope, RequiredKeys: requiredKeys,
		Payload: json.RawMessage(payload),
	}
	intentRequest := request
	intentRequest.Payload = nil
	intent, err := h.store.RecordCredentialMaterializationIntent(intentRequest)
	if err != nil {
		return CredentialMaterialization{}, intentRequest, err
	}
	toolArgs, err := credentialToolArguments(request)
	if err != nil {
		_ = h.store.MarkCredentialMaterializationUnknown(intent.ID, "credential_request_encode_failed")
		return CredentialMaterialization{}, intentRequest, err
	}
	resultText, err := h.RunInternalToolCall(ctx, cluster, instance, "doops_credential_materialize", toolArgs)
	if err != nil {
		_ = h.store.MarkCredentialMaterializationUnknown(intent.ID, credentialOperationErrorCategory(err))
		return CredentialMaterialization{}, intentRequest, err
	}
	var materialization CredentialMaterialization
	if err := json.Unmarshal([]byte(strings.TrimSpace(resultText)), &materialization); err != nil {
		_ = h.store.MarkCredentialMaterializationUnknown(intent.ID, "credential_materialization_result_invalid")
		return CredentialMaterialization{}, intentRequest, err
	}
	if materialization.CredentialID != resource.ID || materialization.VersionID != version.ID ||
		materialization.Namespace != ref.Scope.Namespace || materialization.Status != "verified" {
		_ = h.store.MarkCredentialMaterializationUnknown(intent.ID, "credential_materialization_result_mismatch")
		return CredentialMaterialization{}, intentRequest, ErrCredentialPayloadInvalid
	}
	materialization.GrantID = grantID
	if err := h.validateCredentialMaterializationResult(
		materialization, resource, grantID, ref, requiredKeys, expectedPreviousVersionID,
	); err != nil {
		_ = h.store.MarkCredentialMaterializationUnknown(intent.ID, err.Error())
		if recoverBindingFailure {
			if recoveryErr := h.recoverRejectedCredentialMaterialization(
				ctx, intentRequest, resource, version, expectedPreviousVersionID, grantID, ref,
			); recoveryErr != nil {
				return CredentialMaterialization{}, intentRequest, errors.New("credential_materialization_outcome_unknown")
			}
		}
		return CredentialMaterialization{}, intentRequest, err
	}
	if err := h.store.CompleteCredentialMaterialization(intent.ID, materialization); err != nil {
		_ = h.store.MarkCredentialMaterializationUnknown(intent.ID, "credential_materialization_inventory_failed")
		return CredentialMaterialization{}, intentRequest, err
	}
	return materialization, intentRequest, nil
}

func credentialMaterializeRequest(
	sessionID string,
	authorized AuthorizedCredentialUse,
	ref CredentialPlanReference,
) CredentialMaterializeRequest {
	return CredentialMaterializeRequest{
		SessionID: sessionID, Cluster: authorized.Grant.Scope.Cluster,
		Instance: authorized.Grant.Scope.Instance, CredentialID: authorized.Resource.ID,
		GrantID: authorized.Grant.ID, VersionID: authorized.Version.ID,
		CredentialType: authorized.Resource.Type, Scope: ref.Scope,
		RequiredKeys: append([]string(nil), ref.Scope.RequiredKeys...),
	}
}

func (h *GatewayHub) recoverRejectedCredentialMaterialization(
	ctx context.Context,
	request CredentialMaterializeRequest,
	resource CredentialResource,
	rejectedVersion CredentialVersion,
	expectedPreviousVersionID string,
	grantID string,
	ref CredentialPlanReference,
) error {
	if expectedPreviousVersionID == "" {
		return h.cleanupCredentialVersion(ctx, request)
	}
	previous, err := h.store.CredentialVersionByID(resource.ID, expectedPreviousVersionID)
	if err != nil {
		return err
	}
	if _, _, err := h.materializeCredentialVersionAttempt(
		ctx, request.Cluster, request.Instance, request.SessionID, resource, previous,
		rejectedVersion.ID, grantID, ref, false,
	); err != nil {
		return err
	}
	return h.store.MarkCredentialMaterializationCleaned(request)
}

func (h *GatewayHub) validateCredentialMaterializationResult(
	materialization CredentialMaterialization,
	resource CredentialResource,
	grantID string,
	ref CredentialPlanReference,
	requiredKeys []string,
	expectedPreviousVersionID string,
) error {
	if err := validateCredentialMaterializationMetadata(materialization); err != nil {
		return err
	}
	if materialization.ResourceName != credentialSecretName(resource.ID, grantID) ||
		materialization.ResourceVersion == "" || materialization.ErrorCategory != "" ||
		!sameCredentialKeys(materialization.Keys, requiredKeys) {
		return errors.New("credential_materialization_metadata_invalid")
	}
	expectedSecretType := "Opaque"
	switch resource.Type {
	case CredentialTypeRegistry:
		expectedSecretType = "kubernetes.io/dockerconfigjson"
	case CredentialTypeTLS:
		expectedSecretType = "kubernetes.io/tls"
	case CredentialTypeHelmRepository:
		expectedSecretType = ""
	case CredentialTypeOpaque, CredentialTypeGitToken:
	default:
		return errors.New("credential_materialization_metadata_invalid")
	}
	if materialization.SecretType != expectedSecretType {
		return errors.New("credential_materialization_metadata_invalid")
	}
	if resource.Type == CredentialTypeHelmRepository {
		if materialization.ContextKind != "helm-native" || !validNativeHelmSocketPath(materialization.ContextSocket) {
			return errors.New("credential_materialization_metadata_invalid")
		}
	} else if materialization.ContextKind != "" || materialization.ContextSocket != "" {
		return errors.New("credential_materialization_metadata_invalid")
	}
	switch resource.Type {
	case CredentialTypeRegistry, CredentialTypeHelmRepository, CredentialTypeGitToken:
		if materialization.Digest == "" || materialization.Fingerprint != "" || materialization.ExpiresAt != "" {
			return errors.New("credential_materialization_metadata_invalid")
		}
	case CredentialTypeTLS:
		if materialization.Digest != "" || materialization.Fingerprint == "" || materialization.ExpiresAt == "" {
			return errors.New("credential_materialization_metadata_invalid")
		}
	case CredentialTypeOpaque:
		if materialization.Digest != "" || materialization.Fingerprint != "" || materialization.ExpiresAt != "" {
			return errors.New("credential_materialization_metadata_invalid")
		}
	}
	if materialization.PreviousVersionID != "" &&
		materialization.PreviousVersionID != expectedPreviousVersionID {
		return errors.New("credential_materialization_metadata_invalid")
	}
	return nil
}

func credentialToolArguments(request CredentialMaterializeRequest) (map[string]any, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	var arguments map[string]any
	if err := json.Unmarshal(encoded, &arguments); err != nil {
		return nil, err
	}
	return arguments, nil
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
	body.Scope = normalizeCredentialGrantScope(body.Scope)
	if body.VersionID == "" || validateSession(body.SessionID) != nil ||
		validateCredentialGrantScope(body.Scope) != nil {
		http.Error(w, "invalid credential verify request", http.StatusBadRequest)
		return
	}
	if !h.store.CanUseTargetCredentials(auth.UserID, body.Scope.Cluster, body.Scope.Instance) {
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
		!h.store.CanUseTargetCredentials(auth.UserID, body.Scope.Cluster, body.Scope.Instance) {
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
	evidence := make([]credentialVerificationEvidence, 0, len(grants))
	verifications := make([]CredentialVerification, 0, len(grants))
	for _, grant := range grants {
		ref := CredentialPlanReference{Name: resource.Name, Scope: body.Scope}
		expectedPreviousVersionID := ""
		if activeErr == nil {
			expectedPreviousVersionID = active.ID
		}
		observed, request, err := h.materializeCredentialVersion(
			r.Context(), body.Scope.Cluster, body.Scope.Instance, body.SessionID,
			resource, version, expectedPreviousVersionID, grant.ID, ref,
		)
		if err != nil {
			h.writeCredentialVerificationError(w, auditID, credentialOperationErrorCategory(err), http.StatusBadGateway)
			return
		}
		if activeErr == nil {
			if _, _, err := h.materializeCredentialVersion(
				r.Context(), body.Scope.Cluster, body.Scope.Instance, body.SessionID,
				resource, active, version.ID, grant.ID, ref,
			); err != nil {
				h.writeCredentialVerificationError(w, auditID, "credential_verification_outcome_unknown", http.StatusBadGateway)
				return
			}
			if err := h.store.MarkCredentialMaterializationCleaned(request); err != nil {
				h.writeCredentialVerificationError(w, auditID, "credential_verification_outcome_unknown", http.StatusBadGateway)
				return
			}
		} else if err := h.cleanupCredentialVersion(r.Context(), request); err != nil {
			h.writeCredentialVerificationError(w, auditID, "credential_verification_outcome_unknown", http.StatusBadGateway)
			return
		}
		verifications = append(verifications, CredentialVerification{
			CredentialID: resource.ID, VersionID: version.ID, GrantID: grant.ID,
			Use: body.Scope.Use, Request: request, Evidence: observed,
		})
		evidence = append(evidence, credentialVerificationEvidence{
			Status: observed.Status, ResourceVersion: observed.ResourceVersion,
			Digest: observed.Digest, ErrorCategory: observed.ErrorCategory,
		})
	}
	if err := h.store.RecordCredentialVerificationsAudited(verifications, auditID, AuditFinish{
		Status: "success", Cluster: body.Scope.Cluster, Instance: body.Scope.Instance,
		Scope: &body.Scope, VersionID: version.ID, EndedAt: time.Now().UTC(),
	}); err != nil {
		h.writeCredentialVerificationError(w, auditID, "credential_verification_persist_failed", http.StatusInternalServerError)
		return
	}
	writeJSONHTTP(w, map[string]any{"status": "verified", "evidence": evidence})
}

func (h *GatewayHub) writeCredentialVerificationError(w http.ResponseWriter, auditID int64, category string, status int) {
	if !h.completeCredentialAudit(w, auditID, "error", category) {
		return
	}
	http.Error(w, category, status)
}

func (h *GatewayHub) cleanupCredentialVersion(ctx context.Context, request CredentialMaterializeRequest) error {
	arguments := map[string]any{
		"session_id": request.SessionID, "credential_id": request.CredentialID,
		"grant_id": request.GrantID, "version_id": request.VersionID,
		"namespace": request.Scope.Namespace, "workload": request.Scope.Workload,
	}
	if _, err := h.RunInternalToolCall(ctx, request.Cluster, request.Instance, "doops_credential_cleanup", arguments); err != nil {
		return err
	}
	return h.store.MarkCredentialMaterializationCleaned(request)
}

func (h *GatewayHub) cleanupCredentialMaterializationContext(
	ctx context.Context,
	request CredentialMaterializeRequest,
) error {
	arguments := map[string]any{
		"session_id": request.SessionID, "credential_id": request.CredentialID,
		"grant_id": request.GrantID, "version_id": request.VersionID, "any_version": true,
		"namespace": request.Scope.Namespace, "workload": request.Scope.Workload,
	}
	if _, err := h.RunInternalToolCall(ctx, request.Cluster, request.Instance, "doops_credential_cleanup", arguments); err != nil {
		return err
	}
	return h.store.MarkCredentialMaterializationContextCleaned(request)
}

func (h *GatewayHub) cleanupCredentialGrantMaterializations(
	ctx context.Context,
	credentialID string,
	grantID string,
) error {
	materializations, err := h.store.CredentialMaterializations(credentialID, "", grantID)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{})
	for _, materialization := range materializations {
		key := credentialMaterializationContextKey(materialization.Request)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if err := h.cleanupCredentialMaterializationContext(ctx, materialization.Request); err != nil {
			return err
		}
	}
	return nil
}

func credentialReferenceFromRequest(request CredentialMaterializeRequest) CredentialPlanReference {
	return CredentialPlanReference{Scope: request.Scope}
}

func credentialMaterializationContextKey(request CredentialMaterializeRequest) string {
	scopeJSON, _ := json.Marshal(normalizeCredentialGrantScope(request.Scope))
	return strings.Join([]string{
		request.Cluster, request.Instance, request.GrantID, string(scopeJSON),
	}, "\x00")
}

func credentialOperationErrorCategory(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, ErrCredentialVersionNotFound):
		return "credential_version_not_found"
	case errors.Is(err, ErrCredentialGrantDenied):
		return "credential_grant_denied"
	case errors.Is(err, ErrCredentialForbidden):
		return "credential_forbidden"
	case errors.Is(err, ErrCredentialRevoked):
		return "credential_revoked"
	case errors.Is(err, ErrCredentialPayloadInvalid):
		return "credential_payload_invalid"
	case errors.Is(err, ErrCredentialAuditUnavailable):
		return "credential_audit_unavailable"
	}
	category := sanitizeCredentialCategory(err.Error())
	if category != "credential_operation_failed" {
		return category
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "verification_transport_failed"
	case strings.Contains(strings.ToLower(err.Error()), "target offline"):
		return "target_offline"
	default:
		return "credential_operation_failed"
	}
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

func credentialRequiredKeys(credentialType CredentialType, payload json.RawMessage) ([]string, error) {
	switch credentialType {
	case CredentialTypeRegistry:
		return []string{".dockerconfigjson"}, nil
	case CredentialTypeTLS:
		return []string{"tls.crt", "tls.key"}, nil
	case CredentialTypeHelmRepository:
		return nil, nil
	case CredentialTypeGitToken:
		return []string{"password", "url", "username"}, nil
	case CredentialTypeOpaque:
		var value struct {
			Data map[string]string `json:"data"`
		}
		if err := json.Unmarshal(payload, &value); err != nil {
			return nil, ErrCredentialPayloadInvalid
		}
		return normalizedCredentialKeys(mapKeys(value.Data)), nil
	default:
		return nil, ErrCredentialPayloadInvalid
	}
}
