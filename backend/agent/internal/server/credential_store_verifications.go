package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (s *GatewayStore) RecordCredentialVerification(verification CredentialVerification) error {
	return recordCredentialVerificationWith(s.db, verification)
}

func (s *GatewayStore) RecordCredentialVerificationsAudited(
	verifications []CredentialVerification,
	auditID int64,
	finish AuditFinish,
) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, verification := range verifications {
		if err := recordCredentialVerificationWith(tx, verification); err != nil {
			return err
		}
	}
	if err := finishAuditWith(tx, auditID, finish); err != nil {
		return fmt.Errorf("%w: %v", ErrCredentialAuditUnavailable, err)
	}
	return tx.Commit()
}

func recordCredentialVerificationWith(execer auditExecer, verification CredentialVerification) error {
	verification.CredentialID = strings.TrimSpace(verification.CredentialID)
	verification.VersionID = strings.TrimSpace(verification.VersionID)
	verification.GrantID = strings.TrimSpace(verification.GrantID)
	verification.Request.Payload = nil
	if verification.CredentialID == "" || verification.VersionID == "" || verification.GrantID == "" ||
		!validCredentialUse(verification.Use) || verification.Evidence.Status != "verified" {
		return fmt.Errorf("invalid credential verification")
	}
	requestJSON, err := json.Marshal(verification.Request)
	if err != nil {
		return err
	}
	evidenceJSON, err := json.Marshal(verification.Evidence)
	if err != nil {
		return err
	}
	if verification.ID == "" {
		verification.ID = "credverify_" + randomHex(12)
	}
	contextKey := credentialMaterializationContextKey(verification.Request)
	if verification.VerifiedAt.IsZero() {
		verification.VerifiedAt = time.Now().UTC()
	}
	_, err = execer.Exec(gatewayUpsertSQL(gatewayUpsert{insert: "INSERT INTO credential_verifications\n\t\t(id, credential_id, version_id, grant_id, use, context_key, request_json, evidence_json, status, verified_at)\n\t\tVALUES (?, ?, ?, ?, ?, ?, ?, ?, 'verified', ?)", target: []string{"version_id", "grant_id", "use", "context_key"}, set: []string{"id = excluded.id", "request_json = excluded.request_json", "evidence_json = excluded.evidence_json", "status = excluded.status", "verified_at = excluded.verified_at"}}),
		verification.ID, verification.CredentialID, verification.VersionID, verification.GrantID,
		verification.Use, contextKey, string(requestJSON), string(evidenceJSON), formatTime(verification.VerifiedAt))
	return err
}

func (s *GatewayStore) CredentialVerifications(credentialID, versionID string) ([]CredentialVerification, error) {
	rows, err := s.db.Query(`SELECT id, credential_id, version_id, grant_id, use, request_json, evidence_json, status, verified_at
		FROM credential_verifications WHERE credential_id = ? AND version_id = ? AND status = 'verified'
		ORDER BY grant_id, use, context_key`, strings.TrimSpace(credentialID), strings.TrimSpace(versionID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var verifications []CredentialVerification
	for rows.Next() {
		var verification CredentialVerification
		var requestJSON, evidenceJSON, verifiedAt string
		if err := rows.Scan(&verification.ID, &verification.CredentialID, &verification.VersionID,
			&verification.GrantID, &verification.Use, &requestJSON, &evidenceJSON, &verification.Status, &verifiedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(requestJSON), &verification.Request); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(evidenceJSON), &verification.Evidence); err != nil {
			return nil, err
		}
		verification.VerifiedAt, _ = parseTime(verifiedAt)
		verifications = append(verifications, verification)
	}
	return verifications, rows.Err()
}
