package server

import (
	"database/sql"
	"encoding/json"
	"errors"
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
	// prior is this row as it was before the current attempt replaced it. The
	// attempt overwrites the only record of what an earlier attempt left on the
	// target, so a failure that left the target unchanged puts it back.
	prior *credentialMaterializationPrior
}

type credentialMaterializationPrior struct {
	status        string
	errorCategory string
	requestJSON   string
	evidenceJSON  string
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
	tx, err := s.db.Begin()
	if err != nil {
		return CredentialMaterializationRecord{}, err
	}
	defer tx.Rollback()
	// A revocation moves the resource and grant to revoking before it reads the
	// records it must clean. Refusing an intent once either one has left active
	// keeps every record a revocation has to clean visible to it, so a use that
	// races a revocation cannot add a Secret after the cleanup list was read.
	if err := credentialGrantUsable(tx, record.CredentialID, record.GrantID); err != nil {
		return CredentialMaterializationRecord{}, err
	}
	var prior credentialMaterializationPrior
	err = tx.QueryRow(`SELECT status, error_category, request_json, evidence_json
		FROM credential_materializations
		WHERE version_id = ? AND grant_id = ? AND use = ? AND context_key = ?`,
		record.VersionID, record.GrantID, record.Use, record.ContextKey,
	).Scan(&prior.status, &prior.errorCategory, &prior.requestJSON, &prior.evidenceJSON)
	switch {
	case err == nil:
		record.prior = &prior
	case !errors.Is(err, sql.ErrNoRows):
		return CredentialMaterializationRecord{}, err
	}
	if _, err := tx.Exec(`INSERT INTO credential_materializations
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
		record.ContextKey, string(requestJSON), formatTime(now), formatTime(now)); err != nil {
		return CredentialMaterializationRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return CredentialMaterializationRecord{}, err
	}
	return record, nil
}

type credentialRowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

// credentialGrantUsable returns ErrCredentialRevoked unless the resource and
// the grant are both active. A missing row counts as revoked.
func credentialGrantUsable(q credentialRowQuerier, credentialID, grantID string) error {
	var resourceState, grantState CredentialState
	err := q.QueryRow(`SELECT c.state, g.state
		FROM credentials c JOIN credential_grants g ON g.credential_id = c.id
		WHERE c.id = ? AND g.id = ?`,
		strings.TrimSpace(credentialID), strings.TrimSpace(grantID),
	).Scan(&resourceState, &grantState)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrCredentialRevoked
	case err != nil:
		return err
	case resourceState != CredentialStateActive || grantState != CredentialStateActive:
		return ErrCredentialRevoked
	}
	return nil
}

// CredentialGrantUsable reports, as credentialGrantUsable does, whether a
// grant may still be materialized.
func (s *GatewayStore) CredentialGrantUsable(credentialID, grantID string) error {
	return credentialGrantUsable(s.db, credentialID, grantID)
}

// uncleanedCredentialMaterializations counts the records of a credential, or
// of one of its grants, that may still own something on a target.
func uncleanedCredentialMaterializations(q credentialRowQuerier, credentialID, grantID string) (int, error) {
	query := `SELECT COUNT(*) FROM credential_materializations
		WHERE credential_id = ? AND status NOT IN ('cleaned', ?)`
	args := []any{strings.TrimSpace(credentialID), credentialMaterializationNotApplied}
	if strings.TrimSpace(grantID) != "" {
		query += ` AND grant_id = ?`
		args = append(args, strings.TrimSpace(grantID))
	}
	var count int
	err := q.QueryRow(query, args...).Scan(&count)
	return count, err
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

// credentialMaterializationNotApplied is the terminal status of a failed
// materialization that provably left the target as it was, so revocation has
// nothing to clean on the target.
const credentialMaterializationNotApplied = "not_applied"

// credentialMaterializationUnchangedCategories lists the failures that prove
// the target was not changed. Each one is returned either before anything is
// dispatched or written, or only after the Agent restored the target from its
// snapshot and re-read it to confirm (restoreCredentialTarget). Categories are
// those of the Agent at 3cba4ac. Anything else, including a category this list
// does not name, may have left a change and still needs a cleanup.
var credentialMaterializationUnchangedCategories = map[string]bool{
	// Gateway, before the materialize call reaches the Agent.
	"target_offline":                   true,
	"credential_request_encode_failed": true,
	"execution_audit_unavailable":      true,
	"credential_target_lock_changed":   true,
	// Agent, request and payload checks before any probe or write.
	"credential_request_invalid":  true,
	"credential_use_mismatch":     true,
	"credential_key_mismatch":     true,
	"credential_type_unsupported": true,
	"tls_key_pair_invalid":        true,
	// Agent, read-only registry, Git and model probes before the target is touched.
	"registry_verification_context_invalid": true,
	"registry_request_invalid":              true,
	"registry_manifest_unavailable":         true,
	"registry_manifest_invalid":             true,
	"registry_digest_missing":               true,
	"registry_digest_invalid":               true,
	"registry_digest_mismatch":              true,
	"registry_reference_digest_mismatch":    true,
	"registry_auth_challenge_invalid":       true,
	"registry_auth_failed":                  true,
	"registry_auth_response_invalid":        true,
	"git_remote_invalid":                    true,
	"git_remote_unavailable":                true,
	"git_verification_setup_failed":         true,
	"model_probe_unavailable":               true,
	"model_probe_result_invalid":            true,
	"model_probe_failed":                    true,
	// Agent, a settings file replaced atomically or not at all.
	"model_settings_write_failed": true,
	// Agent, a native Helm broker that removes its own files when it fails.
	"helm_native_context_invalid":     true,
	"helm_native_context_unavailable": true,
	// Agent, the snapshot read before apply, or a failed apply, patch or
	// verification after the snapshot was restored and confirmed.
	"credential_secret_get_failed":            true,
	"credential_secret_verification_failed":   true,
	"credential_secret_ownership_conflict":    true,
	"credential_workload_invalid":             true,
	"credential_workload_get_failed":          true,
	"credential_manifest_invalid":             true,
	"credential_target_mutation_failed":       true,
	"credential_workload_verification_failed": true,
}

// MarkCredentialMaterializationFailed records a materialization attempt that
// failed before the Agent reported success. A failure outside the unchanged
// list becomes outcome_unknown. A failure on the list proves only that this
// attempt left the target as it was, so the row goes back to what it said
// before the attempt, or to not_applied when there was no earlier attempt.
func (s *GatewayStore) MarkCredentialMaterializationFailed(intent CredentialMaterializationRecord, category string) error {
	category = sanitizeCredentialCategory(category)
	if !credentialMaterializationUnchangedCategories[category] {
		return s.MarkCredentialMaterializationUnknown(intent.ID, category)
	}
	if intent.prior != nil {
		_, err := s.db.Exec(`UPDATE credential_materializations
			SET status = ?, error_category = ?, request_json = ?, evidence_json = ?, updated_at = ?
			WHERE id = ?`,
			intent.prior.status, intent.prior.errorCategory, intent.prior.requestJSON, intent.prior.evidenceJSON,
			formatTime(time.Now().UTC()), strings.TrimSpace(intent.ID))
		return err
	}
	_, err := s.db.Exec(`UPDATE credential_materializations
		SET status = ?, error_category = ?, updated_at = ?
		WHERE id = ?`,
		credentialMaterializationNotApplied, category, formatTime(time.Now().UTC()), strings.TrimSpace(intent.ID))
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

// CredentialMaterializations lists the records that may still own something on
// the target. cleaned and not_applied rows own nothing. An outcome_unknown row
// is listed whatever its category: a row written before not_applied existed may
// have replaced an earlier successful attempt of the same row, so its category
// does not prove the target holds nothing.
func (s *GatewayStore) CredentialMaterializations(
	credentialID string,
	versionID string,
	grantID string,
) ([]CredentialMaterializationRecord, error) {
	query := `SELECT id, credential_id, version_id, grant_id, use, context_key,
		request_json, evidence_json, status, error_category, created_at, updated_at
		FROM credential_materializations
		WHERE credential_id = ? AND status NOT IN ('cleaned', 'not_applied')`
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
