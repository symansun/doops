package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrCredentialTargetUnenrolled refuses to materialize credential payload for
// a live target whose Agent session did not authenticate with a target
// enrollment token. Such a session is not this target and must never receive
// credential material.
var ErrCredentialTargetUnenrolled = errors.New("credential_target_unenrolled")

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
		switch materialization.ContextKind {
		case "helm-native":
			if !validNativeHelmSocketPath(materialization.ContextSocket) || materialization.SecretType != "" {
				return errors.New("credential_materialization_metadata_invalid")
			}
		case runtimeOpaqueContextKind:
			if !validRuntimeOpaqueEnvPath(materialization.ContextSocket) || materialization.SecretType != "" {
				return errors.New("credential_materialization_metadata_invalid")
			}
		default:
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

// materializeOption adjusts one materialization without changing the call
// sites that do not need it.
type materializeOption func(*CredentialMaterializeRequest)

// materializeAuthfileOnly asks the Agent to restore only its OCI authfile and
// leave the Kubernetes Secret alone.
func materializeAuthfileOnly(request *CredentialMaterializeRequest) {
	request.AuthfileOnly = true
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
	options ...materializeOption,
) (CredentialMaterialization, CredentialMaterializeRequest, error) {
	return h.materializeCredentialVersionAttempt(
		ctx, cluster, instance, sessionID, resource, version,
		expectedPreviousVersionID, grantID, ref, true, options...,
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
	options ...materializeOption,
) (CredentialMaterialization, CredentialMaterializeRequest, error) {
	// Gate before decryption: an unenrolled live session never causes payload
	// decryption, intentional metadata, or a send.
	if agent := h.getAgent(cluster, instance); agent != nil && agent.TokenID == "" && !h.opts.AllowUnenrolledCredentialDelivery {
		return CredentialMaterialization{}, CredentialMaterializeRequest{}, fmt.Errorf(
			"%w: target %s", ErrCredentialTargetUnenrolled, tunnelKey(cluster, instance))
	}
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
	for _, option := range options {
		if option != nil {
			option(&request)
		}
	}
	intentRequest := request
	intentRequest.Payload = nil
	intent, err := h.store.RecordCredentialMaterializationIntent(intentRequest)
	if err != nil {
		return CredentialMaterialization{}, intentRequest, err
	}
	toolArgs, err := credentialToolArguments(request)
	if err != nil {
		_ = h.store.MarkCredentialMaterializationFailed(intent, "credential_request_encode_failed")
		return CredentialMaterialization{}, intentRequest, err
	}
	resultText, err := h.RunInternalToolCall(ctx, cluster, instance, "doops_credential_materialize", toolArgs)
	if err != nil {
		_ = h.store.MarkCredentialMaterializationFailed(intent, credentialOperationErrorCategory(err))
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
	if request.AuthfileOnly || request.RuntimeEnvOnly {
		if request.RuntimeEnvOnly {
			_ = h.cleanupRuntimeOpaqueVersion(ctx, request)
		}
		return nil
	}
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
	case CredentialTypeHelmRepository, CredentialTypeModelProvider:
		expectedSecretType = ""
	case CredentialTypeOpaque, CredentialTypeGitToken:
		if resource.Type == CredentialTypeOpaque && materialization.ContextKind == runtimeOpaqueContextKind {
			expectedSecretType = ""
		}
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
	} else if resource.Type == CredentialTypeOpaque && materialization.ContextKind == runtimeOpaqueContextKind {
		if !validRuntimeOpaqueEnvPath(materialization.ContextSocket) {
			return errors.New("credential_materialization_metadata_invalid")
		}
	} else if materialization.ContextKind != "" || materialization.ContextSocket != "" {
		return errors.New("credential_materialization_metadata_invalid")
	}
	switch resource.Type {
	case CredentialTypeRegistry, CredentialTypeHelmRepository, CredentialTypeGitToken, CredentialTypeModelProvider:
		if resource.Type != CredentialTypeRegistry && materialization.Digest == "" {
			return errors.New("credential_materialization_metadata_invalid")
		}
		if resource.Type == CredentialTypeRegistry &&
			(ref.Scope.ImageRepository != "" || ref.Scope.ImageDigest != "") && materialization.Digest == "" {
			return errors.New("credential_materialization_metadata_invalid")
		}
		if materialization.Fingerprint != "" || materialization.ExpiresAt != "" {
			return errors.New("credential_materialization_metadata_invalid")
		}
	case CredentialTypeTLS:
		if materialization.Digest != "" || materialization.Fingerprint == "" || materialization.ExpiresAt == "" {
			return errors.New("credential_materialization_metadata_invalid")
		}
	case CredentialTypeOpaque:
		if materialization.ContextKind == runtimeOpaqueContextKind {
			if materialization.Digest == "" || materialization.Fingerprint != "" || materialization.ExpiresAt != "" {
				return errors.New("credential_materialization_metadata_invalid")
			}
		} else if materialization.Digest != "" || materialization.Fingerprint != "" || materialization.ExpiresAt != "" {
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
	case errors.Is(err, ErrCredentialTargetUnenrolled):
		return "credential_target_unenrolled"
	case errors.Is(err, ErrCredentialVersionNotFound):
		return "credential_version_not_found"
	case errors.Is(err, ErrCredentialGrantDenied):
		category, _ := credentialGrantDenialCategory(err)
		return category
	case errors.Is(err, ErrCredentialForbidden):
		return "credential_forbidden"
	case errors.Is(err, ErrCredentialRevoked):
		return "credential_revoked"
	case errors.Is(err, ErrCredentialPayloadInvalid):
		return "credential_payload_invalid"
	case errors.Is(err, ErrCredentialAuditUnavailable):
		return "credential_audit_unavailable"
	case errors.Is(err, ErrSecretKeyMismatch):
		return "credential_key_mismatch"
	case errors.Is(err, ErrCredentialCiphertextInvalid):
		return "credential_ciphertext_invalid"
	}
	category := sanitizeCredentialCategory(err.Error())
	if category != "credential_operation_failed" {
		return category
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "verification_transport_failed"
	case errors.Is(err, errExecutionOutcomeUnknown):
		// The call may have reached the Agent, so its text is not evidence
		// that the target was offline before dispatch.
		return "credential_operation_failed"
	case strings.Contains(strings.ToLower(err.Error()), "target offline"):
		return "target_offline"
	default:
		return "credential_operation_failed"
	}
}

func credentialRequiredKeys(credentialType CredentialType, payload json.RawMessage) ([]string, error) {
	switch credentialType {
	case CredentialTypeRegistry:
		return []string{".dockerconfigjson"}, nil
	case CredentialTypeTLS:
		return []string{"tls.crt", "tls.key"}, nil
	case CredentialTypeHelmRepository, CredentialTypeModelProvider:
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
