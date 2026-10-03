package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const executionAttestationMaxBytes = 1024

const interruptedExecutionError = "gateway restarted before execution completion was recorded; outcome unknown; administrator external reconciliation required"

// MarkExecutionDispatched must succeed before sending an operation to an agent.
func (s *GatewayStore) MarkExecutionDispatched(id int64) error {
	result, err := s.db.Exec(`UPDATE audit_events SET status = 'executing'
		WHERE id = ? AND status = 'running'`, id)
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

// MarkExecutionUncertain requires the caller to first exclude a live operation.
// Losing the execution owner does not establish that the remote operation stopped.
func (s *GatewayStore) MarkExecutionUncertain(id int64) error {
	result, err := s.db.Exec(`UPDATE audit_events
		SET status = 'outcome_unknown',
			error = 'execution owner no longer active; external reconciliation required',
			ended_at = ?
		WHERE id = ? AND status = 'executing'`, formatTime(time.Now().UTC()), id)
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

// RecoverInterruptedExecutions runs at hub construction, before accepting work.
func (s *GatewayStore) RecoverInterruptedExecutions() error {
	_, err := s.db.Exec(`UPDATE audit_events
		SET status = 'outcome_unknown',
			error = CASE WHEN error = '' THEN ? ELSE error || '; ' || ? END,
			ended_at = ?
		WHERE status = 'executing'`,
		interruptedExecutionError, interruptedExecutionError, formatTime(time.Now().UTC()))
	return err
}

// UnresolvedExecutions includes executing rows even when no live operation remains.
// Only a complete target pair narrows the query; blockers are never limited.
func (s *GatewayStore) UnresolvedExecutions(cluster, instance string) ([]AuditEvent, error) {
	query := `SELECT id, user_id, token_id, cluster, instance, action, session,
		command_summary, status, error, started_at, ended_at
		FROM audit_events WHERE status IN ('executing', 'outcome_unknown')`
	var args []any
	if cluster != "" && instance != "" {
		query += " AND cluster = ? AND instance = ?"
		args = append(args, cluster, instance)
	}
	query += " ORDER BY id DESC"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]AuditEvent, 0)
	for rows.Next() {
		var event AuditEvent
		var started, ended string
		if err := rows.Scan(&event.ID, &event.UserID, &event.TokenID, &event.Cluster,
			&event.Instance, &event.Action, &event.Session, &event.CommandSummary,
			&event.Status, &event.Error, &started, &ended); err != nil {
			return nil, err
		}
		event.StartedAt, err = parseTime(started)
		if err != nil {
			return nil, fmt.Errorf("execution audit %d started_at: %w", event.ID, err)
		}
		if ended != "" {
			event.EndedAt, err = parseTime(ended)
			if err != nil {
				return nil, fmt.Errorf("execution audit %d ended_at: %w", event.ID, err)
			}
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

type executionResolutionMetadata struct {
	ExecutionAuditID int64  `json:"execution_audit_id"`
	Attestation      string `json:"attestation"`
	Reason           string `json:"reason"`
	Evidence         string `json:"evidence"`
	MachineVerified  bool   `json:"machine_verified"`
}

// ResolveExecutionOutcome records human external reconciliation, not verification.
// The caller must require platform admin authorization, confirm_stopped, and no
// associated live operation. Reason and evidence each have a 1024-byte UTF-8 limit.
func (s *GatewayStore) ResolveExecutionOutcome(id int64, userID, reason, evidence string) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return errors.New("execution resolution requires an administrator user ID")
	}
	reason, err := executionAttestationText("reason", reason)
	if err != nil {
		return err
	}
	evidence, err = executionAttestationText("evidence", evidence)
	if err != nil {
		return err
	}
	metadata, err := json.Marshal(executionResolutionMetadata{
		ExecutionAuditID: id,
		Attestation:      "administrator_external_reconciliation",
		Reason:           reason,
		Evidence:         evidence,
		MachineVerified:  false,
	})
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Keep the original error, evidence and interruption time intact.
	result, err := tx.Exec(`UPDATE audit_events SET status = 'resolved_manually'
		WHERE id = ? AND status = 'outcome_unknown'`, id)
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
	now := formatTime(time.Now().UTC())
	result, err = tx.Exec(`INSERT INTO audit_events
		(user_id, cluster, instance, correlation_id, action, session,
		 command_summary, status, started_at, ended_at)
		SELECT ?, cluster, instance, correlation_id, 'execution:resolve', session,
			?, 'resolved_manually', ?, ?
		FROM audit_events WHERE id = ?`, userID, string(metadata), now, now, id)
	if err != nil {
		return err
	}
	affected, err = result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New("execution resolution attestation was not recorded")
	}
	return tx.Commit()
}

func executionAttestationText(field, value string) (string, error) {
	if len(value) > executionAttestationMaxBytes || !utf8.ValidString(value) {
		return "", fmt.Errorf("execution resolution %s must be valid UTF-8 and at most %d bytes", field, executionAttestationMaxBytes)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("execution resolution %s is required", field)
	}
	value = sanitizeSecretText(value)
	// Redaction can expand short secrets. Reject expansion beyond the limit rather
	// than accepting the shared sanitizer's 1200-byte tail truncation.
	if len(value) > executionAttestationMaxBytes || !utf8.ValidString(value) {
		return "", fmt.Errorf("sanitized execution resolution %s exceeds %d bytes", field, executionAttestationMaxBytes)
	}
	return value, nil
}
