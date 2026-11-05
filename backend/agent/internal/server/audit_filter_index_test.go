package server

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"
)

// auditFilterRow is one synthetic audit row, kept so the test can compute the
// expected result of every filter without the database.
type auditFilterRow struct {
	id                                               int64
	user, cluster, instance, session, action, status string
}

func (r auditFilterRow) matches(filter AuditFilter) bool {
	for _, field := range []struct{ want, got string }{
		{filter.UserID, r.user}, {filter.Cluster, r.cluster}, {filter.Instance, r.instance},
		{filter.Session, r.session}, {string(filter.Action), r.action}, {filter.Status, r.status},
	} {
		if field.want != "" && field.want != field.got {
			return false
		}
	}
	return true
}

// seedAuditFilterRows writes target audits with per-target sessions and
// credential resource audits, which leave the target empty and store the
// credential id as the session.
func seedAuditFilterRows(t *testing.T, store *GatewayStore, count int) []auditFilterRow {
	t.Helper()
	rng := rand.New(rand.NewSource(6))
	users := []string{"usr_a", "usr_b", "usr_c"}
	targets := [][2]string{{"prod", "node-1"}, {"prod", "node-2"}, {"dev", "node-1"}, {"", ""}}
	actions := []string{"exec", "ask", "read", "credential:verify", "credential:rotate"}
	statuses := []string{"success", "error", "blocked", "outcome_unknown"}
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO audit_events(user_id, cluster, instance, session, action, status, started_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	rows := make([]auditFilterRow, 0, count)
	for range count {
		target := targets[rng.Intn(len(targets))]
		row := auditFilterRow{
			user: users[rng.Intn(len(users))], cluster: target[0], instance: target[1],
			session: fmt.Sprintf("s-%d", rng.Intn(12)),
			action:  actions[rng.Intn(len(actions))], status: statuses[rng.Intn(len(statuses))],
		}
		if row.cluster == "" {
			row.session = fmt.Sprintf("cred_%024d", rng.Intn(4))
		}
		result, err := stmt.Exec(row.user, row.cluster, row.instance, row.session, row.action, row.status, "2026-11-01T00:00:00Z")
		if err != nil {
			t.Fatal(err)
		}
		if row.id, err = result.LastInsertId(); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return rows
}

func auditFilterPlan(t *testing.T, store *GatewayStore, filter AuditFilter) string {
	t.Helper()
	query, args := auditFilterQuery(filter)
	rows, err := store.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(details, " | ")
}

// Every filter combination the console (components/audit-panel.tsx) and
// `doops-gateway audit list` send must read its index in id order, and paging
// with BeforeID must return exactly the matching rows, newest first.
func TestAuditFilterCombinationsUseIndexesAndKeepPaging(t *testing.T) {
	store := openCredentialTestStore(t)
	seeded := seedAuditFilterRows(t, store, 4000)
	const credential = "cred_000000000000000000000001"
	cases := []struct {
		name   string
		filter AuditFilter
		index  string // empty: the unfiltered rowid scan or rowid range
	}{
		{"global", AuditFilter{}, ""},
		{"global action", AuditFilter{Action: ActionCredentialVerify}, "idx_audit_action_id"},
		{"global status", AuditFilter{Status: "blocked"}, "idx_audit_status_id"},
		{"global action and status", AuditFilter{Action: ActionCredentialVerify, Status: "success"}, "idx_audit_action_id"},
		{"self", AuditFilter{UserID: "usr_a"}, "idx_audit_user_id"},
		{"self action", AuditFilter{UserID: "usr_a", Action: ActionCredentialVerify}, "idx_audit_user_action_id"},
		{"self status", AuditFilter{UserID: "usr_a", Status: "blocked"}, "idx_audit_user_status_id"},
		{"self action and status", AuditFilter{UserID: "usr_a", Action: ActionExec, Status: "blocked"}, "idx_audit_user_action_id"},
		{"self session", AuditFilter{UserID: "usr_a", Session: "s-3"}, "idx_audit_user_session_id"},
		{"target", AuditFilter{Cluster: "prod", Instance: "node-1"}, "idx_audit_target_id"},
		{"target action", AuditFilter{Cluster: "prod", Instance: "node-1", Action: ActionCredentialVerify}, "idx_audit_target_action_id"},
		{"target status", AuditFilter{Cluster: "prod", Instance: "node-1", Status: "blocked"}, "idx_audit_target_status_id"},
		{"target action and status", AuditFilter{Cluster: "prod", Instance: "node-1", Action: ActionExec, Status: "blocked"}, "idx_audit_target_action_id"},
		{"target session", AuditFilter{Cluster: "prod", Instance: "node-1", Session: "s-3"}, "idx_audit_session_target_id"},
		{"target session action", AuditFilter{Cluster: "prod", Instance: "node-1", Session: "s-3", Action: ActionExec}, "idx_audit_session_target_id"},
		{"target session status", AuditFilter{Cluster: "prod", Instance: "node-1", Session: "s-3", Status: "blocked"}, "idx_audit_session_target_id"},
		{"credential", AuditFilter{Session: credential}, "idx_audit_session_id"},
		{"credential action", AuditFilter{Session: credential, Action: ActionCredentialRotate}, "idx_audit_session_id"},
		{"credential status", AuditFilter{Session: credential, Status: "error"}, "idx_audit_session_id"},
		{"credential action and status", AuditFilter{Session: credential, Action: ActionCredentialRotate, Status: "error"}, "idx_audit_session_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var want []int64
			for _, row := range slices.Backward(seeded) {
				if row.matches(tc.filter) {
					want = append(want, row.id)
				}
			}
			if len(want) == 0 {
				t.Fatal("synthetic data matches no row; the case checks nothing")
			}
			for _, beforeID := range []int64{0, want[len(want)/2]} {
				filter := tc.filter
				filter.Limit, filter.BeforeID = 500, beforeID
				plan := auditFilterPlan(t, store, filter)
				rowidRead := plan == "SCAN audit_events" ||
					plan == "SEARCH audit_events USING INTEGER PRIMARY KEY (rowid<?)"
				if strings.Contains(plan, "TEMP B-TREE") ||
					tc.index == "" && !rowidRead ||
					tc.index != "" && !strings.Contains(plan, "USING INDEX "+tc.index+" (") {
					t.Fatalf("before_id=%d plan = %q, want an id-ordered read of %q", beforeID, plan, tc.index)
				}
			}
			var got []int64
			filter := tc.filter
			filter.Limit = 37
			for {
				page, err := store.ListAuditFiltered(filter)
				if err != nil {
					t.Fatal(err)
				}
				if len(page) > filter.Limit {
					t.Fatalf("page of %d rows exceeds limit %d", len(page), filter.Limit)
				}
				for _, event := range page {
					got = append(got, event.ID)
				}
				if len(page) < filter.Limit {
					break
				}
				filter.BeforeID = page[len(page)-1].ID
			}
			if !slices.Equal(got, want) {
				t.Fatalf("paged ids differ: got %d rows, want %d", len(got), len(want))
			}
		})
	}
}
