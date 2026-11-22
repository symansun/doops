package server

// Row scanning and payload validation helpers for the credential store.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
)

type credentialScanner interface {
	Scan(dest ...any) error
}

func scanCredentialResource(scanner credentialScanner) (CredentialResource, error) {
	var resource CredentialResource
	var createdAt, updatedAt, revokedAt string
	if err := scanner.Scan(&resource.ID, &resource.Name, &resource.Scope, &resource.OwnerID, &resource.Type, &resource.State,
		&resource.CreatedBy, &createdAt, &updatedAt, &revokedAt); err != nil {
		return CredentialResource{}, err
	}
	resource.CreatedAt, _ = parseTime(createdAt)
	resource.UpdatedAt, _ = parseTime(updatedAt)
	if revokedAt != "" {
		if parsed, err := parseTime(revokedAt); err == nil {
			resource.RevokedAt = &parsed
		}
	}
	return resource, nil
}

func scanCredentialVersion(scanner credentialScanner) (CredentialVersion, error) {
	var version CredentialVersion
	var createdAt, promotedAt, revokedAt string
	if err := scanner.Scan(&version.ID, &version.CredentialID, &version.PayloadDigest, &version.State, &version.CreatedBy,
		&createdAt, &promotedAt, &revokedAt); err != nil {
		return CredentialVersion{}, err
	}
	version.CreatedAt, _ = parseTime(createdAt)
	if promotedAt != "" {
		if parsed, err := parseTime(promotedAt); err == nil {
			version.PromotedAt = &parsed
		}
	}
	if revokedAt != "" {
		if parsed, err := parseTime(revokedAt); err == nil {
			version.RevokedAt = &parsed
		}
	}
	return version, nil
}

func scanCredentialGrant(scanner credentialScanner) (CredentialGrant, error) {
	var grant CredentialGrant
	var scopeJSON, createdAt, revokedAt string
	if err := scanner.Scan(&grant.ID, &grant.CredentialID, new(string), &scopeJSON,
		&grant.State, &grant.CreatedBy, &createdAt, &revokedAt); err != nil {
		return CredentialGrant{}, err
	}
	if err := json.Unmarshal([]byte(scopeJSON), &grant.Scope); err != nil {
		return CredentialGrant{}, err
	}
	grant.CreatedAt, _ = parseTime(createdAt)
	if revokedAt != "" {
		if parsed, err := parseTime(revokedAt); err == nil {
			grant.RevokedAt = &parsed
		}
	}
	return grant, nil
}

func scanCredentialAuthorization(scanner credentialScanner) (CredentialResource, CredentialGrant, error) {
	var resource CredentialResource
	var grant CredentialGrant
	var resourceCreatedAt, resourceUpdatedAt, resourceRevokedAt, scopeJSON, grantCreatedAt, grantRevokedAt string
	if err := scanner.Scan(
		&resource.ID, &resource.Name, &resource.Scope, &resource.OwnerID, &resource.Type, &resource.State,
		&resource.CreatedBy, &resourceCreatedAt, &resourceUpdatedAt, &resourceRevokedAt,
		&grant.ID, &grant.CredentialID, new(string), &scopeJSON, &grant.State, &grant.CreatedBy,
		&grantCreatedAt, &grantRevokedAt,
	); err != nil {
		return CredentialResource{}, CredentialGrant{}, err
	}
	if err := json.Unmarshal([]byte(scopeJSON), &grant.Scope); err != nil {
		return CredentialResource{}, CredentialGrant{}, err
	}
	resource.CreatedAt, _ = parseTime(resourceCreatedAt)
	resource.UpdatedAt, _ = parseTime(resourceUpdatedAt)
	grant.CreatedAt, _ = parseTime(grantCreatedAt)
	return resource, grant, nil
}

func credentialPayloadDigest(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func credentialPayloadAAD(credentialID, versionID string, credentialType CredentialType, digest string) []byte {
	return []byte(strings.Join([]string{"credential", credentialID, versionID, string(credentialType), digest}, "\x00"))
}

func (s *GatewayStore) canManageCredential(resource CredentialResource, actorID string, action GatewayAction) bool {
	if resource.Scope == CredentialScopePersonal {
		return resource.OwnerID == actorID || s.UserIsPlatformAdmin(actorID)
	}
	return s.UserIsPlatformAdmin(actorID)
}

func validCredentialScope(scope CredentialScope) bool {
	return scope == CredentialScopePersonal || scope == CredentialScopePlatform
}

func validCredentialType(credentialType CredentialType) bool {
	switch credentialType {
	case CredentialTypeRegistry, CredentialTypeTLS, CredentialTypeOpaque, CredentialTypeHelmRepository, CredentialTypeGitToken,
		CredentialTypeModelProvider:
		return true
	default:
		return false
	}
}

func validCredentialUse(use CredentialUse) bool {
	switch use {
	case CredentialUseImagePull, CredentialUseGitCheckout, CredentialUseHelmPull, CredentialUseTLS, CredentialUseOpaqueSecret, CredentialUseDatabase,
		CredentialUseModelProvider:
		return true
	default:
		return false
	}
}

func validateCredentialPayload(credentialType CredentialType, payload json.RawMessage) error {
	switch credentialType {
	case CredentialTypeRegistry:
		var value struct {
			Server   string `json:"server"`
			Username string `json:"username"`
			Password string `json:"password"`
			Email    string `json:"email,omitempty"`
		}
		if err := decodeStrictCredentialPayload(payload, &value); err != nil ||
			!validCredentialEndpoint(value.Server, true) ||
			strings.TrimSpace(value.Username) == "" ||
			strings.TrimSpace(value.Password) == "" {
			return ErrCredentialPayloadInvalid
		}
	case CredentialTypeTLS:
		var value struct {
			Certificate string `json:"certificate"`
			PrivateKey  string `json:"privateKey"`
		}
		if err := decodeStrictCredentialPayload(payload, &value); err != nil ||
			strings.TrimSpace(value.Certificate) == "" ||
			strings.TrimSpace(value.PrivateKey) == "" {
			return ErrCredentialPayloadInvalid
		}
	case CredentialTypeOpaque:
		var value struct {
			Data map[string]string `json:"data"`
		}
		if err := decodeStrictCredentialPayload(payload, &value); err != nil || len(value.Data) == 0 {
			return ErrCredentialPayloadInvalid
		}
		for key, item := range value.Data {
			if strings.TrimSpace(key) == "" || item == "" {
				return ErrCredentialPayloadInvalid
			}
		}
	case CredentialTypeHelmRepository:
		var value struct {
			URL      string `json:"url"`
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := decodeStrictCredentialPayload(payload, &value); err != nil ||
			!validCredentialEndpoint(value.URL, false) ||
			strings.TrimSpace(value.Username) == "" ||
			strings.TrimSpace(value.Password) == "" {
			return ErrCredentialPayloadInvalid
		}
	case CredentialTypeGitToken:
		var value struct {
			URL      string `json:"url"`
			Username string `json:"username"`
			Token    string `json:"token"`
		}
		if err := decodeStrictCredentialPayload(payload, &value); err != nil ||
			!validCredentialEndpoint(value.URL, false) ||
			strings.TrimSpace(value.Username) == "" ||
			strings.TrimSpace(value.Token) == "" {
			return ErrCredentialPayloadInvalid
		}
	case CredentialTypeModelProvider:
		if _, err := decodeModelProviderPayload(payload); err != nil {
			return ErrCredentialPayloadInvalid
		}
	default:
		return ErrCredentialPayloadInvalid
	}
	return nil
}

func decodeStrictCredentialPayload(payload []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrCredentialPayloadInvalid
	}
	return nil
}

func validCredentialEndpoint(raw string, allowRegistryHost bool) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	if allowRegistryHost && !strings.Contains(raw, "://") {
		return !strings.ContainsAny(raw, "/?#@ \t\r\n")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Hostname() == "" {
		return false
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && credentialPrivateEndpointAllowed(parsed.Hostname())) {
		return false
	}
	return parsed.RawQuery == "" && parsed.Fragment == ""
}
