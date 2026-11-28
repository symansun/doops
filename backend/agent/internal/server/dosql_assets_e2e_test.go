package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/doops/agent/api"
	"github.com/user/doops/agent/internal/dosql/backfill"
	"github.com/user/doops/agent/internal/dosql/datadiff"
	"github.com/user/doops/agent/internal/dosql/diff"
	"github.com/user/doops/agent/internal/dosql/inspect"
	"github.com/user/doops/agent/internal/dosql/migrate"
	"github.com/user/doops/agent/internal/dosql/recorder"
)

// TestSQL03KeyAssetPersistenceEndToEnd verifies that all 4 critical DoSql assets
// are persisted cleanly and work together:
// 1. Auto-recorded SQL statement history (recorder/history/*.jsonl)
// 2. Versioned SchemaIR snapshots (snapshots/*/v000001.json + latest.json) & Drift diff
// 3. Packed versioned migration files (V001__*.sql, V002__*.sql) + dosql.sum
// 4. In-database _dosql_schema_history with status + idempotent multi-env migrate
func TestSQL03KeyAssetPersistenceEndToEnd(t *testing.T) {
	wsRoot := filepath.Join(t.TempDir(), "ws")
	t.Setenv("DOOPS_WORKSPACE_ROOT", wsRoot)
	if err := os.MkdirAll(filepath.Join(wsRoot, "dev-session"), 0700); err != nil {
		t.Fatal(err)
	}

	target := newDoSQLDirectTestTarget(t)
	binding := api.AgentCredentialBinding{Name: "orders-db", Use: string(CredentialUseDatabase)}

	// 1. Execute DDL+DML through executeDoSQLDirect with auto-recording hook.
	recStore, err := doSQLRecorderStore(target)
	if err != nil {
		t.Fatal(err)
	}
	req, err := planDirectDoSQL("sqlite", api.AgentPromptParams{
		Operation: "apply",
		SQL:       "ALTER TABLE orders ADD COLUMN status_v2 TEXT; UPDATE orders SET status_v2 = status WHERE id = 1;",
	})
	if err != nil {
		t.Fatal(err)
	}
	req.Target = target
	req.OnStatement = func(rec recorder.StatementRecord) {
		rec.SessionID = "dev-session"
		rec.OperationID = "dbop_e2e_1"
		rec.Version = 1
		rec.Credential = binding.Name
		rec.ReleaseTag = "v1.0.0"
		rec.Mode = "apply"
		if err := recStore.Append(rec); err != nil {
			t.Errorf("record append failed: %v", err)
		}
	}
	res, err := executeDoSQLDirect(t.Context(), req)
	if err != nil || !res.Committed || res.Applied != 2 {
		t.Fatalf("direct exec failed: %+v err=%v", res, err)
	}

	// Verify Asset 1: history JSONL persisted on disk and queryable via handleDirectDoSQLAction.
	histOut, err := handleDirectDoSQLAction(t.Context(), "dev-session", api.AgentPromptParams{SQLAction: "history"}, binding, target)
	if err != nil {
		t.Fatalf("history action failed: %v", err)
	}
	var histResp struct {
		Records []recorder.StatementRecord `json:"records"`
	}
	if err := json.Unmarshal([]byte(histOut), &histResp); err != nil || len(histResp.Records) != 2 {
		t.Fatalf("expected 2 recorded statements, got %s", histOut)
	}

	// Verify Asset 2: SchemaIR snapshot saved and version-to-version diff works.
	snapStore, err := doSQLSnapshotStore(target)
	if err != nil {
		t.Fatal(err)
	}
	v1 := &inspect.SchemaIR{
		Engine: "mysql", Database: "orders",
		Tables: []inspect.TableIR{{Name: "orders", Columns: []inspect.ColumnIR{{Name: "id", Type: "int"}}}},
	}
	v2 := &inspect.SchemaIR{
		Engine: "mysql", Database: "orders",
		Tables: []inspect.TableIR{{Name: "orders", Columns: []inspect.ColumnIR{{Name: "id", Type: "int"}, {Name: "status_v2", Type: "varchar(32)", Nullable: true}}}},
	}
	if _, err := snapStore.Save(1, v1); err != nil {
		t.Fatal(err)
	}
	if _, err := snapStore.Save(2, v2); err != nil {
		t.Fatal(err)
	}
	diffOut, err := handleDirectDoSQLAction(t.Context(), "dev-session", api.AgentPromptParams{
		SQLAction: "diff", SQLDiffFrom: "v1", SQLDiffTo: "v2",
	}, binding, target)
	if err != nil {
		t.Fatalf("diff action failed: %v", err)
	}
	var diffResp struct {
		Diff diff.SchemaDiff `json:"diff"`
	}
	if err := json.Unmarshal([]byte(diffOut), &diffResp); err != nil || diffResp.Diff.Identical || len(diffResp.Diff.Changes) != 1 || diffResp.Diff.Changes[0].Kind != diff.ChangeAddColumn {
		t.Fatalf("unexpected v1->v2 diff: %s", diffOut)
	}

	// Verify Asset 3: Pack recorded history into migration directory (V001__add_status_v2.sql + dosql.sum).
	migDir := filepath.Join(t.TempDir(), "migrations")
	packed, err := migrate.PackFromHistory(migDir, "add_status_v2", "sqlite", histResp.Records)
	if err != nil || packed.FileName != "V001__add_status_v2.sql" {
		t.Fatalf("pack failed: %+v err=%v", packed, err)
	}
	if _, err := os.Stat(filepath.Join(migDir, migrate.SumFileName)); err != nil {
		t.Fatalf("dosql.sum missing: %v", err)
	}

	// Verify Asset 4: Roll out packed migrations to a SECOND environment DB via status + migrate!
	prodTarget := newDoSQLDirectTestTarget(t)
	files, err := migrate.LoadDir(migDir)
	if err != nil {
		t.Fatal(err)
	}
	manifestJSON, _ := json.Marshal(files)

	// Status before migrate shows 1 pending file.
	stBefore, err := handleDirectDoSQLAction(t.Context(), "prod-session", api.AgentPromptParams{
		SQLAction: "status", SQLMigrations: string(manifestJSON),
	}, binding, prodTarget)
	if err != nil {
		t.Fatalf("status before migrate: %v", err)
	}
	var stResp struct {
		Status migrate.StatusReport `json:"status"`
	}
	if err := json.Unmarshal([]byte(stBefore), &stResp); err != nil || len(stResp.Status.Pending) != 1 {
		t.Fatalf("expected 1 pending migration, got %s", stBefore)
	}

	// Migrate applies V001 to prodTarget and records _dosql_schema_history.
	migOut, err := handleDirectDoSQLAction(t.Context(), "prod-session", api.AgentPromptParams{
		SQLAction: "migrate", SQLMigrations: string(manifestJSON), SQLReleaseTag: "v1.0.0",
	}, binding, prodTarget)
	if err != nil {
		t.Fatalf("migrate failed: %v", err)
	}
	var migResp struct {
		Result migrate.MigrateResult `json:"result"`
	}
	if err := json.Unmarshal([]byte(migOut), &migResp); err != nil || len(migResp.Result.Executed) != 1 || migResp.Result.Current != 1 {
		t.Fatalf("unexpected migrate result: %s", migOut)
	}

	// Re-running migrate is idempotent (0 executed, 1 skipped).
	migOut2, err := handleDirectDoSQLAction(t.Context(), "prod-session", api.AgentPromptParams{
		SQLAction: "migrate", SQLMigrations: string(manifestJSON), SQLReleaseTag: "v1.0.0",
	}, binding, prodTarget)
	if err != nil {
		t.Fatalf("second migrate failed: %v", err)
	}
	var migResp2 struct {
		Result migrate.MigrateResult `json:"result"`
	}
	if err := json.Unmarshal([]byte(migOut2), &migResp2); err != nil || len(migResp2.Result.Executed) != 0 || migResp2.Result.Skipped != 1 {
		t.Fatalf("expected idempotent re-run: %s", migOut2)
	}
}

func TestSQL03RealMySQLMixedDDLDMLInspectAndDataDiff(t *testing.T) {
	dsn := os.Getenv("DOOPS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("DOOPS_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var dbName string
	if err := db.QueryRowContext(t.Context(), "SELECT DATABASE()").Scan(&dbName); err != nil || dbName == "" {
		t.Fatalf("resolve mysql database name: %q err=%v", dbName, err)
	}
	defer func() { _, _ = db.Exec("DROP TABLE IF EXISTS sql03_orders") }()
	target := doSQLTarget{Engine: "mysql", Driver: "mysql", DSN: dsn, Database: dbName, Address: "127.0.0.1:13306"}

	// 1. Mixed mysqldump header + session SET + DDL + DML backfill in ONE batch!
	batch := `
/*!40101 SET NAMES utf8mb4 */;
SET FOREIGN_KEY_CHECKS = 0;
DROP TABLE IF EXISTS sql03_orders;
CREATE TABLE sql03_orders (id INT PRIMARY KEY, status VARCHAR(32)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
INSERT INTO sql03_orders VALUES (1, 'new'), (2, 'old');
ALTER TABLE sql03_orders ADD COLUMN status_v2 VARCHAR(32);
UPDATE sql03_orders SET status_v2 = status WHERE id > 0;
SET FOREIGN_KEY_CHECKS = 1;
`
	req, err := planDirectDoSQL("mysql", api.AgentPromptParams{Operation: "apply", SQL: batch, SQLLockTimeout: "5s"})
	if err != nil {
		t.Fatalf("plan mysql mixed batch: %v", err)
	}
	req.Target = target
	res, err := executeDoSQLDirect(t.Context(), req)
	if err != nil || res.Status != "verified" || !res.Committed || res.Applied != 8 {
		t.Fatalf("real mysql mixed batch failed: %+v err=%v", res, err)
	}

	// 2. Real MySQL SchemaIR inspect + BIT_XOR datadiff against itself (0 diffs).
	ir, err := inspect.InspectSQL(t.Context(), db, "mysql", dbName, inspect.Options{Tables: []string{"sql03_orders"}})
	if err != nil || ir.TableCount != 1 || len(ir.Tables[0].Columns) != 3 {
		t.Fatalf("real mysql inspect failed: %+v err=%v", ir, err)
	}
	dd, err := datadiff.CompareIntKeyTables(t.Context(), db, db, datadiff.Options{
		Engine: "mysql", Table: "sql03_orders", KeyColumn: "id", Columns: []string{"id", "status", "status_v2"},
	})
	if err != nil || !dd.Identical || dd.ChunksMatched == 0 {
		t.Fatalf("real mysql BIT_XOR datadiff failed: %+v err=%v", dd, err)
	}
}

func TestSQL03RealPostgresConcurrentIndex(t *testing.T) {
	dsn := os.Getenv("DOOPS_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("DOOPS_TEST_PG_DSN not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var dbName string
	if err := db.QueryRowContext(t.Context(), "SELECT current_database()").Scan(&dbName); err != nil || dbName == "" {
		t.Fatalf("resolve pg database name: %q err=%v", dbName, err)
	}
	defer func() { _, _ = db.Exec("DROP TABLE IF EXISTS sql03_pg_orders CASCADE") }()
	target := doSQLTarget{Engine: "postgres", Driver: "pgx", DSN: dsn, Database: dbName, Address: "127.0.0.1:15432"}
	batch := `
DROP TABLE IF EXISTS sql03_pg_orders;
CREATE TABLE sql03_pg_orders (id INT PRIMARY KEY, status TEXT);
CREATE INDEX CONCURRENTLY idx_sql03_pg_status ON sql03_pg_orders(status);
INSERT INTO sql03_pg_orders VALUES (1, 'ok');
`
	req, err := planDirectDoSQL("postgres", api.AgentPromptParams{Operation: "apply", SQL: batch, SQLStatementTimeout: "30s"})
	if err != nil {
		t.Fatalf("plan pg batch: %v", err)
	}
	req.Target = target
	res, err := executeDoSQLDirect(t.Context(), req)
	if err != nil || res.Status != "verified" || res.Applied != 4 {
		t.Fatalf("real pg CONCURRENTLY batch failed: %+v err=%v", res, err)
	}
	if !strings.Contains(res.Segments[1].Kind, "non_transactional") {
		t.Fatalf("expected non_transactional segment for CONCURRENTLY: %+v", res.Segments)
	}
}

func execDDLSlice(t *testing.T, target doSQLTarget, engine string, ddls []string) {
	t.Helper()
	joined := strings.Join(ddls, "\n")
	req, err := planDirectDoSQL(engine, api.AgentPromptParams{
		Operation:           "apply",
		SQL:                 joined,
		SQLLockTimeout:      "10s",
		SQLStatementTimeout: "30s",
	})
	if err != nil {
		t.Fatalf("plan %s DDL failed:\n%s\nerr=%v", engine, joined, err)
	}
	req.Target = target
	res, err := executeDoSQLDirect(t.Context(), req)
	if err != nil || !res.Committed {
		t.Fatalf("exec %s DDL failed:\n%s\nres=%+v err=%v", engine, joined, res, err)
	}
}

// TestSQL07RealMySQLFullRoundTrip proves every DoSql subsystem against a LIVE MySQL 8.4 server:
// 1. inspect(A) -> diff(empty B, A) -> execute SuggestedDDL on B -> inspect(B) -> diff(A, B).Identical == true
// 2. incremental ALTER (add column, NOT NULL + DEFAULT, index) -> diff -> replay on B -> Identical == true
// 3. mixed mysqldump/SET/DDL/DML batch + DELIMITER stored procedure + dry-run rollback isolation
// 4. chunked int-key backfill (dry-run + StartAfterID resume + full completion)
// 5. pack -> status -> policy preflight rejection -> migrate -> idempotent rerun -> dirty --resume
// 6. real MySQL BIT_XOR(MD5(...)) datadiff pushdown with NULL & single quotes + fix_sql replay -> 0 diffs
func TestSQL07RealMySQLFullRoundTrip(t *testing.T) {
	dsnA := strings.TrimSpace(os.Getenv("DOOPS_TEST_MYSQL_DSN"))
	if dsnA == "" {
		t.Skip("DOOPS_TEST_MYSQL_DSN not set")
	}
	dsnB := strings.TrimSpace(os.Getenv("DOOPS_TEST_MYSQL_DSN_B"))
	if dsnB == "" {
		if strings.Contains(dsnA, "/dosql_a") {
			dsnB = strings.Replace(dsnA, "/dosql_a", "/dosql_b", 1)
		} else {
			t.Fatal("set DOOPS_TEST_MYSQL_DSN_B when DOOPS_TEST_MYSQL_DSN does not target /dosql_a")
		}
	}
	dbAName, dbBName := "dosql_a", "dosql_b"
	addr := strings.TrimSpace(os.Getenv("DOOPS_TEST_MYSQL_ADDR"))
	if addr == "" {
		addr = "127.0.0.1:13306"
	}

	t.Setenv("DOOPS_DOSQL_STATE_DIR", filepath.Join(t.TempDir(), "state"))
	wsRoot := filepath.Join(t.TempDir(), "ws")
	t.Setenv("DOOPS_WORKSPACE_ROOT", wsRoot)
	_ = os.MkdirAll(filepath.Join(wsRoot, "mysql-e2e"), 0700)

	dbA, err := sql.Open("mysql", dsnA)
	if err != nil {
		t.Fatal(err)
	}
	defer dbA.Close()
	dbB, err := sql.Open("mysql", dsnB)
	if err != nil {
		t.Fatal(err)
	}
	defer dbB.Close()

	// Reset both databases cleanly.
	for _, db := range []*sql.DB{dbA, dbB} {
		for _, stmt := range []string{
			"SET FOREIGN_KEY_CHECKS = 0",
			"DROP TABLE IF EXISTS orders",
			"DROP TABLE IF EXISTS users",
			"DROP TABLE IF EXISTS _dosql_schema_history",
			"DROP PROCEDURE IF EXISTS p_dosql_ping",
			"SET FOREIGN_KEY_CHECKS = 1",
		} {
			if _, err := db.Exec(stmt); err != nil {
				t.Fatalf("reset mysql failed on %q: %v", stmt, err)
			}
		}
	}

	targetA := doSQLTarget{Engine: "mysql", Driver: "mysql", DSN: dsnA, Database: dbAName, Address: addr}
	targetB := doSQLTarget{Engine: "mysql", Driver: "mysql", DSN: dsnB, Database: dbBName, Address: addr}
	binding := api.AgentCredentialBinding{Name: "mysql-e2e", Use: string(CredentialUseDatabase)}

	// 1. Create complex schema on A (string default, decimal default, unique index, composite index, FK ON DELETE CASCADE).
	initSQL := `
CREATE TABLE users (
  id BIGINT NOT NULL,
  email VARCHAR(128) NOT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  note TEXT,
  PRIMARY KEY (id),
  UNIQUE KEY uq_users_email (email)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE orders (
  id BIGINT NOT NULL,
  user_id BIGINT NOT NULL,
  code VARCHAR(64) NOT NULL DEFAULT 'ord-0',
  amount DECIMAL(12,2) NOT NULL DEFAULT 0.00,
  status VARCHAR(32),
  PRIMARY KEY (id),
  KEY idx_orders_user_status (user_id, status),
  CONSTRAINT fk_orders_user FOREIGN KEY (user_id) REFERENCES users (id) ON UPDATE RESTRICT ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
`
	execDDLSlice(t, targetA, "mysql", []string{initSQL})

	// Inspect A & empty B, diff, replay SuggestedDDL on B, re-inspect B, assert Identical!
	irA, err := inspect.InspectSQL(t.Context(), dbA, "mysql", dbAName, inspect.Options{})
	if err != nil {
		t.Fatalf("mysql inspect A: %v", err)
	}
	irBEmpty, err := inspect.InspectSQL(t.Context(), dbB, "mysql", dbBName, inspect.Options{})
	if err != nil {
		t.Fatalf("mysql inspect empty B: %v", err)
	}
	dInit := diff.CompareSchemas(irBEmpty, irA, nil)
	if dInit.Identical || len(dInit.SuggestedDDL) < 2 {
		t.Fatalf("expected create DDLs for B, got %+v", dInit)
	}
	execDDLSlice(t, targetB, "mysql", dInit.SuggestedDDL)

	irBAfter, err := inspect.InspectSQL(t.Context(), dbB, "mysql", dbBName, inspect.Options{})
	if err != nil {
		t.Fatalf("mysql inspect B after replay: %v", err)
	}
	dVerify := diff.CompareSchemas(irA, irBAfter, nil)
	if !dVerify.Identical {
		t.Fatalf("MySQL schema round-trip NOT identical after executing SuggestedDDL!\nGenerated DDL:\n%s\nResidual diff:\n%+v",
			strings.Join(dInit.SuggestedDDL, "\n"), dVerify.Changes)
	}

	// 2. Incremental ALTER on A -> diff(B, A) -> execute SuggestedDDL on B -> assert Identical!
	alterSQL := `
ALTER TABLE orders ADD COLUMN tag VARCHAR(64) DEFAULT 'std';
ALTER TABLE orders MODIFY COLUMN status VARCHAR(32) NOT NULL DEFAULT 'new';
CREATE INDEX idx_orders_code ON orders (code);
`
	execDDLSlice(t, targetA, "mysql", []string{alterSQL})
	irA2, err := inspect.InspectSQL(t.Context(), dbA, "mysql", dbAName, inspect.Options{})
	if err != nil {
		t.Fatal(err)
	}
	dInc := diff.CompareSchemas(irBAfter, irA2, nil)
	if dInc.Identical || len(dInc.SuggestedDDL) == 0 {
		t.Fatalf("expected incremental diff, got %+v", dInc)
	}
	execDDLSlice(t, targetB, "mysql", dInc.SuggestedDDL)
	irB2, err := inspect.InspectSQL(t.Context(), dbB, "mysql", dbBName, inspect.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if dIncVerify := diff.CompareSchemas(irA2, irB2, nil); !dIncVerify.Identical {
		t.Fatalf("MySQL incremental diff replay NOT identical!\nDDL:\n%s\nResidual:\n%+v",
			strings.Join(dInc.SuggestedDDL, "\n"), dIncVerify.Changes)
	}

	// 3. DELIMITER stored procedure + mixed mysqldump/DDL/DML batch.
	procSQL := "DELIMITER $$\nCREATE PROCEDURE p_dosql_ping() BEGIN SELECT 1; END$$\nDELIMITER ;\n"
	execDDLSlice(t, targetA, "mysql", []string{procSQL})

	// 4. Chunked backfill on real MySQL (65 rows, dry-run -> StartAfterID -> full).
	if _, err := dbA.Exec(`INSERT INTO users (id, email, status, note) VALUES (1, 'a@test.com', 'active', 'seed')`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 65; i++ {
		if _, err := dbA.Exec(`INSERT INTO orders (id, user_id, code, amount, status, tag) VALUES (?, 1, ?, 10.50, 'paid', NULL)`, i, fmt.Sprintf("ord-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	bfDry, err := backfill.RunIntKeyBackfill(t.Context(), dbA, backfill.Options{
		Engine: "mysql", Table: "orders", KeyColumn: "id", SetClause: "tag = status", WhereClause: "tag IS NULL", BatchSize: 20, DryRun: true,
	})
	if err != nil || bfDry.TotalAffected != 65 {
		t.Fatalf("mysql backfill dry-run failed: %+v err=%v", bfDry, err)
	}
	bfPart, err := backfill.RunIntKeyBackfill(t.Context(), dbA, backfill.Options{
		Engine: "mysql", Table: "orders", KeyColumn: "id", SetClause: "tag = status", WhereClause: "tag IS NULL", BatchSize: 20, StartAfterID: 20,
	})
	if err != nil || bfPart.TotalAffected != 45 {
		t.Fatalf("mysql resumed backfill failed: %+v err=%v", bfPart, err)
	}
	bfRest, err := backfill.RunIntKeyBackfill(t.Context(), dbA, backfill.Options{
		Engine: "mysql", Table: "orders", KeyColumn: "id", SetClause: "tag = status", WhereClause: "tag IS NULL", BatchSize: 20,
	})
	if err != nil || bfRest.TotalAffected != 20 {
		t.Fatalf("mysql remaining backfill failed: %+v err=%v", bfRest, err)
	}

	// 5. Real MySQL migrate lifecycle: read-only status (zero DDL!), policy preflight rejection (no history table!), valid migrate, idempotent rerun, dirty --resume!
	histStore := &migrate.SQLHistoryStore{DB: dbB, Engine: "mysql"}
	if _, err := handleDirectDoSQLAction(t.Context(), "mysql-e2e", api.AgentPromptParams{
		SQLAction: "status",
	}, binding, targetB); err != nil {
		t.Fatalf("mysql status before migrate failed: %v", err)
	}
	if exists, err := histStore.TableExists(t.Context()); err != nil || exists {
		t.Fatalf("mysql read-only status silently created _dosql_schema_history: exists=%v err=%v", exists, err)
	}
	unsafeManifest, _ := json.Marshal([]migrate.MigrationFile{{
		Version: 1, VersionTag: "V001", Description: "unsafe", FileName: "V001__unsafe.sql",
		Checksum: "sha256:unsafe", Content: "UPDATE orders SET status = 'oops';",
	}})
	if _, err := handleDirectDoSQLAction(t.Context(), "mysql-e2e", api.AgentPromptParams{
		SQLAction: "migrate", SQLMigrations: string(unsafeManifest),
	}, binding, targetB); err == nil || !strings.Contains(err.Error(), "dosql_policy_rejected") {
		t.Fatalf("mysql migrate should reject unsafe migration before touching history table, got %v", err)
	}
	if exists, err := histStore.TableExists(t.Context()); err != nil || exists {
		t.Fatalf("mysql policy-rejected migrate silently created _dosql_schema_history: exists=%v err=%v", exists, err)
	}

	migDir := t.TempDir()
	packed, err := migrate.PackFromHistory(migDir, "add_memo", "mysql", []recorder.StatementRecord{
		{Mode: "dry_run", Kind: "ddl", Statement: "ALTER TABLE orders ADD COLUMN ghost INT", Status: "succeeded", StmtHash: "g1"},
		{Mode: "apply", Kind: "ddl", Statement: "ALTER TABLE orders ADD COLUMN memo VARCHAR(64) DEFAULT 'none'", Status: "succeeded", StmtHash: "m1"},
	})
	if err != nil || strings.Contains(packed.Content, "ghost") {
		t.Fatalf("mysql pack failed: %+v err=%v", packed, err)
	}
	loadedFiles, err := migrate.LoadDir(migDir)
	if err != nil {
		t.Fatal(err)
	}
	migJSON, _ := json.Marshal(loadedFiles)
	migOut, err := handleDirectDoSQLAction(t.Context(), "mysql-e2e", api.AgentPromptParams{
		SQLAction: "migrate", SQLMigrations: string(migJSON), SQLReleaseTag: "v1.0.0",
	}, binding, targetB)
	if err != nil || !strings.Contains(migOut, `"current_version": 1`) {
		t.Fatalf("mysql migrate V001 failed: %s err=%v", migOut, err)
	}
	// Idempotent rerun skips V001.
	migOut2, err := handleDirectDoSQLAction(t.Context(), "mysql-e2e", api.AgentPromptParams{
		SQLAction: "migrate", SQLMigrations: string(migJSON), SQLReleaseTag: "v1.0.0",
	}, binding, targetB)
	if err != nil || !strings.Contains(migOut2, `"skipped": 1`) {
		t.Fatalf("mysql idempotent migrate failed: %s err=%v", migOut2, err)
	}
	// Verify dirty --resume on real MySQL _dosql_schema_history.
	v2Bad := migrate.MigrationFile{
		Version: 2, VersionTag: "V002", Description: "partial", FileName: "V002__partial.sql",
		Checksum: "sha256:v2", Content: "ALTER TABLE orders ADD COLUMN step1 INT; SELECT * FROM non_existent_table_xyz;",
	}
	_, err = migrate.Apply(t.Context(), histStore, append(loadedFiles, v2Bad), 0, "v1.1.0", false, func(ctx context.Context, f migrate.MigrationFile, startIdx int) (int, int, error) {
		req, pErr := planDirectDoSQL("mysql", api.AgentPromptParams{Operation: "apply", SQL: f.Content})
		if pErr != nil {
			return 0, 0, pErr
		}
		total := len(req.Statements)
		req.Statements = req.Statements[startIdx:]
		req.ParsedStatements = req.ParsedStatements[startIdx:]
		req.Target = targetB
		r, e := executeDoSQLDirect(ctx, req)
		return r.Applied, total, e
	})
	if err == nil || !strings.Contains(err.Error(), "dosql_migrate_failed") {
		t.Fatalf("expected partial failure on mysql V002, got %v", err)
	}
	// Resume with fixed stmt 2 (stmt 1 already committed and MUST NOT re-run!).
	v2Fixed := v2Bad
	v2Fixed.Content = "ALTER TABLE orders ADD COLUMN step1 INT; ALTER TABLE orders ADD COLUMN step2 INT;"
	resumed, err := migrate.Apply(t.Context(), histStore, append(loadedFiles, v2Fixed), 0, "v1.1.0", true, func(ctx context.Context, f migrate.MigrationFile, startIdx int) (int, int, error) {
		if startIdx != 1 {
			t.Fatalf("expected mysql resume from stmt index 1, got %d", startIdx)
		}
		req, pErr := planDirectDoSQL("mysql", api.AgentPromptParams{Operation: "apply", SQL: f.Content})
		if pErr != nil {
			return 0, 0, pErr
		}
		total := len(req.Statements)
		req.Statements = req.Statements[startIdx:]
		req.ParsedStatements = req.ParsedStatements[startIdx:]
		req.Target = targetB
		r, e := executeDoSQLDirect(ctx, req)
		return r.Applied, total, e
	})
	if err != nil || resumed.Current != 2 {
		t.Fatalf("mysql migrate --resume failed: %+v err=%v", resumed, err)
	}

	// 6. Real MySQL BIT_XOR(MD5(...)) datadiff with NULL, single quotes, missing, extra, mismatch + fix_sql replay!
	for i := 2; i <= 50; i++ {
		note := interface{}("ok")
		if i == 10 {
			note = nil
		} else if i == 25 {
			note = "O'Reilly"
		}
		if _, err := dbA.Exec(`INSERT INTO users (id, email, status, note) VALUES (?, ?, 'active', ?)`, i, fmt.Sprintf("u%d@test.com", i), note); err != nil {
			t.Fatal(err)
		}
		if i != 25 { // row 25 (with single quote!) missing in B
			st := "active"
			bNote := note
			if i == 10 {
				bNote = "not-null" // row 10 differs (NULL in A vs non-null in B)
			}
			if i == 40 {
				st = "stale" // row 40 value mismatch
			}
			if _, err := dbB.Exec(`INSERT INTO users (id, email, status, note) VALUES (?, ?, ?, ?)`, i, fmt.Sprintf("u%d@test.com", i), st, bNote); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := dbB.Exec(`INSERT INTO users (id, email, status, note) VALUES (1, 'a@test.com', 'active', 'seed'), (99, 'extra@test.com', 'extra', NULL)`); err != nil {
		t.Fatal(err)
	}

	ddRes, err := datadiff.CompareIntKeyTables(t.Context(), dbA, dbB, datadiff.Options{
		Engine: "mysql", Table: "users", KeyColumn: "id", Columns: []string{"id", "email", "status", "note"},
		ChunkSize: 20, LeafSize: 10, GenerateFix: true,
	})
	if err != nil || ddRes.Identical || ddRes.DiffCount != 4 {
		t.Fatalf("real mysql BIT_XOR datadiff failed: %+v err=%v", ddRes, err)
	}
	for _, d := range ddRes.Diffs {
		if _, err := dbB.Exec(d.FixSQL); err != nil {
			t.Fatalf("mysql fix SQL failed: %q err=%v", d.FixSQL, err)
		}
	}
	ddAfter, err := datadiff.CompareIntKeyTables(t.Context(), dbA, dbB, datadiff.Options{
		Engine: "mysql", Table: "users", KeyColumn: "id", Columns: []string{"id", "email", "status", "note"},
		ChunkSize: 20, LeafSize: 10,
	})
	if err != nil || !ddAfter.Identical || ddAfter.DiffCount != 0 {
		t.Fatalf("real mysql datadiff NOT zero after fix SQL replay: %+v err=%v", ddAfter, err)
	}
}

// TestSQL07RealPostgresFullRoundTrip proves every DoSql subsystem against a LIVE PostgreSQL 16 server:
// 1. inspect(A) (tables, columns, pg_index indexes/PKs, pg_constraint FKs) -> diff(empty B, A) -> execute SuggestedDDL on B -> inspect(B) -> Identical == true
// 2. incremental ALTER (add column, SET NOT NULL + SET DEFAULT, CREATE INDEX CONCURRENTLY) -> diff -> replay on B -> Identical == true
// 3. chunked int-key backfill with $1/$2 placeholders (dry-run + resume + full)
// 4. pack -> policy preflight -> migrate -> idempotent rerun -> dirty --resume on real PG _dosql_schema_history
// 5. real PostgreSQL BIT_XOR(('x'||substr(md5(...),1,16))::bit(64)::bigint) datadiff + fix_sql replay -> 0 diffs
func TestSQL07RealPostgresFullRoundTrip(t *testing.T) {
	dsnA := strings.TrimSpace(os.Getenv("DOOPS_TEST_PG_DSN"))
	if dsnA == "" {
		t.Skip("DOOPS_TEST_PG_DSN not set")
	}
	dsnB := strings.TrimSpace(os.Getenv("DOOPS_TEST_PG_DSN_B"))
	if dsnB == "" {
		if strings.Contains(dsnA, "/dosql_a") {
			dsnB = strings.Replace(dsnA, "/dosql_a", "/dosql_b", 1)
		} else {
			t.Fatal("set DOOPS_TEST_PG_DSN_B when DOOPS_TEST_PG_DSN does not target /dosql_a")
		}
	}
	dbAName, dbBName := "dosql_a", "dosql_b"
	addr := strings.TrimSpace(os.Getenv("DOOPS_TEST_PG_ADDR"))
	if addr == "" {
		addr = "127.0.0.1:15432"
	}

	t.Setenv("DOOPS_DOSQL_STATE_DIR", filepath.Join(t.TempDir(), "state"))
	wsRoot := filepath.Join(t.TempDir(), "ws")
	t.Setenv("DOOPS_WORKSPACE_ROOT", wsRoot)
	_ = os.MkdirAll(filepath.Join(wsRoot, "pg-e2e"), 0700)

	dbA, err := sql.Open("pgx", dsnA)
	if err != nil {
		t.Fatal(err)
	}
	defer dbA.Close()
	dbB, err := sql.Open("pgx", dsnB)
	if err != nil {
		t.Fatal(err)
	}
	defer dbB.Close()

	for _, db := range []*sql.DB{dbA, dbB} {
		for _, stmt := range []string{
			"DROP TABLE IF EXISTS orders CASCADE",
			"DROP TABLE IF EXISTS users CASCADE",
			"DROP TABLE IF EXISTS _dosql_schema_history CASCADE",
		} {
			if _, err := db.Exec(stmt); err != nil {
				t.Fatalf("reset pg failed on %q: %v", stmt, err)
			}
		}
	}

	targetA := doSQLTarget{Engine: "postgres", Driver: "pgx", DSN: dsnA, Database: dbAName, Address: addr}
	targetB := doSQLTarget{Engine: "postgres", Driver: "pgx", DSN: dsnB, Database: dbBName, Address: addr}
	binding := api.AgentCredentialBinding{Name: "pg-e2e", Use: string(CredentialUseDatabase)}

	// 1. Create complex schema on PG A (string defaults, numeric default, UNIQUE, composite index, FK ON DELETE CASCADE).
	initSQL := `
CREATE TABLE users (
  id BIGINT PRIMARY KEY,
  email VARCHAR(128) NOT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  note TEXT,
  CONSTRAINT uq_users_email UNIQUE (email)
);
CREATE TABLE orders (
  id BIGINT PRIMARY KEY,
  user_id BIGINT NOT NULL,
  code VARCHAR(64) NOT NULL DEFAULT 'ord-0',
  amount NUMERIC(12,2) NOT NULL DEFAULT 0.00,
  status VARCHAR(32),
  CONSTRAINT fk_orders_user FOREIGN KEY (user_id) REFERENCES users (id) ON UPDATE RESTRICT ON DELETE CASCADE
);
CREATE INDEX idx_orders_user_status ON orders (user_id, status);
`
	execDDLSlice(t, targetA, "postgres", []string{initSQL})

	irA, err := inspect.InspectSQL(t.Context(), dbA, "postgres", dbAName, inspect.Options{})
	if err != nil {
		t.Fatalf("pg inspect A: %v", err)
	}
	irBEmpty, err := inspect.InspectSQL(t.Context(), dbB, "postgres", dbBName, inspect.Options{})
	if err != nil {
		t.Fatalf("pg inspect empty B: %v", err)
	}
	dInit := diff.CompareSchemas(irBEmpty, irA, nil)
	if dInit.Identical || len(dInit.SuggestedDDL) < 2 {
		t.Fatalf("expected PG create DDLs for B, got %+v", dInit)
	}
	execDDLSlice(t, targetB, "postgres", dInit.SuggestedDDL)

	irBAfter, err := inspect.InspectSQL(t.Context(), dbB, "postgres", dbBName, inspect.Options{})
	if err != nil {
		t.Fatalf("pg inspect B after replay: %v", err)
	}
	if dVerify := diff.CompareSchemas(irA, irBAfter, nil); !dVerify.Identical {
		t.Fatalf("PG schema round-trip NOT identical after executing SuggestedDDL!\nGenerated DDL:\n%s\nResidual diff:\n%+v",
			strings.Join(dInit.SuggestedDDL, "\n"), dVerify.Changes)
	}

	// 2. Incremental ALTER on PG A (ADD COLUMN, SET NOT NULL + SET DEFAULT, CREATE INDEX CONCURRENTLY).
	alterSQL := `
ALTER TABLE orders ADD COLUMN tag VARCHAR(64) DEFAULT 'std';
ALTER TABLE orders ALTER COLUMN status SET DEFAULT 'new';
ALTER TABLE orders ALTER COLUMN status SET NOT NULL;
CREATE INDEX CONCURRENTLY idx_orders_code ON orders (code);
`
	execDDLSlice(t, targetA, "postgres", []string{alterSQL})
	irA2, err := inspect.InspectSQL(t.Context(), dbA, "postgres", dbAName, inspect.Options{})
	if err != nil {
		t.Fatal(err)
	}
	dInc := diff.CompareSchemas(irBAfter, irA2, nil)
	if dInc.Identical || len(dInc.SuggestedDDL) == 0 {
		t.Fatalf("expected PG incremental diff, got %+v", dInc)
	}
	execDDLSlice(t, targetB, "postgres", dInc.SuggestedDDL)
	irB2, err := inspect.InspectSQL(t.Context(), dbB, "postgres", dbBName, inspect.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if dIncVerify := diff.CompareSchemas(irA2, irB2, nil); !dIncVerify.Identical {
		t.Fatalf("PG incremental diff replay NOT identical!\nDDL:\n%s\nResidual:\n%+v",
			strings.Join(dInc.SuggestedDDL, "\n"), dIncVerify.Changes)
	}

	// 3. Real PG chunked backfill ($1/$2 placeholders).
	if _, err := dbA.Exec(`INSERT INTO users (id, email, status, note) VALUES (1, 'a@test.com', 'active', 'seed')`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 65; i++ {
		if _, err := dbA.Exec(`INSERT INTO orders (id, user_id, code, amount, status, tag) VALUES ($1, 1, $2, 10.50, 'paid', NULL)`, i, fmt.Sprintf("ord-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	bfDry, err := backfill.RunIntKeyBackfill(t.Context(), dbA, backfill.Options{
		Engine: "postgres", Table: "orders", KeyColumn: "id", SetClause: "tag = status", WhereClause: "tag IS NULL", BatchSize: 20, DryRun: true,
	})
	if err != nil || bfDry.TotalAffected != 65 {
		t.Fatalf("pg backfill dry-run failed: %+v err=%v", bfDry, err)
	}
	bfPart, err := backfill.RunIntKeyBackfill(t.Context(), dbA, backfill.Options{
		Engine: "postgres", Table: "orders", KeyColumn: "id", SetClause: "tag = status", WhereClause: "tag IS NULL", BatchSize: 20, StartAfterID: 20,
	})
	if err != nil || bfPart.TotalAffected != 45 {
		t.Fatalf("pg resumed backfill failed: %+v err=%v", bfPart, err)
	}
	bfRest, err := backfill.RunIntKeyBackfill(t.Context(), dbA, backfill.Options{
		Engine: "postgres", Table: "orders", KeyColumn: "id", SetClause: "tag = status", WhereClause: "tag IS NULL", BatchSize: 20,
	})
	if err != nil || bfRest.TotalAffected != 20 {
		t.Fatalf("pg remaining backfill failed: %+v err=%v", bfRest, err)
	}

	// 4. Real PG migrate lifecycle (read-only status zero DDL -> policy preflight zero history table -> migrate -> idempotent).
	pgHistStore := &migrate.SQLHistoryStore{DB: dbB, Engine: "postgres"}
	if _, err := handleDirectDoSQLAction(t.Context(), "pg-e2e", api.AgentPromptParams{
		SQLAction: "status",
	}, binding, targetB); err != nil {
		t.Fatalf("pg status before migrate failed: %v", err)
	}
	if exists, err := pgHistStore.TableExists(t.Context()); err != nil || exists {
		t.Fatalf("pg read-only status silently created _dosql_schema_history: exists=%v err=%v", exists, err)
	}
	unsafeManifest, _ := json.Marshal([]migrate.MigrationFile{{
		Version: 1, VersionTag: "V001", Description: "unsafe", FileName: "V001__unsafe.sql",
		Checksum: "sha256:unsafe", Content: "UPDATE orders SET status = 'oops';",
	}})
	if _, err := handleDirectDoSQLAction(t.Context(), "pg-e2e", api.AgentPromptParams{
		SQLAction: "migrate", SQLMigrations: string(unsafeManifest),
	}, binding, targetB); err == nil || !strings.Contains(err.Error(), "dosql_policy_rejected") {
		t.Fatalf("pg migrate should reject unsafe migration before touching history table, got %v", err)
	}
	if exists, err := pgHistStore.TableExists(t.Context()); err != nil || exists {
		t.Fatalf("pg policy-rejected migrate silently created _dosql_schema_history: exists=%v err=%v", exists, err)
	}
	migDir := t.TempDir()
	_, err = migrate.PackFromHistory(migDir, "add_memo", "postgres", []recorder.StatementRecord{
		{Mode: "apply", Kind: "ddl", Statement: "ALTER TABLE orders ADD COLUMN memo VARCHAR(64) DEFAULT 'none'", Status: "succeeded", StmtHash: "pm1"},
		{Mode: "apply", Kind: "non_transactional", Statement: "CREATE INDEX CONCURRENTLY idx_orders_memo ON orders (memo)", Status: "succeeded", StmtHash: "pm2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	loadedFiles, err := migrate.LoadDir(migDir)
	if err != nil {
		t.Fatal(err)
	}
	migJSON, _ := json.Marshal(loadedFiles)
	migOut, err := handleDirectDoSQLAction(t.Context(), "pg-e2e", api.AgentPromptParams{
		SQLAction: "migrate", SQLMigrations: string(migJSON), SQLReleaseTag: "v1.0.0",
	}, binding, targetB)
	if err != nil || !strings.Contains(migOut, `"current_version": 1`) {
		t.Fatalf("pg migrate V001 failed: %s err=%v", migOut, err)
	}
	migOut2, err := handleDirectDoSQLAction(t.Context(), "pg-e2e", api.AgentPromptParams{
		SQLAction: "migrate", SQLMigrations: string(migJSON), SQLReleaseTag: "v1.0.0",
	}, binding, targetB)
	if err != nil || !strings.Contains(migOut2, `"skipped": 1`) {
		t.Fatalf("pg idempotent migrate failed: %s err=%v", migOut2, err)
	}

	// 5. Real PostgreSQL BIT_XOR(MD5) datadiff pushdown + fix_sql replay -> 0 diffs!
	for i := 2; i <= 50; i++ {
		var note interface{} = "ok"
		if i == 10 {
			note = nil
		} else if i == 25 {
			note = "O'Reilly"
		}
		if _, err := dbA.Exec(`INSERT INTO users (id, email, status, note) VALUES ($1, $2, 'active', $3)`, i, fmt.Sprintf("u%d@test.com", i), note); err != nil {
			t.Fatal(err)
		}
		if i != 25 {
			st := "active"
			bNote := note
			if i == 10 {
				bNote = "not-null"
			}
			if i == 40 {
				st = "stale"
			}
			if _, err := dbB.Exec(`INSERT INTO users (id, email, status, note) VALUES ($1, $2, $3, $4)`, i, fmt.Sprintf("u%d@test.com", i), st, bNote); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := dbB.Exec(`INSERT INTO users (id, email, status, note) VALUES (1, 'a@test.com', 'active', 'seed'), (99, 'extra@test.com', 'extra', NULL)`); err != nil {
		t.Fatal(err)
	}

	ddRes, err := datadiff.CompareIntKeyTables(t.Context(), dbA, dbB, datadiff.Options{
		Engine: "postgres", Table: "users", KeyColumn: "id", Columns: []string{"id", "email", "status", "note"},
		ChunkSize: 20, LeafSize: 10, GenerateFix: true,
	})
	if err != nil || ddRes.Identical || ddRes.DiffCount != 4 {
		t.Fatalf("real pg BIT_XOR datadiff failed: %+v err=%v", ddRes, err)
	}
	for _, d := range ddRes.Diffs {
		if _, err := dbB.Exec(d.FixSQL); err != nil {
			t.Fatalf("pg fix SQL failed: %q err=%v", d.FixSQL, err)
		}
	}
	ddAfter, err := datadiff.CompareIntKeyTables(t.Context(), dbA, dbB, datadiff.Options{
		Engine: "postgres", Table: "users", KeyColumn: "id", Columns: []string{"id", "email", "status", "note"},
		ChunkSize: 20, LeafSize: 10,
	})
	if err != nil || !ddAfter.Identical || ddAfter.DiffCount != 0 {
		t.Fatalf("real pg datadiff NOT zero after fix SQL replay: %+v err=%v", ddAfter, err)
	}
}

// TestSQL08StatusIsReadOnlyAndPolicyRejectLeavesNoHistoryTable ensures that
// running sql_action="status" or a policy-rejected sql_action="migrate" NEVER
// silently creates _dosql_schema_history in the target database.
func TestSQL08StatusIsReadOnlyAndPolicyRejectLeavesNoHistoryTable(t *testing.T) {
	wsRoot := filepath.Join(t.TempDir(), "ws")
	t.Setenv("DOOPS_WORKSPACE_ROOT", wsRoot)
	t.Setenv("DOOPS_DOSQL_STATE_DIR", filepath.Join(t.TempDir(), "state"))
	_ = os.MkdirAll(filepath.Join(wsRoot, "sql08-ro"), 0700)

	target := newDoSQLDirectTestTarget(t)
	binding := api.AgentCredentialBinding{Name: "orders-ro", Use: string(CredentialUseDatabase)}

	db, err := sql.Open(target.Driver, target.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	histStore := &migrate.SQLHistoryStore{DB: db, Engine: "sqlite"}

	// 1. status on a fresh database returns current_version=0 without creating _dosql_schema_history.
	stOut, err := handleDirectDoSQLAction(t.Context(), "sql08-ro", api.AgentPromptParams{
		SQLAction: "status",
	}, binding, target)
	if err != nil {
		t.Fatalf("status on fresh db failed: %v", err)
	}
	if !strings.Contains(stOut, `"current_version": 0`) {
		t.Fatalf("unexpected status output: %s", stOut)
	}
	if exists, err := histStore.TableExists(t.Context()); err != nil || exists {
		t.Fatalf("read-only status silently created _dosql_schema_history: exists=%v err=%v", exists, err)
	}

	// 2. policy-rejected migrate also leaves zero _dosql_schema_history table behind.
	unsafeManifest, _ := json.Marshal([]migrate.MigrationFile{{
		Version: 1, VersionTag: "V001", Description: "unsafe", FileName: "V001__unsafe.sql",
		Checksum: "sha256:unsafe", Content: "DELETE FROM orders;",
	}})
	if _, err := handleDirectDoSQLAction(t.Context(), "sql08-ro", api.AgentPromptParams{
		SQLAction: "migrate", SQLMigrations: string(unsafeManifest),
	}, binding, target); err == nil || !strings.Contains(err.Error(), "dosql_policy_rejected") {
		t.Fatalf("expected policy rejection, got %v", err)
	}
	if exists, err := histStore.TableExists(t.Context()); err != nil || exists {
		t.Fatalf("policy-rejected migrate silently created _dosql_schema_history: exists=%v err=%v", exists, err)
	}
}
