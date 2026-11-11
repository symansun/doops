package server

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Keep controller admission and polls until a later terminal observation of
// that exact Job exists. Materialize the evidence before deleting any rows.
const deleteCompletedAuditSQL = `WITH terminal AS MATERIALIZED (
	SELECT cluster, instance, correlation_id,
		json_extract(CASE WHEN json_valid(tail) THEN tail ELSE '{}' END, '$.result.job') AS job,
		MAX(id) AS id
	FROM audit_events
	WHERE action = 'agent:upgrade' AND status IN ('success', 'error')
		AND json_extract(CASE WHEN json_valid(tail) THEN tail ELSE '{}' END, '$.result.outcome') IN ('succeeded', 'failed')
	GROUP BY cluster, instance, correlation_id, job
)
DELETE FROM audit_events WHERE julianday(started_at) < julianday(?)
	AND status NOT IN ('running', 'executing', 'outcome_unknown') AND action != 'audit:purge'
	AND (status NOT IN ('accepted', 'observing') OR EXISTS (
		SELECT 1 FROM terminal WHERE terminal.id > audit_events.id
			AND terminal.cluster = audit_events.cluster AND terminal.instance = audit_events.instance
			AND terminal.correlation_id = audit_events.correlation_id AND terminal.correlation_id != ''
			AND terminal.job = json_extract(CASE WHEN json_valid(audit_events.tail) THEN audit_events.tail ELSE '{}' END, '$.result.job')
	))`

// PurgeAuditBefore requires a current platform admin for remote callers.
func (s *GatewayStore) PurgeAuditBefore(auth TokenAuth, before time.Time) (int64, error) {
	if !s.UserIsPlatformAdmin(auth.UserID) {
		return 0, ErrUnauthorized
	}
	return s.purgeAuditBefore(auth.UserID, auth.TokenID, before)
}

// PurgeLocalAuditBefore is for the local maintenance command with direct database
// access only. HTTP handlers must use PurgeAuditBefore for authorization.
func (s *GatewayStore) PurgeLocalAuditBefore(before time.Time) (int64, error) {
	return s.purgeAuditBefore(fmt.Sprintf("local:uid:%d", os.Geteuid()), "", before)
}

// The deletion and its receipt share a transaction. An unavailable audit write
// must leave every original event intact, including unknown execution outcomes.
func (s *GatewayStore) purgeAuditBefore(userID, tokenID string, before time.Time) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`INSERT INTO audit_events
		(user_id, token_id, action, session, command_summary, status, started_at)
		VALUES (?, ?, 'audit:purge', 'audit-purge', ?, 'running', ?)`,
		userID, tokenID, "audit:purge before="+formatTime(before.UTC()), formatTime(time.Now().UTC()))
	if err != nil {
		return 0, err
	}
	auditID, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	result, err = tx.Exec(deleteCompletedAuditSQL, formatTime(before.UTC()))
	if err != nil {
		return 0, err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	evidence, _ := json.Marshal(map[string]interface{}{"before": formatTime(before.UTC()), "deleted": deleted})
	if err := finishAuditWith(tx, auditID, AuditFinish{Status: "success", Tail: string(evidence)}); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return deleted, nil
}
