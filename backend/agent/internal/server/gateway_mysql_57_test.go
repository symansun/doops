package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestGatewayMySQLVersionParsing(t *testing.T) {
	valid := []struct {
		raw       string
		major     int
		minor     int
		patch     int
		supported bool
		is57      bool
	}{
		{"5.7.44-log", 5, 7, 44, true, true},
		{"5.7.22", 5, 7, 22, true, true},
		{"5.7.21-log", 5, 7, 21, false, true},
		{"5.6.51", 5, 6, 51, false, true},
		{"8.0.36", 8, 0, 36, true, false},
		{"8.4.13", 8, 4, 13, true, false},
		{"9.0.1", 9, 0, 1, true, false},
	}
	for _, tc := range valid {
		v, err := parseGatewayMySQLVersion(tc.raw)
		if err != nil {
			t.Fatalf("parseGatewayMySQLVersion(%q) returned unexpected error: %v", tc.raw, err)
		}
		if v.Major != tc.major || v.Minor != tc.minor || v.Patch != tc.patch {
			t.Errorf("parseGatewayMySQLVersion(%q) = %d.%d.%d, want %d.%d.%d", tc.raw, v.Major, v.Minor, v.Patch, tc.major, tc.minor, tc.patch)
		}
		if v.supportsGateway() != tc.supported {
			t.Errorf("version %q supportsGateway() = %v, want %v", tc.raw, v.supportsGateway(), tc.supported)
		}
		if v.isMySQL57() != tc.is57 {
			t.Errorf("version %q isMySQL57() = %v, want %v", tc.raw, v.isMySQL57(), tc.is57)
		}
	}
	for _, bad := range []string{"", "not-a-version", "5", "a.b.c", "-1.0.0"} {
		if _, err := parseGatewayMySQLVersion(bad); err == nil {
			t.Errorf("parseGatewayMySQLVersion(%q) accepted invalid version", bad)
		}
	}
}

func TestGatewayMySQL57SchemaTranslation(t *testing.T) {
	v84 := gatewayMySQLServerVersion{Major: 8, Minor: 4, Patch: 13, Raw: "8.4.13"}
	ddl84, err := gatewayMySQLSchemaSQLForVersion(v84)
	if err != nil {
		t.Fatalf("schema for 8.4: %v", err)
	}
	if ddl84 != gatewayMySQLSchemaSQL {
		t.Fatal("MySQL 8.4 DDL must stay byte-identical to gateway_mysql_schema.sql")
	}

	v57 := gatewayMySQLServerVersion{Major: 5, Minor: 7, Patch: 44, Raw: "5.7.44-log"}
	ddl57, err := gatewayMySQLSchemaSQLForVersion(v57)
	if err != nil {
		t.Fatalf("schema for 5.7: %v", err)
	}
	for _, line := range strings.Split(ddl57, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToUpper(trimmed), "UNIQUE KEY ") {
			continue
		}
		for _, forbidden := range []string{"((IF(", "(SHA2("} {
			if strings.Contains(trimmed, forbidden) {
				t.Errorf("MySQL 5.7 DDL still contains MySQL 8 functional UNIQUE KEY %q", trimmed)
			}
		}
	}
	for table, cols := range gatewayMySQL57VirtualKeyColumns {
		for col := range cols {
			if !strings.Contains(ddl57, col) {
				t.Errorf("MySQL 5.7 DDL missing virtual key column %s.%s", table, col)
			}
		}
	}
	if strings.Contains(ddl57, "host VARCHAR(253) NOT NULL UNIQUE,") {
		t.Error("MySQL 5.7 DDL still defines inline VARCHAR(253) UNIQUE on proxy_routes.host")
	}
	if strings.Contains(ddl57, "UNIQUE KEY idx_edge_routes_hostname (hostname)") {
		t.Error("MySQL 5.7 DDL still indexes raw VARCHAR(253) on edge_routes.hostname")
	}
	if !strings.Contains(ddl57, "PRIMARY KEY (cluster, instance, node_name(191)),") {
		t.Error("MySQL 5.7 DDL missing 191-char prefix on inventory_node_links primary key")
	}
	stmts, err := gatewayMySQLStatements(ddl57)
	if err != nil {
		t.Fatalf("split 5.7 DDL: %v", err)
	}
	var tables, triggers int
	for _, s := range stmts {
		u := strings.ToUpper(strings.TrimSpace(s))
		switch {
		case strings.HasPrefix(u, "CREATE TABLE "):
			tables++
		case strings.HasPrefix(u, "CREATE TRIGGER "):
			triggers++
		}
	}
	if tables != len(gatewayMySQLTestTables) {
		t.Fatalf("5.7 CREATE TABLE count = %d, want %d", tables, len(gatewayMySQLTestTables))
	}
	if triggers != len(gatewayMySQLTestTriggers) {
		t.Fatalf("5.7 CREATE TRIGGER count = %d, want %d", triggers, len(gatewayMySQLTestTriggers))
	}
}

func TestGatewayMySQL57AmbientCollation(t *testing.T) {
	prevDialect := gatewayDialect()
	prev57 := gatewayIsMySQL57()
	t.Cleanup(func() {
		gatewaySetDialect(prevDialect)
		gatewaySetMySQL57(prev57)
	})

	gatewaySetDialect(gatewayDialectMySQL)
	gatewaySetMySQL57(false)
	if got := gatewayJSONTextScalarFor(gatewayDialectMySQL, "json_extract(c.details,'$.identity')"); !strings.Contains(got, "COLLATE utf8mb4_0900_bin") {
		t.Fatalf("MySQL 8 scalar = %q, want utf8mb4_0900_bin", got)
	}

	gatewaySetMySQL57(true)
	if got := gatewayJSONTextScalarFor(gatewayDialectMySQL, "json_extract(c.details,'$.identity')"); !strings.Contains(got, "COLLATE utf8mb4_bin") || strings.Contains(got, "0900") {
		t.Fatalf("MySQL 5.7 scalar = %q, want utf8mb4_bin without 0900", got)
	}

	// Verify Close restores both dialect and mysql57 flag.
	store := &GatewayStore{
		backend:         "mysql",
		dialect:         gatewayDialectMySQL,
		mysql57:         true,
		previousDialect: gatewayDialectSQLite,
		previousMySQL57: false,
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close synthetic store: %v", err)
	}
	if gatewayDialect() != gatewayDialectSQLite || gatewayIsMySQL57() {
		t.Fatalf("after Close: dialect=%q mysql57=%v, want sqlite/false", gatewayDialect(), gatewayIsMySQL57())
	}
}

// TestMySQL57GatewayEndToEnd exercises the full Gateway runtime and offline
// copier against a real MySQL 5.7 server (such as Aliyun RDS MySQL 5.7.44).
// Gated on DOOPS_TEST_GATEWAY_MYSQL57_PORT: unset skips; set-but-unreachable
// fails closed.
func TestMySQL57GatewayEndToEnd(t *testing.T) {
	port := strings.TrimSpace(os.Getenv("DOOPS_TEST_GATEWAY_MYSQL57_PORT"))
	if port == "" {
		t.Skip("set DOOPS_TEST_GATEWAY_MYSQL57_PORT to run live MySQL 5.7 backend verification")
	}
	addr := net.JoinHostPort("127.0.0.1", port)
	adminCfg := mysql.Config{
		User:                 "root",
		Passwd:               os.Getenv("DOOPS_TEST_GATEWAY_MYSQL57_PASSWORD"),
		Net:                  "tcp",
		Addr:                 addr,
		AllowNativePasswords: true,
		Timeout:              8 * time.Second,
	}
	admin, err := sql.Open("mysql", adminCfg.FormatDSN())
	if err != nil {
		t.Fatalf("open MySQL 5.7 admin: %v", err)
	}
	t.Cleanup(func() { admin.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		t.Fatalf("disposable MySQL 5.7 server at %s is unavailable: %v", addr, err)
	}
	ver, err := gatewayMySQLQueryVersion(ctx, admin)
	if err != nil {
		t.Fatalf("query MySQL 5.7 version: %v", err)
	}
	if !ver.isMySQL57() {
		t.Fatalf("DOOPS_TEST_GATEWAY_MYSQL57_PORT points at %s, want MySQL 5.7.x", ver.Raw)
	}

	makeDB := func(collation string) (string, string) {
		t.Helper()
		name := "gw57_" + strings.ToLower(randomHex(6))
		if _, err := admin.ExecContext(ctx, "CREATE DATABASE `"+name+"` DEFAULT CHARACTER SET utf8mb4 DEFAULT COLLATE "+collation); err != nil {
			t.Fatalf("create db %s (%s): %v", name, collation, err)
		}
		t.Cleanup(func() {
			cctx, ccancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer ccancel()
			_, _ = admin.ExecContext(cctx, "DROP DATABASE IF EXISTS `"+name+"`")
		})
		cfg := adminCfg
		cfg.DBName = name
		return name, cfg.FormatDSN()
	}

	// 1. Fresh startup + idempotent reopen + core runtime flows under utf8mb4_general_ci (Aliyun RDS 5.7 default).
	_, dsn := makeDB("utf8mb4_general_ci")
	dataDir := t.TempDir()
	t.Setenv("DOOPS_GATEWAY_STORE", "mysql")
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", dsn)
	t.Setenv("DOOPS_GATEWAY_DATA_DIR", dataDir)

	store, err := OpenGatewayStore("")
	if err != nil {
		t.Fatalf("open MySQL 5.7 store (initial): %v", err)
	}
	if !store.mysql57 || !gatewayIsMySQL57() {
		t.Fatalf("store did not enter MySQL 5.7 mode: store.mysql57=%v ambient=%v", store.mysql57, gatewayIsMySQL57())
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close initial MySQL 5.7 store: %v", err)
	}

	// Reopen existing MySQL 5.7 schema (idempotent startup + collation checks).
	store, err = OpenGatewayStore("")
	if err != nil {
		t.Fatalf("reopen MySQL 5.7 store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Create admin user + session token.
	adminUser, err := store.CreateUser("admin57")
	if err != nil {
		t.Fatalf("create admin user: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, "UPDATE users SET is_admin=1 WHERE id=?", adminUser.ID); err != nil {
		t.Fatalf("promote admin57: %v", err)
	}
	tok, err := store.CreateToken(CreateTokenRequest{
		Kind:      TokenKindUser,
		UserID:    adminUser.ID,
		Name:      "tok57",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create admin token: %v", err)
	}
	auth := TokenAuth{UserID: adminUser.ID, TokenID: tok.ID, Kind: TokenKindUser}

	// Mark agent online + reconcile agent resources (exercises COLLATE utf8mb4_bin + parameterized IN).
	now := time.Now().UTC()
	avail := true
	if err := store.MarkAgentOnline(AgentStatus{
		Cluster: "c57", Instance: "i57", Generation: 1, ConnectedAt: now, Mode: "kubernetes", KubernetesAvailable: &avail,
	}); err != nil {
		t.Fatalf("MarkAgentOnline: %v", err)
	}
	agent := &GatewayAgent{Cluster: "c57", Instance: "i57", Generation: 1, ConnectedAt: now}
	snap := &agentResourceSnapshot{
		Mode:                "kubernetes",
		KubernetesAvailable: true,
		Complete:            []string{"cluster", "machine"},
		Resources: []agentResourceObservation{
			{Kind: "cluster", Key: "current", Name: "c57", Identity: "uid-c57"},
			{Kind: "machine", Key: "m57", Name: "node-57", Identity: "uid-m57"},
		},
	}
	snap.canonicalize()
	if _, err := store.reconcileAgentResources(ctx, agent, snap, agentResourceDigest(*snap)); err != nil {
		t.Fatalf("reconcileAgentResources on MySQL 5.7: %v", err)
	}

	// Exercise CTE-free inventory, workspace, topology, monitor summary, and inspection reads.
	if clusters, err := store.listInventoryClusters(ctx, auth); err != nil || len(clusters) != 0 {
		t.Fatalf("listInventoryClusters: len=%d err=%v", len(clusters), err)
	}
	if links, err := store.listNodeLinks(ctx, auth); err != nil || len(links) != 0 {
		t.Fatalf("listNodeLinks: len=%d err=%v", len(links), err)
	}
	reqMachines := httptest.NewRequest(http.MethodGet, "/v1/assets/machines", nil)
	if _, err := store.readMachineInventory(reqMachines, auth); err != nil {
		t.Fatalf("readMachineInventory on MySQL 5.7: %v", err)
	}
	reqScope := httptest.NewRequest(http.MethodGet, "/v1/assets/workspace?view=targets", nil)
	if _, err := store.readWorkspaceAssets(reqScope, auth); err != nil {
		t.Fatalf("readWorkspaceAssets(targets) on MySQL 5.7: %v", err)
	}
	reqScopeManage := httptest.NewRequest(http.MethodGet, "/v1/assets/workspace?view=manage&cluster=c57&instance=i57", nil)
	if _, err := store.readWorkspaceAssets(reqScopeManage, auth); err != nil {
		t.Fatalf("readWorkspaceAssets(manage / workspaceCatalog) on MySQL 5.7: %v", err)
	}
	reqInsp := httptest.NewRequest(http.MethodGet, "/v1/assets/inspections?view=overview", nil)
	if _, err := store.inspectionRead(reqInsp, auth); err != nil {
		t.Fatalf("inspectionRead(overview) on MySQL 5.7: %v", err)
	}
	reqWeekly := httptest.NewRequest(http.MethodGet, "/v1/assets/inspections?view=weekly", nil)
	if _, err := store.inspectionRead(reqWeekly, auth); err != nil {
		t.Fatalf("inspectionRead(weekly) on MySQL 5.7: %v", err)
	}
	reqAlerts := httptest.NewRequest(http.MethodGet, "/v1/assets/monitor?view=alerts", nil)
	if _, err := store.readMonitorSummary(ctx, auth, reqAlerts.URL.Query()); err != nil {
		t.Fatalf("readMonitorSummary on MySQL 5.7: %v", err)
	}
	if _, err := store.readAssetTopology(ctx, auth, ""); err != nil {
		t.Fatalf("readAssetTopology on MySQL 5.7: %v", err)
	}

	// Exercise CTE-free audit purge (both upgrade-evidence and ordinary batch) on MySQL 5.7.
	cutoff := time.Now().UTC().Truncate(time.Millisecond)
	seeds, wantDeleted := auditPurgeParitySeeds(cutoff)
	ids := auditPurgeInsertSeeds(t, store.db, seeds, "mysql57-purge")
	purgeRes, err := store.PurgeExpiredAudit(cutoff)
	if err != nil {
		t.Fatalf("PurgeExpiredAudit on MySQL 5.7: %v", err)
	}
	if purgeRes.Deleted != int64(len(wantDeleted)) {
		t.Fatalf("PurgeExpiredAudit deleted %d rows on MySQL 5.7, want %d", purgeRes.Deleted, len(wantDeleted))
	}
	for idx, id := range ids {
		var present int
		if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_events WHERE id=?", id).Scan(&present); err != nil {
			t.Fatalf("check purged row %d: %v", idx, err)
		}
		expectGone := false
		for _, d := range wantDeleted {
			if d == idx {
				expectGone = true
				break
			}
		}
		if expectGone && present != 0 {
			t.Errorf("audit purge on MySQL 5.7 kept row %d (%s/%s)", idx, seeds[idx].action, seeds[idx].status)
		}
		if !expectGone && present != 1 {
			t.Errorf("audit purge on MySQL 5.7 deleted protected row %d (%s/%s)", idx, seeds[idx].action, seeds[idx].status)
		}
	}

	// Verify virtual generated unique key enforcement on MySQL 5.7 (credential_grants_identity)
	// and verify declaredAgentResources projects active credential grants cleanly.
	nowStr := formatTime(now)
	if _, err := store.db.ExecContext(ctx, `INSERT INTO credentials(id,name,scope,owner_id,type,state,created_by,created_at,updated_at,revoked_at) VALUES('cred-1','c1','platform','',?,'active',?,?,?,'')`, string(CredentialTypeOpaque), adminUser.ID, nowStr, nowStr); err != nil {
		t.Fatalf("insert credential: %v", err)
	}
	grantScope := `{"cluster":"c57","instance":"i57","use":"` + string(CredentialUseOpaqueSecret) + `"}`
	if _, err := store.db.ExecContext(ctx, `INSERT INTO credential_grants(id,credential_id,grantee_id,scope_json,state,created_by,created_at,revoked_at) VALUES('g1','cred-1',?,?,'active',?,?,'')`, adminUser.ID, grantScope, adminUser.ID, nowStr); err != nil {
		t.Fatalf("insert first grant: %v", err)
	}
	_, dupErr := store.db.ExecContext(ctx, `INSERT INTO credential_grants(id,credential_id,grantee_id,scope_json,state,created_by,created_at,revoked_at) VALUES('g2','cred-1',?,?,'active',?,?,'')`, adminUser.ID, grantScope, adminUser.ID, nowStr)
	var myErr *mysql.MySQLError
	if !errors.As(dupErr, &myErr) || myErr.Number != 1062 {
		t.Fatalf("duplicate grant on MySQL 5.7 virtual key returned %v, want MySQL error 1062", dupErr)
	}
	manifestWithCred, err := store.reconcileAgentResources(ctx, agent, snap, agentResourceDigest(*snap))
	if err != nil {
		t.Fatalf("reconcileAgentResources with credential grant on MySQL 5.7: %v", err)
	}
	if manifestWithCred.Manifest.Counts["credential"] != 1 {
		t.Fatalf("manifest credential count on MySQL 5.7 = %d, want 1 (counts=%v)", manifestWithCred.Manifest.Counts["credential"], manifestWithCred.Manifest.Counts)
	}

	// Verify 767-byte-safe VARCHAR(253) UNIQUE virtual keys on proxy_routes and edge_routes
	// (distinct >191-char hostnames both succeed; duplicate >191-char hostname fails with 1062).
	longPrefix := strings.Repeat("a", 195) + "."
	longHostA := longPrefix + "alpha.example.test"
	longHostB := longPrefix + "bravo.example.test"
	if _, err := store.db.ExecContext(ctx, `INSERT INTO proxy_routes(id,host,cluster,instance,upstream,enabled,created_at,updated_at) VALUES('pr-1',?,'c57','i57','http://127.0.0.1:8080',1,?,?)`, longHostA, nowStr, nowStr); err != nil {
		t.Fatalf("insert long proxy_route A: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO proxy_routes(id,host,cluster,instance,upstream,enabled,created_at,updated_at) VALUES('pr-2',?,'c57','i57','http://127.0.0.1:8081',1,?,?)`, longHostB, nowStr, nowStr); err != nil {
		t.Fatalf("insert long proxy_route B (>191-char suffix diff): %v", err)
	}
	_, dupProxyErr := store.db.ExecContext(ctx, `INSERT INTO proxy_routes(id,host,cluster,instance,upstream,enabled,created_at,updated_at) VALUES('pr-3',?,'c57','i57','http://127.0.0.1:8082',1,?,?)`, longHostA, nowStr, nowStr)
	if !errors.As(dupProxyErr, &myErr) || myErr.Number != 1062 {
		t.Fatalf("duplicate long proxy_route host returned %v, want MySQL error 1062", dupProxyErr)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO edge_routes(hostname,cluster,instance,backend,mode,enabled,max_conns,note,created_at,updated_at) VALUES(?,'c57','i57','b1','tls-passthrough',1,0,'',?,?)`, longHostA, nowStr, nowStr); err != nil {
		t.Fatalf("insert long edge_route A: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO edge_routes(hostname,cluster,instance,backend,mode,enabled,max_conns,note,created_at,updated_at) VALUES(?,'c57','i57','b2','tls-passthrough',1,0,'',?,?)`, longHostB, nowStr, nowStr); err != nil {
		t.Fatalf("insert long edge_route B (>191-char suffix diff): %v", err)
	}
	_, dupEdgeErr := store.db.ExecContext(ctx, `INSERT INTO edge_routes(hostname,cluster,instance,backend,mode,enabled,max_conns,note,created_at,updated_at) VALUES(?,'c57','i57','b3','tls-passthrough',1,0,'',?,?)`, longHostA, nowStr, nowStr)
	if !errors.As(dupEdgeErr, &myErr) || myErr.Number != 1062 {
		t.Fatalf("duplicate long edge_route hostname returned %v, want MySQL error 1062", dupEdgeErr)
	}

	// Verify MySQL 5.7 correlated scalar subquery COALESCE(CAST(JSON_ARRAYAGG(...) AS CHAR), '[]')
	// returns empty '[]' (not NULL) on machines with zero tags and zero login accounts.
	if _, err := store.db.ExecContext(ctx, `INSERT INTO inventory_machines(asset_id,name,kind,address,cluster,instance,created_at,revision) VALUES('m57-empty','Empty Tags Machine','physical','10.0.0.1','','',?,1)`, nowStr); err != nil {
		t.Fatalf("insert inventory_machines m57-empty: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO inventory_machines(asset_id,name,kind,address,cluster,instance,created_at,revision) VALUES('m57-tagged','Tagged Machine','physical','10.0.0.2','','',?,1)`, nowStr); err != nil {
		t.Fatalf("insert inventory_machines m57-tagged: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO inventory_machine_tags(asset_id,tag) VALUES('m57-tagged','gpu'),('m57-tagged','env=prod')`); err != nil {
		t.Fatalf("insert inventory_machine_tags: %v", err)
	}
	mTx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("begin machine list tx on MySQL 5.7: %v", err)
	}
	listedMachines, listErr := listInventoryMachines(mTx, auth)
	_ = mTx.Rollback()
	if listErr != nil {
		t.Fatalf("listInventoryMachines on MySQL 5.7 failed: %v", listErr)
	}
	if len(listedMachines) != 2 || len(listedMachines[0].Tags) != 0 || len(listedMachines[1].Tags) != 2 || listedMachines[1].Tags[0] != "env=prod" || listedMachines[1].Tags[1] != "gpu" {
		t.Fatalf("unexpected MySQL 5.7 listed machines: %+v", listedMachines)
	}

	// Verify batched TouchAgentsContext (single UPDATE ... CASE), multi-batch
	// storeObservationEpoch (>150 resources), multi-endpoint webhook enqueue
	// (no MySQL open-rows-during-exec error), and UTF-8 bounded execution records
	// all work cleanly on real MySQL 5.7 with InterpolateParams=true.
	if err := store.MarkAgentOnline(AgentStatus{
		Cluster: "c57-b", Instance: "i57-b", Generation: 3, ConnectedAt: now, Mode: "host",
	}); err != nil {
		t.Fatalf("MarkAgentOnline c57-b: %v", err)
	}
	touchedAt := now.Add(30 * time.Second).Truncate(time.Millisecond)
	if err := store.TouchAgentsContext(ctx, []AgentHeartbeatUpdate{
		{Cluster: "c57-b", Instance: "i57-b", Generation: 3, LastSeen: touchedAt},
		{Cluster: "c57", Instance: "i57", Generation: 1, LastSeen: touchedAt},
		{Cluster: "c57", Instance: "i57", Generation: 99, LastSeen: touchedAt.Add(time.Hour)},
	}); err != nil {
		t.Fatalf("batched TouchAgentsContext on MySQL 5.7: %v", err)
	}
	var gotLastSeenA, gotLastSeenB string
	if err := store.db.QueryRowContext(ctx, `SELECT last_seen FROM agent_status WHERE cluster='c57' AND instance='i57'`).Scan(&gotLastSeenA); err != nil || gotLastSeenA != formatTime(touchedAt.UTC()) {
		t.Fatalf("batched TouchAgentsContext c57/i57 LastSeen=%s err=%v, want %s", gotLastSeenA, err, formatTime(touchedAt.UTC()))
	}
	if err := store.db.QueryRowContext(ctx, `SELECT last_seen FROM agent_status WHERE cluster='c57-b' AND instance='i57-b'`).Scan(&gotLastSeenB); err != nil || gotLastSeenB != formatTime(touchedAt.UTC()) {
		t.Fatalf("batched TouchAgentsContext c57-b/i57-b LastSeen=%s err=%v, want %s", gotLastSeenB, err, formatTime(touchedAt.UTC()))
	}

	// Reconcile 170 observations (spans 2 multi-row batches of 150 + 20).
	bigSnap := &agentResourceSnapshot{
		Mode:                "kubernetes",
		KubernetesAvailable: true,
		Complete:            []string{"cluster", "namespace"},
		Resources: []agentResourceObservation{
			{Kind: "cluster", Key: "current", Name: "c57", Identity: "uid-c57"},
		},
	}
	for i := 0; i < 169; i++ {
		name := fmt.Sprintf("ns-%03d", i)
		bigSnap.Resources = append(bigSnap.Resources, agentResourceObservation{
			Kind: "namespace", Key: name, Name: name, Identity: fmt.Sprintf("uid-ns-%03d", i),
		})
	}
	bigSnap.canonicalize()
	bigManifest, err := store.reconcileAgentResources(ctx, agent, bigSnap, agentResourceDigest(*bigSnap))
	if err != nil {
		t.Fatalf("multi-batch reconcileAgentResources (170 rows) on MySQL 5.7: %v", err)
	}
	if bigManifest.Manifest.Counts["namespace"] != 169 {
		t.Fatalf("multi-batch namespace count = %d, want 169", bigManifest.Manifest.Counts["namespace"])
	}

	// EnqueuePlatformEvent with 2 matching webhook endpoints must not trip
	// MySQL "commands out of sync" / "driver: bad connection".
	t.Setenv(secretKeyEnv, "mysql57-webhook-test-key-32bytes!")
	for _, epName := range []string{"wh57-a", "wh57-b"} {
		if _, _, err := store.CreateWebhookEndpoint(ctx, WebhookCreateInput{
			Name:       epName,
			URL:        "https://93.184.216.34/" + epName,
			EventTypes: []string{"execution.finished"},
		}); err != nil {
			t.Fatalf("CreateWebhookEndpoint %s on MySQL 5.7: %v", epName, err)
		}
	}
	whEv, err := newPlatformEvent("execution.finished", now, adminUser.ID, "c57", "i57", "", map[string]any{"audit_id": "57"}, "mysql57-wh-dedupe")
	if err != nil {
		t.Fatalf("newPlatformEvent: %v", err)
	}
	if _, inserted, err := store.EnqueuePlatformEvent(ctx, whEv); err != nil || !inserted {
		t.Fatalf("EnqueuePlatformEvent with 2 endpoints on MySQL 5.7: inserted=%v err=%v", inserted, err)
	}
	var deliveryCount int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM webhook_deliveries WHERE event_id=?", whEv.ID).Scan(&deliveryCount); err != nil || deliveryCount != 2 {
		t.Fatalf("webhook_deliveries count on MySQL 5.7 = %d err=%v, want 2", deliveryCount, err)
	}

	// Verify asset_monitor detail read with multiple series (cpu + memory) on MySQL 5.7
	// closes the outer series cursor before querying samples/rules/alerts (BUG-1 regression).
	monNowMs := time.Now().UnixMilli()
	monCPU, monMem, monThresh := 0.25, float64(64*1024*1024), 0.1
	monTx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin monitor seed tx on MySQL 5.7: %v", err)
	}
	monReq := monitorRequest{Cluster: "c57", Instance: "i57"}
	for _, sample := range []monitorInput{
		{monitorAsset: monitorAsset{Type: "app", AssetID: "pod-57", Namespace: "orders", Origin: "k8s_top"}, Metric: "cpu", Unit: "cores", monitorPoint: monitorPoint{At: monNowMs, Status: "ok", Value: &monCPU}},
		{monitorAsset: monitorAsset{Type: "app", AssetID: "pod-57", Namespace: "orders", Origin: "k8s_top"}, Metric: "memory", Unit: "bytes", monitorPoint: monitorPoint{At: monNowMs, Status: "ok", Value: &monMem}},
	} {
		if _, err := storeMonitorPoint(ctx, monTx, monReq, sample, "k8s_top", adminUser.ID); err != nil {
			_ = monTx.Rollback()
			t.Fatalf("storeMonitorPoint on MySQL 5.7: %v", err)
		}
	}
	if _, err := saveMonitorRule(monTx, monitorRequest{
		Cluster:      "c57",
		Instance:     "i57",
		monitorAsset: monitorAsset{Type: "app", AssetID: "pod-57", Namespace: "orders", Origin: "k8s_top"},
		Metric:       "cpu",
		monitorRule:  monitorRule{Threshold: &monThresh, Duration: 1},
	}, monNowMs, adminUser.ID); err != nil {
		_ = monTx.Rollback()
		t.Fatalf("saveMonitorRule on MySQL 5.7: %v", err)
	}
	if err := monTx.Commit(); err != nil {
		t.Fatalf("commit monitor seed tx on MySQL 5.7: %v", err)
	}
	monDetailAny, err := store.readAssetMonitor(ctx, auth, url.Values{
		"cluster": {"c57"}, "instance": {"i57"}, "type": {"app"}, "asset_id": {"pod-57"}, "namespace": {"orders"}, "origin": {"k8s_top"}, "range": {"1h"},
	})
	if err != nil {
		t.Fatalf("readAssetMonitor detail on MySQL 5.7 failed: %v", err)
	}
	monDetailMap, _ := monDetailAny.(map[string]any)
	monSeries, _ := monDetailMap["series"].([]monitorSeries)
	if len(monSeries) != 2 {
		t.Fatalf("readAssetMonitor detail on MySQL 5.7 series len = %d, want 2", len(monSeries))
	}

	// Persist an oversized Chinese execution record (must truncate on UTF-8 rune boundary).
	zhRec := &executionRecord{
		id:        "exec-57-utf8",
		tool:      "shell",
		cluster:   "c57",
		instance:  "i57",
		ownerID:   adminUser.ID,
		status:    "success",
		errText:   "中文错误\x88字节",
		terminal:  []byte(strings.Repeat("中", executionRecordTerminalLimit/3+20)),
		createdAt: now,
		updatedAt: now,
	}
	if err := store.saveExecutionRecord(zhRec, time.Hour); err != nil {
		t.Fatalf("saveExecutionRecord oversized Chinese UTF-8 on MySQL 5.7: %v", err)
	}

	// 1.5. Exercise File-First audit document storage + export + purge + OPTIMIZE TABLE reclaim and consistent store backup on MySQL 5.7 (with virtual generated columns).
	t.Setenv("DOOPS_AUDIT_STORAGE", "file")
	_ = store.Close()
	store, err = OpenGatewayStore("")
	if err != nil {
		t.Fatalf("reopen MySQL 5.7 store with DOOPS_AUDIT_STORAGE=file: %v", err)
	}
	if store.AuditStorageMode() != "file" {
		t.Fatalf("MySQL 5.7 audit storage mode = %q, want file", store.AuditStorageMode())
	}
	old57 := now.Add(-2 * time.Hour)
	cutoff57 := now.Add(-1 * time.Hour)
	auditID57, err := store.StartAudit(AuditEvent{
		Cluster:        "c57",
		Instance:       "i57",
		Action:         ActionExec,
		Session:        "s57-export",
		CommandSummary: "mysql57-export-row",
		StartedAt:      old57,
	})
	if err != nil {
		t.Fatalf("StartAudit on MySQL 5.7: %v", err)
	}
	tail57 := "ok-57-文档化审计正文"
	if err := store.FinishAudit(auditID57, AuditFinish{Status: "success", Tail: tail57, EndedAt: old57.Add(time.Second)}); err != nil {
		t.Fatalf("FinishAudit on MySQL 5.7: %v", err)
	}
	var dbTail57 string
	if err := store.db.QueryRowContext(ctx, `SELECT tail FROM audit_events WHERE id=?`, auditID57).Scan(&dbTail57); err != nil {
		t.Fatalf("read MySQL 5.7 audit_events.tail: %v", err)
	}
	if _, ok := parseAuditDocPointer(dbTail57); !ok {
		t.Fatalf("expected $audit_doc pointer in MySQL 5.7 audit_events.tail, got %q", dbTail57)
	}
	// Missing -confirm-database must fail closed on MySQL with exit 2.
	if code, err := RunGatewayAuditExport(ctx, GatewayAuditExportOptions{
		OutputDir: t.TempDir(),
		Before:    &cutoff57,
		Purge:     true,
		Reclaim:   true,
	}, io.Discard); code != GatewayMySQLCopyExitUsage || err == nil {
		t.Fatalf("MySQL 5.7 audit export without -confirm-database: code=%d err=%v, want exit 2", code, err)
	}
	// Export + purge + OPTIMIZE TABLE reclaim with matching -confirm-database.
	if code, err := RunGatewayAuditExport(ctx, GatewayAuditExportOptions{
		ConfirmDatabase:       store.mysqlDatabase,
		OutputDir:             t.TempDir(),
		Before:                &cutoff57,
		IncludeUnresolved:     true,
		IncludePlatformEvents: true,
		Purge:                 true,
		Reclaim:               true,
		KeepDocDays:           30,
	}, io.Discard); code != GatewayMySQLCopyExitOK || err != nil {
		t.Fatalf("RunGatewayAuditExport on MySQL 5.7 failed: code=%d err=%v", code, err)
	}
	var left57 int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE id=?`, auditID57).Scan(&left57); err != nil || left57 != 0 {
		t.Fatalf("MySQL 5.7 exported audit row still present: left=%d err=%v", left57, err)
	}
	// Even after DB purge, ListAuditFiltered must serve auditID57 with hydrated tail from <dataDir>/audit/records/.
	fallback57, err := store.ListAuditFiltered(AuditFilter{Session: "s57-export", Limit: 5})
	if err != nil || len(fallback57) != 1 || fallback57[0].ID != auditID57 || fallback57[0].Tail != tail57 {
		t.Fatalf("MySQL 5.7 audit document fallback mismatch: len=%d err=%v got=%+v", len(fallback57), err, fallback57)
	}
	t.Setenv("DOOPS_AUDIT_STORAGE", "db")
	// Consistent store backup must skip MySQL 5.7 VIRTUAL GENERATED columns cleanly.
	if code, err := RunGatewayStoreBackup(ctx, GatewayStoreBackupOptions{
		OutputDir: t.TempDir(),
	}, io.Discard); code != GatewayMySQLCopyExitOK || err != nil {
		t.Fatalf("RunGatewayStoreBackup on MySQL 5.7 failed: code=%d err=%v", code, err)
	}

	// 2. Full offline SQLite -> MySQL 5.7 copy with digest verification + fail-closed unexpected generated column check.
	_ = store.Close()
	t.Setenv("DOOPS_GATEWAY_STORE", "sqlite")
	sqlitePath := filepath.Join(t.TempDir(), "src.db")
	sqliteStore, err := OpenGatewayStore(sqlitePath)
	if err != nil {
		t.Fatalf("open SQLite source: %v", err)
	}
	if _, err := sqliteStore.CreateUser("copyuser"); err != nil {
		t.Fatalf("seed SQLite source user: %v", err)
	}
	if _, err := sqliteStore.db.ExecContext(ctx, `INSERT INTO proxy_routes(id,host,cluster,instance,upstream,enabled,created_at,updated_at) VALUES('pr-copy',?,'c57','i57','http://127.0.0.1:8080',1,?,?)`, longHostA, nowStr, nowStr); err != nil {
		t.Fatalf("seed SQLite proxy_route: %v", err)
	}
	if _, err := sqliteStore.db.ExecContext(ctx, `INSERT INTO edge_routes(hostname,cluster,instance,backend,mode,enabled,max_conns,note,created_at,updated_at) VALUES(?,'c57','i57','b1','tls-passthrough',1,0,'',?,?)`, longHostB, nowStr, nowStr); err != nil {
		t.Fatalf("seed SQLite edge_route: %v", err)
	}
	// Seed a historical split-UTF-8 audit row to verify strict-mode MySQL 5.7
	// copy normalizes non-PK invalid UTF-8 bytes explicitly with U+FFFD.
	if _, err := sqliteStore.db.ExecContext(ctx, `INSERT INTO audit_events(user_id,token_id,cluster,instance,action,session,command_summary,status,error,tail,bytes_in,bytes_out,started_at,ended_at,scope_json,started_at_ms) VALUES('','','c57','i57','exec','s57-utf8',?,'success','',?,0,0,?,?,'{}',1778466219000)`, "\x88\x5fcmd", "\x88\xb0 Helm", nowStr, nowStr); err != nil {
		t.Fatalf("seed SQLite invalid UTF-8 audit row: %v", err)
	}
	_ = sqliteStore.Close()

	srcDB, err := sql.Open("sqlite", "file:"+sqlitePath+"?mode=ro&_pragma=query_only(ON)")
	if err != nil {
		t.Fatalf("reopen SQLite source ro: %v", err)
	}
	defer srcDB.Close()

	_, copyDSN := makeDB("utf8mb4_bin")
	dstDB, err := sql.Open("mysql", copyDSN)
	if err != nil {
		t.Fatalf("open MySQL 5.7 copy destination: %v", err)
	}
	defer dstDB.Close()
	if _, err := installGatewayMySQLSchema(ctx, dstDB); err != nil {
		t.Fatalf("install MySQL 5.7 schema for copy: %v", err)
	}
	report, err := copyGatewaySQLiteToMySQL(ctx, srcDB, dstDB, false)
	if err != nil {
		t.Fatalf("copyGatewaySQLiteToMySQL into MySQL 5.7 failed: %v", err)
	}
	if report.TableRows["users"] != 1 || report.TableRows["proxy_routes"] != 1 || report.TableRows["edge_routes"] != 1 || report.TableRows["audit_events"] != 1 || report.UTF8RepairedCells != 2 || report.UTF8RepairedTables["audit_events"] != 2 || report.SHA256 == "" {
		t.Fatalf("unexpected MySQL 5.7 copy report: users=%d proxy_routes=%d edge_routes=%d audit_events=%d utf8_repaired=%d sha256=%q", report.TableRows["users"], report.TableRows["proxy_routes"], report.TableRows["edge_routes"], report.TableRows["audit_events"], report.UTF8RepairedCells, report.SHA256)
	}
	var got57Cmd, got57Tail string
	if err := dstDB.QueryRowContext(ctx, `SELECT command_summary, tail FROM audit_events WHERE session='s57-utf8'`).Scan(&got57Cmd, &got57Tail); err != nil {
		t.Fatalf("read copied audit_events row on MySQL 5.7: %v", err)
	}
	if got57Cmd != "\uFFFD_cmd" || got57Tail != "\uFFFD Helm" {
		t.Fatalf("MySQL 5.7 normalized audit_events = (%q, %q), want (%q, %q)", got57Cmd, got57Tail, "\uFFFD_cmd", "\uFFFD Helm")
	}

	// Negative test: an unallowlisted VIRTUAL GENERATED column on MySQL 5.7 must fail closed.
	_, badDSN := makeDB("utf8mb4_bin")
	badDB, err := sql.Open("mysql", badDSN)
	if err != nil {
		t.Fatalf("open MySQL 5.7 negative copy db: %v", err)
	}
	defer badDB.Close()
	if _, err := installGatewayMySQLSchema(ctx, badDB); err != nil {
		t.Fatalf("install MySQL 5.7 schema for negative copy: %v", err)
	}
	if _, err := badDB.ExecContext(ctx, "ALTER TABLE users ADD COLUMN _rogue_gen VARCHAR(64) GENERATED ALWAYS AS (name) VIRTUAL"); err != nil {
		t.Fatalf("add rogue virtual column: %v", err)
	}
	if _, err := copyGatewaySQLiteToMySQL(ctx, srcDB, badDB, true); !errors.Is(err, errGatewayCopySchema) {
		t.Fatalf("copier accepted unallowlisted virtual column on MySQL 5.7: err=%v, want %v", err, errGatewayCopySchema)
	}
}
