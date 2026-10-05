package server

import (
	"database/sql"
	"fmt"
	"time"
)

const interruptedExecutionError = "gateway restarted before execution completion was recorded; outcome unknown; machine terminal evidence required"

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
			error = 'execution owner no longer active; machine terminal evidence required',
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
