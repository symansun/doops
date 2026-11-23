package server

// Credential grant lifecycle: create, revoke, finalise and authorise grants.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func credentialGrantDenialCategory(err error) (string, bool) {
	switch {
	case errors.Is(err, ErrCredentialBindingTargetMismatch):
		return "credential_binding_target_mismatch", true
	case errors.Is(err, ErrCredentialBindingScopeInvalid):
		return "credential_binding_scope_invalid", true
	case errors.Is(err, ErrDoSQLDatabaseBindingMissing):
		return "dosql_database_binding_missing", true
	case errors.Is(err, ErrDoSQLCredentialNotBound):
		return "dosql_credential_not_bound", true
	case errors.Is(err, ErrCredentialGrantDenied):
		return "credential_grant_denied", true
	}
	return "", false
}

func (s *GatewayStore) CreateCredentialGrant(req CredentialGrantCreateRequest) (CredentialGrant, error) {
	return s.createCredentialGrant(req, 0)
}

func (s *GatewayStore) CreateCredentialGrantAudited(req CredentialGrantCreateRequest, auditID int64) (CredentialGrant, error) {
	return s.createCredentialGrant(req, auditID)
}

func (s *GatewayStore) createCredentialGrant(req CredentialGrantCreateRequest, auditID int64) (CredentialGrant, error) {
	req.CredentialID = strings.TrimSpace(req.CredentialID)
	req.CreatedBy = strings.TrimSpace(req.CreatedBy)
	if req.CredentialID == "" || req.CreatedBy == "" {
		return CredentialGrant{}, fmt.Errorf("credential and creator are required")
	}
	resource, err := s.credentialByID(req.CredentialID)
	if err != nil {
		return CredentialGrant{}, err
	}
	if resource.State != CredentialStateActive {
		return CredentialGrant{}, ErrCredentialRevoked
	}
	if !s.canManageCredential(resource, req.CreatedBy, ActionCredentialGrant) {
		return CredentialGrant{}, ErrCredentialForbidden
	}
	grant := CredentialGrant{
		ID:           "credgrant_" + randomHex(12),
		CredentialID: resource.ID,
		Scope:        normalizeCredentialGrantScope(req.Scope),
		State:        CredentialStateActive,
		CreatedBy:    req.CreatedBy,
		CreatedAt:    time.Now().UTC(),
	}
	if grant.Scope.Use == "" {
		grant.Scope.Use = credentialUseForType(resource.Type)
	}
	if err := validateCredentialGrantScope(grant.Scope); err != nil {
		return CredentialGrant{}, err
	}
	if resource.Type == CredentialTypeModelProvider {
		if grant.Scope.Use != CredentialUseModelProvider {
			return CredentialGrant{}, errors.New("credential_use_mismatch")
		}
		// doagent has one settings file, so a target takes one model provider.
		conflict, err := s.modelProviderGrantConflicts(resource.ID, grant.Scope)
		if err != nil {
			return CredentialGrant{}, err
		}
		if conflict {
			return CredentialGrant{}, errors.New("model_provider_already_bound")
		}
	}
	scopeJSON, err := json.Marshal(grant.Scope)
	if err != nil {
		return CredentialGrant{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return CredentialGrant{}, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id, credential_id, grantee_id, scope_json, state, created_by, created_at, revoked_at
		FROM credential_grants WHERE credential_id = ? AND state = ? ORDER BY id`,
		grant.CredentialID, CredentialStateActive)
	if err != nil {
		return CredentialGrant{}, err
	}
	var semanticMatches []CredentialGrant
	for rows.Next() {
		existing, scanErr := scanCredentialGrant(rows)
		if scanErr != nil {
			rows.Close()
			return CredentialGrant{}, scanErr
		}
		if credentialScopeEqual(existing.Scope, grant.Scope) {
			semanticMatches = append(semanticMatches, existing)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return CredentialGrant{}, err
	}
	rows.Close()
	if len(semanticMatches) == 1 {
		existing := semanticMatches[0]
		if existingScopeJSON, marshalErr := json.Marshal(existing.Scope); marshalErr != nil {
			return CredentialGrant{}, marshalErr
		} else if string(existingScopeJSON) != string(scopeJSON) {
			if _, updateErr := tx.Exec(`UPDATE credential_grants SET scope_json = ?, created_by = ?, created_at = ?, revoked_at = '' WHERE id = ?`,
				string(scopeJSON), grant.CreatedBy, formatTime(grant.CreatedAt), existing.ID); updateErr != nil {
				return CredentialGrant{}, updateErr
			}
			if err := invalidateCredentialGrantVerifications(tx, existing.ID); err != nil {
				return CredentialGrant{}, err
			}
			existing.Scope = grant.Scope
			existing.CreatedBy = grant.CreatedBy
			existing.CreatedAt = grant.CreatedAt
		}
		if auditID != 0 {
			if err := finishAuditWith(tx, auditID, credentialGrantSuccessAudit(existing)); err != nil {
				return CredentialGrant{}, fmt.Errorf("%w: %v", ErrCredentialAuditUnavailable, err)
			}
		}
		if err := tx.Commit(); err != nil {
			return CredentialGrant{}, err
		}
		return existing, nil
	}
	if len(semanticMatches) > 1 {
		// Legacy rows may contain several execution-metadata variants for one
		// target/use authorization. Keep the stable first grant and refresh its
		// metadata instead of turning equivalent grants into an authorization block.
		existing := semanticMatches[0]
		if _, updateErr := tx.Exec(`UPDATE credential_grants SET scope_json = ?, created_by = ?, created_at = ?, revoked_at = '' WHERE id = ?`,
			string(scopeJSON), grant.CreatedBy, formatTime(grant.CreatedAt), existing.ID); updateErr != nil {
			return CredentialGrant{}, updateErr
		}
		if err := invalidateCredentialGrantVerifications(tx, existing.ID); err != nil {
			return CredentialGrant{}, err
		}
		existing.Scope = grant.Scope
		existing.CreatedBy = grant.CreatedBy
		existing.CreatedAt = grant.CreatedAt
		if auditID != 0 {
			if err := finishAuditWith(tx, auditID, credentialGrantSuccessAudit(existing)); err != nil {
				return CredentialGrant{}, fmt.Errorf("%w: %v", ErrCredentialAuditUnavailable, err)
			}
		}
		if err := tx.Commit(); err != nil {
			return CredentialGrant{}, err
		}
		return existing, nil
	}
	// Clear evidence from grants revoked by older Gateway versions before
	// their stable IDs are reused by the upsert below.
	if _, err := tx.Exec(`DELETE FROM credential_verifications WHERE grant_id IN (
		SELECT id FROM credential_grants WHERE credential_id = ? AND grantee_id = ? AND scope_json = ? AND state = ?
	)`, grant.CredentialID, grant.CreatedBy, string(scopeJSON), CredentialStateRevoked); err != nil {
		return CredentialGrant{}, err
	}
	_, err = tx.Exec(gatewayUpsertSQL(gatewayUpsert{insert: "INSERT INTO credential_grants\n\t\t(id, credential_id, grantee_id, scope_json, state, revoked_at, created_by, created_at)\n\t\tVALUES (?, ?, ?, ?, ?, '', ?, ?)", target: []string{"credential_id", "grantee_id", "scope_json"}, set: []string{"state = excluded.state", "created_by = excluded.created_by", "created_at = excluded.created_at", "revoked_at = ''"}}),
		grant.ID, grant.CredentialID, grant.CreatedBy, string(scopeJSON), grant.State,
		grant.CreatedBy, formatTime(grant.CreatedAt))
	if err != nil {
		return CredentialGrant{}, err
	}
	var existing CredentialGrant
	var storedScope, createdAt, revokedAt string
	err = tx.QueryRow(`SELECT id, credential_id, grantee_id, scope_json, state, created_by, created_at, revoked_at
		FROM credential_grants
		WHERE credential_id = ? AND grantee_id = ? AND scope_json = ?`,
		grant.CredentialID, grant.CreatedBy, string(scopeJSON),
	).Scan(
		&existing.ID, &existing.CredentialID, new(string), &storedScope,
		&existing.State, &existing.CreatedBy, &createdAt, &revokedAt,
	)
	if err != nil {
		return CredentialGrant{}, err
	}
	if err := json.Unmarshal([]byte(storedScope), &existing.Scope); err != nil {
		return CredentialGrant{}, err
	}
	existing.CreatedAt, _ = parseTime(createdAt)
	if auditID != 0 {
		if err := finishAuditWith(tx, auditID, credentialGrantSuccessAudit(existing)); err != nil {
			return CredentialGrant{}, fmt.Errorf("%w: %v", ErrCredentialAuditUnavailable, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return CredentialGrant{}, err
	}
	return existing, nil
}

// credentialGrantSuccessAudit is the success audit of a created grant. It keeps
// only the identifiers credentialTargetAudit accepts, so a request scope whose
// cluster or instance is free text leaves the target columns and the stored
// scope JSON empty.
func credentialGrantSuccessAudit(existing CredentialGrant) AuditFinish {
	target := credentialTargetAudit(existing.Scope, existing.ID)
	target.Status = "success"
	target.EndedAt = time.Now().UTC()
	return target
}

func (s *GatewayStore) RevokeCredentialGrant(credentialID, grantID, actorID string) (CredentialGrant, error) {
	grant, err := s.BeginCredentialGrantRevocation(credentialID, grantID, actorID)
	if err != nil {
		return CredentialGrant{}, err
	}
	if grant.State == CredentialStateRevoked {
		return grant, nil
	}
	return s.FinalizeCredentialGrantRevocation(credentialID, grantID, 0)
}

func (s *GatewayStore) BeginCredentialGrantRevocation(credentialID, grantID, actorID string) (CredentialGrant, error) {
	resource, err := s.credentialByID(strings.TrimSpace(credentialID))
	if err != nil {
		return CredentialGrant{}, err
	}
	if !s.canManageCredential(resource, strings.TrimSpace(actorID), ActionCredentialGrant) {
		return CredentialGrant{}, ErrCredentialForbidden
	}
	grant, err := s.credentialGrantByID(resource.ID, strings.TrimSpace(grantID))
	if err != nil {
		return CredentialGrant{}, err
	}
	if grant.State == CredentialStateRevoked {
		return grant, nil
	}
	if grant.State == CredentialStateRevoking {
		return grant, nil
	}
	result, err := s.db.Exec(`UPDATE credential_grants
		SET state = ?, revoked_at = ''
		WHERE id = ? AND credential_id = ? AND state = ?`,
		CredentialStateRevoking, grant.ID, resource.ID, CredentialStateActive)
	if err != nil {
		return CredentialGrant{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return CredentialGrant{}, err
	}
	if affected != 1 {
		return CredentialGrant{}, ErrCredentialGrantDenied
	}
	grant.State = CredentialStateRevoking
	grant.RevokedAt = nil
	return grant, nil
}

func invalidateCredentialGrantVerifications(tx *sql.Tx, grantID string) error {
	_, err := tx.Exec("DELETE FROM credential_verifications WHERE grant_id = ?", grantID)
	return err
}

func (s *GatewayStore) FinalizeCredentialGrantRevocation(
	credentialID string,
	grantID string,
	auditID int64,
) (CredentialGrant, error) {
	grant, err := s.credentialGrantByID(strings.TrimSpace(credentialID), strings.TrimSpace(grantID))
	if err != nil {
		return CredentialGrant{}, err
	}
	if grant.State == CredentialStateRevoked {
		if auditID != 0 {
			if err := s.FinishAudit(auditID, credentialGrantSuccessAudit(grant)); err != nil {
				return CredentialGrant{}, fmt.Errorf("%w: %v", ErrCredentialAuditUnavailable, err)
			}
		}
		return grant, nil
	}
	if grant.State != CredentialStateRevoking {
		return CredentialGrant{}, fmt.Errorf("credential grant is not revoking")
	}
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return CredentialGrant{}, err
	}
	defer tx.Rollback()
	// A use already under way when the revocation began can record or finish a
	// materialization after the cleanup list was read. The grant reaches revoked
	// only when none of its records may still own something on the target.
	if pending, err := uncleanedCredentialMaterializations(tx, grant.CredentialID, grant.ID); err != nil {
		return CredentialGrant{}, err
	} else if pending != 0 {
		return CredentialGrant{}, ErrCredentialCleanupPending
	}
	result, err := tx.Exec(`UPDATE credential_grants
		SET state = ?, revoked_at = ?
		WHERE id = ? AND credential_id = ? AND state = ?`,
		CredentialStateRevoked, formatTime(now), grant.ID, grant.CredentialID, CredentialStateRevoking)
	if err != nil {
		return CredentialGrant{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return CredentialGrant{}, err
	}
	if affected != 1 {
		return CredentialGrant{}, ErrCredentialGrantDenied
	}
	if err := invalidateCredentialGrantVerifications(tx, grant.ID); err != nil {
		return CredentialGrant{}, err
	}
	if auditID != 0 {
		if err := finishAuditWith(tx, auditID, credentialGrantSuccessAudit(grant)); err != nil {
			return CredentialGrant{}, fmt.Errorf("%w: %v", ErrCredentialAuditUnavailable, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return CredentialGrant{}, err
	}
	grant.State = CredentialStateRevoked
	grant.RevokedAt = &now
	return grant, nil
}

func (s *GatewayStore) ActiveCredentialGrants(credentialID string) ([]CredentialGrant, error) {
	rows, err := s.db.Query(`SELECT id, credential_id, grantee_id, scope_json, state, created_by, created_at, revoked_at
		FROM credential_grants WHERE credential_id = ? AND state = ? ORDER BY id`,
		strings.TrimSpace(credentialID), CredentialStateActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var grants []CredentialGrant
	seenTargets := make(map[string]struct{})
	for rows.Next() {
		grant, err := scanCredentialGrant(rows)
		if err != nil {
			return nil, err
		}
		targetKey := grant.Scope.Cluster + "\x00" + grant.Scope.Instance
		if _, exists := seenTargets[targetKey]; exists {
			continue
		}
		seenTargets[targetKey] = struct{}{}
		grants = append(grants, grant)
	}
	return grants, rows.Err()
}

func (s *GatewayStore) CredentialGrantsForContext(credentialID string, scope CredentialGrantScope) ([]CredentialGrant, error) {
	grants, err := s.ActiveCredentialGrants(credentialID)
	if err != nil {
		return nil, err
	}
	matches := make([]CredentialGrant, 0, len(grants))
	seenTargets := make(map[string]struct{})
	for _, grant := range grants {
		if credentialScopeEqual(grant.Scope, scope) {
			targetKey := grant.Scope.Cluster + "\x00" + grant.Scope.Instance
			if _, exists := seenTargets[targetKey]; exists {
				continue
			}
			seenTargets[targetKey] = struct{}{}
			matches = append(matches, grant)
		}
	}
	if len(matches) == 0 {
		return nil, ErrCredentialGrantDenied
	}
	return matches, nil
}

func (s *GatewayStore) CredentialGrants(credentialID string) ([]CredentialGrant, error) {
	rows, err := s.db.Query(`SELECT id, credential_id, grantee_id, scope_json, state, created_by, created_at, revoked_at
		FROM credential_grants WHERE credential_id = ? ORDER BY created_at DESC, id DESC`, strings.TrimSpace(credentialID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	grants := make([]CredentialGrant, 0)
	for rows.Next() {
		grant, err := scanCredentialGrant(rows)
		if err != nil {
			return nil, err
		}
		grants = append(grants, grant)
	}
	return grants, rows.Err()
}

func (s *GatewayStore) credentialGrantByID(credentialID, grantID string) (CredentialGrant, error) {
	row := s.db.QueryRow(`SELECT id, credential_id, grantee_id, scope_json, state, created_by, created_at, revoked_at
		FROM credential_grants WHERE credential_id = ? AND id = ?`, credentialID, grantID)
	grant, err := scanCredentialGrant(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CredentialGrant{}, ErrCredentialGrantDenied
	}
	return grant, err
}
