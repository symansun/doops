package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrSecretKeyUnavailable       = errors.New("DOOPS_GATEWAY_SECRET_KEY is required")
	ErrCredentialNotFound         = errors.New("credential not found")
	ErrCredentialVersionNotFound  = errors.New("credential version not found")
	ErrCredentialRevoked          = errors.New("credential is revoked")
	ErrCredentialGrantDenied      = errors.New("credential grant denied")
	ErrCredentialForbidden        = errors.New("credential operation forbidden")
	ErrCredentialPayloadInvalid   = errors.New("credential payload is invalid")
	ErrCredentialBundleNotFound   = errors.New("credential bundle not found")
	ErrCredentialAmbiguous        = errors.New("credential reference is ambiguous")
	ErrCredentialAuditUnavailable = errors.New("credential audit unavailable")
	// ErrCredentialCleanupPending refuses a revocation's terminal state while a
	// materialization record may still own something on a target.
	ErrCredentialCleanupPending = errors.New("credential cleanup pending")

	// Specific grant denials keep errors.Is(err, ErrCredentialGrantDenied) and its
	// 403 status, but report which binding precondition failed.
	ErrCredentialBindingTargetMismatch = fmt.Errorf("%w: credential is bound to another target", ErrCredentialGrantDenied)
	ErrCredentialBindingScopeInvalid   = fmt.Errorf("%w: target binding scope is invalid", ErrCredentialGrantDenied)
	ErrDoSQLDatabaseBindingMissing     = fmt.Errorf("%w: target has no database binding", ErrCredentialGrantDenied)
	ErrDoSQLCredentialNotBound         = fmt.Errorf("%w: named credential has no active database binding on this target", ErrCredentialGrantDenied)
)

// credentialGrantDenialCategory must run before the generic grant-denied case.

func (s *GatewayStore) CreateCredential(req CredentialCreateRequest) (CredentialResource, error) {
	return s.createCredential(req, 0)
}

func (s *GatewayStore) CreateCredentialAudited(req CredentialCreateRequest, auditID int64) (CredentialResource, error) {
	return s.createCredential(req, auditID)
}

func (s *GatewayStore) createCredential(req CredentialCreateRequest, auditID int64) (CredentialResource, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.OwnerID = strings.TrimSpace(req.OwnerID)
	req.CreatedBy = strings.TrimSpace(req.CreatedBy)
	if req.Name == "" || req.CreatedBy == "" {
		return CredentialResource{}, fmt.Errorf("credential name and creator are required")
	}
	if !validCredentialScope(req.Scope) || !validCredentialType(req.Type) {
		return CredentialResource{}, fmt.Errorf("invalid credential scope or type")
	}
	if req.Scope == CredentialScopePersonal && req.OwnerID == "" {
		return CredentialResource{}, fmt.Errorf("personal credential owner is required")
	}
	if req.Scope == CredentialScopePlatform {
		req.OwnerID = ""
	}
	now := time.Now().UTC()
	resource := CredentialResource{
		ID:        "cred_" + randomHex(12),
		Name:      req.Name,
		Scope:     req.Scope,
		OwnerID:   req.OwnerID,
		Type:      req.Type,
		State:     CredentialStateActive,
		CreatedBy: req.CreatedBy,
		CreatedAt: now,
		UpdatedAt: now,
	}
	tx, err := s.db.Begin()
	if err != nil {
		return CredentialResource{}, err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO credentials
		(id, name, scope, owner_id, type, state, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		resource.ID, resource.Name, resource.Scope, resource.OwnerID, resource.Type, resource.State,
		resource.CreatedBy, formatTime(now), formatTime(now))
	if err != nil {
		return CredentialResource{}, err
	}
	if auditID != 0 {
		// The ID is only known now. Every other resource-level credential audit
		// carries it as credential and session, so the create is found the same way.
		if _, err := tx.Exec(`UPDATE audit_events SET credential_id = ?, session = ?
			WHERE id = ? AND status IN ('running', 'executing')`,
			resource.ID, resource.ID, auditID); err != nil {
			return CredentialResource{}, fmt.Errorf("%w: %v", ErrCredentialAuditUnavailable, err)
		}
		if err := finishAuditWith(tx, auditID, AuditFinish{Status: "success", EndedAt: now}); err != nil {
			return CredentialResource{}, fmt.Errorf("%w: %v", ErrCredentialAuditUnavailable, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return CredentialResource{}, err
	}
	return resource, nil
}

func (s *GatewayStore) ListCredentials(filter CredentialListFilter) ([]CredentialResource, error) {
	args := []any{machineVaultScope}
	where := []string{"scope <> ?"}
	if actorID := strings.TrimSpace(filter.ActorID); actorID != "" {
		where = append(where, "scope = ? AND owner_id = ?")
		args = append(args, CredentialScopePersonal, actorID)
	}
	if filter.Scope != "" {
		if !validCredentialScope(filter.Scope) {
			return nil, fmt.Errorf("invalid credential scope")
		}
		where = append(where, "scope = ?")
		args = append(args, filter.Scope)
	}
	rows, err := s.db.Query(`SELECT id, name, scope, owner_id, type, state, created_by, created_at, updated_at, revoked_at
		FROM credentials WHERE `+strings.Join(where, " AND ")+` ORDER BY created_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var resources []CredentialResource
	for rows.Next() {
		resource, err := scanCredentialResource(rows)
		if err != nil {
			return nil, err
		}
		resources = append(resources, resource)
	}
	return resources, rows.Err()
}

func (s *GatewayStore) ListCredentialAccess(
	actorID, cluster, instance string,
) ([]CredentialListEntry, error) {
	actorID = strings.TrimSpace(actorID)
	cluster = strings.TrimSpace(cluster)
	instance = strings.TrimSpace(instance)
	if actorID == "" || cluster == "" || instance == "" {
		return nil, fmt.Errorf("credential actor, cluster, and instance are required")
	}
	// Credential visibility follows the selected target permission. There is
	// no second per-user credential grant.
	args := []any{CredentialStateActive, CredentialStateActive, CredentialVersionActive}
	rows, err := s.db.Query(`SELECT
			c.id, c.name, c.scope, c.owner_id, c.type, c.state, c.created_by,
			c.created_at, c.updated_at, c.revoked_at,
			g.id, g.credential_id, g.grantee_id, g.scope_json, g.state,
			g.created_by, g.created_at, g.revoked_at
		FROM credentials c
		JOIN credential_grants g ON g.credential_id = c.id
		WHERE c.state = ? AND g.state = ?
		  AND EXISTS (
			SELECT 1 FROM credential_versions v
			WHERE v.credential_id = c.id AND v.state = ?
		  )
		ORDER BY c.created_at DESC, g.created_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]CredentialListEntry, 0)
	entryByID := make(map[string]int)
	for rows.Next() {
		var resource CredentialResource
		var grant CredentialGrant
		var resourceCreatedAt, resourceUpdatedAt, resourceRevokedAt string
		var grantScope, grantCreatedAt, grantRevokedAt string
		if err := rows.Scan(
			&resource.ID, &resource.Name, &resource.Scope, &resource.OwnerID, &resource.Type,
			&resource.State, &resource.CreatedBy, &resourceCreatedAt, &resourceUpdatedAt, &resourceRevokedAt,
			&grant.ID, &grant.CredentialID, new(string), &grantScope, &grant.State,
			&grant.CreatedBy, &grantCreatedAt, &grantRevokedAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(grantScope), &grant.Scope); err != nil {
			return nil, err
		}
		grant.Scope = normalizeCredentialGrantScope(grant.Scope)
		if grant.Scope.Cluster != cluster || grant.Scope.Instance != instance {
			continue
		}
		resource.CreatedAt, _ = parseTime(resourceCreatedAt)
		resource.UpdatedAt, _ = parseTime(resourceUpdatedAt)
		if resourceRevokedAt != "" {
			parsed, _ := parseTime(resourceRevokedAt)
			resource.RevokedAt = &parsed
		}
		grant.CreatedAt, _ = parseTime(grantCreatedAt)
		if grantRevokedAt != "" {
			parsed, _ := parseTime(grantRevokedAt)
			grant.RevokedAt = &parsed
		}
		index, exists := entryByID[resource.ID]
		if !exists {
			index = len(entries)
			entryByID[resource.ID] = index
			entries = append(entries, CredentialListEntry{
				CredentialResource: resource,
				Grants:             []CredentialGrant{},
			})
		}
		entries[index].Grants = append(entries[index].Grants, grant)
	}
	return entries, rows.Err()
}

func (s *GatewayStore) credentialByID(id string) (CredentialResource, error) {
	row := s.db.QueryRow(`SELECT id, name, scope, owner_id, type, state, created_by, created_at, updated_at, revoked_at
		FROM credentials WHERE id = ? AND scope <> ?`, strings.TrimSpace(id), machineVaultScope)
	resource, err := scanCredentialResource(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CredentialResource{}, ErrCredentialNotFound
	}
	return resource, err
}

func (s *GatewayStore) CredentialByID(id string) (CredentialResource, error) {
	return s.credentialByID(id)
}

func (s *GatewayStore) canManageCredential(resource CredentialResource, actorID string, action GatewayAction) bool {
	if resource.Scope == CredentialScopePersonal {
		return resource.OwnerID == actorID || s.UserIsPlatformAdmin(actorID)
	}
	return s.UserIsPlatformAdmin(actorID)
}
