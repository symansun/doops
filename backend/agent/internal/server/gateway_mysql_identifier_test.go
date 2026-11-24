package server

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// TestGatewayMySQLIdentifierRendering covers INT-18 offline: the use column is
// quoted only through the dialect layer, the three credential materialization
// statements render safely for MySQL while the audit still fires on the bare
// SQLite spelling, and the existence guard asks the right catalog.
func TestGatewayMySQLIdentifierRendering(t *testing.T) {
	if got, want := gatewayIdentifierFor(gatewayDialectSQLite, "use"), "use"; got != want {
		t.Errorf("SQLite identifier rendering = %q, want %q", got, want)
	}
	if got, want := gatewayIdentifierFor(gatewayDialectMySQL, "use"), "`use`"; got != want {
		t.Errorf("MySQL identifier rendering = %q, want %q", got, want)
	}
	if got, want := gatewayIdentifierFor(gatewayDialectMySQL, "a.b"), "`a`.`b`"; got != want {
		t.Errorf("MySQL dotted identifier rendering = %q, want %q", got, want)
	}
	if got, want := gatewayIdentifierFor(gatewayDialectMySQL, "we`ird"), "`we``ird`"; got != want {
		t.Errorf("MySQL embedded backtick rendering = %q, want %q", got, want)
	}

	materialization := []struct {
		name   string
		mysql  string
		sqlite string
	}{
		{"prior lookup", credentialMaterializationPriorSQLFor(gatewayDialectMySQL), credentialMaterializationPriorSQLFor(gatewayDialectSQLite)},
		{"cleanup by version", credentialMaterializationCleanedSQLFor(gatewayDialectMySQL, true), credentialMaterializationCleanedSQLFor(gatewayDialectSQLite, true)},
		{"cleanup by credential", credentialMaterializationCleanedSQLFor(gatewayDialectMySQL, false), credentialMaterializationCleanedSQLFor(gatewayDialectSQLite, false)},
	}
	for _, statement := range materialization {
		if !strings.Contains(statement.mysql, "`use`") {
			t.Errorf("%s MySQL rendering does not quote the use column: %s", statement.name, statement.mysql)
		}
		if gatewayMySQLTestHasRuntimeIssue(statement.mysql, gatewayMySQLIssueReservedUse) {
			t.Errorf("%s MySQL rendering still trips the reserved_use audit: %s", statement.name, statement.mysql)
		}
		if !strings.Contains(statement.sqlite, " use ") && !strings.Contains(statement.sqlite, ", use") {
			t.Errorf("%s SQLite rendering does not spell use bare: %s", statement.name, statement.sqlite)
		}
		if !gatewayMySQLTestHasRuntimeIssue(statement.sqlite, gatewayMySQLIssueReservedUse) {
			t.Errorf("%s SQLite rendering no longer trips the reserved_use audit, so the rule was weakened: %s", statement.name, statement.sqlite)
		}
	}
	// The upsert insert text stays SQLite-form; gatewayMySQLUpsertInsert owns the
	// quoting for MySQL, so quoting it twice would produce ``use``.
	// The two upsert literals quote the use column in the SQL text itself, which
	// SQLite also accepts as a quoted identifier; gatewayMySQLUpsertInsert must
	// leave an already-quoted column alone instead of double-quoting it.
	intentMysql := credentialMaterializationIntentUpsertFor(gatewayDialectMySQL)
	intentSqlite := credentialMaterializationIntentUpsertFor(gatewayDialectSQLite)
	for name, rendering := range map[string]string{"mysql": intentMysql.render(gatewayDialectMySQL), "sqlite": intentSqlite.render(gatewayDialectSQLite)} {
		if !strings.Contains(rendering, "`use`") {
			t.Errorf("intent upsert %s rendering does not quote the use column: %s", name, rendering)
		}
		if strings.Contains(rendering, "``use``") {
			t.Errorf("intent upsert %s rendering double-quotes the use column: %s", name, rendering)
		}
	}
	// MySQL never emits a conflict target, so its upsert text is fully quoted. The
	// SQLite text keeps the bare target spelling for ON CONFLICT; SQLite accepts
	// that, and the audit still sees the bare name there rather than in MySQL SQL.
	if gatewayMySQLTestHasRuntimeIssue(intentMysql.render(gatewayDialectMySQL), gatewayMySQLIssueReservedUse) {
		t.Errorf("intent upsert MySQL rendering trips the reserved_use audit: %s", intentMysql.render(gatewayDialectMySQL))
	}
	if !strings.Contains(intentMysql.render(gatewayDialectMySQL), "ON DUPLICATE KEY UPDATE") {
		t.Errorf("intent upsert MySQL rendering = %q, want ON DUPLICATE KEY UPDATE", intentMysql.render(gatewayDialectMySQL))
	}
	// The rule itself still fires on a bare use, so the audit was not widened away.
	if !gatewayMySQLTestHasRuntimeIssue("SELECT 1 FROM t WHERE use = ?", gatewayMySQLIssueReservedUse) {
		t.Errorf("reserved_use audit no longer fires on a bare use column")
	}

	mysqlGuard := gatewayExistingTableCountSQL(gatewayDialectMySQL, 2)
	sqliteGuard := gatewayExistingTableCountSQL(gatewayDialectSQLite, 2)
	if !strings.Contains(mysqlGuard, "information_schema.tables") || !strings.Contains(mysqlGuard, "table_schema = DATABASE()") {
		t.Errorf("MySQL existence guard = %q, want information_schema.tables scoped to DATABASE()", mysqlGuard)
	}
	if strings.Count(mysqlGuard, "?") != 2 || strings.Count(sqliteGuard, "?") != 2 {
		t.Errorf("existence guards do not bind one parameter per table: mysql=%q sqlite=%q", mysqlGuard, sqliteGuard)
	}
	if gatewayMySQLTestHasRuntimeIssue(mysqlGuard, gatewayMySQLIssueSQLiteIntrospection) {
		t.Errorf("MySQL existence guard still trips the sqlite_introspection audit: %s", mysqlGuard)
	}
	if !strings.Contains(sqliteGuard, "sqlite_schema") {
		t.Errorf("SQLite existence guard = %q, want sqlite_schema", sqliteGuard)
	}
	if !gatewayMySQLTestHasRuntimeIssue(sqliteGuard, gatewayMySQLIssueSQLiteIntrospection) {
		t.Errorf("SQLite existence guard no longer trips the sqlite_introspection audit, so the rule was weakened: %s", sqliteGuard)
	}
	// The production wiring must choose the SQLite spelling on the current store.
	if got, want := gatewayUseColumn, gatewayIdentifierFor(gatewayDialectStore, "use"); got != want {
		t.Errorf("production use column = %q, want %q", got, want)
	}
	if !strings.Contains(gatewayExistingTableCountSQL(gatewayDialectStore, 1), "sqlite_schema") {
		t.Errorf("production existence guard does not use the SQLite catalog: %s", gatewayExistingTableCountSQL(gatewayDialectStore, 1))
	}
}

// TestGatewayMySQLMaterializationStatementsExecuteOnMySQL runs the three INT-18
// statements against a real MySQL and an in-memory SQLite with the same rows, and
// requires equal results: the prior lookup finds nothing before the intent, the
// intent upsert updates on conflict, the cleanup update reports one row and the
// surviving row is byte-identical on both sides.
func TestGatewayMySQLMaterializationStatementsExecuteOnMySQL(t *testing.T) {
	mysqlDB := gatewayMySQLTestOpenEmpty(t)
	sqliteDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open the SQLite reference database: %v", err)
	}
	sqliteDB.SetMaxOpenConns(1)
	defer sqliteDB.Close()
	ctx := context.Background()

	dialects := map[string]*sql.DB{"mysql": mysqlDB, "sqlite": sqliteDB}
	names := []string{"mysql", "sqlite"}
	final := map[string]string{}
	guards := map[string]int{}
	for _, name := range names {
		dialect := gatewaySQLDialect(name)
		db := dialects[name]
		// The fixture quotes with a literal, not the helper under test, so a
		// broken helper cannot make its own fixture pass.
		use := "use"
		if name == "mysql" {
			use = "`use`"
		}
		// The statements under test name the production table, so the fixture
		// uses the same name inside this test's isolated MySQL database and
		// in-memory SQLite database.
		create := "CREATE TABLE credential_materializations (id VARCHAR(64) PRIMARY KEY, credential_id VARCHAR(64) NOT NULL, version_id VARCHAR(64) NOT NULL, grant_id VARCHAR(64) NOT NULL, " + use + " VARCHAR(64) NOT NULL, context_key VARCHAR(64) NOT NULL, request_json TEXT NOT NULL, evidence_json TEXT NOT NULL, status VARCHAR(16) NOT NULL, error_category VARCHAR(64) NOT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL)"
		statements := []string{
			create,
			"CREATE UNIQUE INDEX int18_scope ON credential_materializations (version_id, grant_id, " + use + ", context_key)",
			"CREATE TABLE int18_present_a (id BIGINT PRIMARY KEY)",
			"CREATE TABLE int18_present_b (id BIGINT PRIMARY KEY)",
		}
		for _, statement := range statements {
			if _, err := db.ExecContext(ctx, statement); err != nil {
				t.Fatalf("%s: %s: %v", name, statement, err)
			}
		}

		prior := credentialMaterializationPriorSQLFor(dialect)
		var status, category, request, evidence string
		err := db.QueryRowContext(ctx, prior, "v1", "g1", "db", "ctx").Scan(&status, &category, &request, &evidence)
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("%s: prior lookup before the intent = %v, want sql.ErrNoRows", name, err)
		}

		upsert := credentialMaterializationIntentUpsertFor(dialect).render(dialect)
		insert := func(id string, request string, stamp int64) {
			t.Helper()
			if _, err := db.ExecContext(ctx, upsert, id, "cred1", "v1", "g1", "db", "ctx", request, stamp, stamp); err != nil {
				t.Fatalf("%s: intent upsert %s: %v\n%s", name, id, err, upsert)
			}
		}
		insert("m1", "{\"req\":1}", 100)
		insert("m2", "{\"req\":2}", 200)

		err = db.QueryRowContext(ctx, prior, "v1", "g1", "db", "ctx").Scan(&status, &category, &request, &evidence)
		if err != nil {
			t.Fatalf("%s: prior lookup after the intent: %v", name, err)
		}
		if status != "pending" || category != "" || request != "{\"req\":2}" || evidence != "{}" {
			t.Errorf("%s: prior lookup after the conflict = (%q, %q, %q, %q), want (pending, \"\", {\"req\":2}, {})", name, status, category, request, evidence)
		}

		cleanup := credentialMaterializationCleanedSQLFor(dialect, true)
		result, err := db.ExecContext(ctx, cleanup, 300, "v1", "g1", "db", "ctx")
		if err != nil {
			t.Fatalf("%s: cleanup update: %v\n%s", name, err, cleanup)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			t.Fatalf("%s: cleanup update affected %d rows (err %v), want 1", name, affected, err)
		}

		read := "SELECT id, status, error_category, request_json, evidence_json FROM credential_materializations WHERE version_id = ? AND grant_id = ? AND " + use + " = ? AND context_key = ?"
		var id string
		if err := db.QueryRowContext(ctx, read, "v1", "g1", "db", "ctx").Scan(&id, &status, &category, &request, &evidence); err != nil {
			t.Fatalf("%s: read back the surviving row: %v", name, err)
		}
		final[name] = id + "|" + status + "|" + category + "|" + request + "|" + evidence

		guard := gatewayExistingTableCountSQL(dialect, 2)
		var count int
		if err := db.QueryRowContext(ctx, guard, "int18_present_a", "int18_present_b").Scan(&count); err != nil {
			t.Fatalf("%s: existence guard: %v\n%s", name, err, guard)
		}
		if count != 2 {
			t.Errorf("%s: existence guard counted %d tables, want 2", name, count)
		}
		if _, err := db.ExecContext(ctx, "DROP TABLE int18_present_b"); err != nil {
			t.Fatalf("%s: drop the second fixture table: %v", name, err)
		}
		if err := db.QueryRowContext(ctx, guard, "int18_present_a", "int18_present_b").Scan(&count); err != nil {
			t.Fatalf("%s: existence guard after the drop: %v", name, err)
		}
		if count != 1 {
			t.Errorf("%s: existence guard counted %d tables after the drop, want 1 (callers compare == n, so a missing table fails closed)", name, count)
		}
		guards[name] = count
	}

	if final["mysql"] != final["sqlite"] {
		t.Errorf("MySQL and SQLite disagree on the surviving row: mysql=%q sqlite=%q", final["mysql"], final["sqlite"])
	}
	if want := "m2|cleaned||{\"req\":2}|{}"; final["mysql"] != want {
		t.Errorf("surviving row = %q, want %q", final["mysql"], want)
	}
	if guards["mysql"] != guards["sqlite"] {
		t.Errorf("existence guards disagree: mysql=%d sqlite=%d", guards["mysql"], guards["sqlite"])
	}
}
