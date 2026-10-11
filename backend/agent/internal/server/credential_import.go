package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// This request contains references only. Secret values travel on the private
// Agent control channel, never through a public tool result or shell stream.
type credentialSecretImportRequest struct {
	SessionID     string   `json:"session_id"`
	Cluster       string   `json:"cluster"`
	Instance      string   `json:"instance"`
	Namespace     string   `json:"namespace"`
	SecretName    string   `json:"secret_name"`
	Keys          []string `json:"keys"`
	ConnectionURI string   `json:"connection_uri,omitempty"`
	PasswordKey   string   `json:"password_key,omitempty"`
}

type credentialSecretImportResult struct {
	Payload         json.RawMessage `json:"payload"`
	UID             string          `json:"uid"`
	ResourceVersion string          `json:"resource_version"`
}

func validateCredentialSecretImport(req credentialSecretImportRequest) error {
	if validateSession(req.SessionID) != nil || req.Cluster == "" || req.Instance == "" ||
		!validKubernetesCredentialName(req.Namespace) || !validKubernetesCredentialName(req.SecretName) ||
		len(req.Keys) == 0 || len(req.Keys) > 64 {
		return errors.New("credential_import_request_invalid")
	}
	seen := make(map[string]bool, len(req.Keys))
	for _, key := range req.Keys {
		if len(key) == 0 || len(key) > 253 || seen[key] {
			return errors.New("credential_import_keys_invalid")
		}
		for _, char := range key {
			if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
				char >= '0' && char <= '9' || char == '.' || char == '_' || char == '-') {
				return errors.New("credential_import_keys_invalid")
			}
		}
		seen[key] = true
	}
	if req.ConnectionURI == "" && req.PasswordKey == "" {
		return nil
	}
	u, err := url.Parse(req.ConnectionURI)
	if err != nil || u == nil || (u.Scheme != "postgres" && u.Scheme != "postgresql" && u.Scheme != "mongodb") ||
		u.Hostname() == "" || u.User == nil || u.User.Username() == "" ||
		u.Path == "" || u.Path == "/" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		len(req.Keys) != 1 || req.Keys[0] != req.PasswordKey {
		return errors.New("credential_import_connection_invalid")
	}
	if _, hasPassword := u.User.Password(); hasPassword {
		return errors.New("credential_import_connection_invalid")
	}
	return nil
}

func handleCredentialSecretImportTool(ctx context.Context, raw json.RawMessage) (string, error) {
	var req credentialSecretImportRequest
	if decodeStrictCredentialPayload(raw, &req) != nil {
		return "", errors.New("credential_import_request_invalid")
	}
	if err := validateCredentialSecretImport(req); err != nil {
		return "", err
	}
	secret, err := inspectCredentialSecret(ctx, req.Namespace, req.SecretName)
	defer clear(secret.Data)
	if err != nil {
		return "", errors.New("credential_import_source_unavailable")
	}
	if secret.SecretType != "Opaque" {
		return "", errors.New("credential_import_source_type_invalid")
	}
	data := make(map[string]string, len(req.Keys))
	defer clear(data)
	for _, key := range req.Keys {
		value, exists := secret.Data[key]
		if !exists || value == "" {
			return "", errors.New("credential_import_key_missing")
		}
		data[key] = value
	}
	if req.ConnectionURI != "" {
		u, _ := url.Parse(req.ConnectionURI)
		u.User = url.UserPassword(u.User.Username(), data[req.PasswordKey])
		clear(data)
		data["connection_uri"] = u.String()
	}
	payload, err := json.Marshal(map[string]any{"data": data})
	if err != nil || len(payload) > 1<<20 {
		return "", ErrCredentialPayloadInvalid
	}
	defer clear(payload)
	result, err := json.Marshal(credentialSecretImportResult{
		Payload: payload, UID: secret.UID, ResourceVersion: secret.ResourceVersion,
	})
	if err != nil {
		return "", errors.New("credential_import_result_invalid")
	}
	defer clear(result)
	return string(result), nil
}

func (h *GatewayHub) handleCredentialSecretImport(w http.ResponseWriter, r *http.Request, auth TokenAuth, resource CredentialResource) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var req credentialSecretImportRequest
	if decodeCredentialJSON(w, r, &req) != nil || validateCredentialSecretImport(req) != nil {
		http.Error(w, "credential_import_request_invalid", http.StatusBadRequest)
		return
	}
	if resource.Type != CredentialTypeOpaque {
		http.Error(w, "credential_import_type_invalid", http.StatusBadRequest)
		return
	}
	if !h.canRotateCredential(auth.UserID, resource) || !h.store.UserCan(auth.UserID, req.Cluster, req.Instance, ActionExec) {
		http.Error(w, "credential_import_forbidden", http.StatusForbidden)
		return
	}
	unlock, err := h.acquireCredentialMutationLocks(r.Context(), []string{resource.ID})
	if err != nil {
		http.Error(w, "operation canceled", http.StatusConflict)
		return
	}
	defer unlock()
	if h.store.validateUserTokenAuth(auth) != nil || !h.canRotateCredential(auth.UserID, resource) ||
		!h.store.UserCan(auth.UserID, req.Cluster, req.Instance, ActionExec) {
		http.Error(w, "credential_import_forbidden", http.StatusForbidden)
		return
	}
	resource, err = h.store.CredentialByID(resource.ID)
	if err != nil || resource.State != CredentialStateActive {
		http.Error(w, "credential_revoked", http.StatusConflict)
		return
	}
	if _, err := h.store.secretGCM(); err != nil {
		http.Error(w, "credential_key_unavailable", http.StatusServiceUnavailable)
		return
	}
	auditID, err := h.store.StartAudit(AuditEvent{
		UserID: auth.UserID, TokenID: auth.TokenID, CredentialID: resource.ID,
		Cluster: req.Cluster, Instance: req.Instance, Session: req.SessionID,
		CorrelationID: "credop_" + randomHex(12), Action: ActionCredentialRotate,
		CommandSummary: "credential import Secret " + req.Namespace + "/" + req.SecretName +
			" keys=" + strings.Join(req.Keys, ","), StartedAt: time.Now().UTC(),
	})
	if err != nil {
		http.Error(w, "credential_audit_unavailable", http.StatusInternalServerError)
		return
	}
	fail := func(category string, status int) {
		if h.completeCredentialAudit(w, auditID, "error", category) {
			http.Error(w, category, status)
		}
	}
	raw, _ := json.Marshal(req)
	var args map[string]any
	_ = json.Unmarshal(raw, &args)
	result, err := h.RunInternalToolCall(contextWithInternalAuditAuth(r.Context(), auth),
		req.Cluster, req.Instance, "doops_credential_import", args)
	if err != nil {
		fail("credential_import_failed", http.StatusBadGateway)
		return
	}
	var imported credentialSecretImportResult
	resultBytes := []byte(result)
	defer clear(resultBytes)
	result = ""
	if decodeStrictCredentialPayload(resultBytes, &imported) != nil || imported.UID == "" || imported.ResourceVersion == "" ||
		len(imported.Payload) > 1<<20 || validateCredentialPayload(resource.Type, imported.Payload) != nil {
		clear(imported.Payload)
		fail("credential_import_result_invalid", http.StatusBadGateway)
		return
	}
	defer clear(imported.Payload)
	var payload struct {
		Data map[string]string `json:"data"`
	}
	_ = json.Unmarshal(imported.Payload, &payload)
	defer clear(payload.Data)
	expectedKeys := req.Keys
	if req.ConnectionURI != "" {
		expectedKeys = []string{"connection_uri"}
	}
	if !sameCredentialKeys(expectedKeys, mapKeys(payload.Data)) {
		fail("credential_import_keys_invalid", http.StatusBadGateway)
		return
	}
	if h.store.validateUserTokenAuth(auth) != nil || !h.canRotateCredential(auth.UserID, resource) ||
		!h.store.UserCan(auth.UserID, req.Cluster, req.Instance, ActionExec) {
		fail("credential_import_forbidden", http.StatusForbidden)
		return
	}
	version, err := h.store.PutCredentialVersionAudited(CredentialVersionPutRequest{
		CredentialID: resource.ID, Payload: bytes.TrimSpace(imported.Payload), CreatedBy: auth.UserID,
	}, auditID)
	if err != nil {
		fail(credentialErrorCategory(err), credentialHTTPStatus(err))
		return
	}
	writeJSONHTTP(w, version)
}
