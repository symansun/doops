package server

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
)

//go:embed audit_indexes.sql
var auditIndexesSQL string

func (s *GatewayStore) optimizeAuditIndexes(ctx context.Context) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, auditIndexesSQL); err != nil {
		return nil, err
	}
	// An index is listed once per filter combination whose plan must use it.
	definitions := []struct {
		name    string
		columns []string
		query   string
		args    []any
	}{
		{"idx_audit_user_id", []string{"user_id", "id"}, "EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE user_id=? ORDER BY id DESC LIMIT 500", []any{"user"}},
		{"idx_audit_user_session_id", []string{"user_id", "session", "id"}, "EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE user_id=? AND session=? ORDER BY id DESC LIMIT 500", []any{"user", "session"}},
		{"idx_audit_target_id", []string{"cluster", "instance", "id"}, "EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE cluster=? AND instance=? ORDER BY id DESC LIMIT 500", []any{"cluster", "instance"}},
		{"idx_audit_action_id", []string{"action", "id", "status"}, "EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE action=? ORDER BY id DESC LIMIT 500", []any{"action"}},
		{"idx_audit_status_id", []string{"status", "id", "action"}, "EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE status=? ORDER BY id DESC LIMIT 500", []any{"status"}},
		{"idx_audit_user_action_id", []string{"user_id", "action", "id", "status"}, "EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE user_id=? AND action=? ORDER BY id DESC LIMIT 500", []any{"user", "action"}},
		{"idx_audit_user_status_id", []string{"user_id", "status", "id", "action"}, "EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE user_id=? AND status=? ORDER BY id DESC LIMIT 500", []any{"user", "status"}},
		{"idx_audit_target_action_id", []string{"cluster", "instance", "action", "id", "status"}, "EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE cluster=? AND instance=? AND action=? ORDER BY id DESC LIMIT 500", []any{"cluster", "instance", "action"}},
		{"idx_audit_target_status_id", []string{"cluster", "instance", "status", "id", "action"}, "EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE cluster=? AND instance=? AND status=? ORDER BY id DESC LIMIT 500", []any{"cluster", "instance", "status"}},
		{"idx_audit_session_target_id", []string{"session", "cluster", "instance", "id"}, "EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE cluster=? AND instance=? AND session=? ORDER BY id DESC LIMIT 500", []any{"cluster", "instance", "session"}},
		{"idx_audit_session_target_id", []string{"session", "cluster", "instance", "id"}, "EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE session=? ORDER BY id DESC LIMIT 500", []any{"session"}},
	}
	plans := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		rows, err := tx.QueryContext(ctx, "PRAGMA index_xinfo('"+definition.name+"')")
		if err != nil {
			return nil, err
		}
		var columns []string
		validOrder := true
		for rows.Next() {
			var sequence, cid, descending, key int
			var name, collation *string
			if err := rows.Scan(&sequence, &cid, &name, &descending, &collation, &key); err != nil {
				rows.Close()
				return nil, err
			}
			if key == 0 {
				continue
			}
			if name == nil {
				validOrder = false
				continue
			}
			columns = append(columns, *name)
			if (*name == "id" && descending != 1) || (*name != "id" && descending != 0) {
				validOrder = false
			}
		}
		readErr := rows.Err()
		rows.Close()
		if readErr != nil {
			return nil, readErr
		}
		if !validOrder || !sameStringList(columns, definition.columns) {
			return nil, fmt.Errorf("audit index definition mismatch: %s", definition.name)
		}
		var id, parent, unused int
		var plan string
		if err := tx.QueryRowContext(ctx, definition.query, definition.args...).Scan(&id, &parent, &unused, &plan); err != nil {
			return nil, err
		}
		if !strings.Contains(plan, definition.name) {
			return nil, fmt.Errorf("audit query plan does not use %s", definition.name)
		}
		plans = append(plans, plan)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return plans, nil
}
