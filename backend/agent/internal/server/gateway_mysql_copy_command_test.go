package server

// INT-49: the cutover gained a command-line entry (doops-gateway store
// mysql-copy). These tests drive the command's own function, not the binary, and
// only against a disposable MySQL 8.4 reached through
// DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT. When the variable is unset the cases skip;
// when it is set and the server is unreachable they fail instead of skipping
// (INT-14: a cutover rehearsal that silently did not run is worse than none).

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

// gatewayMySQLCopyCommandCanary is a deliberately fake payload. The command must
// never echo the DSN, so a DSN carrying this value proves the redaction.
const gatewayMySQLCopyCommandCanary = "canary-int49-dsn-8a4c7e21"

type gatewayMySQLCopyCommandSeed struct {
	UserID     string
	ScheduleID string
	RunID      string
	AuditIDs   []int64
}

func gatewayMySQLCopyCommandPort(t *testing.T) string {
	t.Helper()
	port := strings.TrimSpace(os.Getenv("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT"))
	if port == "" {
		t.Skip("requires a disposable local MySQL 8 (DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT)")
	}
	if n, err := strconv.ParseUint(port, 10, 16); err != nil || n == 0 {
		t.Fatalf("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT=%q is not a valid port", port)
	}
	return port
}

// gatewayMySQLCopyCommandDestination creates a uniquely named, disposable
// database with the collation the Gateway startup check requires.
func gatewayMySQLCopyCommandDestination(t *testing.T, port string) (*sql.DB, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	admin, err := sql.Open("mysql", "root@tcp(127.0.0.1:"+port+")/?parseTime=true")
	if err != nil {
		t.Fatalf("open the MySQL admin connection on port %s: %v", port, err)
	}
	if err := admin.PingContext(ctx); err != nil {
		_ = admin.Close()
		t.Fatalf("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT=%s is set but MySQL is unreachable (fail closed, not skip): %v", port, err)
	}
	var raw [6]byte
	if _, err := rand.Read(raw[:]); err != nil {
		_ = admin.Close()
		t.Fatalf("generate a disposable database name: %v", err)
	}
	name := "doops_int49_copy_" + hex.EncodeToString(raw[:])
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name+" CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
		_ = admin.Close()
		t.Fatalf("create the disposable MySQL database %s: %v", name, err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name)
		_ = admin.Close()
	})
	var collation string
	if err := admin.QueryRowContext(ctx, "SELECT DEFAULT_COLLATION_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME=?", name).Scan(&collation); err != nil {
		t.Fatalf("read the collation of %s: %v", name, err)
	}
	if collation != "utf8mb4_0900_ai_ci" {
		t.Fatalf("disposable database %s collation = %s, want utf8mb4_0900_ai_ci", name, collation)
	}
	cfg := mysql.NewConfig()
	cfg.User, cfg.Net, cfg.Addr = "root", "tcp", net.JoinHostPort("127.0.0.1", port)
	cfg.DBName = name
	cfg.ParseTime = true
	cfg.Collation = collation
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		t.Fatalf("build the MySQL connector for %s: %v", name, err)
	}
	db := sql.OpenDB(connector)
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("connect to the disposable database %s: %v", name, err)
	}
	return db, name
}

// gatewayMySQLCopyCommandPrepareSchema installs the native Gateway schema. The
// self-referencing foreign key stays in place: the copier handles it data-aware
// in the same transaction, so a cutover needs no manual DDL.
func gatewayMySQLCopyCommandPrepareSchema(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	if _, err := installGatewayMySQLSchema(ctx, db); err != nil {
		t.Fatalf("install the native Gateway MySQL schema: %v", err)
	}
}

// gatewayMySQLCopyCommandSeedSource builds a real SQLite store through its own
// API, so the snapshot carries rows written by the code that runs in production.
func gatewayMySQLCopyCommandSeedSource(t *testing.T) (*GatewayStore, gatewayMySQLCopyCommandSeed) {
	t.Helper()
	return gatewayMySQLCopyCommandSeedStore(t, true)
}

// gatewayMySQLCopyCommandFreshSource seeds the store without extending it to the
// frozen 70-table export, so the snapshot has the table set a freshly installed
// Gateway of the previous release actually wrote: the current release creates
// dozens of module tables only in MySQL.
func gatewayMySQLCopyCommandFreshSource(t *testing.T) (*GatewayStore, gatewayMySQLCopyCommandSeed) {
	t.Helper()
	return gatewayMySQLCopyCommandSeedStore(t, false)
}

func gatewayMySQLCopyCommandSeedStore(t *testing.T, extend bool) (*GatewayStore, gatewayMySQLCopyCommandSeed) {
	t.Helper()
	ctx := t.Context()
	t.Setenv("DOOPS_GATEWAY_STORE", "sqlite")
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("k", 32))
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("open the SQLite store: %v", err)
	}
	base := time.Now().UTC().Truncate(time.Millisecond)
	seed := gatewayMySQLCopyCommandSeed{}
	users := make([]string, 0, 2)
	for _, name := range []string{"int49-alice", "int49-bob"} {
		user, err := store.CreateUserWithPassword(CreateUserRequest{Name: name, Password: "cutover-test--" + name})
		if err != nil {
			t.Fatalf("seed user %s: %v", name, err)
		}
		users = append(users, user.ID)
		if err := store.GrantUser(user.ID, ScopeGrant{Cluster: "int49-cluster", Instance: "int49-instance"}); err != nil {
			t.Fatalf("seed the target grant of %s: %v", name, err)
		}
	}
	seed.UserID = users[0]
	for i, status := range []string{"success", "error", "success"} {
		auditID, err := store.StartAudit(AuditEvent{
			UserID:         users[i%len(users)],
			Cluster:        "int49-cluster",
			Instance:       "int49-instance",
			Action:         GatewayAction("monitor.inspect"),
			Session:        "int49-mysql-copy-command",
			CommandSummary: fmt.Sprintf("int49 copy command audit %d", i+1),
			Status:         "running",
			StartedAt:      base.Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Fatalf("seed audit event %d: %v", i+1, err)
		}
		finish := AuditFinish{Status: status, BytesIn: 10, BytesOut: 20, Model: "int49-model", InputTokens: 1, OutputTokens: 2, TotalTokens: 3}
		if status == "error" {
			finish.Error = "int49 copy command error"
		}
		if err := store.FinishAudit(auditID, finish); err != nil {
			t.Fatalf("finish audit event %d: %v", i+1, err)
		}
		seed.AuditIDs = append(seed.AuditIDs, auditID)
	}
	schedule, err := store.SaveMonitorSchedule(ctx, monitorScheduleRecord{
		ID:              "int49-schedule",
		Name:            "INT-49 copy command schedule",
		Description:     "seeded through the store API",
		Enabled:         true,
		ScopeMode:       "targets",
		Targets:         []monitorTargetRef{{Cluster: "int49-cluster", Instance: "int49-instance"}},
		Tags:            []string{"int49"},
		Prompt:          "inspect the INT-49 target",
		Command:         "doops inspect",
		IntervalSeconds: 300,
		NextRunAtMs:     base.UnixMilli() + 300000,
		CreatedBy:       seed.UserID,
		CreatedAt:       formatTime(base),
		UpdatedAt:       formatTime(base),
	}, false)
	if err != nil {
		t.Fatalf("seed the monitor schedule: %v", err)
	}
	seed.ScheduleID = schedule.ID
	run := monitorRunRecord{
		ID:             "int49-run",
		ScheduleID:     schedule.ID,
		ScheduleName:   schedule.Name,
		TriggerKind:    "manual",
		TriggeredBy:    seed.UserID,
		Status:         "warning",
		Skills:         []string{"int49-skill"},
		Prompt:         "inspect the INT-49 target",
		Command:        "doops inspect",
		TotalTargets:   1,
		TotalAssets:    1,
		WarningChecks:  1,
		StartedAtMs:    base.UnixMilli(),
		FinishedAtMs:   base.UnixMilli() + 1500,
		DurationMs:     1500,
		Summary:        "INT-49 copy command warning",
		MetricsWritten: 1,
	}
	if err := store.CreateMonitorRun(ctx, run); err != nil {
		t.Fatalf("seed the monitor run: %v", err)
	}
	seed.RunID = run.ID
	item := monitorRunItemRecord{
		RunID:           run.ID,
		Cluster:         "int49-cluster",
		Instance:        "int49-instance",
		AssetID:         "int49-asset-1",
		AssetName:       "INT-49 asset",
		AssetKind:       "machine",
		Strategy:        "skill:int49-skill",
		CollectorMethod: "skill",
		DispatchMode:    "skill",
		Status:          "warning",
		Reason:          "seeded",
		StartedAtMs:     base.UnixMilli(),
		FinishedAtMs:    base.UnixMilli() + 1200,
		DurationMs:      1200,
		Evidence:        map[string]any{"skill_slug": "int49-skill", "exec_mode": "skill"},
		ArtifactFormat:  "json",
		ArtifactJSON:    json.RawMessage(`{"status":"warning","summary":"int49"}`),
		RawOutput:       "int49 raw output",
	}
	if _, err := store.PersistMonitorRunResults(ctx, run, []monitorRunItemRecord{item}, nil, nil); err != nil {
		t.Fatalf("seed the monitor run item: %v", err)
	}
	if extend {
		gatewayMySQLCopyCommandExtendLiveStore(t, store.db)
	} else {
		gatewayMySQLCopyCommandDropAutoMigratedAssetTables(t, store.db)
	}
	return store, seed
}

// gatewayMySQLCopyCommandDropAutoMigratedAssetTables strips the auto-migrated
// asset/inventory/resource-sync tables from a fresh SQLite store so the
// INT-53/INT-57 cutover tests keep exercising a legacy pre-auto-migration
// SQLite snapshot that lacks those tables (native_schema_only).
func gatewayMySQLCopyCommandDropAutoMigratedAssetTables(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatalf("disable foreign_keys before simulating legacy snapshot: %v", err)
	}
	for _, line := range strings.Split(gatewaySQLiteAssetSchemaSQL, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "CREATE TABLE IF NOT EXISTS ") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 6 {
			continue
		}
		table := strings.SplitN(fields[5], "(", 2)[0]
		if _, err := db.Exec("DROP TABLE IF EXISTS " + table); err != nil {
			t.Fatalf("drop auto-migrated table %s for legacy snapshot simulation: %v", table, err)
		}
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatalf("re-enable foreign_keys after simulating legacy snapshot: %v", err)
	}
}

// gatewayMySQLCopyCommandExtendLiveStore makes the seeded store look like a
// Gateway database written by the release the cutover starts from: that schema
// still carries the operator tables the current release creates only in MySQL.
// The frozen export is the reviewed list, and the legacy columns the copier
// deliberately skips are added with their DDL defaults.
func gatewayMySQLCopyCommandExtendLiveStore(t *testing.T, db *sql.DB) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "gateway-sqlite-schema.sql"))
	if err != nil {
		t.Fatalf("read the frozen SQLite export: %v", err)
	}
	for _, statement := range strings.Split(string(data), ";") {
		statement = strings.TrimSpace(statement)
		if !strings.HasPrefix(strings.ToUpper(statement), "CREATE TABLE") {
			continue
		}
		if _, err := db.Exec(statement); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("extend the live store with a frozen table: %v", err)
		}
	}
	for _, statement := range []string{
		"ALTER TABLE audit_events ADD COLUMN project TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE audit_events ADD COLUMN environment TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE audit_events ADD COLUMN template TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE audit_events ADD COLUMN namespace TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE users ADD COLUMN account_type TEXT NOT NULL DEFAULT 'person'",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("add a legacy column to the live store: %v", err)
		}
	}
	want, _, err := gatewayMySQLExpectedObjects()
	if err != nil {
		t.Fatalf("read the native MySQL schema inventory: %v", err)
	}
	var have int
	if err := db.QueryRow("SELECT COUNT(*) FROM main.pragma_table_list WHERE schema = 'main' AND type = 'table' AND name NOT LIKE 'sqlite_%'").Scan(&have); err != nil {
		t.Fatalf("count the extended live store tables: %v", err)
	}
	if have != len(want) {
		t.Fatalf("the extended live store has %d tables, want %d (the frozen export plus the current release)", have, len(want))
	}
}

// gatewayMySQLCopyCommandSnapshot closes the seeded store and copies its file to
// a read-only snapshot, the artefact gateway-sqlite-snapshot.py produces.
func gatewayMySQLCopyCommandSnapshot(t *testing.T, store *GatewayStore) string {
	t.Helper()
	path := store.dbPath
	if err := store.Close(); err != nil {
		t.Fatalf("close the seeded store before taking the snapshot: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the seeded SQLite file: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("the seeded SQLite file is empty")
	}
	snapshot := filepath.Join(t.TempDir(), "gateway-snapshot.db")
	if err := os.WriteFile(snapshot, data, 0o444); err != nil {
		t.Fatalf("write the read-only snapshot: %v", err)
	}
	return snapshot
}

func gatewayMySQLCopyCommandRun(t *testing.T, opts GatewayMySQLCopyOptions) (int, error, string) {
	t.Helper()
	var out strings.Builder
	code, err := RunGatewayMySQLCopy(t.Context(), opts, &out)
	return code, err, out.String()
}

func gatewayMySQLCopyCommandTables(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query("SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_TYPE='BASE TABLE' ORDER BY TABLE_NAME")
	if err != nil {
		t.Fatalf("list the destination tables: %v", err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan the destination tables: %v", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list the destination tables: %v", err)
	}
	return tables
}

func gatewayMySQLCopyCommandTableRows(t *testing.T, db *sql.DB) map[string]int64 {
	t.Helper()
	counts := map[string]int64{}
	for _, table := range gatewayMySQLCopyCommandTables(t, db) {
		var n int64
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
			t.Fatalf("count the destination table %s: %v", table, err)
		}
		counts[table] = n
	}
	return counts
}

// gatewayMySQLCopyCommandReported parses the table=<name> rows=<n> lines a real
// copy prints.
func gatewayMySQLCopyCommandReported(t *testing.T, out string) map[string]int64 {
	t.Helper()
	rows := map[string]int64{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.HasPrefix(fields[0], "table=") {
			continue
		}
		for _, field := range fields[1:] {
			if !strings.HasPrefix(field, "rows=") {
				continue
			}
			n, err := strconv.ParseInt(strings.TrimPrefix(field, "rows="), 10, 64)
			if err != nil {
				t.Fatalf("unparsable row count %q in the copy summary", field)
			}
			rows[strings.TrimPrefix(fields[0], "table=")] = n
		}
	}
	return rows
}

// gatewayMySQLCopyCommandDestinationOnly parses the per-table destination-only
// evidence from either path: the preview prints copy=no lines, the real run
// prints native lines and skipped_table lines.
func gatewayMySQLCopyCommandDestinationOnly(t *testing.T, out string) map[string]string {
	t.Helper()
	buckets := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		var name string
		switch {
		case strings.HasPrefix(fields[0], "table=") && strings.Contains(line, "source=absent"):
			name = strings.TrimPrefix(fields[0], "table=")
		case strings.HasPrefix(fields[0], "skipped_table="):
			name = strings.TrimPrefix(fields[0], "skipped_table=")
		default:
			continue
		}
		for _, field := range fields[1:] {
			if strings.HasPrefix(field, "reason=") {
				buckets[name] = strings.TrimPrefix(field, "reason=")
				break
			}
		}
	}
	return buckets
}

func gatewayMySQLCopyCommandSourceCounts(t *testing.T, snapshot string) map[string]int64 {
	t.Helper()
	db, err := gatewayMySQLCopyOpenSource(snapshot)
	if err != nil {
		t.Fatalf("open the snapshot read-only for the comparison: %v", err)
	}
	defer db.Close()
	rows, err := db.Query("SELECT name FROM pragma_table_list WHERE schema='main' AND type='table' ORDER BY name")
	if err != nil {
		t.Fatalf("list the snapshot tables: %v", err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatalf("scan the snapshot tables: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("list the snapshot tables: %v", err)
	}
	// The command opens the snapshot with a single connection, so the list must
	// be drained before the per-table counts run or they wait on themselves.
	if err := rows.Close(); err != nil {
		t.Fatalf("close the snapshot table list: %v", err)
	}
	counts := map[string]int64{}
	for _, name := range names {
		var n int64
		if err := db.QueryRow("SELECT COUNT(*) FROM main." + name).Scan(&n); err != nil {
			t.Fatalf("count the snapshot table %s: %v", name, err)
		}
		counts[name] = n
	}
	return counts
}

// TestGatewayMySQLCopyCommandDryRunWritesNothing proves the review mode is
// read-only: the table list and every row count must be identical before and
// after, and the snapshot itself must be untouched.

// gatewayMySQLCopyCommandNativeTables counts the tables the native MySQL schema
// installs. The cutover cases assert the destination table count against it
// instead of a literal: a new native table (INT-82a added execution_records)
// must not silently turn these cases red, and a parser regression must not make
// them vacuous.
func gatewayMySQLCopyCommandNativeTables(t *testing.T) int {
	t.Helper()
	statements, err := gatewayMySQLStatements(gatewayMySQLSchemaSQL)
	if err != nil {
		t.Fatalf("parse the native MySQL schema: %v", err)
	}
	native := 0
	for _, statement := range statements {
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(statement)), "CREATE TABLE") {
			native++
		}
	}
	if native < 50 {
		t.Fatalf("the native MySQL schema parsed into %d tables; the statement parser is likely broken", native)
	}
	return native
}

func TestGatewayMySQLCopyCommandDryRunWritesNothing(t *testing.T) {
	port := gatewayMySQLCopyCommandPort(t)
	store, _ := gatewayMySQLCopyCommandSeedSource(t)
	snapshot := gatewayMySQLCopyCommandSnapshot(t, store)
	before, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatalf("read the snapshot: %v", err)
	}
	db, name := gatewayMySQLCopyCommandDestination(t, port)
	gatewayMySQLCopyCommandPrepareSchema(t, t.Context(), db)
	// The ledger must not exist yet and no table may hold a row.
	native := gatewayMySQLCopyCommandNativeTables(t)
	if counts := gatewayMySQLCopyCommandTableRows(t, db); len(counts) != native {
		t.Fatalf("prepared destination holds %d tables, want the %d native tables", len(counts), native)
	}
	for table, n := range gatewayMySQLCopyCommandTableRows(t, db) {
		if n != 0 {
			t.Fatalf("prepared destination table %s already holds %d rows", table, n)
		}
	}
	tablesBefore := gatewayMySQLCopyCommandTables(t, db)
	rowsBefore := gatewayMySQLCopyCommandTableRows(t, db)
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", "root@tcp(127.0.0.1:"+port+")/"+name+"?parseTime=true")

	code, err, out := gatewayMySQLCopyCommandRun(t, GatewayMySQLCopyOptions{Source: snapshot, ConfirmDatabase: name, DryRun: true})
	if code != GatewayMySQLCopyExitOK || err != nil {
		t.Fatalf("dry-run exit code = %d, err = %v, want 0 and no error; output:\n%s", code, err, out)
	}
	for _, want := range []string{"table=users source_rows=2", "table=grants source_rows=2", "table=audit_events source_rows=3", "table=monitor_runs source_rows=1", "target=empty copy=yes", "plan: tables=", "dry-run complete"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run plan is missing %q; output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "root@tcp") || strings.Contains(out, "parseTime=true") {
		t.Fatalf("the dry-run output echoed the DSN:\n%s", out)
	}

	if got := gatewayMySQLCopyCommandTables(t, db); !reflect.DeepEqual(got, tablesBefore) {
		t.Fatalf("dry-run changed the destination table list: %v, want %v", got, tablesBefore)
	}
	if got := gatewayMySQLCopyCommandTableRows(t, db); !reflect.DeepEqual(got, rowsBefore) {
		t.Fatalf("dry-run changed the destination row counts: %v, want %v", got, rowsBefore)
	}
	for table, n := range gatewayMySQLCopyCommandTableRows(t, db) {
		if n != 0 {
			t.Fatalf("dry-run wrote %d rows into %s; a dry run must write nothing", n, table)
		}
	}
	after, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatalf("read the snapshot after the dry-run: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("the dry-run modified the snapshot file")
	}
}

// TestGatewayMySQLCopyCommandRejectsUnconfirmedDatabase proves the database name
// gate fires before any connection is made: a typo must never reach the server.
func TestGatewayMySQLCopyCommandRejectsUnconfirmedDatabase(t *testing.T) {
	port := gatewayMySQLCopyCommandPort(t)
	store, _ := gatewayMySQLCopyCommandSeedSource(t)
	snapshot := gatewayMySQLCopyCommandSnapshot(t, store)
	db, name := gatewayMySQLCopyCommandDestination(t, port)
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", "root:"+gatewayMySQLCopyCommandCanary+"@tcp(127.0.0.1:"+port+")/"+name+"?parseTime=true")

	code, err, out := gatewayMySQLCopyCommandRun(t, GatewayMySQLCopyOptions{Source: snapshot, ConfirmDatabase: name + "_typo"})
	if code != GatewayMySQLCopyExitUsage || err == nil {
		t.Fatalf("mismatched --confirm-database exit code = %d, err = %v, want 2 and an error", code, err)
	}
	for _, want := range []string{"mysql_copy_confirm_database_mismatch", name + "_typo", name} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the mismatch error %q is missing %q", err, want)
		}
	}
	if strings.Contains(err.Error(), gatewayMySQLCopyCommandCanary) || strings.Contains(out, gatewayMySQLCopyCommandCanary) {
		t.Fatalf("the mismatch error leaked the DSN canary: %v", err)
	}
	if tables := gatewayMySQLCopyCommandTables(t, db); len(tables) != 0 {
		t.Fatalf("the refused command created %d tables in %s", len(tables), name)
	}

	// The same database with the confirmed name must pass the gate and be rejected
	// by the next precondition instead, which proves the gate - not a broken DSN -
	// produced the failure above. A real DSN is required here: the canary above is
	// not a real password, so connecting with it can only fail authentication.
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", "root@tcp(127.0.0.1:"+port+")/"+name+"?parseTime=true")
	// The empty database is a legitimate cutover target: the command installs the
	// native schema itself and copies. Reaching exit 0 proves the gate above, not a
	// broken DSN, produced the earlier failure.
	code, err, out = gatewayMySQLCopyCommandRun(t, GatewayMySQLCopyOptions{Source: snapshot, ConfirmDatabase: name})
	if code != GatewayMySQLCopyExitOK || err != nil {
		t.Fatalf("confirmed database exit code = %d, err = %v, want 0; output:\n%s", code, err, out)
	}
	if !strings.Contains(out, "mysql-copy complete:") {
		t.Fatalf("the confirmed copy did not complete; output:\n%s", out)
	}
}

// TestGatewayMySQLCopyCommandSnapshotIsReadOnly shows the source is opened with
// mode=ro and query_only, and that the file itself cannot be written.
func TestGatewayMySQLCopyCommandSnapshotIsReadOnly(t *testing.T) {
	store, _ := gatewayMySQLCopyCommandSeedSource(t)
	snapshot := gatewayMySQLCopyCommandSnapshot(t, store)
	info, err := os.Stat(snapshot)
	if err != nil {
		t.Fatalf("stat the snapshot: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o444 {
		t.Fatalf("snapshot mode = %o, want 0444", perm)
	}
	db, err := gatewayMySQLCopyOpenSource(snapshot)
	if err != nil {
		t.Fatalf("open the snapshot through the command's read-only opener: %v", err)
	}
	defer db.Close()
	var queryOnly int
	if err := db.QueryRow("PRAGMA query_only").Scan(&queryOnly); err != nil || queryOnly != 1 {
		t.Fatalf("PRAGMA query_only = %d (err %v), want 1", queryOnly, err)
	}
	if _, err := db.Exec("CREATE TABLE zz_int49_write_probe (id INTEGER)"); err == nil {
		t.Fatalf("a write through the read-only opener succeeded")
	}
	if _, err := db.Exec("INSERT INTO users (id) VALUES ('int49-write-probe')"); err == nil {
		t.Fatalf("an INSERT through the read-only opener succeeded")
	}
	if os.Geteuid() != 0 {
		if err := os.WriteFile(snapshot, []byte("int49 rewrite"), 0o644); err == nil {
			t.Fatalf("the 0444 snapshot was writable on disk")
		}
	}
}

// TestGatewayMySQLCopyCommandCopiesPopulatedStore is the real delivery: a store
// seeded through its own API is snapshotted, copied by the command, and compared
// row by row against the snapshot.
func TestGatewayMySQLCopyCommandCopiesPopulatedStore(t *testing.T) {
	port := gatewayMySQLCopyCommandPort(t)
	store, seed := gatewayMySQLCopyCommandSeedSource(t)
	snapshot := gatewayMySQLCopyCommandSnapshot(t, store)
	sourceCounts := gatewayMySQLCopyCommandSourceCounts(t, snapshot)
	for table, want := range map[string]int64{"users": 2, "grants": 2, "audit_events": 3, "monitor_schedules": 1, "monitor_runs": 1, "monitor_run_items": 1} {
		if sourceCounts[table] != want {
			t.Fatalf("snapshot %s = %d rows, want %d (the seed did not reach the snapshot)", table, sourceCounts[table], want)
		}
	}
	db, name := gatewayMySQLCopyCommandDestination(t, port)
	gatewayMySQLCopyCommandPrepareSchema(t, t.Context(), db)
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", "root@tcp(127.0.0.1:"+port+")/"+name+"?parseTime=true")

	code, err, out := gatewayMySQLCopyCommandRun(t, GatewayMySQLCopyOptions{Source: snapshot, ConfirmDatabase: name})
	if code != GatewayMySQLCopyExitOK || err != nil {
		t.Fatalf("copy exit code = %d, err = %v, want 0 and no error; output:\n%s", code, err, out)
	}
	if !strings.Contains(out, "verified=yes") || !strings.Contains(out, "mysql-copy complete:") {
		t.Fatalf("the summary is missing its verification line; output:\n%s", out)
	}
	reported := gatewayMySQLCopyCommandReported(t, out)
	if len(reported) == 0 {
		t.Fatalf("the summary printed no per-table row counts; output:\n%s", out)
	}
	for table, want := range sourceCounts {
		if strings.HasPrefix(table, "sqlite_") {
			continue // SQLite's own bookkeeping tables are never copied.
		}
		got, ok := reported[table]
		if !ok {
			t.Fatalf("the summary does not mention %s", table)
		}
		if got != want {
			t.Fatalf("%s: summary says %d rows, the snapshot holds %d", table, got, want)
		}
		var destination int64
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&destination); err != nil {
			t.Fatalf("%s: count the copied rows: %v", table, err)
		}
		if destination != want {
			t.Fatalf("%s: destination holds %d rows, want %d", table, destination, want)
		}
	}
	for _, table := range []string{"users", "grants", "audit_events", "monitor_schedules", "monitor_runs", "monitor_run_items"} {
		if reported[table] == 0 {
			t.Fatalf("%s was reported empty after a real copy", table)
		}
	}

	// Key columns, not only counts: identity and status values must survive.
	var userName string
	if err := db.QueryRow("SELECT name FROM users WHERE id=?", seed.UserID).Scan(&userName); err != nil {
		t.Fatalf("read the copied user: %v", err)
	}
	if userName != "int49-alice" {
		t.Fatalf("copied user name = %q, want int49-alice", userName)
	}
	var runStatus, runSchedule string
	if err := db.QueryRow("SELECT status, schedule_id FROM monitor_runs WHERE id=?", seed.RunID).Scan(&runStatus, &runSchedule); err != nil {
		t.Fatalf("read the copied monitor run: %v", err)
	}
	if runStatus != "warning" || runSchedule != seed.ScheduleID {
		t.Fatalf("copied monitor_runs = (%q, %q), want (warning, %q)", runStatus, runSchedule, seed.ScheduleID)
	}
	var successes, failures int
	if err := db.QueryRow("SELECT SUM(status='success'), SUM(status='error') FROM audit_events").Scan(&successes, &failures); err != nil {
		t.Fatalf("read the copied audit statuses: %v", err)
	}
	if successes != 2 || failures != 1 {
		t.Fatalf("copied audit_events statuses = (%d success, %d error), want (2, 1)", successes, failures)
	}
	var ledgerRows int
	var version string
	if err := db.QueryRow("SELECT COUNT(*), COALESCE(MIN(version), '') FROM gateway_schema_migrations").Scan(&ledgerRows, &version); err != nil {
		t.Fatalf("read the schema ledger: %v", err)
	}
	if ledgerRows != 1 || version != gatewaySchemaBaselineVersion {
		t.Fatalf("gateway_schema_migrations = %d row(s) version %q, want exactly one %q", ledgerRows, version, gatewaySchemaBaselineVersion)
	}
	if info, err := os.Stat(snapshot); err != nil || info.Mode().Perm() != 0o444 {
		t.Fatalf("the snapshot changed during the copy: mode %v, err %v", info.Mode().Perm(), err)
	}
}

// TestGatewayMySQLCopyCommandRefusesOccupiedDestination proves the command never
// overwrites, merges or clears an existing row.
func TestGatewayMySQLCopyCommandRefusesOccupiedDestination(t *testing.T) {
	port := gatewayMySQLCopyCommandPort(t)
	store, _ := gatewayMySQLCopyCommandSeedSource(t)
	snapshot := gatewayMySQLCopyCommandSnapshot(t, store)
	db, name := gatewayMySQLCopyCommandDestination(t, port)
	gatewayMySQLCopyCommandPrepareSchema(t, t.Context(), db)
	if _, err := db.Exec("INSERT INTO gateway_schema_migrations (version, applied_at) VALUES (?, ?)", gatewaySchemaBaselineVersion, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("make the destination non-empty: %v", err)
	}
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", "root@tcp(127.0.0.1:"+port+")/"+name+"?parseTime=true")

	code, err, _ := gatewayMySQLCopyCommandRun(t, GatewayMySQLCopyOptions{Source: snapshot, ConfirmDatabase: name})
	if code != GatewayMySQLCopyExitUsage || err == nil || !strings.Contains(err.Error(), "mysql_copy_destination_not_empty") {
		t.Fatalf("occupied destination exit code = %d, err = %v, want 2 and mysql_copy_destination_not_empty", code, err)
	}
	if !strings.Contains(err.Error(), "gateway_schema_migrations") {
		t.Fatalf("the refusal does not name the non-empty table: %v", err)
	}
	var ledger, users int64
	if err := db.QueryRow("SELECT COUNT(*) FROM gateway_schema_migrations").Scan(&ledger); err != nil {
		t.Fatalf("count the preserved ledger rows: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM users").Scan(&users); err != nil {
		t.Fatalf("count the untouched users rows: %v", err)
	}
	if ledger != 1 || users != 0 {
		t.Fatalf("the refused copy changed the destination: ledger=%d users=%d, want 1 and 0", ledger, users)
	}
}

// TestGatewayMySQLCopyCommandRefusesUnpreparedDestination covers a database that
// has tables but no native Gateway schema and no baseline row.
func TestGatewayMySQLCopyCommandRefusesUnpreparedDestination(t *testing.T) {
	port := gatewayMySQLCopyCommandPort(t)
	store, _ := gatewayMySQLCopyCommandSeedSource(t)
	snapshot := gatewayMySQLCopyCommandSnapshot(t, store)
	db, name := gatewayMySQLCopyCommandDestination(t, port)
	if _, err := db.Exec("CREATE TABLE zz_int49_foreign (id INT PRIMARY KEY) ENGINE=InnoDB"); err != nil {
		t.Fatalf("create the foreign table: %v", err)
	}
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", "root@tcp(127.0.0.1:"+port+")/"+name+"?parseTime=true")

	code, err, _ := gatewayMySQLCopyCommandRun(t, GatewayMySQLCopyOptions{Source: snapshot, ConfirmDatabase: name})
	if code != GatewayMySQLCopyExitUsage || err == nil || !strings.Contains(err.Error(), "mysql_copy_destination_schema_missing") {
		t.Fatalf("unprepared destination exit code = %d, err = %v, want 2 and mysql_copy_destination_schema_missing", code, err)
	}
	if !strings.Contains(err.Error(), "DOOPS_GATEWAY_STORE=mysql") {
		t.Fatalf("the refusal does not tell the operator to start the Gateway once on mysql: %v", err)
	}
	if tables := gatewayMySQLCopyCommandTables(t, db); len(tables) != 1 || tables[0] != "zz_int49_foreign" {
		t.Fatalf("the refused copy changed the destination tables: %v", tables)
	}
}

// TestGatewayMySQLCopyCommandDryRunOnEmptyDestinationPredictsTheCutover keeps
// the dry run honest about an empty database: it projects the schema install and
// the copy without creating a table, and reports the code the real run would use.
func TestGatewayMySQLCopyCommandDryRunOnEmptyDestinationPredictsTheCutover(t *testing.T) {
	port := gatewayMySQLCopyCommandPort(t)
	store, _ := gatewayMySQLCopyCommandSeedSource(t)
	snapshot := gatewayMySQLCopyCommandSnapshot(t, store)
	db, name := gatewayMySQLCopyCommandDestination(t, port)
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", "root@tcp(127.0.0.1:"+port+")/"+name+"?parseTime=true")

	code, err, out := gatewayMySQLCopyCommandRun(t, GatewayMySQLCopyOptions{Source: snapshot, ConfirmDatabase: name, DryRun: true})
	if code != GatewayMySQLCopyExitOK || err != nil {
		t.Fatalf("empty dry-run exit code = %d, err = %v, want 0: the real run installs the schema and copies, so the projection accepts the same database", code, err)
	}
	if !strings.Contains(out, "plan(preview):") || !strings.Contains(out, "table=users source_rows=2") {
		t.Fatalf("the empty dry-run did not print the preview plan; output:\n%s", out)
	}
	if !strings.Contains(out, "the real run installs the native Gateway MySQL schema here first") {
		t.Fatalf("the empty dry-run did not project the schema install; output:\n%s", out)
	}
	if tables := gatewayMySQLCopyCommandTables(t, db); len(tables) != 0 {
		sort.Strings(tables)
		t.Fatalf("the dry-run installed %d tables: %v", len(tables), tables)
	}
}

// TestGatewayMySQLCopyPredictEmptyDestination pins the projection of the empty
// destination: the real run installs the schema and accepts it, including the
// self-reference the copier handles data-aware.
func TestGatewayMySQLCopyPredictEmptyDestination(t *testing.T) {
	var buf strings.Builder
	if code, err := gatewayMySQLCopyPredictEmptyDestination(&buf, "doops_gateway", 70); code != GatewayMySQLCopyExitOK || err != nil {
		t.Fatalf("code = %d, err = %v, want 0", code, err)
	}
	if !strings.Contains(buf.String(), "the real run would install the schema and copy") || !strings.Contains(buf.String(), "(70 tables, version=") {
		t.Fatalf("the acceptance line is missing or does not state the schema projection; output:\n%s", buf.String())
	}
}

// TestGatewayMySQLSchemaSelfReferencesMatchInstalledSchema keeps the static scan
// honest: it decides the dry-run verdict, so it must agree with what the
// installer actually creates. A schema change that adds or drops a
// self-reference has to update this scan in the same change.
func TestGatewayMySQLSchemaSelfReferencesMatchInstalledSchema(t *testing.T) {
	port := gatewayMySQLCopyCommandPort(t)
	db, _ := gatewayMySQLCopyCommandDestination(t, port)
	if _, err := installGatewayMySQLSchema(t.Context(), db); err != nil {
		t.Fatalf("install the native schema: %v", err)
	}
	rows, err := db.QueryContext(t.Context(), "SELECT DISTINCT TABLE_NAME FROM information_schema.KEY_COLUMN_USAGE WHERE TABLE_SCHEMA = DATABASE() AND REFERENCED_TABLE_NAME = TABLE_NAME")
	if err != nil {
		t.Fatalf("read the installed self-references: %v", err)
	}
	defer rows.Close()
	var installed []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan the installed self-reference: %v", err)
		}
		installed = append(installed, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate the installed self-references: %v", err)
	}
	sort.Strings(installed)
	static, err := gatewayMySQLSchemaSelfReferences()
	if err != nil {
		t.Fatalf("scan the embedded schema: %v", err)
	}
	if !reflect.DeepEqual(static, installed) {
		t.Fatalf("the static scan reports self-references %v but the installed schema has %v", static, installed)
	}
}

// TestGatewayMySQLCopyCommandSelfReferenceIsCopiedDataAware proves the cutover
// keeps the inventory_machines self-reference and still lands parent and child
// in one transaction: the copier inserts the child with a NULL host, then
// backfills it before the readback.
func TestGatewayMySQLCopyCommandSelfReferenceIsCopiedDataAware(t *testing.T) {
	port := gatewayMySQLCopyCommandPort(t)
	store, _ := gatewayMySQLCopyCommandSeedSource(t)
	if _, err := store.db.Exec("INSERT INTO inventory_machines (asset_id, name, kind, address, host_asset_id, cluster, instance, revision, active) VALUES " +
		"('int50-host', 'int50 host', 'physical', '10.50.0.1', NULL, '', '', 1, 1), " +
		"('int50-vm', 'int50 vm', 'vm', '10.50.0.2', 'int50-host', '', '', 1, 1)"); err != nil {
		t.Fatalf("seed the self-referencing inventory: %v", err)
	}
	snapshot := gatewayMySQLCopyCommandSnapshot(t, store)
	db, name := gatewayMySQLCopyCommandDestination(t, port)
	if _, err := installGatewayMySQLSchema(t.Context(), db); err != nil {
		t.Fatalf("install the native schema with its self-reference: %v", err)
	}
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", "root@tcp(127.0.0.1:"+port+")/"+name+"?parseTime=true")

	code, err, out := gatewayMySQLCopyCommandRun(t, GatewayMySQLCopyOptions{Source: snapshot, ConfirmDatabase: name})
	if code != GatewayMySQLCopyExitOK || err != nil {
		t.Fatalf("self-referencing copy exit code = %d, err = %v, want 0; output:\n%s", code, err, out)
	}
	var constraints int
	if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.KEY_COLUMN_USAGE WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='inventory_machines' AND COLUMN_NAME='host_asset_id' AND REFERENCED_TABLE_NAME='inventory_machines'").Scan(&constraints); err != nil {
		t.Fatalf("read the destination self-reference: %v", err)
	}
	if constraints != 1 {
		t.Fatalf("the destination self-reference count = %d, want 1: the cutover must keep the constraint", constraints)
	}
	var host, kind string
	if err := db.QueryRow("SELECT host_asset_id, kind FROM inventory_machines WHERE asset_id='int50-vm'").Scan(&host, &kind); err != nil {
		t.Fatalf("read the copied child row: %v", err)
	}
	if host != "int50-host" || kind != "vm" {
		t.Fatalf("the copied child row = (%s, %s), want (int50-host, vm)", host, kind)
	}
	var rows int64
	if err := db.QueryRow("SELECT COUNT(*) FROM gateway_schema_migrations").Scan(&rows); err != nil {
		t.Fatalf("count the ledger: %v", err)
	}
	if rows != 1 {
		t.Fatalf("ledger rows = %d, want exactly 1", rows)
	}
}

// TestGatewayMySQLCopyCommandMovesDataDirs moves the filesystem half with the
// database: artifacts and Skill packages land under the target data dir, and the
// rows that index them still resolve.
func TestGatewayMySQLCopyCommandMovesDataDirs(t *testing.T) {
	port := gatewayMySQLCopyCommandPort(t)
	store, seed := gatewayMySQLCopyCommandSeedSource(t)
	sourceData := t.TempDir()
	artifactRel := "2026-11-24/int50-run/item_1"
	artifactDir := filepath.Join(sourceData, "artifacts", filepath.FromSlash(artifactRel))
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("create the source artifact directory: %v", err)
	}
	body := []byte("{\"int50\":\"artifact\"}\n")
	if err := os.WriteFile(filepath.Join(artifactDir, monitorArtifactRawName), body, 0o644); err != nil {
		t.Fatalf("write the source artifact: %v", err)
	}
	size, sha, err := gatewayCopyArtifactDigest(artifactDir)
	if err != nil {
		t.Fatalf("digest the source artifact: %v", err)
	}
	if _, err := store.db.Exec("UPDATE monitor_run_items SET artifact_dir=?, artifact_bytes=?, artifact_sha256=? WHERE run_id=?", artifactRel, size, sha, seed.RunID); err != nil {
		t.Fatalf("index the source artifact: %v", err)
	}
	// The builtin catalog indexes Skill packages relative to the data dir; a real
	// deployment materializes them there, so the test does too.
	skillRows, err := store.db.Query("SELECT slug, storage_dir FROM platform_skills WHERE storage_dir != ''")
	if err != nil {
		t.Fatalf("read the seeded skill index: %v", err)
	}
	type skillIndex struct{ slug, dir string }
	var indexed []skillIndex
	for skillRows.Next() {
		var item skillIndex
		if err := skillRows.Scan(&item.slug, &item.dir); err != nil {
			skillRows.Close()
			t.Fatalf("scan the seeded skill index: %v", err)
		}
		indexed = append(indexed, item)
	}
	skillRows.Close()
	if err := skillRows.Err(); err != nil {
		t.Fatalf("iterate the seeded skill index: %v", err)
	}
	if len(indexed) == 0 {
		t.Fatalf("the seeded store indexes no Skill package")
	}
	for _, item := range indexed {
		dir := filepath.Join(sourceData, filepath.FromSlash(item.dir))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create the source skill directory for %s: %v", item.slug, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("int50 skill\n"), 0o644); err != nil {
			t.Fatalf("write the source skill %s: %v", item.slug, err)
		}
	}
	slug := indexed[0].slug
	skillRel := "skills/int50-skill"
	skillDir := filepath.Join(sourceData, filepath.FromSlash(skillRel))
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("create the source skill directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("int50 skill\n"), 0o644); err != nil {
		t.Fatalf("write the source skill: %v", err)
	}
	if _, err := store.db.Exec("UPDATE platform_skills SET storage_dir=? WHERE slug=?", skillRel, slug); err != nil {
		t.Fatalf("index the source skill: %v", err)
	}

	targetData := t.TempDir()
	snapshot := gatewayMySQLCopyCommandSnapshot(t, store)
	db, name := gatewayMySQLCopyCommandDestination(t, port)
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", "root@tcp(127.0.0.1:"+port+")/"+name+"?parseTime=true")

	code, err, out := gatewayMySQLCopyCommandRun(t, GatewayMySQLCopyOptions{Source: snapshot, ConfirmDatabase: name, SourceDataDir: sourceData, TargetDataDir: targetData})
	if code != GatewayMySQLCopyExitOK || err != nil {
		t.Fatalf("data-dir copy exit code = %d, err = %v; output:\n%s", code, err, out)
	}
	for _, want := range []string{"artifacts_files=1", fmt.Sprintf("skills_files=%d", len(indexed)+1), "artifact_rows=1", fmt.Sprintf("skill_rows=%d", len(indexed)), "verified=yes"} {
		if !strings.Contains(out, want) {
			t.Fatalf("data-dir summary is missing %q; output:\n%s", want, out)
		}
	}
	copied, err := os.ReadFile(filepath.Join(targetData, "artifacts", filepath.FromSlash(artifactRel), monitorArtifactRawName))
	if err != nil || string(copied) != string(body) {
		t.Fatalf("the artifact was not copied byte for byte: %v", err)
	}
	if info, err := os.Stat(filepath.Join(targetData, filepath.FromSlash(skillRel))); err != nil || !info.IsDir() {
		t.Fatalf("the Skill package was not copied: %v", err)
	}
	var storedDir string
	if err := db.QueryRow("SELECT artifact_dir FROM monitor_run_items WHERE run_id=?", seed.RunID).Scan(&storedDir); err != nil {
		t.Fatalf("read the copied artifact index: %v", err)
	}
	if storedDir != artifactRel {
		t.Fatalf("artifact_dir = %q, want %q", storedDir, artifactRel)
	}
	if err := db.QueryRow("SELECT storage_dir FROM platform_skills WHERE slug=?", slug).Scan(&storedDir); err != nil {
		t.Fatalf("read the copied skill index: %v", err)
	}
	if storedDir != skillRel {
		t.Fatalf("storage_dir = %q, want %q", storedDir, skillRel)
	}
}

// TestGatewayMySQLCopyCommandMissingArtifactRollsBack proves a missing file is a
// failed cutover: the transaction rolls back, the copied tree is removed, and the
// error names the run and item whose artifact is absent.
func TestGatewayMySQLCopyCommandMissingArtifactRollsBack(t *testing.T) {
	port := gatewayMySQLCopyCommandPort(t)
	store, seed := gatewayMySQLCopyCommandSeedSource(t)
	sourceData := t.TempDir()
	artifactRel := "2026-11-24/int50-missing/item_1"
	artifactDir := filepath.Join(sourceData, "artifacts", filepath.FromSlash(artifactRel))
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("create the source artifact directory: %v", err)
	}
	raw := filepath.Join(artifactDir, monitorArtifactRawName)
	if err := os.WriteFile(raw, []byte("int50 artifact\n"), 0o644); err != nil {
		t.Fatalf("write the source artifact: %v", err)
	}
	size, sha, err := gatewayCopyArtifactDigest(artifactDir)
	if err != nil {
		t.Fatalf("digest the source artifact: %v", err)
	}
	if _, err := store.db.Exec("UPDATE monitor_run_items SET artifact_dir=?, artifact_bytes=?, artifact_sha256=? WHERE run_id=?", artifactRel, size, sha, seed.RunID); err != nil {
		t.Fatalf("index the source artifact: %v", err)
	}
	targetData := t.TempDir()
	snapshot := gatewayMySQLCopyCommandSnapshot(t, store)
	if err := os.Remove(raw); err != nil {
		t.Fatalf("remove the indexed artifact before the cutover: %v", err)
	}
	db, name := gatewayMySQLCopyCommandDestination(t, port)
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", "root@tcp(127.0.0.1:"+port+")/"+name+"?parseTime=true")

	code, err, out := gatewayMySQLCopyCommandRun(t, GatewayMySQLCopyOptions{Source: snapshot, ConfirmDatabase: name, SourceDataDir: sourceData, TargetDataDir: targetData})
	if code != GatewayMySQLCopyExitRuntime || err == nil || !strings.Contains(err.Error(), "mysql_copy_artifact_missing") {
		t.Fatalf("missing artifact exit code = %d, err = %v, want 1 and mysql_copy_artifact_missing; output:\n%s", code, err, out)
	}
	if !strings.Contains(err.Error(), seed.RunID) {
		t.Fatalf("the failure does not name the run whose artifact is missing (%s): %v", seed.RunID, err)
	}
	var users int64
	if err := db.QueryRow("SELECT COUNT(*) FROM users").Scan(&users); err != nil {
		t.Fatalf("count the rolled-back users: %v", err)
	}
	if users != 0 {
		t.Fatalf("the failed cutover left %d user rows behind; it must roll back", users)
	}
	if _, err := os.Stat(filepath.Join(targetData, "artifacts")); !os.IsNotExist(err) {
		t.Fatalf("the failed cutover left artifacts under the target data dir: %v", err)
	}
}

// TestGatewayMySQLCopyCommandFreshSnapshotCutsOver is the INT-53 delivery case:
// a snapshot from a freshly installed Gateway that never created this release's
// module tables must still cut over. The tables the snapshot lacks are listed as
// native_schema_only, the destination keeps them empty, and every table the
// snapshot does hold is copied and verified.
func TestGatewayMySQLCopyCommandFreshSnapshotCutsOver(t *testing.T) {
	port := gatewayMySQLCopyCommandPort(t)
	store, _ := gatewayMySQLCopyCommandFreshSource(t)
	snapshot := gatewayMySQLCopyCommandSnapshot(t, store)
	sourceCounts := gatewayMySQLCopyCommandSourceCounts(t, snapshot)
	nonInternal := 0
	for table := range sourceCounts {
		if !strings.HasPrefix(table, "sqlite_") {
			nonInternal++
		}
	}
	if native := gatewayMySQLCopyCommandNativeTables(t); nonInternal >= native {
		t.Fatalf("the fresh snapshot holds %d tables, want fewer than the %d native tables (the case is not exercising a fresh deployment)", nonInternal, native)
	}
	if sourceCounts["users"] != 2 || sourceCounts["audit_events"] != 3 {
		t.Fatalf("fresh snapshot users=%d audit_events=%d, want 2 and 3", sourceCounts["users"], sourceCounts["audit_events"])
	}
	db, name := gatewayMySQLCopyCommandDestination(t, port)
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", "root@tcp(127.0.0.1:"+port+")/"+name+"?parseTime=true")

	code, err, out := gatewayMySQLCopyCommandRun(t, GatewayMySQLCopyOptions{Source: snapshot, ConfirmDatabase: name})
	if code != GatewayMySQLCopyExitOK || err != nil {
		t.Fatalf("fresh snapshot cutover exit code = %d, err = %v, want 0; output:\n%s", code, err, out)
	}
	if strings.Contains(out, "mysql_copy_schema_mismatch") {
		t.Fatalf("the fresh snapshot was rejected as a schema mismatch; output:\n%s", out)
	}
	for _, want := range []string{
		"reason=native_schema_only",
		"table=cluster_inventory_sources source=absent destination=empty reason=native_schema_only",
		"mysql-copy complete:",
		"verified=yes",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("fresh snapshot output is missing %q; output:\n%s", want, out)
		}
	}
	native := gatewayMySQLCopyCommandNativeTables(t)
	if tables := gatewayMySQLCopyCommandTables(t, db); len(tables) != native {
		t.Fatalf("the destination holds %d tables after the cutover, want the %d native tables", len(tables), native)
	}
	reported := gatewayMySQLCopyCommandReported(t, out)
	for table, want := range sourceCounts {
		if strings.HasPrefix(table, "sqlite_") {
			continue
		}
		if reported[table] != want {
			t.Fatalf("%s: summary says %d rows, the snapshot holds %d", table, reported[table], want)
		}
	}
	if _, ok := reported["cluster_inventory_sources"]; ok {
		t.Fatalf("the summary reported a row count for cluster_inventory_sources, a table the snapshot does not hold")
	}
	var users, audits int64
	if err := db.QueryRow("SELECT COUNT(*) FROM users").Scan(&users); err != nil {
		t.Fatalf("count the copied users: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM audit_events").Scan(&audits); err != nil {
		t.Fatalf("count the copied audit events: %v", err)
	}
	if users != sourceCounts["users"] || audits != sourceCounts["audit_events"] {
		t.Fatalf("copied users/audit_events = %d/%d, want %d/%d", users, audits, sourceCounts["users"], sourceCounts["audit_events"])
	}
	var ledgerRows int
	var version string
	if err := db.QueryRow("SELECT COUNT(*), COALESCE(MIN(version), '') FROM gateway_schema_migrations").Scan(&ledgerRows, &version); err != nil {
		t.Fatalf("read the schema ledger: %v", err)
	}
	if ledgerRows != 1 || version != gatewaySchemaBaselineVersion {
		t.Fatalf("gateway_schema_migrations = %d row(s) version %q, want exactly one %q", ledgerRows, version, gatewaySchemaBaselineVersion)
	}

	// The real proof the database is usable: start the Gateway against it and ask
	// for health. The startup path re-verifies the ledger and the runtime seeds.
	t.Setenv("DOOPS_GATEWAY_STORE", "mysql")
	t.Setenv("DOOPS_GATEWAY_DATA_DIR", t.TempDir())
	mysqlStore, err := OpenGatewayStore(filepath.Join(t.TempDir(), "unused.db"))
	if err != nil {
		t.Fatalf("start the Gateway store on the copied database: %v", err)
	}
	defer mysqlStore.Close()
	hub := NewGatewayHub(mysqlStore, GatewayHubOptions{AllowOpenAgentRegistration: true})
	defer hub.CloseTunnels()
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ok") {
		t.Fatalf("GET /health on the copied database = %d %q, want 200 ok", rec.Code, rec.Body.String())
	}
}

// TestGatewayMySQLCopyCommandPreviewBucketsMatchTheRun is the INT-57 case: the
// dry run and the real run must classify each table the snapshot lacks into the
// same bucket and report the same counts. A preview that says 40 native-only and
// a run that says 33+7 retires the operator's ability to reconcile the review.
func TestGatewayMySQLCopyCommandPreviewBucketsMatchTheRun(t *testing.T) {
	port := gatewayMySQLCopyCommandPort(t)
	store, _ := gatewayMySQLCopyCommandFreshSource(t)
	snapshot := gatewayMySQLCopyCommandSnapshot(t, store)
	db, name := gatewayMySQLCopyCommandDestination(t, port)
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", "root@tcp(127.0.0.1:"+port+")/"+name+"?parseTime=true")

	code, err, out := gatewayMySQLCopyCommandRun(t, GatewayMySQLCopyOptions{Source: snapshot, ConfirmDatabase: name, DryRun: true})
	if code != GatewayMySQLCopyExitOK || err != nil {
		t.Fatalf("preview dry run exit code = %d, err = %v, want 0; output:\n%s", code, err, out)
	}
	preview := gatewayMySQLCopyCommandDestinationOnly(t, out)
	native, legacy := 0, 0
	for _, bucket := range preview {
		switch bucket {
		case gatewayCopyBucketNativeSchemaOnly:
			native++
		case gatewayCopyBucketLegacyDestinationOnly:
			legacy++
		default:
			t.Fatalf("preview used unknown bucket %q", bucket)
		}
	}
	if native == 0 || legacy != len(gatewayCopyLegacyTables) {
		t.Fatalf("preview buckets native_schema_only=%d legacy_destination_only=%d, want >0 and %d; output:\n%s", native, legacy, len(gatewayCopyLegacyTables), out)
	}
	wantSummary := fmt.Sprintf("destination_only=%d (native_schema_only=%d legacy_destination_only=%d)", len(preview), native, legacy)
	if !strings.Contains(out, wantSummary) {
		t.Fatalf("preview summary is missing %q; output:\n%s", wantSummary, out)
	}
	if tables := gatewayMySQLCopyCommandTables(t, db); len(tables) != 0 {
		t.Fatalf("the preview dry run installed %d tables; it must not execute DDL", len(tables))
	}

	code, err, out = gatewayMySQLCopyCommandRun(t, GatewayMySQLCopyOptions{Source: snapshot, ConfirmDatabase: name})
	if code != GatewayMySQLCopyExitOK || err != nil {
		t.Fatalf("real cutover exit code = %d, err = %v, want 0; output:\n%s", code, err, out)
	}
	run := gatewayMySQLCopyCommandDestinationOnly(t, out)
	if !reflect.DeepEqual(preview, run) {
		t.Fatalf("dry-run and real-run destination-only buckets differ:\npreview=%v\nrun=%v\noutput:\n%s", preview, run, out)
	}
	if !strings.Contains(out, fmt.Sprintf("destination_only: total=%d (native_schema_only=%d legacy_destination_only=%d)", len(run), native, legacy)) {
		t.Fatalf("real run summary is missing the shared bucket counts; output:\n%s", out)
	}
	wantTables := gatewayMySQLCopyCommandNativeTables(t)
	if tables := gatewayMySQLCopyCommandTables(t, db); len(tables) != wantTables {
		t.Fatalf("the destination holds %d tables after the cutover, want %d", len(tables), wantTables)
	}
}

// TestGatewayMySQLCopyCommandFreshSnapshotRefusesOccupiedSchemaOnlyTable covers
// the same fresh snapshot against a destination where one native-schema table the
// snapshot never had already holds a row. Accepting that table would silently
// merge or overwrite rows, so the run must stop with the same stale error the
// real precondition uses and leave every table untouched.
func TestGatewayMySQLCopyCommandFreshSnapshotRefusesOccupiedSchemaOnlyTable(t *testing.T) {
	port := gatewayMySQLCopyCommandPort(t)
	store, _ := gatewayMySQLCopyCommandFreshSource(t)
	snapshot := gatewayMySQLCopyCommandSnapshot(t, store)
	db, name := gatewayMySQLCopyCommandDestination(t, port)
	gatewayMySQLCopyCommandPrepareSchema(t, t.Context(), db)
	if _, err := db.Exec("INSERT INTO cluster_inventory_sources (cluster, instance, asset_id, name, verified_at, verified_by, scope) VALUES ('int53-cluster', 'int53-instance', 'int53-asset', 'INT-53 source', '2026-11-24T00:00:00Z', 'int53', 'list_namespaces')"); err != nil {
		t.Fatalf("occupy a native-schema-only table: %v", err)
	}
	before := gatewayMySQLCopyCommandTableRows(t, db)
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", "root@tcp(127.0.0.1:"+port+")/"+name+"?parseTime=true")

	code, err, out := gatewayMySQLCopyCommandRun(t, GatewayMySQLCopyOptions{Source: snapshot, ConfirmDatabase: name})
	if code != GatewayMySQLCopyExitUsage || err == nil || !strings.Contains(err.Error(), "mysql_copy_destination_stale") {
		t.Fatalf("occupied schema-only table exit code = %d, err = %v, want 2 and mysql_copy_destination_stale; output:\n%s", code, err, out)
	}
	for _, want := range []string{"cluster_inventory_sources", "1 row"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal %q is missing %q", err, want)
		}
	}
	if after := gatewayMySQLCopyCommandTableRows(t, db); !reflect.DeepEqual(after, before) {
		t.Fatalf("the refused copy changed the destination rows: before=%v after=%v", before, after)
	}

	// The dry run must reach the same verdict and the same error code: a review
	// that says yes and a real run that says no is the INT-53 defect.
	code, err, out = gatewayMySQLCopyCommandRun(t, GatewayMySQLCopyOptions{Source: snapshot, ConfirmDatabase: name, DryRun: true})
	if code != GatewayMySQLCopyExitUsage || err == nil || !strings.Contains(err.Error(), "mysql_copy_destination_stale") {
		t.Fatalf("dry-run on the occupied schema-only table exit code = %d, err = %v, want 2 and mysql_copy_destination_stale; output:\n%s", code, err, out)
	}
	if !strings.Contains(err.Error(), "cluster_inventory_sources") {
		t.Fatalf("the dry-run refusal does not name the occupied table: %v", err)
	}
	if after := gatewayMySQLCopyCommandTableRows(t, db); !reflect.DeepEqual(after, before) {
		t.Fatalf("the dry run changed the destination rows: before=%v after=%v", before, after)
	}
}

// TestGatewayMySQLCopyCommandFreshSnapshotPreviewMatchesTheRun pins the two
// verdicts together for a fresh snapshot: an empty database is a go in both
// modes, and a database with one occupied schema-only table is a precondition
// failure in both modes.
func TestGatewayMySQLCopyCommandFreshSnapshotPreviewMatchesTheRun(t *testing.T) {
	port := gatewayMySQLCopyCommandPort(t)
	store, _ := gatewayMySQLCopyCommandFreshSource(t)
	snapshot := gatewayMySQLCopyCommandSnapshot(t, store)

	empty, emptyName := gatewayMySQLCopyCommandDestination(t, port)
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", "root@tcp(127.0.0.1:"+port+")/"+emptyName+"?parseTime=true")
	code, err, out := gatewayMySQLCopyCommandRun(t, GatewayMySQLCopyOptions{Source: snapshot, ConfirmDatabase: emptyName, DryRun: true})
	if code != GatewayMySQLCopyExitOK || err != nil {
		t.Fatalf("empty-database dry run exit code = %d, err = %v, want 0; output:\n%s", code, err, out)
	}
	if !strings.Contains(out, "plan(preview):") || !strings.Contains(out, "reason=native_schema_only") {
		t.Fatalf("the empty-database preview does not explain the native-schema-only tables; output:\n%s", out)
	}
	if tables := gatewayMySQLCopyCommandTables(t, empty); len(tables) != 0 {
		t.Fatalf("the empty-database dry run installed %d tables", len(tables))
	}

	occupied, occupiedName := gatewayMySQLCopyCommandDestination(t, port)
	gatewayMySQLCopyCommandPrepareSchema(t, t.Context(), occupied)
	if _, err := occupied.Exec("INSERT INTO cluster_inventory_sources (cluster, instance, asset_id, name, verified_at, verified_by, scope) VALUES ('int53-cluster', 'int53-instance', 'int53-asset', 'INT-53 source', '2026-11-24T00:00:00Z', 'int53', 'list_namespaces')"); err != nil {
		t.Fatalf("occupy a native-schema-only table: %v", err)
	}
	before := gatewayMySQLCopyCommandTableRows(t, occupied)
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", "root@tcp(127.0.0.1:"+port+")/"+occupiedName+"?parseTime=true")
	dryCode, dryErr, dryOut := gatewayMySQLCopyCommandRun(t, GatewayMySQLCopyOptions{Source: snapshot, ConfirmDatabase: occupiedName, DryRun: true})
	runCode, runErr, runOut := gatewayMySQLCopyCommandRun(t, GatewayMySQLCopyOptions{Source: snapshot, ConfirmDatabase: occupiedName})
	if dryCode != GatewayMySQLCopyExitUsage || dryErr == nil || !strings.Contains(dryErr.Error(), "mysql_copy_destination_stale") {
		t.Fatalf("occupied dry run exit code = %d, err = %v, want 2 and mysql_copy_destination_stale; output:\n%s", dryCode, dryErr, dryOut)
	}
	if runCode != GatewayMySQLCopyExitUsage || runErr == nil || !strings.Contains(runErr.Error(), "mysql_copy_destination_stale") {
		t.Fatalf("occupied real run exit code = %d, err = %v, want 2 and mysql_copy_destination_stale; output:\n%s", runCode, runErr, runOut)
	}
	if after := gatewayMySQLCopyCommandTableRows(t, occupied); !reflect.DeepEqual(after, before) {
		t.Fatalf("the refused runs changed the destination rows: before=%v after=%v", before, after)
	}
}
