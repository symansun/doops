package server

import (
	"encoding/json"
	"strings"
)

func (s *GatewayStore) AuthorizeCredentialUse(req CredentialUseRequest) (AuthorizedCredentialUse, error) {
	resource, grant, err := s.AuthorizeCredentialGrant(req)
	if err != nil {
		return AuthorizedCredentialUse{}, err
	}
	version, err := s.ActiveCredentialVersion(resource.ID)
	if err != nil {
		return AuthorizedCredentialUse{}, err
	}
	return AuthorizedCredentialUse{Resource: resource, Version: version, Grant: grant}, nil
}

func (s *GatewayStore) AuthorizeCredentialGrant(req CredentialUseRequest) (CredentialResource, CredentialGrant, error) {
	req.ActorID = strings.TrimSpace(req.ActorID)
	req.CredentialID = strings.TrimSpace(req.CredentialID)
	req.Credential = strings.TrimSpace(req.Credential)
	req.Scope = normalizeCredentialGrantScope(req.Scope)
	if req.ActorID == "" || (req.CredentialID == "" && req.Credential == "") ||
		req.Scope.Cluster == "" || req.Scope.Instance == "" {
		return CredentialResource{}, CredentialGrant{}, ErrCredentialGrantDenied
	}
	if !s.UserCanUseTarget(req.ActorID, req.Scope.Cluster, req.Scope.Instance) {
		return CredentialResource{}, CredentialGrant{}, ErrCredentialForbidden
	}
	if req.Scope.Use != "" && !validCredentialUse(req.Scope.Use) {
		return CredentialResource{}, CredentialGrant{}, ErrCredentialGrantDenied
	}
	credentialSelector := "c.name = ?"
	credentialValue := req.Credential
	if req.CredentialID != "" {
		credentialSelector = "c.id = ?"
		credentialValue = req.CredentialID
	}
	rows, err := s.db.Query(`SELECT c.id, c.name, c.scope, c.owner_id, c.type, c.state, c.created_by, c.created_at, c.updated_at, c.revoked_at,
			g.id, g.credential_id, g.grantee_id, g.scope_json, g.state, g.created_by, g.created_at, g.revoked_at
		FROM credential_grants g JOIN credentials c ON c.id = g.credential_id
		WHERE `+credentialSelector+` AND c.state = ? AND g.state = ? ORDER BY c.id, g.id`,
		credentialValue, CredentialStateActive, CredentialStateActive)
	if err != nil {
		return CredentialResource{}, CredentialGrant{}, err
	}
	defer rows.Close()
	var matches []AuthorizedCredentialUse
	otherTarget, invalidScope := false, false
	for rows.Next() {
		resource, grant, err := scanCredentialAuthorization(rows)
		if err != nil {
			return CredentialResource{}, CredentialGrant{}, err
		}
		if grant.Scope.Use == "" {
			grant.Scope.Use = credentialUseForType(resource.Type)
		}
		if grant.Scope.Cluster != req.Scope.Cluster || grant.Scope.Instance != req.Scope.Instance {
			otherTarget = true
			continue
		}
		if validateCredentialGrantScope(grant.Scope) != nil {
			invalidScope = true
			continue
		}
		if len(matches) != 0 && matches[len(matches)-1].Resource.ID == resource.ID &&
			credentialScopeEqual(matches[len(matches)-1].Grant.Scope, grant.Scope) {
			continue
		}
		matches = append(matches, AuthorizedCredentialUse{Resource: resource, Grant: grant})
	}
	if err := rows.Err(); err != nil {
		return CredentialResource{}, CredentialGrant{}, err
	}
	if len(matches) == 0 {
		switch {
		case invalidScope:
			return CredentialResource{}, CredentialGrant{}, ErrCredentialBindingScopeInvalid
		case otherTarget:
			return CredentialResource{}, CredentialGrant{}, ErrCredentialBindingTargetMismatch
		}
		return CredentialResource{}, CredentialGrant{}, ErrCredentialGrantDenied
	}
	if len(matches) > 1 {
		return CredentialResource{}, CredentialGrant{}, ErrCredentialAmbiguous
	}
	return matches[0].Resource, matches[0].Grant, nil
}

// HasUnpromotedDatabaseBinding reports whether the target has an active opaque
// database binding whose credential still lacks an active version. Auto DoSql
// discovery hides such credentials, so this separates "not promoted" from
// "never bound".
func (s *GatewayStore) HasUnpromotedDatabaseBinding(cluster, instance string) (bool, error) {
	rows, err := s.db.Query(`SELECT g.scope_json
		FROM credentials c JOIN credential_grants g ON g.credential_id = c.id
		WHERE c.state = ? AND c.type = ? AND g.state = ?
		  AND NOT EXISTS (
			SELECT 1 FROM credential_versions v
			WHERE v.credential_id = c.id AND v.state = ?
		  )`,
		CredentialStateActive, CredentialTypeOpaque, CredentialStateActive, CredentialVersionActive)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var scopeJSON string
		if err := rows.Scan(&scopeJSON); err != nil {
			return false, err
		}
		var scope CredentialGrantScope
		if err := json.Unmarshal([]byte(scopeJSON), &scope); err != nil {
			return false, err
		}
		scope = normalizeCredentialGrantScope(scope)
		if scope.Cluster == cluster && scope.Instance == instance && scope.Use == CredentialUseDatabase {
			return true, nil
		}
	}
	return false, rows.Err()
}
