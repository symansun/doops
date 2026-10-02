package server

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"strings"
	"time"
)

//go:embed audit_indexes.sql
var auditIndexesSQL string

func (h *GatewayHub) HandleAdminAuditOptimize(w http.ResponseWriter, r *http.Request) {
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r)
		return
	}
	if !h.store.UserIsPlatformAdmin(auth.UserID) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		SessionID string `json:"session_id"`
	}
	if err := decodeCredentialJSON(w, r, &input); err != nil {
		http.Error(w, "invalid audit maintenance request", http.StatusBadRequest)
		return
	}
	if err := validateSession(input.SessionID); err != nil {
		http.Error(w, "valid session_id is required", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
	defer cancel()
	plans, err := h.store.optimizeAuditIndexes(ctx)
	if err != nil {
		http.Error(w, "audit_index_maintenance_failed", http.StatusInternalServerError)
		return
	}
	writeJSONHTTP(w, map[string]interface{}{"optimized": true, "definitions_verified": true, "query_plans": plans})
}

func (s *GatewayStore) optimizeAuditIndexes(ctx context.Context) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, auditIndexesSQL); err != nil {
		return nil, err
	}
	definitions := []struct {
		name    string
		columns []string
		query   string
		args    []any
	}{
		{"idx_audit_user_id", []string{"user_id", "id"}, "EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE user_id=? ORDER BY id DESC LIMIT 500", []any{"user"}},
		{"idx_audit_user_session_id", []string{"user_id", "session", "id"}, "EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE user_id=? AND session=? ORDER BY id DESC LIMIT 500", []any{"user", "session"}},
		{"idx_audit_target_id", []string{"cluster", "instance", "id"}, "EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE cluster=? AND instance=? ORDER BY id DESC LIMIT 500", []any{"cluster", "instance"}},
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
