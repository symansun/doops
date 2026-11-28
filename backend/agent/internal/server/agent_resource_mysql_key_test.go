package server

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// INT-44. Two MySQL-only failures kept the target resource sync from ever
// publishing a manifest:
//
//  1. the column named key is a MySQL 8.4 reserved word, so the observation read
//     died with Error 1064 near 'key'; and
//  2. the declared-resource UNION put json_extract (utf8mb4_bin) in the same
//     column position as the utf8mb4_0900_bin identity columns, so MySQL raised
//     Error 1271 illegal mix of collations.
//
// The tests below pin the renderings and then reconcile the same fixture through
// both engines, so the two backends are proven equivalent instead of merely
// compiling.

// The upsert insert names the reserved column as a quoted identifier on both
// engines (the same literal serves SQLite and MySQL, and SQLite accepts
// backticks), while the read keeps the bare spelling SQLite always emitted.
const agentObservationUpsertSQLiteLiteral = "INSERT INTO agent_observations(cluster,instance,kind,`key`,details,first_seen,last_seen,generation,misses) VALUES (?,?,?,?,?,?,?,?,0)"
const agentObservationReadSQLiteLiteral = "SELECT details,last_seen,misses FROM agent_observations WHERE cluster=? AND instance=? ORDER BY kind,key"

// TestAgentResourceKeyRendering keeps SQLite byte-identical to the statements
// that shipped before INT-44 and requires every MySQL rendering to quote key.
func TestAgentResourceKeyRendering(t *testing.T) {
	previous := gatewayDialect()
	t.Cleanup(func() { gatewaySetDialect(previous) })

	gatewaySetDialect(gatewayDialectSQLite)
	// SQLite keeps the conflict clause it always emitted; only the MySQL branch
	// changes shape, so the whole SQLite statement must equal the pre-INT-44 text.
	sqliteUpsert := agentObservationUpsertSQLiteLiteral +
		" ON CONFLICT(cluster,instance,kind,key) DO UPDATE SET details=excluded.details, last_seen=excluded.last_seen, generation=excluded.generation, misses=0"
	if got := gatewayUpsertSQL(agentObservationUpsert()); got != sqliteUpsert {
		t.Fatalf("SQLite observation upsert changed byte-for-byte:\n got %s\nwant %s", got, sqliteUpsert)
	}
	if got := readAgentObservationsSQLFor(gatewayDialectSQLite); got != agentObservationReadSQLiteLiteral {
		t.Fatalf("SQLite observation read changed byte-for-byte:\n got %s\nwant %s", got, agentObservationReadSQLiteLiteral)
	}
	if got := declaredAgentResourcesSQLFor(gatewayDialectSQLite); strings.Contains(got, "COLLATE") || strings.Contains(got, "JSON_UNQUOTE") {
		t.Fatalf("SQLite declared-resource UNION gained a MySQL-only rendering: %s", got)
	}

	gatewaySetDialect(gatewayDialectMySQL)
	upsert := gatewayUpsertSQL(agentObservationUpsert())
	if !strings.Contains(upsert, "`key`") {
		t.Fatalf("MySQL observation upsert does not quote key: %s", upsert)
	}
	read := readAgentObservationsSQLFor(gatewayDialectMySQL)
	if !strings.Contains(read, "ORDER BY kind,`key`") {
		t.Fatalf("MySQL observation read does not quote key: %s", read)
	}
	declared := declaredAgentResourcesSQLFor(gatewayDialectMySQL)
	if !strings.Contains(declared, "COLLATE utf8mb4_0900_bin") || strings.Contains(declared, "utf8mb4_general_ci") {
		t.Fatalf("MySQL declared-resource UNION lacks the explicit 0900_bin collation: %s", declared)
	}
	for _, statement := range []string{upsert, read, declared} {
		for _, issue := range gatewayMySQLRuntimeIssues(statement) {
			if issue.code == gatewayMySQLIssueReservedBareIdentifier {
				t.Fatalf("MySQL rendering still spells key bare: %s", statement)
			}
		}
	}
}

// agentResourceEngineRun records what one engine published for one fixture.
type agentResourceEngineRun struct {
	manifestRevision uint64
	counts           map[string]int
	states           map[string]string
	observations     []string
}

// TestAgentObservationKeyColumnAcrossEngines reconciles the same two-generation
// fixture through SQLite and a real MySQL 8.4 database and requires identical
// rows, identical misses counters and identical manifest revisions.
func TestAgentObservationKeyColumnAcrossEngines(t *testing.T) {
	ctx := context.Background()
	// connected must be inside the Agent lease window, otherwise reconcile
	// refuses with target_manifest_agent_offline.
	connected := time.Now().UTC()
	available := true
	agent := &GatewayAgent{Cluster: "ops", Instance: "left", Generation: 4, ConnectedAt: connected}

	// Generation 4 observes a cluster and a machine; generation 5 drops the
	// machine, so its misses counter must advance to 1 on both engines.
	full := &agentResourceSnapshot{
		Mode:                "kubernetes",
		KubernetesAvailable: true,
		Complete:            []string{"cluster", "machine"},
		Resources: []agentResourceObservation{
			{Kind: "cluster", Key: "current", Name: "ops", Identity: "id-1"},
			{Kind: "machine", Key: "web-1", Name: "web-1", Identity: "id-2"},
		},
	}
	full.canonicalize()
	reduced := &agentResourceSnapshot{
		Mode:                "kubernetes",
		KubernetesAvailable: true,
		Complete:            []string{"cluster", "machine"},
		Resources:           full.Resources[:1],
	}
	reduced.canonicalize()

	run := func(store *GatewayStore, generation uint64) agentResourceEngineRun {
		t.Helper()
		if err := store.MarkAgentOnline(AgentStatus{Cluster: "ops", Instance: "left", Generation: generation, ConnectedAt: connected, Mode: "kubernetes", KubernetesAvailable: &available}); err != nil {
			t.Fatalf("mark agent online: %v", err)
		}
		snapshot := reduced
		if generation == 4 {
			snapshot = full
		}
		agent.Generation = generation
		status, err := store.reconcileAgentResources(ctx, agent, snapshot, agentResourceDigest(*snapshot))
		if err != nil {
			t.Fatalf("reconcile on %s: %v", store.backend, err)
		}
		manifest := status.Manifest
		out := agentResourceEngineRun{manifestRevision: manifest.Revision, counts: manifest.Counts, states: map[string]string{}}
		for _, resource := range manifest.Resources {
			out.states[resource.Ref] = resource.State
		}
		rows, err := store.db.QueryContext(ctx,
			"SELECT kind,"+gatewayKeyColumnFor(store.dialect)+",misses FROM agent_observations WHERE cluster=? AND instance=? ORDER BY kind,"+gatewayKeyColumnFor(store.dialect),
			"ops", "left")
		if err != nil {
			t.Fatalf("read observations on %s: %v", store.backend, err)
		}
		defer rows.Close()
		for rows.Next() {
			var kind, key string
			var misses int
			if err := rows.Scan(&kind, &key, &misses); err != nil {
				t.Fatalf("scan observations on %s: %v", store.backend, err)
			}
			out.observations = append(out.observations, fmt.Sprintf("%s/%s=%d", kind, key, misses))
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate observations on %s: %v", store.backend, err)
		}
		return out
	}

	t.Setenv("DOOPS_GATEWAY_STORE", "sqlite")
	sqliteDir := t.TempDir()
	sqliteStore, err := OpenGatewayStore(sqliteDir + "/gateway.db")
	if err != nil {
		t.Fatalf("open SQLite store: %v", err)
	}
	defer sqliteStore.Close()
	// The SQLite backend builds the machine inventory through its migrations;
	// the MySQL backend installs the same tables with the canonical schema.
	for _, name := range []string{"machine_inventory", "agent_resource_sync", "cluster_inventory", "machine_diagnostics", "business_inventory", "asset_catalog", "agent_asset_scope"} {
		data, err := os.ReadFile("../../../docs/migrations/" + name + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sqliteStore.db.Exec(string(data)); err != nil {
			t.Fatalf("apply %s migration: %v", name, err)
		}
	}
	sqliteFirst := run(sqliteStore, 4)
	sqliteSecond := run(sqliteStore, 5)

	mysqlStore := openGatewayMySQLResourceTestStore(t)
	defer mysqlStore.Close()
	mysqlFirst := run(mysqlStore, 4)
	mysqlSecond := run(mysqlStore, 5)

	compare := func(stage string, sqliteRun, mysqlRun agentResourceEngineRun) {
		t.Helper()
		if sqliteRun.manifestRevision != mysqlRun.manifestRevision {
			t.Fatalf("%s manifest_revision: sqlite %d, mysql %d", stage, sqliteRun.manifestRevision, mysqlRun.manifestRevision)
		}
		if fmt.Sprint(sqliteRun.counts) != fmt.Sprint(mysqlRun.counts) {
			t.Fatalf("%s counts: sqlite %v, mysql %v", stage, sqliteRun.counts, mysqlRun.counts)
		}
		if fmt.Sprint(sqliteRun.states) != fmt.Sprint(mysqlRun.states) {
			t.Fatalf("%s states: sqlite %v, mysql %v", stage, sqliteRun.states, mysqlRun.states)
		}
		if fmt.Sprint(sqliteRun.observations) != fmt.Sprint(mysqlRun.observations) {
			t.Fatalf("%s observations: sqlite %v, mysql %v", stage, sqliteRun.observations, mysqlRun.observations)
		}
		t.Logf("%s: revision=%d counts=%v states=%v observations=%v", stage, sqliteRun.manifestRevision, sqliteRun.counts, sqliteRun.states, sqliteRun.observations)
	}
	compare("first sync", sqliteFirst, mysqlFirst)
	compare("second sync", sqliteSecond, mysqlSecond)
	if len(mysqlSecond.observations) != 2 || !strings.Contains(strings.Join(mysqlSecond.observations, ","), "machine/web-1=1") {
		t.Fatalf("dropped machine did not advance its miss counter: %v", mysqlSecond.observations)
	}
	if mysqlSecond.manifestRevision != mysqlFirst.manifestRevision+1 {
		t.Fatalf("dropped machine did not republish the manifest: %d -> %d", mysqlFirst.manifestRevision, mysqlSecond.manifestRevision)
	}
}

// openGatewayMySQLResourceTestStore builds the real MySQL store against a fresh
// utf8mb4_0900_bin database, which is the collation canonical schema installs and
// the only one that reproduces Error 1271.
func openGatewayMySQLResourceTestStore(t *testing.T) *GatewayStore {
	t.Helper()
	db := gatewayCopyTestDestination(t)
	var database string
	if err := db.QueryRow("SELECT DATABASE()").Scan(&database); err != nil {
		t.Fatalf("read fixture database name: %v", err)
	}
	port := strings.TrimSpace(os.Getenv("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT"))
	t.Setenv("DOOPS_GATEWAY_STORE", "mysql")
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", fmt.Sprintf("root@tcp(%s)/%s?parseTime=true", net.JoinHostPort("127.0.0.1", port), database))
	t.Setenv("DOOPS_GATEWAY_DATA_DIR", t.TempDir())
	store, err := OpenGatewayStore(t.TempDir() + "/unused.db")
	if err != nil {
		t.Fatalf("open MySQL store: %v", err)
	}
	return store
}
