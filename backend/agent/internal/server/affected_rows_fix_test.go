package server

// INT-65: the three RowsAffected() sites that treated "MySQL changed no rows"
// as "the row is gone". SQLite's changes() counts matched rows, so the SQLite
// store alone can never reproduce the defect: this file runs the six contract
// cases against the real OpenGatewayStore SQLite backend, and
// affected_rows_mysql_test.go runs the same six against a real MySQL 8.4 through
// DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT.
//
// Each pair is one success/wrong-error contract:
//   1. unarchiving an already-unarchived record succeeds and returns the record;
//   2. unarchiving a cluster/instance that does not exist still returns
//      ErrAgentStatusNotFound;
//   3. disabling an already-disabled user returns nil;
//   4. disabling a user id that does not exist still returns sql.ErrNoRows;
//   5. re-confirming the applied revision the heartbeat already wrote returns nil;
//   6. an agent that is really offline, or has no resource-state row, still
//      returns errResourceSyncDisconnected.

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func newAffectedRowsSQLiteStore(t *testing.T) *GatewayStore {
	t.Helper()
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("open SQLite gateway store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// createAgentResourceStateTable ensures the resource-sync state table exists.
// OpenGatewayStore's SQLite migrate() now auto-creates it; keeping the
// IF NOT EXISTS statement preserves compatibility if a fixture drops it first.
func createAgentResourceStateTable(t *testing.T, store *GatewayStore) {
	t.Helper()
	if _, err := store.db.Exec(`CREATE TABLE IF NOT EXISTS agent_resource_state (
		cluster TEXT NOT NULL,
		instance TEXT NOT NULL,
		mode TEXT NOT NULL DEFAULT '' CHECK (mode IN ('', 'kubernetes', 'docker', 'linux')),
		kubernetes_available INTEGER CHECK (kubernetes_available IN (0, 1)),
		supported INTEGER NOT NULL DEFAULT 0 CHECK (supported IN (0, 1)),
		manifest_revision INTEGER NOT NULL DEFAULT 0 CHECK (manifest_revision >= 0),
		manifest_hash TEXT NOT NULL DEFAULT '',
		generated_at TEXT NOT NULL DEFAULT '',
		connection_at TEXT NOT NULL DEFAULT '',
		observation_hash TEXT NOT NULL DEFAULT '',
		last_observed_at TEXT NOT NULL DEFAULT '',
		applied_revision INTEGER NOT NULL DEFAULT 0 CHECK (applied_revision >= 0),
		PRIMARY KEY(cluster, instance),
		FOREIGN KEY(cluster, instance) REFERENCES agent_status(cluster, instance) ON DELETE CASCADE
	) STRICT`); err != nil {
		t.Fatalf("create agent_resource_state: %v", err)
	}
}

// seedOfflineAgent writes one offline agent_status row, the state
// UnarchiveAgentStatus and the archive endpoint operate on.
func seedOfflineAgent(t *testing.T, store *GatewayStore, cluster, instance string, generation uint64) {
	t.Helper()
	if err := store.MarkAgentOnline(AgentStatus{Cluster: cluster, Instance: instance, Generation: generation}); err != nil {
		t.Fatalf("mark agent online: %v", err)
	}
	if err := store.MarkAgentOffline(cluster, instance, generation, time.Now().UTC()); err != nil {
		t.Fatalf("mark agent offline: %v", err)
	}
}

// Registration creates the state row. Simulate its first published revision.
func seedAgentResourceState(t *testing.T, store *GatewayStore, cluster, instance string, manifestRevision uint64) {
	t.Helper()
	if _, err := store.db.Exec(`UPDATE agent_resource_state SET manifest_revision=?,last_observed_at=? WHERE cluster=? AND instance=?`,
		manifestRevision, formatTime(time.Now().UTC()), cluster, instance); err != nil {
		t.Fatalf("seed agent_resource_state: %v", err)
	}
}

func readAppliedRevision(t *testing.T, store *GatewayStore, cluster, instance string) uint64 {
	t.Helper()
	var applied uint64
	if err := store.db.QueryRow(`SELECT applied_revision FROM agent_resource_state WHERE cluster = ? AND instance = ?`,
		cluster, instance).Scan(&applied); err != nil {
		t.Fatalf("read applied_revision: %v", err)
	}
	return applied
}

// TestUnarchiveAgentStatusRepeatedCallSucceeds covers cases 1 and 2 on SQLite.
func TestUnarchiveAgentStatusRepeatedCallSucceeds(t *testing.T) {
	t.Parallel()
	store := newAffectedRowsSQLiteStore(t)
	seedOfflineAgent(t, store, "cluster-a", "agent-1", 3)

	// Never archived: the UPDATE matches the row and changes nothing. That is
	// the shape MySQL reports as 0 and the old code turned into a 404.
	agent, err := store.UnarchiveAgentStatus("cluster-a", "agent-1")
	if err != nil {
		t.Fatalf("unarchive a record that was never archived: %v", err)
	}
	if agent.Cluster != "cluster-a" || agent.Instance != "agent-1" || !agent.ArchivedAt.IsZero() {
		t.Fatalf("unarchive returned %+v, want the live unarchived record", agent)
	}

	if _, err := store.ArchiveAgentStatus("cluster-a", "agent-1", "actor", time.Now().UTC()); err != nil {
		t.Fatalf("archive agent: %v", err)
	}
	if _, err := store.UnarchiveAgentStatus("cluster-a", "agent-1"); err != nil {
		t.Fatalf("first unarchive after archive: %v", err)
	}
	agent, err = store.UnarchiveAgentStatus("cluster-a", "agent-1")
	if err != nil {
		t.Fatalf("repeat unarchive of an already-unarchived record: %v", err)
	}
	if !agent.ArchivedAt.IsZero() || agent.ArchivedBy != "" {
		t.Fatalf("repeat unarchive left archive marks: %+v", agent)
	}

	if _, err := store.UnarchiveAgentStatus("cluster-a", "missing"); !errors.Is(err, ErrAgentStatusNotFound) {
		t.Fatalf("unarchive of a missing instance = %v, want ErrAgentStatusNotFound", err)
	}
	if _, err := store.UnarchiveAgentStatus("missing", "agent-1"); !errors.Is(err, ErrAgentStatusNotFound) {
		t.Fatalf("unarchive in a missing cluster = %v, want ErrAgentStatusNotFound", err)
	}
}

// TestSetUserDisabledRepeatedCallSucceeds covers cases 3 and 4 on SQLite.
func TestSetUserDisabledRepeatedCallSucceeds(t *testing.T) {
	t.Parallel()
	store := newAffectedRowsSQLiteStore(t)
	user, err := store.CreateUser("operator")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := store.SetUserDisabled(user.ID, true); err != nil {
			t.Fatalf("disable pass %d: %v", i+1, err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := store.SetUserDisabled(user.ID, false); err != nil {
			t.Fatalf("enable pass %d: %v", i+1, err)
		}
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
	if err := store.SetUserDisabled("no-such-user", false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("enable of a missing user = %v, want sql.ErrNoRows", err)
	}
}

// TestSaveAgentResourceAppliedRepeatedRevisionSucceeds covers cases 5 and 6 on
// SQLite.
func TestSaveAgentResourceAppliedRepeatedRevisionSucceeds(t *testing.T) {
	t.Parallel()
	store := newAffectedRowsSQLiteStore(t)
	createAgentResourceStateTable(t, store)
	ctx := context.Background()
	seedOfflineAgent(t, store, "cluster-a", "agent-1", 5)
	agent := &GatewayAgent{Cluster: "cluster-a", Instance: "agent-1", Generation: 5}
	if err := store.MarkAgentOnline(AgentStatus{Cluster: agent.Cluster, Instance: agent.Instance, Generation: agent.Generation, ConnectedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("mark agent online: %v", err)
	}
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

	for i := 0; i < 2; i++ {
		if err := store.saveAgentResourceApplied(ctx, agent, 9); err != nil {
			t.Fatalf("apply revision pass %d: %v", i+1, err)
		}
	}
	if applied := readAppliedRevision(t, store, agent.Cluster, agent.Instance); applied != 9 {
		t.Fatalf("applied_revision = %d, want 9", applied)
	}

	// A different revision that the manifest never offered is still a
	// disconnect: the predicate compares manifest_revision.
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
