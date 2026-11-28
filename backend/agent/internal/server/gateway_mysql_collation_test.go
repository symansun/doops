package server

import (
	"context"
	"database/sql"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

// The MySQL Gateway renders the json_each/json_group_array family as
// JSON_TABLE ... COLUMNS(...). MySQL gives a JSON_TABLE column the collation of
// the *session*, not of the column it is compared with, so a session collation
// that differs from the table collation raises
//
//	Error 1267 (HY000): Illegal mix of collations (utf8mb4_0900_ai_ci,IMPLICIT)
//	and (utf8mb4_general_ci,IMPLICIT) for operation '='
//
// the first time target resource sync reconciles agent observations. These tests
// pin both halves of the fix: the store negotiates the database collation, and a
// database (or a DSN) that would still mismatch fails at startup instead of at
// run time.

// gatewayMySQLCollationTestDSN creates a scratch database with an explicit
// default collation and returns a DSN for it. The explicit default matters:
// the defect only reproduces when the database collation is not the driver's
// built-in utf8mb4_general_ci.
func gatewayMySQLCollationTestDSN(t *testing.T, databaseCollation string) string {
	t.Helper()
	port := strings.TrimSpace(os.Getenv("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT"))
	if port == "" {
		t.Skip("unset DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT to skip this test")
	}
	addr := net.JoinHostPort("127.0.0.1", port)
	adminConfig := mysql.Config{User: "root", Net: "tcp", Addr: addr, AllowNativePasswords: true, Timeout: 5 * time.Second}
	admin, err := sql.Open("mysql", adminConfig.FormatDSN())
	if err != nil {
		t.Fatalf("open MySQL admin connection: %v", err)
	}
	t.Cleanup(func() { admin.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		t.Fatalf("the disposable MySQL 8.4 server at %s is unavailable: %v (unset DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT to skip this test)", addr, err)
	}
	database := "gateway_collation_" + strings.ToLower(randomHex(8))
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE `"+database+"` DEFAULT CHARACTER SET utf8mb4 DEFAULT COLLATE "+databaseCollation); err != nil {
		t.Fatalf("create MySQL collation test database: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = admin.ExecContext(cleanupCtx, "DROP DATABASE `"+database+"`")
	})
	config := mysql.Config{User: "root", Net: "tcp", Addr: addr, DBName: database, AllowNativePasswords: true, Timeout: 5 * time.Second}
	return config.FormatDSN()
}

func openGatewayMySQLCollationTestStore(t *testing.T, dsn string) *GatewayStore {
	t.Helper()
	t.Setenv("DOOPS_GATEWAY_STORE", "mysql")
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", dsn)
	t.Setenv("DOOPS_GATEWAY_DATA_DIR", t.TempDir())
	store, err := openGatewayMySQLStore(context.Background())
	if err != nil {
		t.Fatalf("open MySQL store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// TestGatewayMySQLSessionCollationMatchesDatabase is the end-to-end guard: the
// session the driver negotiated must equal the database default, otherwise the
// JSON_TABLE comparison below is the exact statement that failed on 2026-11-24.
func TestGatewayMySQLSessionCollationMatchesDatabase(t *testing.T) {
	store := openGatewayMySQLCollationTestStore(t, gatewayMySQLCollationTestDSN(t, "utf8mb4_0900_ai_ci"))
	var session, database string
	if err := store.db.QueryRow("SELECT @@collation_connection, @@collation_database").Scan(&session, &database); err != nil {
		t.Fatalf("read session collation: %v", err)
	}
	if database != "utf8mb4_0900_ai_ci" {
		t.Fatalf("test database collation = %q, want utf8mb4_0900_ai_ci", database)
	}
	if session != database {
		t.Fatalf("session collation = %q but the database default is %q; JSON_TABLE comparisons raise Error 1267", session, database)
	}
	var count int
	err := store.db.QueryRow(
		"SELECT COUNT(*) FROM agent_observations WHERE cluster = ? AND instance = ? AND kind IN (SELECT jt.value FROM JSON_TABLE(?, '$[*]' COLUMNS(value VARCHAR(512) PATH '$[0]')) AS jt)",
		"c1", "i1", "[]",
	).Scan(&count)
	if err != nil {
		t.Fatalf("JSON_TABLE comparison under the negotiated session collation: %v", err)
	}
	if count != 0 {
		t.Fatalf("JSON_TABLE comparison returned %d rows for an empty table", count)
	}
}

// TestGatewayMySQLStoreRejectsTableWithForeignCollation proves the startup guard
// is load-bearing: aligning the session is not enough when a table overrides the
// database collation, so the store must refuse to start and name the table.
func TestGatewayMySQLStoreRejectsTableWithForeignCollation(t *testing.T) {
	dsn := gatewayMySQLCollationTestDSN(t, "utf8mb4_0900_ai_ci")
	admin, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open scratch database: %v", err)
	}
	defer admin.Close()
	if _, err := admin.Exec("CREATE TABLE foreign_collation (id VARCHAR(16) NOT NULL) DEFAULT COLLATE utf8mb4_general_ci"); err != nil {
		t.Fatalf("create foreign-collation table: %v", err)
	}
	t.Setenv("DOOPS_GATEWAY_STORE", "mysql")
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", dsn)
	t.Setenv("DOOPS_GATEWAY_DATA_DIR", t.TempDir())
	_, err = openGatewayMySQLStore(context.Background())
	if err == nil {
		t.Fatal("openGatewayMySQLStore accepted a table whose collation differs from the database default")
	}
	for _, want := range []string{"gateway_mysql_collation_mismatch", "foreign_collation=utf8mb4_general_ci"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q; the operator has to see which table breaks the session collation", err, want)
		}
	}
}

// TestGatewayMySQLStoreRejectsDSNCollationOverride keeps the DSN from silently
// re-introducing the mismatch the session alignment just removed.
func TestGatewayMySQLStoreRejectsDSNCollationOverride(t *testing.T) {
	dsn := gatewayMySQLCollationTestDSN(t, "utf8mb4_0900_ai_ci") + "&collation=utf8mb4_general_ci"
	t.Setenv("DOOPS_GATEWAY_STORE", "mysql")
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", dsn)
	t.Setenv("DOOPS_GATEWAY_DATA_DIR", t.TempDir())
	_, err := openGatewayMySQLStore(context.Background())
	if err == nil {
		t.Fatal("openGatewayMySQLStore accepted a DSN collation that differs from the database default")
	}
	for _, want := range []string{"gateway_mysql_collation_mismatch", "utf8mb4_general_ci", "utf8mb4_0900_ai_ci"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// TestGatewayMySQLStoreRejectsColumnWithForeignCollation covers the granularity
// the table check cannot see: MySQL keeps table_collation at the database
// default when a single column is altered, so the mismatch is invisible until a
// comparison on that column raises Error 1267 at run time.
func TestGatewayMySQLStoreRejectsColumnWithForeignCollation(t *testing.T) {
	dsn := gatewayMySQLCollationTestDSN(t, "utf8mb4_0900_ai_ci")
	store := openGatewayMySQLCollationTestStore(t, dsn)
	if _, err := store.db.Exec("ALTER TABLE monitor_runs MODIFY summary TEXT COLLATE utf8mb4_general_ci NOT NULL"); err != nil {
		t.Fatalf("alter one column collation: %v", err)
	}
	// The table collation stays at the database default, so only a column-level
	// guard can refuse this database.
	var tableCollation string
	if err := store.db.QueryRow("SELECT table_collation FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'monitor_runs'").Scan(&tableCollation); err != nil {
		t.Fatalf("read table collation: %v", err)
	}
	if tableCollation != "utf8mb4_0900_ai_ci" {
		t.Fatalf("table collation = %q, want the database default so the table check cannot catch this", tableCollation)
	}
	store.Close()
	t.Setenv("DOOPS_GATEWAY_STORE", "mysql")
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", dsn)
	t.Setenv("DOOPS_GATEWAY_DATA_DIR", t.TempDir())
	_, err := openGatewayMySQLStore(context.Background())
	if err == nil {
		t.Fatal("openGatewayMySQLStore accepted a column whose collation differs from the database default")
	}
	for _, want := range []string{"gateway_mysql_collation_mismatch", "monitor_runs.summary=utf8mb4_general_ci"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q; the operator has to see which column breaks the comparison", err, want)
		}
	}
}

// TestGatewayMySQLSessionCollationAssertionSeesPooledChange proves the
// post-schema assertion is load-bearing: a connection that changed its session
// collation and went back into the pool must be refused, because that is exactly
// what a re-introduced SET NAMES in the schema would do.
func TestGatewayMySQLSessionCollationAssertionSeesPooledChange(t *testing.T) {
	dsn := gatewayMySQLCollationTestDSN(t, "utf8mb4_0900_ai_ci")
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		t.Fatalf("build connector: %v", err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	// One connection, one idle slot: the statement below cannot be hidden by a
	// second connection the assertion might prefer.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("take connection: %v", err)
	}
	if _, err := conn.ExecContext(context.Background(), "SET NAMES utf8mb4 COLLATE utf8mb4_general_ci"); err != nil {
		t.Fatalf("set a foreign session collation: %v", err)
	}
	conn.Close()
	err = gatewayMySQLAssertSessionCollation(context.Background(), db, "utf8mb4_0900_ai_ci")
	if err == nil {
		t.Fatal("the post-schema assertion accepted a pooled session collated utf8mb4_general_ci")
	}
	for _, want := range []string{"gateway_mysql_collation_mismatch", "post-schema session collation", "utf8mb4_general_ci"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// TestGatewayMySQLStoreRejectsDatabaseCollationTheDriverCannotSet pins the
// boundary between the two failure shapes: utf8mb4_general_ci can be set and is
// compared by JSON_TABLE, but utf8mb4_0900_bin is unknown to go-sql-driver, which
// silently negotiates utf8mb4_general_ci instead. A database like that cannot
// host the Gateway schema at all, and the error has to say so.
func TestGatewayMySQLStoreRejectsDatabaseCollationTheDriverCannotSet(t *testing.T) {
	dsn := gatewayMySQLCollationTestDSN(t, "utf8mb4_0900_bin")
	t.Setenv("DOOPS_GATEWAY_STORE", "mysql")
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", dsn)
	t.Setenv("DOOPS_GATEWAY_DATA_DIR", t.TempDir())
	_, err := openGatewayMySQLStore(context.Background())
	if err == nil {
		t.Fatal("openGatewayMySQLStore accepted a database whose default collation the driver cannot set")
	}
	for _, want := range []string{"gateway_mysql_collation_mismatch", "utf8mb4_0900_bin", "falls back to utf8mb4_general_ci", "utf8mb4_0900_ai_ci"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q; the operator needs the cause and the usable default", err, want)
		}
	}
}
