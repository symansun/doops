package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
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
}

type CredentialBinding struct {
	Use                CredentialUse
	Namespace          string
	Workload           CredentialWorkloadRef
	RegistryRepository string
	RegistryReference  string
	RequiredKeys       []string
}

type credentialVerifyHTTPBody struct {
	VersionID          string                `json:"version_id"`
	GrantID            string                `json:"grant_id"`
	Cluster            string                `json:"cluster"`
	Instance           string                `json:"instance"`
	SessionID          string                `json:"session_id"`
	Use                CredentialUse         `json:"use"`
	Namespace          string                `json:"namespace"`
	Workload           CredentialWorkloadRef `json:"workload,omitempty"`
	RegistryRepository string                `json:"registry_repository,omitempty"`
	RegistryReference  string                `json:"registry_reference,omitempty"`
	RequiredKeys       []string              `json:"required_keys,omitempty"`
}

type credentialVerificationEvidence struct {
	Status          string `json:"status"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
	Digest          string `json:"digest,omitempty"`
	ErrorCategory   string `json:"errorCategory,omitempty"`
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
	if materialization.ErrorCategory != "" && !validCredentialErrorCategory(materialization.ErrorCategory) {
		return errors.New("credential_materialization_metadata_invalid")
	}
	for _, key := range materialization.Keys {
		if !validCredentialMetadataToken(key, 253) ||
			credentialScalarContainsInlineMaterial(key) {
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

func validCredentialSHA256Digest(value string) bool {
	const prefix = "sha256:"
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, prefix) && validLowerHex(strings.TrimPrefix(value, prefix), 64)
}

func credentialScalarContainsInlineMaterial(value string) bool {
	for _, pattern := range inlineSecretPatterns {
		if pattern.MatchString(strings.TrimSpace(value)) {
			return true
		}
	}
	return false
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
	ref CredentialBinding,
) (CredentialMaterialization, CredentialMaterializeRequest, error) {
	return h.materializeCredentialVersionAttempt(
		ctx,
		cluster,
		instance,
		sessionID,
		resource,
		version,
		expectedPreviousVersionID,
		grantID,
		ref,
		true,
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
	ref CredentialBinding,
	recoverBindingFailure bool,
) (CredentialMaterialization, CredentialMaterializeRequest, error) {
	payload, err := h.store.ResolveCredentialPayload(resource.ID, version.ID)
	if err != nil {
		return CredentialMaterialization{}, CredentialMaterializeRequest{}, err
	}
	defer clear(payload)
	requiredKeys := append([]string(nil), ref.RequiredKeys...)
	if len(requiredKeys) == 0 {
		requiredKeys, err = credentialRequiredKeys(resource.Type, payload)
	}
	if err != nil {
		return CredentialMaterialization{}, CredentialMaterializeRequest{}, err
	}
	request := CredentialMaterializeRequest{
		SessionID:          sessionID,
		Cluster:            cluster,
		Instance:           instance,
		CredentialID:       resource.ID,
		GrantID:            grantID,
		VersionID:          version.ID,
		CredentialType:     resource.Type,
		Use:                ref.Use,
		Namespace:          ref.Namespace,
		Workload:           ref.Workload,
		RegistryRepository: ref.RegistryRepository,
		RegistryReference:  ref.RegistryReference,
		RequiredKeys:       requiredKeys,
		Payload:            json.RawMessage(payload),
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
	if materialization.CredentialID != resource.ID ||
		materialization.VersionID != version.ID ||
		materialization.Namespace != ref.Namespace ||
		materialization.Status != "verified" {
		_ = h.store.MarkCredentialMaterializationUnknown(intent.ID, "credential_materialization_result_mismatch")
		return CredentialMaterialization{}, intentRequest, ErrCredentialPayloadInvalid
	}
	materialization.GrantID = grantID
	if err := h.validateCredentialMaterializationResult(
		materialization,
		resource,
		grantID,
		ref,
		requiredKeys,
		expectedPreviousVersionID,
	); err != nil {
		_ = h.store.MarkCredentialMaterializationUnknown(intent.ID, err.Error())
		if recoverBindingFailure {
			if recoveryErr := h.recoverRejectedCredentialMaterialization(
				ctx,
				intentRequest,
				resource,
				version,
				expectedPreviousVersionID,
				grantID,
				ref,
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

func (h *GatewayHub) recoverRejectedCredentialMaterialization(
	ctx context.Context,
	request CredentialMaterializeRequest,
	resource CredentialResource,
	rejectedVersion CredentialVersion,
	expectedPreviousVersionID string,
	grantID string,
	ref CredentialBinding,
) error {
	if expectedPreviousVersionID == "" {
		return h.cleanupCredentialVersion(ctx, request)
	}
	previous, err := h.store.CredentialVersionByID(resource.ID, expectedPreviousVersionID)
	if err != nil {
		return err
	}
	if _, _, err := h.materializeCredentialVersionAttempt(
		ctx,
		request.Cluster,
		request.Instance,
		request.SessionID,
		resource,
		previous,
		rejectedVersion.ID,
		grantID,
		ref,
		false,
	); err != nil {
		return err
	}
	return h.store.MarkCredentialMaterializationCleaned(request)
}

func (h *GatewayHub) validateCredentialMaterializationResult(
	materialization CredentialMaterialization,
	resource CredentialResource,
	grantID string,
	ref CredentialBinding,
	requiredKeys []string,
	expectedPreviousVersionID string,
) error {
	if err := validateCredentialMaterializationMetadata(materialization); err != nil {
		return err
	}
	if materialization.ResourceName != credentialSecretName(resource.ID, grantID) ||
		materialization.ResourceVersion == "" ||
		materialization.ErrorCategory != "" ||
		!sameCredentialKeys(materialization.Keys, requiredKeys) {
		return errors.New("credential_materialization_metadata_invalid")
	}

	var expectedSecretType string
	switch resource.Type {
	case CredentialTypeRegistry:
		expectedSecretType = "kubernetes.io/dockerconfigjson"
	case CredentialTypeTLS:
		expectedSecretType = "kubernetes.io/tls"
	case CredentialTypeOpaque, CredentialTypeHelmRepository, CredentialTypeGitToken:
		expectedSecretType = "Opaque"
	default:
		return errors.New("credential_materialization_metadata_invalid")
	}
	if materialization.SecretType != expectedSecretType {
		return errors.New("credential_materialization_metadata_invalid")
	}

	switch resource.Type {
	case CredentialTypeRegistry, CredentialTypeHelmRepository, CredentialTypeGitToken:
		if materialization.Digest == "" ||
			materialization.Fingerprint != "" ||
			materialization.ExpiresAt != "" {
			return errors.New("credential_materialization_metadata_invalid")
		}
	case CredentialTypeTLS:
		if materialization.Digest != "" ||
			materialization.Fingerprint == "" ||
			materialization.ExpiresAt == "" {
			return errors.New("credential_materialization_metadata_invalid")
		}
	case CredentialTypeOpaque:
		if materialization.Digest != "" ||
			materialization.Fingerprint != "" ||
			materialization.ExpiresAt != "" {
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
	body.GrantID = strings.TrimSpace(body.GrantID)
	body.Cluster = strings.TrimSpace(body.Cluster)
	body.Instance = strings.TrimSpace(body.Instance)
	body.SessionID = strings.TrimSpace(body.SessionID)
	body.Namespace = strings.TrimSpace(body.Namespace)
	body.RegistryRepository = strings.TrimSpace(body.RegistryRepository)
	body.RegistryReference = strings.TrimSpace(body.RegistryReference)
	if body.VersionID == "" || body.GrantID == "" || body.Cluster == "" ||
		body.Instance == "" || body.SessionID == "" || body.Namespace == "" ||
		!validCredentialUse(body.Use) {
		http.Error(w, "invalid credential verify request", http.StatusBadRequest)
		return
	}
	if !h.store.UserCan(auth.UserID, body.Cluster, body.Instance, ActionCredentialUse) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	grant, err := h.store.credentialGrantByID(resource.ID, body.GrantID)
	if err != nil || grant.State != CredentialStateActive ||
		grant.Cluster != body.Cluster || grant.Instance != body.Instance ||
		grant.Namespace != body.Namespace || !containsCredentialUse(grant.Uses, body.Use) {
		http.Error(w, "credential_grant_denied", http.StatusForbidden)
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
	binding := CredentialBinding{
		Use:                body.Use,
		Namespace:          body.Namespace,
		Workload:           body.Workload,
		RegistryRepository: body.RegistryRepository,
		RegistryReference:  body.RegistryReference,
		RequiredKeys:       append([]string(nil), body.RequiredKeys...),
	}
	h.credentialMutationMu.Lock()
	defer h.credentialMutationMu.Unlock()
	active, activeErr := h.store.ActiveCredentialVersion(resource.ID)
	if activeErr != nil && !errors.Is(activeErr, ErrCredentialVersionNotFound) {
		http.Error(w, credentialErrorCategory(activeErr), credentialHTTPStatus(activeErr))
		return
	}
	expectedPreviousVersionID := ""
	if activeErr == nil {
		expectedPreviousVersionID = active.ID
	}
	observed, request, err := h.materializeCredentialVersion(
		r.Context(), body.Cluster, body.Instance, body.SessionID,
		resource, version, expectedPreviousVersionID, grant.ID, binding,
	)
	if err != nil {
		http.Error(w, credentialOperationErrorCategory(err), http.StatusBadGateway)
		return
	}
	if activeErr == nil {
		if _, _, err := h.materializeCredentialVersion(
			r.Context(), body.Cluster, body.Instance, body.SessionID,
			resource, active, version.ID, grant.ID, binding,
		); err != nil {
			http.Error(w, "credential_verification_outcome_unknown", http.StatusBadGateway)
			return
		}
		if err := h.store.MarkCredentialMaterializationCleaned(request); err != nil {
			http.Error(w, "credential_verification_outcome_unknown", http.StatusBadGateway)
			return
		}
	} else if err := h.cleanupCredentialVersion(r.Context(), request); err != nil {
		http.Error(w, "credential_verification_outcome_unknown", http.StatusBadGateway)
		return
	}
	verification := CredentialVerification{
		CredentialID: resource.ID,
		VersionID:    version.ID,
		GrantID:      grant.ID,
		Use:          body.Use,
		Request:      request,
		Evidence:     observed,
	}
	if err := h.store.RecordCredentialVerification(verification); err != nil {
		http.Error(w, "credential_verification_persist_failed", http.StatusInternalServerError)
		return
	}
	writeJSONHTTP(w, map[string]any{
		"status": "verified",
		"evidence": credentialVerificationEvidence{
			Status:          observed.Status,
			ResourceVersion: observed.ResourceVersion,
			Digest:          observed.Digest,
			ErrorCategory:   observed.ErrorCategory,
		},
	})
}

func (h *GatewayHub) cleanupCredentialVersion(ctx context.Context, request CredentialMaterializeRequest) error {
	arguments := map[string]any{
		"session_id":    request.SessionID,
		"credential_id": request.CredentialID,
		"grant_id":      request.GrantID,
		"version_id":    request.VersionID,
		"namespace":     request.Namespace,
		"workload":      request.Workload,
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
		"session_id":    request.SessionID,
		"credential_id": request.CredentialID,
		"grant_id":      request.GrantID,
		"version_id":    request.VersionID,
		"any_version":   true,
		"namespace":     request.Namespace,
		"workload":      request.Workload,
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

func credentialReferenceFromRequest(request CredentialMaterializeRequest) CredentialBinding {
	return CredentialBinding{
		Use:                request.Use,
		Namespace:          request.Namespace,
		Workload:           request.Workload,
		RegistryRepository: request.RegistryRepository,
		RegistryReference:  request.RegistryReference,
	}
}

func credentialMaterializationContextKey(request CredentialMaterializeRequest) string {
	return strings.Join([]string{
		request.Cluster,
		request.Instance,
		request.GrantID,
		request.Namespace,
		string(request.Use),
		request.Workload.Kind,
		request.Workload.Name,
		request.RegistryRepository,
		request.RegistryReference,
	}, "\x00")
}

func credentialOperationErrorCategory(err error) string {
	if err == nil {
		return ""
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
	for index := len(applied) - 1; index >= 0; index-- {
		request := applied[index].Request
		if previousErr == nil {
			if _, _, err := h.materializeCredentialVersion(
				ctx,
				request.Cluster,
				request.Instance,
				request.SessionID,
				resource,
				previous,
				request.VersionID,
				request.GrantID,
				credentialReferenceFromRequest(request),
			); err != nil {
				return err
			}
			continue
		}
		if err := h.cleanupCredentialVersion(ctx, request); err != nil {
			return err
		}
	}
	return nil
}

func credentialRequiredKeys(credentialType CredentialType, payload json.RawMessage) ([]string, error) {
	switch credentialType {
	case CredentialTypeRegistry:
		return []string{".dockerconfigjson"}, nil
	case CredentialTypeTLS:
		return []string{"tls.crt", "tls.key"}, nil
	case CredentialTypeHelmRepository, CredentialTypeGitToken:
		return []string{"password", "url", "username"}, nil
	case CredentialTypeOpaque:
		var value struct {
			Data map[string]string `json:"data"`
		}
		if err := decodeStrictCredentialPayload(payload, &value); err != nil || len(value.Data) == 0 {
			return nil, ErrCredentialPayloadInvalid
		}
		return mapKeys(value.Data), nil
	default:
		return nil, ErrCredentialPayloadInvalid
	}
}
