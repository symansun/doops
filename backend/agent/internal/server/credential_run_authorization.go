package server

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

const credentialRunAuthorizationTTL = 15 * time.Minute

var ErrCredentialPrepareBindingInvalid = errors.New("credential prepare binding invalid")

func (s *GatewayStore) IssueCredentialRunAuthorization(
	auth TokenAuth,
	request credentialPrepareHTTPBody,
	run CredentialRun,
) error {
	if strings.TrimSpace(run.ID) == "" || strings.TrimSpace(auth.UserID) == "" ||
		strings.TrimSpace(auth.TokenID) == "" || run.Cluster != request.Cluster ||
		run.Instance != request.Instance || run.Mode != request.Mode {
		return ErrCredentialPrepareBindingInvalid
	}
	digest, err := digestJSON(run)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = s.db.Exec(`INSERT INTO credential_run_authorizations
		(run_id, user_id, token_id, cluster, instance, session_id, workflow_path,
		 workspace_commit, mode, run_digest, state, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'issued', ?, ?)`,
		run.ID, auth.UserID, auth.TokenID, request.Cluster, request.Instance,
		request.SessionID, request.WorkflowPath, request.WorkspaceCommit, request.Mode,
		digest, formatTime(now), formatTime(now.Add(credentialRunAuthorizationTTL)))
	return err
}

func (s *GatewayStore) ConsumeCredentialRunAuthorization(
	auth TokenAuth,
	cluster string,
	instance string,
	sessionID string,
	workflowPath string,
	workspaceCommit string,
	mode string,
	run CredentialRun,
) error {
	digest, err := digestJSON(run)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var authorization CredentialRunAuthorization
	var createdAt, expiresAt, consumedAt string
	err = tx.QueryRow(`SELECT run_id, user_id, token_id, cluster, instance, session_id,
			workflow_path, workspace_commit, mode, run_digest, state, created_at, expires_at, consumed_at
		FROM credential_run_authorizations WHERE run_id = ?`, strings.TrimSpace(run.ID)).Scan(
		&authorization.RunID, &authorization.UserID, &authorization.TokenID,
		&authorization.Cluster, &authorization.Instance, &authorization.SessionID,
		&authorization.WorkflowPath, &authorization.WorkspaceCommit, &authorization.Mode,
		&authorization.RunDigest, &authorization.State, &createdAt, &expiresAt, &consumedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCredentialPrepareBindingInvalid
	}
	if err != nil {
		return err
	}
	authorization.ExpiresAt, err = parseTime(expiresAt)
	if err != nil {
		return err
	}
	if authorization.State != "issued" || !authorization.ExpiresAt.After(now) ||
		authorization.UserID != auth.UserID || authorization.TokenID != auth.TokenID ||
		authorization.Cluster != strings.TrimSpace(cluster) ||
		authorization.Instance != strings.TrimSpace(instance) ||
		authorization.SessionID != strings.TrimSpace(sessionID) ||
		authorization.WorkflowPath != strings.TrimSpace(workflowPath) ||
		authorization.WorkspaceCommit != strings.TrimSpace(workspaceCommit) ||
		authorization.Mode != strings.TrimSpace(mode) || authorization.RunDigest != digest ||
		run.Cluster != authorization.Cluster || run.Instance != authorization.Instance ||
		run.Mode != authorization.Mode {
		return ErrCredentialPrepareBindingInvalid
	}
	for _, materialization := range run.Materializations {
		if strings.TrimSpace(materialization.CredentialID) == "" ||
			strings.TrimSpace(materialization.VersionID) == "" ||
			strings.TrimSpace(materialization.GrantID) == "" {
			return ErrCredentialPrepareBindingInvalid
		}
		var credentialState, versionState, grantState string
		err := tx.QueryRow(`SELECT c.state, v.state, g.state
			FROM credentials c
			JOIN credential_versions v ON v.credential_id = c.id
			JOIN credential_grants g ON g.credential_id = c.id
			WHERE c.id = ? AND v.id = ? AND g.id = ?`,
			materialization.CredentialID, materialization.VersionID, materialization.GrantID,
		).Scan(&credentialState, &versionState, &grantState)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrCredentialPrepareBindingInvalid
		}
		if err != nil {
			return err
		}
		if credentialState != string(CredentialStateActive) ||
			versionState != string(CredentialVersionActive) ||
			grantState != string(CredentialStateActive) {
			return ErrCredentialPrepareBindingInvalid
		}
	}
	result, err := tx.Exec(`UPDATE credential_run_authorizations
		SET state = 'consumed', consumed_at = ?
		WHERE run_id = ? AND state = 'issued'`,
		formatTime(now), authorization.RunID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrCredentialPrepareBindingInvalid
	}
	return tx.Commit()
}

func (s *GatewayStore) RevokeCredentialRunAuthorization(runID string) error {
	result, err := s.db.Exec(`UPDATE credential_run_authorizations
		SET state = 'revoked', consumed_at = ?
		WHERE run_id = ? AND state = 'issued'`,
		formatTime(time.Now().UTC()), strings.TrimSpace(runID))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrCredentialPrepareBindingInvalid
	}
	return nil
}
