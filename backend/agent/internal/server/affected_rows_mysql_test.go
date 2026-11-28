package server

// INT-65: the three RowsAffected() defects only reproduce on MySQL, because
// MySQL's affected-rows count is "rows changed" while SQLite's is "rows
// matched". This file runs the same six contract cases as
// affected_rows_fix_test.go against a real MySQL 8.4 through the production
// store contract, plus the raw-SQL control that prints affected=0 for a
// same-value UPDATE on the same server.
//
// The gate is DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT (a MySQL 8.4 that accepts root
// without a password), the same variable the copier and credential e2e
// tests use. It is fail-closed: unset means a visible skip with the reason, set
// but unreachable means FAIL, because a silently skipped gate would call the
// contract green on a release nobody checked.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func affectedRowsMySQLPort(t *testing.T) string {
	t.Helper()
	port := strings.TrimSpace(os.Getenv("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT"))
	if port == "" {
		t.Skip("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT is unset: skipping the MySQL affected-rows contract (SQLite matches rows, so it cannot reproduce the changed-rows defect)")
	}
	return port
}

// openAffectedRowsMySQLStore opens a throwaway MySQL database through the
// production store contract. The dialect switch is process global, so the
// cleanup restores SQLite before the connection closes and these tests never
// run in parallel.
func openAffectedRowsMySQLStore(t *testing.T) *GatewayStore {
	t.Helper()
	port := affectedRowsMySQLPort(t)
	admin, err := sql.Open("mysql", "root@tcp(127.0.0.1:"+port+")/?parseTime=true")
	if err != nil {
		t.Fatalf("open MySQL admin connection on port %s: %v", port, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		_ = admin.Close()
		t.Fatalf("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT=%s is set but MySQL is unreachable (fail closed, not skip): %v", port, err)
	}
	name := fmt.Sprintf("doops_affrows_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE `"+name+"` CHARACTER SET utf8mb4"); err != nil {
		_ = admin.Close()
		t.Fatalf("create disposable MySQL database %s: %v", name, err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS `"+name+"`")
		_ = admin.Close()
	})
	t.Setenv("DOOPS_GATEWAY_DATA_DIR", t.TempDir())
	t.Setenv("DOOPS_GATEWAY_STORE", "mysql")
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", "root@tcp(127.0.0.1:"+port+")/"+name+"?parseTime=true")
	store, err := OpenGatewayStore("")
	if err != nil {
		t.Fatalf("open MySQL gateway store: %v", err)
	}
	t.Cleanup(func() {
		gatewaySetDialect(gatewayDialectSQLite)
		_ = store.Close()
	})
	if got := gatewayDialect(); got != gatewayDialectMySQL {
		t.Fatalf("gateway dialect = %v, want mysql", got)
	}
	return store
}

// TestAffectedRowsMySQLControlSameValueUpdate is the control the fix rests on:
// on this exact server, an UPDATE whose SET values match the stored row reports
// affected=0 even though the row is still there. Each line is printed so the
// report can quote the raw result instead of trusting the store.
func TestAffectedRowsMySQLControlSameValueUpdate(t *testing.T) {
	store := openAffectedRowsMySQLStore(t)
	ctx := context.Background()

	seedOfflineAgent(t, store, "cluster-a", "agent-1", 7)
	assertSameValueUpdateReportsZero(t, ctx, store, "agent_status unarchive",
		`UPDATE agent_status SET archived_at='', archived_by='' WHERE cluster=? AND instance=?`,
		"cluster-a", "agent-1")

	user, err := store.CreateUser("operator")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	assertSameValueUpdateReportsZero(t, ctx, store, "users disable (already 0)",
		`UPDATE users SET disabled=? WHERE id=?`, false, user.ID)

	seedAgentOnlineForResource(t, store, "cluster-a", "agent-2", 4)
	seedAgentResourceState(t, store, "cluster-a", "agent-2", 11)
	assertSameValueUpdateReportsZero(t, ctx, store, "agent_resource_state applied_revision (already 0)",
		`UPDATE agent_resource_state SET applied_revision=? WHERE cluster=? AND instance=?`, 0, "cluster-a", "agent-2")
}

// assertSameValueUpdateReportsZero runs one raw UPDATE and fails when MySQL
// reports a changed row: without affected=0 the fix would be untestable.
func assertSameValueUpdateReportsZero(t *testing.T, ctx context.Context, store *GatewayStore, label, statement string, args ...any) {
	t.Helper()
	result, err := store.db.ExecContext(ctx, statement, args...)
	if err != nil {
		t.Fatalf("control UPDATE %s: %v", label, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("control UPDATE %s rows affected: %v", label, err)
	}
	t.Logf("INT-65 control on MySQL: same-value UPDATE affected=%d (want 0) for %s: %s", affected, label, statement)
	if affected != 0 {
		t.Fatalf("control UPDATE %s affected=%d, want 0: this server does not implement MySQL's changed-rows semantics, so the test cases below would not prove anything", label, affected)
	}
}

// seedAgentOnlineForResource marks the agent online so the EXISTS predicate in
// agentResourceAppliedPredicate matches.
func seedAgentOnlineForResource(t *testing.T, store *GatewayStore, cluster, instance string, generation uint64) {
	t.Helper()
	if err := store.MarkAgentOnline(AgentStatus{Cluster: cluster, Instance: instance, Generation: generation, ConnectedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("mark agent online: %v", err)
	}
}

// TestAffectedRowsMySQLUnarchiveRepeatedCallSucceeds covers cases 1 and 2.
func TestAffectedRowsMySQLUnarchiveRepeatedCallSucceeds(t *testing.T) {
	store := openAffectedRowsMySQLStore(t)
	seedOfflineAgent(t, store, "cluster-a", "agent-1", 3)
	if _, err := store.ArchiveAgentStatus("cluster-a", "agent-1", "actor", time.Now().UTC()); err != nil {
		t.Fatalf("archive agent: %v", err)
	}
	agent, err := store.UnarchiveAgentStatus("cluster-a", "agent-1")
	if err != nil {
		t.Fatalf("first unarchive: %v", err)
	}
	if !agent.ArchivedAt.IsZero() || agent.ArchivedBy != "" {
		t.Fatalf("first unarchive left archive marks: %+v", agent)
	}
	// The second call writes the values the row already has: MySQL reports 0.
	agent, err = store.UnarchiveAgentStatus("cluster-a", "agent-1")
	if err != nil {
		t.Fatalf("repeat unarchive of an already-unarchived record on MySQL: %v", err)
	}
	if agent.Cluster != "cluster-a" || agent.Instance != "agent-1" || !agent.ArchivedAt.IsZero() {
		t.Fatalf("repeat unarchive returned %+v, want the live unarchived record", agent)
	}
	if _, err := store.UnarchiveAgentStatus("cluster-a", "missing"); !errors.Is(err, ErrAgentStatusNotFound) {
		t.Fatalf("unarchive of a missing instance = %v, want ErrAgentStatusNotFound", err)
	}
}

// TestAffectedRowsMySQLSetUserDisabledRepeatedCallSucceeds covers cases 3 and 4.
func TestAffectedRowsMySQLSetUserDisabledRepeatedCallSucceeds(t *testing.T) {
	store := openAffectedRowsMySQLStore(t)
	user, err := store.CreateUser("operator")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := store.SetUserDisabled(user.ID, true); err != nil {
		t.Fatalf("first disable: %v", err)
	}
	if err := store.SetUserDisabled(user.ID, true); err != nil {
		t.Fatalf("repeat disable on MySQL: %v", err)
	}
	if err := store.SetUserDisabled(user.ID, false); err != nil {
		t.Fatalf("first enable: %v", err)
	}
	if err := store.SetUserDisabled(user.ID, false); err != nil {
		t.Fatalf("repeat enable on MySQL: %v", err)
	}
	var disabled int
	if err := store.db.QueryRow(`SELECT disabled FROM users WHERE id = ?`, user.ID).Scan(&disabled); err != nil {
		t.Fatalf("read users.disabled: %v", err)
	}
	if disabled != 0 {
		t.Fatalf("users.disabled = %d after enabling, want 0", disabled)
	}
	if err := store.SetUserDisabled("no-such-user", true); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("disable of a missing user = %v, want sql.ErrNoRows", err)
	}
}

// TestAffectedRowsMySQLSaveAgentResourceAppliedRepeatedRevisionSucceeds covers
// cases 5 and 6.
func TestAffectedRowsMySQLSaveAgentResourceAppliedRepeatedRevisionSucceeds(t *testing.T) {
	store := openAffectedRowsMySQLStore(t)
	ctx := context.Background()
	seedOfflineAgent(t, store, "cluster-a", "agent-1", 5)
	seedAgentOnlineForResource(t, store, "cluster-a", "agent-1", 5)
	agent := &GatewayAgent{Cluster: "cluster-a", Instance: "agent-1", Generation: 5}
	var connectedAt string
	if err := store.db.QueryRow(`SELECT connected_at FROM agent_status WHERE cluster = ? AND instance = ?`,
		agent.Cluster, agent.Instance).Scan(&connectedAt); err != nil {
		t.Fatalf("read connected_at: %v", err)
	}
	parsed, err := parseTime(connectedAt)
	if err != nil {
		t.Fatalf("parse connected_at %q: %v", connectedAt, err)
	}
	agent.ConnectedAt = parsed
	seedAgentResourceState(t, store, agent.Cluster, agent.Instance, 9)

	if err := store.saveAgentResourceApplied(ctx, agent, 9); err != nil {
		t.Fatalf("first apply of revision 9: %v", err)
	}
	// The heartbeat already wrote applied_revision=9, so MySQL reports 0 here.
	if err := store.saveAgentResourceApplied(ctx, agent, 9); err != nil {
		t.Fatalf("repeat apply of revision 9 on MySQL: %v", err)
	}
	if applied := readAppliedRevision(t, store, agent.Cluster, agent.Instance); applied != 9 {
		t.Fatalf("applied_revision = %d, want 9", applied)
	}
	if err := store.saveAgentResourceApplied(ctx, agent, 10); !errors.Is(err, errResourceSyncDisconnected) {
		t.Fatalf("apply of an unseen revision = %v, want errResourceSyncDisconnected", err)
	}
	if _, err := store.db.Exec(`UPDATE agent_status SET status = 'offline' WHERE cluster = ? AND instance = ?`,
		agent.Cluster, agent.Instance); err != nil {
		t.Fatalf("mark agent offline: %v", err)
	}
	if err := store.saveAgentResourceApplied(ctx, agent, 9); !errors.Is(err, errResourceSyncDisconnected) {
		t.Fatalf("apply for an offline agent = %v, want errResourceSyncDisconnected", err)
	}
	if _, err := store.db.Exec(`INSERT INTO agent_status (cluster, instance, status, generation, connected_at, last_seen, updated_at)
		SELECT cluster, 'agent-2', 'online', 1, connected_at, last_seen, updated_at FROM agent_status WHERE cluster = ? AND instance = ?`,
		agent.Cluster, agent.Instance); err != nil {
		t.Fatalf("seed second online agent: %v", err)
	}
	stale := &GatewayAgent{Cluster: agent.Cluster, Instance: "agent-2", Generation: 1, ConnectedAt: agent.ConnectedAt}
	if err := store.saveAgentResourceApplied(ctx, stale, 9); !errors.Is(err, errResourceSyncDisconnected) {
		t.Fatalf("apply without an agent_resource_state row = %v, want errResourceSyncDisconnected", err)
	}
}
