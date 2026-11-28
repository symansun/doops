package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *GatewayStore) PutCredentialVersion(req CredentialVersionPutRequest) (CredentialVersion, error) {
	return s.putCredentialVersion(req, 0)
}

func (s *GatewayStore) PutCredentialVersionAudited(req CredentialVersionPutRequest, auditID int64) (CredentialVersion, error) {
	return s.putCredentialVersion(req, auditID)
}

func (s *GatewayStore) putCredentialVersion(req CredentialVersionPutRequest, auditID int64) (CredentialVersion, error) {
	req.CredentialID = strings.TrimSpace(req.CredentialID)
	req.CreatedBy = strings.TrimSpace(req.CreatedBy)
	if req.CredentialID == "" || req.CreatedBy == "" {
		return CredentialVersion{}, fmt.Errorf("credential id and creator are required")
	}
	if len(req.Payload) == 0 || !json.Valid(req.Payload) {
		return CredentialVersion{}, ErrCredentialPayloadInvalid
	}
	resource, err := s.credentialByID(req.CredentialID)
	if err != nil {
		return CredentialVersion{}, err
	}
	if resource.State != CredentialStateActive {
		return CredentialVersion{}, ErrCredentialRevoked
	}
	if !s.canManageCredential(resource, req.CreatedBy, ActionCredentialRotate) {
		return CredentialVersion{}, ErrCredentialForbidden
	}
	if err := validateCredentialPayload(resource.Type, req.Payload); err != nil {
		return CredentialVersion{}, ErrCredentialPayloadInvalid
	}

	digest := credentialPayloadDigest(req.Payload)
	now := time.Now().UTC()
	version := CredentialVersion{
		ID:            "credver_" + randomHex(12),
		CredentialID:  resource.ID,
		PayloadDigest: digest,
		State:         CredentialVersionStaged,
		CreatedBy:     req.CreatedBy,
		CreatedAt:     now,
	}
	if req.Activate {
		version.State = CredentialVersionActive
		version.PromotedAt = &now
	}
	ciphertext, err := s.encryptSecretWithAAD(string(req.Payload), credentialPayloadAAD(resource.ID, version.ID, resource.Type, digest))
	if err != nil {
		return CredentialVersion{}, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return CredentialVersion{}, err
	}
	defer tx.Rollback()
	if req.Activate {
		if _, err := tx.Exec(`UPDATE credential_versions SET state = ? WHERE credential_id = ? AND state = ?`,
			CredentialVersionSuperseded, resource.ID, CredentialVersionActive); err != nil {
			return CredentialVersion{}, fmt.Errorf("%w: %v", ErrCredentialAuditUnavailable, err)
		}
	}
	var promotedAt string
	if version.PromotedAt != nil {
		promotedAt = formatTime(*version.PromotedAt)
	}
	if _, err := tx.Exec(`INSERT INTO credential_versions
		(id, credential_id, payload_ciphertext, payload_digest, state, revoked_at, created_by, created_at, promoted_at)
		VALUES (?, ?, ?, ?, ?, '', ?, ?, ?)`,
		version.ID, version.CredentialID, ciphertext, version.PayloadDigest, version.State,
		version.CreatedBy, formatTime(version.CreatedAt), promotedAt); err != nil {
		return CredentialVersion{}, err
	}
	if _, err := tx.Exec(`UPDATE credentials SET updated_at = ? WHERE id = ?`, formatTime(now), resource.ID); err != nil {
		return CredentialVersion{}, err
	}
	if auditID != 0 {
		if err := finishAuditWith(tx, auditID, AuditFinish{
			Status: "success", VersionID: version.ID, EndedAt: now,
		}); err != nil {
			return CredentialVersion{}, fmt.Errorf("%w: %v", ErrCredentialAuditUnavailable, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return CredentialVersion{}, err
	}
	return version, nil
}

func (s *GatewayStore) ResolveCredentialPayload(credentialID, versionID string) (json.RawMessage, error) {
	credentialID = strings.TrimSpace(credentialID)
	versionID = strings.TrimSpace(versionID)
	var resource CredentialResource
	var version CredentialVersion
	var ciphertext string
	err := s.db.QueryRow(`SELECT c.id, c.name, c.scope, c.owner_id, c.type, c.state, c.created_by, c.created_at, c.updated_at, c.revoked_at,
			v.id, v.credential_id, v.payload_ciphertext, v.payload_digest, v.state, v.created_by, v.created_at, v.promoted_at, v.revoked_at
		FROM credentials c JOIN credential_versions v ON v.credential_id = c.id
		WHERE c.id = ? AND v.id = ? AND c.scope <> ?`, credentialID, versionID, machineVaultScope).Scan(
		&resource.ID, &resource.Name, &resource.Scope, &resource.OwnerID, &resource.Type, &resource.State,
		&resource.CreatedBy, new(string), new(string), new(string),
		&version.ID, &version.CredentialID, &ciphertext, &version.PayloadDigest, &version.State,
		&version.CreatedBy, new(string), new(string), new(string),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCredentialVersionNotFound
	}
	if err != nil {
		return nil, err
	}
	if resource.State == CredentialStateRevoked || version.State == CredentialVersionRevoked {
		return nil, ErrCredentialRevoked
	}
	plain, err := s.decryptSecretWithAAD(ciphertext, credentialPayloadAAD(resource.ID, version.ID, resource.Type, version.PayloadDigest))
	if err != nil {
		return nil, err
	}
	return json.RawMessage(plain), nil
}

func (s *GatewayStore) ActiveCredentialVersion(credentialID string) (CredentialVersion, error) {
	resource, err := s.credentialByID(credentialID)
	if err != nil {
		return CredentialVersion{}, err
	}
	if resource.State != CredentialStateActive {
		return CredentialVersion{}, ErrCredentialRevoked
	}
	version, err := s.credentialVersionByState(resource.ID, CredentialVersionActive)
	if errors.Is(err, sql.ErrNoRows) {
		return CredentialVersion{}, ErrCredentialVersionNotFound
	}
	return version, err
}

func (s *GatewayStore) PromoteCredentialVersion(credentialID, versionID, actorID string) error {
	return s.promoteCredentialVersion(credentialID, versionID, actorID, 0)
}

func (s *GatewayStore) PromoteCredentialVersionAudited(credentialID, versionID, actorID string, auditID int64) error {
	return s.promoteCredentialVersion(credentialID, versionID, actorID, auditID)
}

func (s *GatewayStore) promoteCredentialVersion(credentialID, versionID, actorID string, auditID int64) error {
	resource, err := s.credentialByID(credentialID)
	if err != nil {
		return err
	}
	if resource.State != CredentialStateActive {
		return ErrCredentialRevoked
	}
	if !s.canManageCredential(resource, strings.TrimSpace(actorID), ActionCredentialRotate) {
		return ErrCredentialForbidden
	}
	version, err := s.credentialVersionByID(resource.ID, versionID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCredentialVersionNotFound
	}
	if err != nil {
		return err
	}
	if version.State != CredentialVersionStaged {
		return fmt.Errorf("credential version is not staged")
	}
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE credential_versions SET state = ? WHERE credential_id = ? AND state = ?`,
		CredentialVersionSuperseded, resource.ID, CredentialVersionActive); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE credential_versions SET state = ?, promoted_at = ? WHERE id = ?`,
		CredentialVersionActive, formatTime(now), version.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE credentials SET updated_at = ? WHERE id = ?`, formatTime(now), resource.ID); err != nil {
		return err
	}
	if auditID != 0 {
		if err := finishAuditWith(tx, auditID, AuditFinish{
			Status: "success", VersionID: version.ID, EndedAt: now,
		}); err != nil {
			return fmt.Errorf("%w: %v", ErrCredentialAuditUnavailable, err)
		}
	}
	return tx.Commit()
}

func (s *GatewayStore) credentialVersionByID(credentialID, versionID string) (CredentialVersion, error) {
	row := s.db.QueryRow(`SELECT id, credential_id, payload_digest, state, created_by, created_at, promoted_at, revoked_at
		FROM credential_versions WHERE credential_id = ? AND id = ?`, strings.TrimSpace(credentialID), strings.TrimSpace(versionID))
	return scanCredentialVersion(row)
}

func (s *GatewayStore) CredentialVersionByID(credentialID, versionID string) (CredentialVersion, error) {
	version, err := s.credentialVersionByID(credentialID, versionID)
	if errors.Is(err, sql.ErrNoRows) {
		return CredentialVersion{}, ErrCredentialVersionNotFound
	}
	return version, err
}

func (s *GatewayStore) CredentialVersions(credentialID string) ([]CredentialVersion, error) {
	rows, err := s.db.Query(`SELECT id, credential_id, payload_digest, state, created_by, created_at, promoted_at, revoked_at
		FROM credential_versions WHERE credential_id = ? ORDER BY created_at DESC, id DESC`, strings.TrimSpace(credentialID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := make([]CredentialVersion, 0)
	for rows.Next() {
		version, err := scanCredentialVersion(rows)
		if err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

func (s *GatewayStore) credentialVersionByState(credentialID string, state CredentialVersionState) (CredentialVersion, error) {
	row := s.db.QueryRow(`SELECT id, credential_id, payload_digest, state, created_by, created_at, promoted_at, revoked_at
		FROM credential_versions WHERE credential_id = ? AND state = ?`, credentialID, state)
	return scanCredentialVersion(row)
}

func (s *GatewayStore) CredentialVersionByState(credentialID string, state CredentialVersionState) (CredentialVersion, error) {
	version, err := s.credentialVersionByState(strings.TrimSpace(credentialID), state)
	if errors.Is(err, sql.ErrNoRows) {
		return CredentialVersion{}, ErrCredentialVersionNotFound
	}
	return version, err
}
