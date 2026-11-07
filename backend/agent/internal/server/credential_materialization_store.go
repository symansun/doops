package server

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

type CredentialMaterializationRecord struct {
	ID            string
	CredentialID  string
	VersionID     string
	GrantID       string
	Use           CredentialUse
	ContextKey    string
	Request       CredentialMaterializeRequest
	Evidence      CredentialMaterialization
	Status        string
	ErrorCategory string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (s *GatewayStore) RecordCredentialMaterializationIntent(
	request CredentialMaterializeRequest,
) (CredentialMaterializationRecord, error) {
	request.Payload = nil
	contextKey := credentialMaterializationContextKey(request)
	if strings.TrimSpace(request.CredentialID) == "" ||
		strings.TrimSpace(request.VersionID) == "" ||
		strings.TrimSpace(request.GrantID) == "" ||
		contextKey == "" || validateCredentialGrantScope(request.Scope) != nil {
		return CredentialMaterializationRecord{}, ErrCredentialPayloadInvalid
	}
	requestJSON, err := json.Marshal(request)
	if err != nil {
		return CredentialMaterializationRecord{}, err
	}
	now := time.Now().UTC()
	record := CredentialMaterializationRecord{
		ID:           "credmat_" + randomHex(12),
		CredentialID: request.CredentialID,
		VersionID:    request.VersionID,
		GrantID:      request.GrantID,
		Use:          request.Scope.Use,
		ContextKey:   contextKey,
		Request:      request,
		Status:       "pending",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	_, err = s.db.Exec(`INSERT INTO credential_materializations
		(id, credential_id, version_id, grant_id, use, context_key, request_json,
		 evidence_json, status, error_category, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, '{}', 'pending', '', ?, ?)
		ON CONFLICT(version_id, grant_id, use, context_key) DO UPDATE SET
			id = excluded.id,
			request_json = excluded.request_json,
			evidence_json = '{}',
			status = 'pending',
			error_category = '',
			updated_at = excluded.updated_at`,
		record.ID, record.CredentialID, record.VersionID, record.GrantID, record.Use,
		record.ContextKey, string(requestJSON), formatTime(now), formatTime(now))
	return record, err
}

func (s *GatewayStore) CompleteCredentialMaterialization(
	id string,
	evidence CredentialMaterialization,
) error {
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	result, err := s.db.Exec(`UPDATE credential_materializations
		SET evidence_json = ?, status = 'verified', error_category = '', updated_at = ?
		WHERE id = ?`,
		string(evidenceJSON), formatTime(time.Now().UTC()), strings.TrimSpace(id))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *GatewayStore) MarkCredentialMaterializationUnknown(id, category string) error {
	_, err := s.db.Exec(`UPDATE credential_materializations
		SET status = 'outcome_unknown', error_category = ?, updated_at = ?
		WHERE id = ?`,
		sanitizeCredentialCategory(category), formatTime(time.Now().UTC()), strings.TrimSpace(id))
	return err
}

func (s *GatewayStore) MarkCredentialMaterializationCleaned(request CredentialMaterializeRequest) error {
	_, err := s.db.Exec(`UPDATE credential_materializations
		SET status = 'cleaned', error_category = '', updated_at = ?
		WHERE version_id = ? AND grant_id = ? AND use = ? AND context_key = ?`,
		formatTime(time.Now().UTC()), request.VersionID, request.GrantID, request.Scope.Use,
		credentialMaterializationContextKey(request))
	return err
}

func (s *GatewayStore) MarkCredentialMaterializationContextCleaned(request CredentialMaterializeRequest) error {
	_, err := s.db.Exec(`UPDATE credential_materializations
		SET status = 'cleaned', error_category = '', updated_at = ?
		WHERE credential_id = ? AND grant_id = ? AND use = ? AND context_key = ?`,
		formatTime(time.Now().UTC()), request.CredentialID, request.GrantID, request.Scope.Use,
		credentialMaterializationContextKey(request))
	return err
}

func (s *GatewayStore) CredentialMaterializations(
	credentialID string,
	versionID string,
	grantID string,
) ([]CredentialMaterializationRecord, error) {
	query := `SELECT id, credential_id, version_id, grant_id, use, context_key,
		request_json, evidence_json, status, error_category, created_at, updated_at
		FROM credential_materializations
		WHERE credential_id = ? AND status != 'cleaned'`
	args := []any{strings.TrimSpace(credentialID)}
	if strings.TrimSpace(versionID) != "" {
		query += ` AND version_id = ?`
		args = append(args, strings.TrimSpace(versionID))
	}
	if strings.TrimSpace(grantID) != "" {
		query += ` AND grant_id = ?`
		args = append(args, strings.TrimSpace(grantID))
	}
	query += ` ORDER BY grant_id, use, context_key, updated_at DESC, created_at DESC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []CredentialMaterializationRecord
	for rows.Next() {
		var record CredentialMaterializationRecord
		var requestJSON, evidenceJSON, createdAt, updatedAt string
		if err := rows.Scan(
			&record.ID, &record.CredentialID, &record.VersionID, &record.GrantID,
			&record.Use, &record.ContextKey, &requestJSON, &evidenceJSON,
			&record.Status, &record.ErrorCategory, &createdAt, &updatedAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(requestJSON), &record.Request); err != nil {
			return nil, err
		}
		if evidenceJSON != "" && evidenceJSON != "{}" {
			if err := json.Unmarshal([]byte(evidenceJSON), &record.Evidence); err != nil {
				return nil, err
			}
		}
		record.CreatedAt, _ = parseTime(createdAt)
		record.UpdatedAt, _ = parseTime(updatedAt)
		records = append(records, record)
	}
	return records, rows.Err()
}

func sanitizeCredentialCategory(category string) string {
	category = strings.TrimSpace(category)
	if category == "" {
		return "credential_operation_failed"
	}
	for _, char := range category {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return "credential_operation_failed"
		}
	}
	return category
}
