package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (s *GatewayStore) RevokeCredential(credentialID, actorID string) error {
	resource, err := s.credentialByID(credentialID)
	if err != nil {
		return err
	}
	if !s.canManageCredential(resource, strings.TrimSpace(actorID), ActionCredentialRevoke) {
		return ErrCredentialForbidden
	}
	if resource.State == CredentialStateRevoked {
		return nil
	}
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE credentials SET state = ?, revoked_at = ?, updated_at = ? WHERE id = ?`,
		CredentialStateRevoked, formatTime(now), formatTime(now), resource.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE credential_versions SET state = ?, revoked_at = ? WHERE credential_id = ? AND state != ?`,
		CredentialVersionRevoked, formatTime(now), resource.ID, CredentialVersionRevoked); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE credential_grants SET state = ?, revoked_at = ? WHERE credential_id = ? AND state != ?`,
		CredentialStateRevoked, formatTime(now), resource.ID, CredentialStateRevoked); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *GatewayStore) BeginCredentialRevocation(credentialID, actorID string) (CredentialResource, error) {
	resource, err := s.credentialByID(credentialID)
	if err != nil {
		return CredentialResource{}, err
	}
	if !s.canManageCredential(resource, strings.TrimSpace(actorID), ActionCredentialRevoke) {
		return CredentialResource{}, ErrCredentialForbidden
	}
	if resource.State == CredentialStateRevoked {
		return resource, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return CredentialResource{}, err
	}
	defer tx.Rollback()
	if resource.State == CredentialStateActive {
		now := time.Now().UTC()
		if _, err := tx.Exec(`UPDATE credentials SET state = ?, updated_at = ? WHERE id = ? AND state = ?`,
			CredentialStateRevoking, formatTime(now), resource.ID, CredentialStateActive); err != nil {
			return CredentialResource{}, err
		}
		resource.State = CredentialStateRevoking
		resource.UpdatedAt = now
	}
	// Revoking a resource revokes all of its grants, so they follow it to
	// revoking. This also moves grants that a revocation started before this
	// rule left active, so no grant reads as active while it is being cleaned.
	if _, err := tx.Exec(`UPDATE credential_grants SET state = ?, revoked_at = '' WHERE credential_id = ? AND state = ?`,
		CredentialStateRevoking, resource.ID, CredentialStateActive); err != nil {
		return CredentialResource{}, err
	}
	if err := tx.Commit(); err != nil {
		return CredentialResource{}, err
	}
	return resource, nil
}

func (s *GatewayStore) FinalizeCredentialRevocation(credentialID string) error {
	return s.finalizeCredentialRevocation(credentialID, 0)
}

func (s *GatewayStore) FinalizeCredentialRevocationAudited(credentialID string, auditID int64) error {
	return s.finalizeCredentialRevocation(credentialID, auditID)
}

func (s *GatewayStore) finalizeCredentialRevocation(credentialID string, auditID int64) error {
	resource, err := s.credentialByID(credentialID)
	if err != nil {
		return err
	}
	if resource.State == CredentialStateRevoked {
		if auditID != 0 {
			if err := s.FinishAudit(auditID, AuditFinish{
				Status: "success", EndedAt: time.Now().UTC(),
			}); err != nil {
				return fmt.Errorf("%w: %v", ErrCredentialAuditUnavailable, err)
			}
		}
		return nil
	}
	if resource.State != CredentialStateRevoking {
		return fmt.Errorf("credential is not revoking")
	}
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The same check as for a grant, over every grant of the resource.
	if pending, err := uncleanedCredentialMaterializations(tx, resource.ID, ""); err != nil {
		return err
	} else if pending != 0 {
		return ErrCredentialCleanupPending
	}
	if _, err := tx.Exec(`UPDATE credentials SET state = ?, revoked_at = ?, updated_at = ? WHERE id = ?`,
		CredentialStateRevoked, formatTime(now), formatTime(now), resource.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE credential_versions SET state = ?, revoked_at = ? WHERE credential_id = ? AND state != ?`,
		CredentialVersionRevoked, formatTime(now), resource.ID, CredentialVersionRevoked); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE credential_grants SET state = ?, revoked_at = ? WHERE credential_id = ? AND state != ?`,
		CredentialStateRevoked, formatTime(now), resource.ID, CredentialStateRevoked); err != nil {
		return err
	}
	if auditID != 0 {
		if err := finishAuditWith(tx, auditID, AuditFinish{Status: "success", EndedAt: now}); err != nil {
			return fmt.Errorf("%w: %v", ErrCredentialAuditUnavailable, err)
		}
	}
	return tx.Commit()
}

// ForceFinalizeOfflineCredentialRevocation abandons uncleaned materializations
// across all grants of a revoking credential whose targets are offline and
// advances the resource, versions, and grants to revoked in one audited
// transaction.
func (s *GatewayStore) ForceFinalizeOfflineCredentialRevocation(
	credentialID string,
	reason string,
	auditID int64,
) ([]string, error) {
	resource, err := s.credentialByID(credentialID)
	if err != nil {
		return nil, err
	}
	if resource.State == CredentialStateRevoked {
		if auditID != 0 {
			if err := s.FinishAudit(auditID, AuditFinish{
				Status:  "success",
				Tail:    forceOfflineRevocationAuditSummary(reason, nil),
				EndedAt: time.Now().UTC(),
			}); err != nil {
				return nil, fmt.Errorf("%w: %v", ErrCredentialAuditUnavailable, err)
			}
		}
		return nil, nil
	}
	if resource.State != CredentialStateRevoking {
		return nil, fmt.Errorf("credential is not revoking")
	}
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	abandonedTargets, err := abandonOfflineMaterializations(tx, resource.ID, "", now)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE credentials SET state = ?, revoked_at = ?, updated_at = ? WHERE id = ?`,
		CredentialStateRevoked, formatTime(now), formatTime(now), resource.ID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE credential_versions SET state = ?, revoked_at = ? WHERE credential_id = ? AND state != ?`,
		CredentialVersionRevoked, formatTime(now), resource.ID, CredentialVersionRevoked); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE credential_grants SET state = ?, revoked_at = ? WHERE credential_id = ? AND state != ?`,
		CredentialStateRevoked, formatTime(now), resource.ID, CredentialStateRevoked); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM credential_verifications WHERE credential_id = ?`, resource.ID); err != nil {
		return nil, err
	}
	if auditID != 0 {
		if err := finishAuditWith(tx, auditID, AuditFinish{
			Status:  "success",
			Tail:    forceOfflineRevocationAuditSummary(reason, abandonedTargets),
			EndedAt: now,
		}); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrCredentialAuditUnavailable, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return abandonedTargets, nil
}

func abandonOfflineMaterializations(tx *sql.Tx, credentialID, grantID string, now time.Time) ([]string, error) {
	query := `SELECT request_json FROM credential_materializations
		WHERE credential_id = ? AND status NOT IN ('cleaned', ?, ?)`
	args := []any{strings.TrimSpace(credentialID), credentialMaterializationNotApplied, credentialMaterializationAbandonedOffline}
	if strings.TrimSpace(grantID) != "" {
		query += ` AND grant_id = ?`
		args = append(args, strings.TrimSpace(grantID))
	}
	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	var targets []string
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			_ = rows.Close()
			return nil, err
		}
		var req CredentialMaterializeRequest
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			continue
		}
		if target := strings.TrimSpace(req.Cluster) + "/" + strings.TrimSpace(req.Instance); target != "/" && !containsString(targets, target) {
			targets = append(targets, target)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	sort.Strings(targets)

	updateSQL := `UPDATE credential_materializations
		SET status = ?, error_category = ?, updated_at = ?
		WHERE credential_id = ? AND status NOT IN ('cleaned', ?, ?)`
	updateArgs := []any{
		credentialMaterializationAbandonedOffline,
		credentialMaterializationAbandonedOfflineCategory,
		formatTime(now),
		strings.TrimSpace(credentialID),
		credentialMaterializationNotApplied,
		credentialMaterializationAbandonedOffline,
	}
	if strings.TrimSpace(grantID) != "" {
		updateSQL += ` AND grant_id = ?`
		updateArgs = append(updateArgs, strings.TrimSpace(grantID))
	}
	if _, err := tx.Exec(updateSQL, updateArgs...); err != nil {
		return nil, err
	}
	return targets, nil
}

func forceOfflineRevocationAuditSummary(reason string, abandonedTargets []string) string {
	targetsText := "none"
	if len(abandonedTargets) > 0 {
		targetsText = strings.Join(abandonedTargets, ",")
	}
	return sanitizeSecretText(fmt.Sprintf("force_offline=true abandoned_targets=%s reason=%s", targetsText, strings.TrimSpace(reason)))
}
