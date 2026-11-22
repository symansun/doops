package server

import (
	"fmt"
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
