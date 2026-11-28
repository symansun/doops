package server

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestGatewayStoreBackendSelectionRejectsUnknown(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_STORE", "postgres")
	_, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err == nil {
		t.Fatal("OpenGatewayStore accepted an unknown backend")
	}
	for _, want := range []string{"gateway_store_invalid", "postgres", "allowed: sqlite, mysql"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q; an operator has to see the typo and the allowed values", err, want)
		}
	}
}

func TestGatewayStoreMySQLRequiresDSN(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_STORE", "mysql")
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", "")
	_, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err == nil || !strings.Contains(err.Error(), "gateway_mysql_dsn_missing") {
		t.Fatalf("mysql backend without a DSN returned %v, want gateway_mysql_dsn_missing", err)
	}
	if !strings.Contains(err.Error(), "DOOPS_GATEWAY_MYSQL_DSN") {
		t.Errorf("error %q does not name the variable the operator has to set", err)
	}
}

// TestGatewayDialectIsolationPerStore proves that a process hosting a SQLite
// store and a MySQL store renders each store's SQL with its own dialect, even
// when the two render calls interleave. It does not need a MySQL server: the
// question is which text each store produces.
func TestGatewayDialectIsolationPerStore(t *testing.T) {
	gatewaySetDialect(gatewayDialectSQLite)
	t.Cleanup(func() { gatewaySetDialect(gatewayDialectSQLite) })
	sqliteStore, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open SQLite store: %v", err)
	}
	t.Cleanup(func() { sqliteStore.Close() })
	if sqliteStore.backend != "sqlite" || sqliteStore.dialect != gatewayDialectSQLite {
		t.Fatalf("SQLite store reports backend=%q dialect=%q", sqliteStore.backend, sqliteStore.dialect)
	}
	mysqlStore := &GatewayStore{dialect: gatewayDialectMySQL, backend: "mysql"}

	render := func(store *GatewayStore) (string, string) {
		var concat, now string
		store.useDialect(func() {
			dialectRenderOrder = append(dialectRenderOrder, store.dialect)
			concat = gatewayConcatSQL("a", "b")
			now = gatewayEpochNowSQL()
		})
		return concat, now
	}
	dialectRenderOrder = nil
	sqliteConcat, sqliteNow := render(sqliteStore)
	mysqlConcat, mysqlNow := render(mysqlStore)
	sqliteConcatAgain, _ := render(sqliteStore)

	if sqliteConcat != "a || b" || !strings.HasPrefix(sqliteNow, "unixepoch()") {
		t.Errorf("SQLite store rendered concat=%q now=%q, want SQLite syntax", sqliteConcat, sqliteNow)
	}
	if mysqlConcat != "CONCAT(a, b)" || !strings.HasPrefix(mysqlNow, "UNIX_TIMESTAMP()") {
		t.Errorf("MySQL store rendered concat=%q now=%q, want MySQL syntax", mysqlConcat, mysqlNow)
	}
	if sqliteConcatAgain != sqliteConcat {
		t.Errorf("the SQLite store changed dialect after a MySQL render: %q, want %q", sqliteConcatAgain, sqliteConcat)
	}
	if got := gatewayDialect(); got != gatewayDialectSQLite {
		t.Errorf("the process dialect after the scopes = %q, want sqlite restored", got)
	}
	if len(dialectRenderOrder) != 3 || dialectRenderOrder[1] != gatewayDialectMySQL {
		t.Errorf("render order = %v, want sqlite, mysql, sqlite", dialectRenderOrder)
	}
}

var dialectRenderOrder []gatewaySQLDialect

// TestGatewayMySQLRuntimeStartup needs a disposable MySQL 8.4 server. It is the
// real thing the slice promises: one schema install, one baseline ledger row on
// every later startup, and a startup log with host and database but no
// credential.
func TestGatewayMySQLRuntimeStartup(t *testing.T) {
	port := strings.TrimSpace(os.Getenv("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT"))
	if port == "" {
		t.Skip("set DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT to run the MySQL runtime startup test")
	}
	addr := net.JoinHostPort("127.0.0.1", port)
	adminConfig := mysql.Config{User: "root", Passwd: os.Getenv("DOOPS_TEST_GATEWAY_MYSQL_PASSWORD"), Net: "tcp", Addr: addr, AllowNativePasswords: true, Timeout: 5 * time.Second}
	admin, err := sql.Open("mysql", adminConfig.FormatDSN())
	if err != nil {
		t.Fatalf("open MySQL admin connection: %v", err)
	}
	t.Cleanup(func() { admin.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		t.Fatalf("the disposable MySQL server at %s is unavailable: %v", addr, err)
	}
	database := "gateway_runtime_store_" + strings.ToLower(randomHex(6))
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE `"+database+"`"); err != nil {
		t.Fatalf("create runtime database: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = admin.ExecContext(cleanupCtx, "DROP DATABASE `"+database+"`")
	})
	config := mysql.Config{User: "root", Passwd: os.Getenv("DOOPS_TEST_GATEWAY_MYSQL_PASSWORD"), Net: "tcp", Addr: addr, DBName: database, AllowNativePasswords: true, Timeout: 5 * time.Second}
	dsn := config.FormatDSN()
	dataDir := filepath.Join(t.TempDir(), "data")
	t.Setenv("DOOPS_GATEWAY_STORE", "mysql")
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", dsn)
	t.Setenv("DOOPS_GATEWAY_DATA_DIR", dataDir)
	previous := gatewayDialect()
	t.Cleanup(func() { gatewaySetDialect(previous) })

	var logs bytes.Buffer
	previousLog := log.Writer()
	log.SetOutput(&logs)
	store, err := OpenGatewayStore("")
	log.SetOutput(previousLog)
	if err != nil {
		t.Fatalf("open MySQL runtime store: %v (log: %s)", err, logs.String())
	}
	t.Cleanup(func() { store.Close() })
	if store.backend != "mysql" || store.dialect != gatewayDialectMySQL {
		t.Fatalf("store reports backend=%q dialect=%q, want mysql", store.backend, store.dialect)
	}
	if !strings.Contains(logs.String(), "backend=mysql address="+addr+" database="+database) {
		t.Errorf("startup log %q does not name backend, host and database", logs.String())
	}
	if strings.Contains(logs.String(), "root") || strings.Contains(logs.String(), "@tcp") {
		t.Errorf("startup log leaked a credential or the raw DSN: %q", logs.String())
	}
	ledger := gatewayRuntimeLedgerRows(t, store.db)
	if len(ledger) != 1 || ledger[0] != gatewaySchemaBaselineVersion {
		t.Fatalf("fresh MySQL store ledger = %v, want exactly one %s row", ledger, gatewaySchemaBaselineVersion)
	}
	var tables int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'BASE TABLE'").Scan(&tables); err != nil {
		t.Fatalf("count MySQL runtime tables: %v", err)
	}
	if tables != len(gatewayMySQLTestTables) {
		t.Errorf("installed %d tables, want the %d-table native schema", tables, len(gatewayMySQLTestTables))
	}

	// INT-33: the MySQL install path must run the same startup seeds SQLite's
	// migrate() runs. These are the observable results an operator sees, not the
	// seed call sites.
	if store.dbDir != dataDir {
		t.Errorf("store dbDir = %q, want the DOOPS_GATEWAY_DATA_DIR root %q", store.dbDir, dataDir)
	}
	if store.skillStorageRoot() == "" || store.monitorArtifactRoot() == "" {
		t.Errorf("MySQL store has no filesystem root: skills=%q artifacts=%q", store.skillStorageRoot(), store.monitorArtifactRoot())
	}
	if !strings.Contains(logs.String(), "data_dir="+dataDir) {
		t.Errorf("startup log %q does not name the resolved data dir %q", logs.String(), dataDir)
	}
	var systemSkills int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM platform_skills WHERE scope='system'").Scan(&systemSkills); err != nil {
		t.Fatalf("count seeded system skills: %v", err)
	}
	if systemSkills != len(builtinSystemSkills) {
		t.Errorf("seeded %d system skills, want the %d builtin packages", systemSkills, len(builtinSystemSkills))
	}
	rows, err := store.db.Query("SELECT slug FROM platform_mcp_servers WHERE scope='system' ORDER BY slug")
	if err != nil {
		t.Fatalf("read seeded MCP servers: %v", err)
	}
	var mcpSlugs []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			rows.Close()
			t.Fatalf("scan seeded MCP server: %v", err)
		}
		mcpSlugs = append(mcpSlugs, slug)
	}
	rows.Close()
	var wantMCP []string
	for _, spec := range builtinSystemMCPServers {
		wantMCP = append(wantMCP, spec.Slug)
	}
	sort.Strings(wantMCP)
	if !reflect.DeepEqual(mcpSlugs, wantMCP) {
		t.Errorf("seeded MCP servers = %v, want %v", mcpSlugs, wantMCP)
	}
	var edgeVersions int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM edge_route_revision").Scan(&edgeVersions); err != nil {
		t.Fatalf("count edge route revisions: %v", err)
	}
	if edgeVersions != 1 {
		t.Errorf("edge_route_revision holds %d rows, want exactly 1", edgeVersions)
	}
	inspectScript := filepath.Join(dataDir, "skills", "inspect-vm-host", "scripts", "inspect.sh")
	info, err := os.Stat(inspectScript)
	if err != nil {
		t.Fatalf("the seeded inspect-vm-host package is not on disk: %v", err)
	}
	if info.Mode().Perm()&0111 == 0 {
		t.Errorf("%s mode = %v, want an executable bit", inspectScript, info.Mode().Perm())
	}

	// A second startup must reuse the schema and keep the ledger at one row.
	second, err := OpenGatewayStore("")
	if err != nil {
		t.Fatalf("re-open MySQL runtime store: %v", err)
	}
	t.Cleanup(func() { second.Close() })
	if ledger := gatewayRuntimeLedgerRows(t, second.db); len(ledger) != 1 {
		t.Errorf("ledger after a second startup = %v, want one row", ledger)
	}
}

func TestGatewayMySQLRuntimeAuthFailureDoesNotLeakPassword(t *testing.T) {
	port := strings.TrimSpace(os.Getenv("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT"))
	if port == "" {
		t.Skip("set DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT to run the MySQL runtime auth test")
	}
	addr := net.JoinHostPort("127.0.0.1", port)
	canary := "synthetic"
	t.Setenv("DOOPS_GATEWAY_STORE", "mysql")
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", fmt.Sprintf("root:%s@tcp(%s)/gateway_runtime_auth?timeout=5s", canary, addr))
	// The data directory is checked before the DSN is dialled, and the default
	// (/var/lib/doops-gateway) is not writable for a normal user or a CI runner.
	// Without this the authentication path is never reached and the test would
	// assert on a data-dir error instead of the credential failure it is about.
	t.Setenv("DOOPS_GATEWAY_DATA_DIR", t.TempDir())
	var logs bytes.Buffer
	previousLog := log.Writer()
	log.SetOutput(&logs)
	_, err := OpenGatewayStore("")
	log.SetOutput(previousLog)
	if err == nil {
		t.Fatal("a wrong MySQL credential opened the store")
	}
	if !strings.Contains(err.Error(), "mysql_authentication_failed") {
		t.Errorf("wrong credential error = %q, want mysql_authentication_failed", err)
	}
	for _, leak := range []string{canary, "root:"} {
		if strings.Contains(err.Error(), leak) || strings.Contains(logs.String(), leak) {
			t.Errorf("failure path leaked %q: error=%q log=%q", leak, err, logs.String())
		}
	}
}

func gatewayRuntimeLedgerRows(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query("SELECT version FROM gateway_schema_migrations ORDER BY version")
	if err != nil {
		t.Fatalf("read runtime ledger: %v", err)
	}
	defer rows.Close()
	var versions []string
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			t.Fatalf("scan runtime ledger: %v", err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate runtime ledger: %v", err)
	}
	return versions
}
