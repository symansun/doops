package server

// Row scanning and payload validation helpers for the credential store.

import (
	"encoding/json"
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
