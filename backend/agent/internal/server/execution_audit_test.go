package server

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func openExecutionAuditTestStore(t *testing.T, path string) *GatewayStore {
	t.Helper()
	store, err := OpenGatewayStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func startExecutionAuditTestEvent(t *testing.T, store *GatewayStore, event AuditEvent) int64 {
	t.Helper()
	id, err := store.StartAudit(event)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func executionAuditTestEvent(t *testing.T, store *GatewayStore, id int64) AuditEvent {
	t.Helper()
	events, err := store.ListAuditFiltered(AuditFilter{BeforeID: id + 1, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ID != id {
		t.Fatalf("audit %d missing: %#v", id, events)
	}
	return events[0]
}

func executionAuditTestCount(t *testing.T, store *GatewayStore) int {
	t.Helper()
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM audit_events").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// Fault injection uses a connection-local runtime limit, not schema changes.
func setExecutionAuditTestLengthLimit(t *testing.T, store *GatewayStore, limit int) {
	t.Helper()
	conn, err := store.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := sqlite.Limit(conn, sqlite3.SQLITE_LIMIT_LENGTH, limit); err != nil {
		t.Fatal(err)
	}
}

func TestExecutionAuditDispatchAndRecoverySurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	store := openExecutionAuditTestStore(t, path)
	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	id := startExecutionAuditTestEvent(t, store, AuditEvent{
		UserID: "operator", TokenID: "operator-token", Cluster: "cluster-a",
		Instance: "agent-a", Action: ActionExec, Session: "repair-1",
		CommandSummary: "restart test workload", CorrelationID: "request-1",
		StartedAt: started,
	})
	running := startExecutionAuditTestEvent(t, store, AuditEvent{Status: "running"})
	completed := startExecutionAuditTestEvent(t, store, AuditEvent{Status: "success"})
	unknown := startExecutionAuditTestEvent(t, store, AuditEvent{Status: "outcome_unknown"})
	before := executionAuditTestEvent(t, store, id)
	if err := store.MarkExecutionDispatched(id); err != nil {
		t.Fatal(err)
	}
	before.Status = "executing"
	if got := executionAuditTestEvent(t, store, id); !reflect.DeepEqual(got, before) {
		t.Fatalf("dispatch changed audit evidence: %#v", got)
	}
	if _, err := store.db.Exec(`UPDATE audit_events SET error = ?, tail = ?, bytes_out = ? WHERE id = ?`,
		"transport disconnected", "partial output", 42, id); err != nil {
		t.Fatal(err)
	}
	unknownBefore := executionAuditTestEvent(t, store, unknown)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = openExecutionAuditTestStore(t, path)
	unresolved, err := store.UnresolvedExecutions("cluster-a", "agent-a")
	if err != nil || len(unresolved) != 1 || unresolved[0].ID != id || unresolved[0].Status != "executing" {
		t.Fatalf("dispatched execution did not persist: %#v, %v", unresolved, err)
	}
	recoveryStarted := time.Now().UTC()
	if err := store.RecoverInterruptedExecutions(); err != nil {
		t.Fatal(err)
	}
	recovered := executionAuditTestEvent(t, store, id)
	if recovered.Status != "outcome_unknown" || !strings.Contains(recovered.Error, "gateway restarted") ||
		!strings.Contains(recovered.Error, "transport disconnected") ||
		recovered.EndedAt.Before(recoveryStarted) || recovered.EndedAt.After(time.Now().UTC()) {
		t.Fatalf("incorrect recovery: %#v", recovered)
	}
	before.Status = recovered.Status
	before.Error = recovered.Error
	before.EndedAt = recovered.EndedAt
	before.Tail = "partial output"
	before.BytesOut = 42
	if !reflect.DeepEqual(recovered, before) {
		t.Fatalf("recovery changed original evidence: %#v", recovered)
	}
	if got := executionAuditTestEvent(t, store, running); got.Status != "running" {
		t.Fatalf("undispatched audit changed: %#v", got)
	}
	if got := executionAuditTestEvent(t, store, completed); got.Status != "success" {
		t.Fatalf("completed audit changed: %#v", got)
	}
	if got := executionAuditTestEvent(t, store, unknown); !reflect.DeepEqual(got, unknownBefore) {
		t.Fatalf("previously unknown audit changed: %#v", got)
	}
	if err := store.RecoverInterruptedExecutions(); err != nil {
		t.Fatal(err)
	}
	if got := executionAuditTestEvent(t, store, id); !reflect.DeepEqual(got, recovered) {
		t.Fatalf("recovery is not idempotent: %#v", got)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = openExecutionAuditTestStore(t, path)
	if got := executionAuditTestEvent(t, store, id); !reflect.DeepEqual(got, recovered) {
		t.Fatalf("recovery did not persist: %#v", got)
	}
	if executionAuditTestCount(t, store) != 4 {
		t.Fatal("dispatch or recovery replaced/duplicated audit events")
	}
}

func TestExecutionAuditRejectsInvalidTransitions(t *testing.T) {
	for _, status := range []string{"running", "executing", "outcome_unknown", "success", "error", "resolved_manually"} {
		t.Run(status, func(t *testing.T) {
			store := openExecutionAuditTestStore(t, filepath.Join(t.TempDir(), "gateway.db"))
			id := startExecutionAuditTestEvent(t, store, AuditEvent{Status: status})
			if status != "running" {
				if err := store.MarkExecutionDispatched(id); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("dispatch %s error = %v", status, err)
				}
			}
			if status != "executing" {
				if err := store.MarkExecutionUncertain(id); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("mark uncertain %s error = %v", status, err)
				}
			}
			if got := executionAuditTestEvent(t, store, id); got.Status != status {
				t.Fatalf("rejected transition changed audit: %#v", got)
			}
			if executionAuditTestCount(t, store) != 1 {
				t.Fatal("rejected transition appended an event")
			}
		})
	}
	store := openExecutionAuditTestStore(t, filepath.Join(t.TempDir(), "gateway.db"))
	for _, id := range []int64{-1, 0, 999} {
		if err := store.MarkExecutionDispatched(id); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("dispatch nonexistent %d error = %v", id, err)
		}
		if err := store.MarkExecutionUncertain(id); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("mark uncertain nonexistent %d error = %v", id, err)
		}
	}
	if executionAuditTestCount(t, store) != 0 {
		t.Fatal("invalid IDs created audit records")
	}
}

func TestExecutionAuditUnresolvedIncludesAllUnsafeStatesAndExactTargetPairs(t *testing.T) {
	store := openExecutionAuditTestStore(t, filepath.Join(t.TempDir(), "gateway.db"))
	for i := 0; i < 504; i++ {
		status := "executing"
		if i%2 == 1 {
			status = "outcome_unknown"
		}
		cluster, instance := "cluster-a", "agent-a"
		if i%4 >= 2 {
			cluster = "cluster-b"
		}
		if i%2 == 1 {
			instance = "agent-b"
		}
		startExecutionAuditTestEvent(t, store, AuditEvent{Status: status, Cluster: cluster, Instance: instance})
	}
	started := time.Now().UTC().Add(-time.Hour)
	ended := started.Add(time.Minute)
	quotedID := startExecutionAuditTestEvent(t, store, AuditEvent{
		Status: "outcome_unknown", Cluster: "cluster' OR 1=1 --", Instance: "agent'quoted",
		UserID: "operator", TokenID: "operator-token", Action: ActionAsk, Session: "session-1",
		CommandSummary: "inspect workload", StartedAt: started,
	})
	if err := store.FinishAudit(quotedID, AuditFinish{Status: "outcome_unknown", Error: "completion unavailable", EndedAt: ended}); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"running", "success", "error", "resolved_manually"} {
		startExecutionAuditTestEvent(t, store, AuditEvent{Status: status, Cluster: "cluster-a", Instance: "agent-a"})
	}
	for _, tc := range []struct {
		name, cluster, instance string
		want                    int
	}{
		{"all", "", "", 505},
		{"cluster only does not filter", "cluster-a", "", 505},
		{"instance only does not filter", "", "agent-a", 505},
		{"executing target", "cluster-a", "agent-a", 126},
		{"unknown target", "cluster-b", "agent-b", 126},
		{"missing target", "absent", "absent", 0},
		{"SQL injection is a literal", "' OR 1=1 --", "agent-a", 0},
		{"quoted target", "cluster' OR 1=1 --", "agent'quoted", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events, err := store.UnresolvedExecutions(tc.cluster, tc.instance)
			if err != nil || len(events) != tc.want {
				t.Fatalf("got %d events, want %d: %v", len(events), tc.want, err)
			}
			for i, event := range events {
				if event.Status != "executing" && event.Status != "outcome_unknown" {
					t.Fatalf("nonblocking event returned: %#v", event)
				}
				if i > 0 && events[i-1].ID <= event.ID {
					t.Fatal("events are not ordered by descending ID")
				}
				if tc.cluster != "" && tc.instance != "" &&
					(event.Cluster != tc.cluster || event.Instance != tc.instance) {
					t.Fatalf("incorrect target: %#v", event)
				}
				if event.ID == quotedID && (event.UserID != "operator" || event.TokenID != "operator-token" ||
					event.Action != ActionAsk || event.Session != "session-1" || event.CommandSummary != "inspect workload" ||
					event.Error != "completion unavailable" || !event.StartedAt.Equal(started) || !event.EndedAt.Equal(ended)) {
					t.Fatalf("admin display evidence missing: %#v", event)
				}
			}
		})
	}
}

func TestExecutionAuditUnknownRemainsBlockingAlongsideHistoricalManualRecords(t *testing.T) {
	store := openExecutionAuditTestStore(t, filepath.Join(t.TempDir(), "gateway.db"))
	unknownID := startExecutionAuditTestEvent(t, store, AuditEvent{Status: "outcome_unknown", Cluster: "cluster-a", Instance: "agent-a"})
	manualID := startExecutionAuditTestEvent(t, store, AuditEvent{Status: "resolved_manually", Cluster: "cluster-a", Instance: "agent-a"})

	unresolved, err := store.UnresolvedExecutions("cluster-a", "agent-a")
	if err != nil || len(unresolved) != 1 || unresolved[0].ID != unknownID || unresolved[0].Status != "outcome_unknown" {
		t.Fatalf("unknown execution was not retained as the blocker: %#v, %v", unresolved, err)
	}
	if got := executionAuditTestEvent(t, store, manualID); got.Status != "resolved_manually" {
		t.Fatalf("historical manual audit was not preserved for read-only access: %#v", got)
	}
}

func TestExecutionAuditFailedCompletionRemainsBlockingAndUnprunable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	store := openExecutionAuditTestStore(t, path)
	id := startExecutionAuditTestEvent(t, store, AuditEvent{
		Cluster: "cluster-a", Instance: "agent-a", StartedAt: time.Now().Add(-24 * time.Hour),
	})
	if err := store.MarkExecutionDispatched(id); err != nil {
		t.Fatal(err)
	}
	setExecutionAuditTestLengthLimit(t, store, 768)
	err := store.FinishAudit(id, AuditFinish{Status: "success", Tail: strings.Repeat("t", 1024)})
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code() != sqlite3.SQLITE_TOOBIG {
		t.Fatalf("expected completion audit failure, got %v", err)
	}
	for _, want := range []string{"executing", "outcome_unknown"} {
		events, err := store.UnresolvedExecutions("cluster-a", "agent-a")
		if err != nil || len(events) != 1 || events[0].ID != id || events[0].Status != want {
			t.Fatalf("failed completion ceased blocking: %#v, %v", events, err)
		}
		if count, err := store.DeleteAuditBefore(time.Now()); err != nil || count != 0 {
			t.Fatalf("pruned %s execution: count=%d, error=%v", want, count, err)
		}
		if want == "executing" {
			before := executionAuditTestEvent(t, store, id)
			markedAt := time.Now().UTC()
			if err := store.MarkExecutionUncertain(id); err != nil {
				t.Fatal(err)
			}
			uncertain := executionAuditTestEvent(t, store, id)
			if uncertain.EndedAt.Before(markedAt) || uncertain.EndedAt.After(time.Now().UTC()) {
				t.Fatalf("missing uncertainty timestamp: %#v", uncertain)
			}
			before.Status = "outcome_unknown"
			before.Error = "execution owner no longer active; machine terminal evidence required"
			before.EndedAt = uncertain.EndedAt
			if !reflect.DeepEqual(uncertain, before) {
				t.Fatalf("incorrect orphan transition: %#v", uncertain)
			}
		}
	}
	uncertain := executionAuditTestEvent(t, store, id)
	if err := store.MarkExecutionUncertain(id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("repeated uncertainty transition error = %v", err)
	}
	if got := executionAuditTestEvent(t, store, id); !reflect.DeepEqual(got, uncertain) {
		t.Fatalf("repeated uncertainty changed original evidence: %#v", got)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = openExecutionAuditTestStore(t, path)
	if got := executionAuditTestEvent(t, store, id); !reflect.DeepEqual(got, uncertain) {
		t.Fatalf("uncertain execution did not persist: %#v", got)
	}
	if executionAuditTestCount(t, store) != 1 {
		t.Fatal("uncertainty transition appended an audit event")
	}
}

func TestExecutionAuditPruningNeverDeletesUnsafeStates(t *testing.T) {
	store := openExecutionAuditTestStore(t, filepath.Join(t.TempDir(), "gateway.db"))
	old := time.Now().Add(-48 * time.Hour)
	for _, status := range []string{"executing", "outcome_unknown", "running", "success", "error", "resolved_manually"} {
		startExecutionAuditTestEvent(t, store, AuditEvent{Status: status, StartedAt: old})
	}
	recent := startExecutionAuditTestEvent(t, store, AuditEvent{Status: "success"})
	if count, err := store.DeleteAuditBefore(old.Add(time.Hour)); err != nil || count != 4 {
		t.Fatalf("pruning count=%d, error=%v, want 4 safe old events", count, err)
	}
	events, err := store.UnresolvedExecutions("", "")
	if err != nil || len(events) != 2 || events[0].Status != "outcome_unknown" || events[1].Status != "executing" {
		t.Fatalf("unsafe states lost during pruning: %#v, %v", events, err)
	}
	if got := executionAuditTestEvent(t, store, recent); got.Status != "success" {
		t.Fatalf("recent safe audit changed: %#v", got)
	}
	if executionAuditTestCount(t, store) != 3 {
		t.Fatal("pruning removed the wrong records")
	}
}

func TestExecutionAuditClosedDatabaseFailsClosed(t *testing.T) {
	store := openExecutionAuditTestStore(t, filepath.Join(t.TempDir(), "gateway.db"))
	id := startExecutionAuditTestEvent(t, store, AuditEvent{Status: "outcome_unknown"})
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkExecutionDispatched(id); err == nil {
		t.Fatal("dispatch ignored closed database")
	}
	if err := store.MarkExecutionUncertain(id); err == nil {
		t.Fatal("uncertainty transition ignored closed database")
	}
	if err := store.RecoverInterruptedExecutions(); err == nil {
		t.Fatal("recovery ignored closed database")
	}
	if events, err := store.UnresolvedExecutions("", ""); err == nil || events != nil {
		t.Fatalf("closed database reported a blocker list: %#v, %v", events, err)
	}
	if _, err := store.DeleteAuditBefore(time.Now()); err == nil {
		t.Fatal("pruning ignored closed database")
	}
}
