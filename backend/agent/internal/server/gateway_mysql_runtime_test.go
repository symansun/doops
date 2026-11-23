package server

import (
	"context"
	"database/sql"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

type gatewayMySQLRuntimeSQLLiteral struct {
	file  string
	line  int
	query string
	// funcName is the enclosing top-level declaration: a function or method
	// name, or "" for a package-level var/const. Guards that need to tell a
	// one-shot migration statement from a statement on the live path (the
	// transient *_v2 tables) branch on it.
	funcName string
}

func TestGatewayMySQLRuntimeSQLAudit(t *testing.T) {
	literals := gatewayMySQLRuntimeSQLLiterals(t)
	counts := make(map[gatewayMySQLRuntimeIssueCode]int)
	locations := make(map[gatewayMySQLRuntimeIssueCode][]string)
	for _, literal := range literals {
		for _, issue := range gatewayMySQLRuntimeIssues(literal.query) {
			counts[issue.code]++
			locations[issue.code] = append(locations[issue.code], literal.file+":"+strconv.Itoa(literal.line))
		}
	}

	want := map[gatewayMySQLRuntimeIssueCode]int{
		gatewayMySQLIssueNumberedParameter:   4,
		gatewayMySQLIssueOnConflict:          31,
		gatewayMySQLIssueInsertOrIgnore:      4,
		gatewayMySQLIssueUpdateOrIgnore:      1,
		gatewayMySQLIssueJSONEach:            5,
		gatewayMySQLIssueJSONAggregate:       2,
		gatewayMySQLIssueAggregateFilter:     1,
		gatewayMySQLIssueJulianDay:           3,
		gatewayMySQLIssueUnixEpoch:           4,
		gatewayMySQLIssueMaterializedCTE:     1,
		gatewayMySQLIssueSQLiteConcat:        3,
		gatewayMySQLIssueNullSafeParameter:   1,
		gatewayMySQLIssueSQLiteIntrospection: 8,
		gatewayMySQLIssueSQLiteExplainPlan:   13,
		gatewayMySQLIssueCreateIndexIfAbsent: 29,
		gatewayMySQLIssueDropIndexIfAbsent:   2,
		gatewayMySQLIssueAutoIncrement:       8,
		gatewayMySQLIssuePartialIndex:        1,
		gatewayMySQLIssueReservedUse:         12,
	}
	if len(counts) != len(want) {
		t.Fatalf("MySQL runtime audit found issue categories %v, want %v", counts, want)
	}
	for code, expected := range want {
		if counts[code] != expected {
			t.Errorf("MySQL runtime audit found %d %s findings, want %d at %s", counts[code], code, expected, strings.Join(locations[code], ", "))
		}
	}

	for _, rule := range gatewayMySQLRuntimeIssueRules {
		if strings.TrimSpace(rule.description) == "" || strings.TrimSpace(rule.replacement) == "" {
			t.Errorf("MySQL runtime rule %q has no audit explanation", rule.code)
		}
	}
}

func TestGatewayMySQLRuntimeDialectExamples(t *testing.T) {
	tests := []struct {
		name  string
		query string
		code  gatewayMySQLRuntimeIssueCode
	}{
		{name: "numbered parameter", query: "SELECT 1 WHERE id=?3", code: gatewayMySQLIssueNumberedParameter},
		{name: "upsert", query: "INSERT INTO t(id) VALUES(?) ON CONFLICT(id) DO NOTHING", code: gatewayMySQLIssueOnConflict},
		{name: "insert ignore", query: "INSERT OR IGNORE INTO t(id) VALUES(?)", code: gatewayMySQLIssueInsertOrIgnore},
		{name: "update ignore", query: "UPDATE OR IGNORE t SET id=?", code: gatewayMySQLIssueUpdateOrIgnore},
		{name: "JSON table", query: "SELECT key FROM json_each(?)", code: gatewayMySQLIssueJSONEach},
		{name: "JSON aggregate", query: "SELECT json_group_array(value ORDER BY value) FROM t", code: gatewayMySQLIssueJSONAggregate},
		{name: "aggregate filter", query: "SELECT count(*) FILTER (WHERE state='open') FROM t", code: gatewayMySQLIssueAggregateFilter},
		{name: "time", query: "SELECT julianday(started_at) FROM t", code: gatewayMySQLIssueJulianDay},
		{name: "epoch", query: "SELECT unixepoch(started_at) FROM t", code: gatewayMySQLIssueUnixEpoch},
		{name: "materialized", query: "WITH x AS MATERIALIZED (SELECT 1) SELECT * FROM x", code: gatewayMySQLIssueMaterializedCTE},
		{name: "concat", query: "UPDATE t SET error=error || '; ' || ?", code: gatewayMySQLIssueSQLiteConcat},
		{name: "null", query: "SELECT value IS ? FROM t", code: gatewayMySQLIssueNullSafeParameter},
		{name: "introspection", query: "SELECT name FROM sqlite_schema", code: gatewayMySQLIssueSQLiteIntrospection},
		{name: "explain plan", query: "EXPLAIN QUERY PLAN SELECT 1", code: gatewayMySQLIssueSQLiteExplainPlan},
		{name: "create index", query: "CREATE INDEX IF NOT EXISTS ix ON t(value)", code: gatewayMySQLIssueCreateIndexIfAbsent},
		{name: "drop index", query: "DROP INDEX IF EXISTS ix", code: gatewayMySQLIssueDropIndexIfAbsent},
		{name: "auto increment", query: "id INTEGER PRIMARY KEY AUTOINCREMENT", code: gatewayMySQLIssueAutoIncrement},
		{name: "partial index", query: "CREATE UNIQUE INDEX ix ON t(value) WHERE state='open'", code: gatewayMySQLIssuePartialIndex},
		{name: "reserved use", query: "SELECT use FROM credential_materializations", code: gatewayMySQLIssueReservedUse},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			issues := gatewayMySQLRuntimeIssues(tc.query)
			for _, issue := range issues {
				if issue.code == tc.code {
					return
				}
			}
			t.Fatalf("query was not classified as %s", tc.code)
		})
	}
}

func TestGatewayMySQLRuntimeEquivalentSyntax(t *testing.T) {
	db := openGatewayMySQLRuntimeTestDB(t)
	defer db.Close()

	statements := []string{
		`CREATE TABLE runtime_dialect_values (id INT PRIMARY KEY, state VARCHAR(16) NOT NULL, value_text VARCHAR(16), started_at DATETIME(6), UNIQUE KEY runtime_dialect_state (state))`,
		`INSERT INTO runtime_dialect_values (id, state, value_text, started_at) VALUES (?, ?, ?, ?)`,
		`INSERT INTO runtime_dialect_values (id, state, value_text, started_at) VALUES (?, ?, ?, ?) AS new ON DUPLICATE KEY UPDATE value_text = new.value_text`,
		`INSERT INTO runtime_dialect_values (id, state, value_text, started_at) VALUES (?, ?, ?, ?) ON DUPLICATE KEY UPDATE id = id`,
		`UPDATE runtime_dialect_values SET value_text = CONCAT(COALESCE(value_text, ''), ? ) WHERE id = ?`,
		`SELECT SUM(CASE WHEN state = 'open' THEN 1 ELSE 0 END), SUM(CASE WHEN state = 'missing' THEN 1 ELSE 0 END) FROM runtime_dialect_values`,
		`SELECT value_text <=> ? FROM runtime_dialect_values WHERE id = ?`,
		`SELECT COUNT(*) FROM runtime_dialect_values WHERE started_at >= STR_TO_DATE(?, '%Y-%m-%dT%H:%i:%s.%fZ')`,
		`SELECT UNIX_TIMESTAMP(STR_TO_DATE(?, '%Y-%m-%dT%H:%i:%s.%fZ'))`,
		`SELECT jt.item_key FROM JSON_TABLE(?, '$[*]' COLUMNS (item_key VARCHAR(64) PATH '$')) AS jt`,
		`SELECT JSON_ARRAYAGG(value_text) FROM (SELECT value_text FROM runtime_dialect_values ORDER BY value_text) AS ordered_values`,
		`SELECT JSON_ARRAYAGG(JSON_OBJECT('value', value_text)) FROM (SELECT value_text FROM runtime_dialect_values ORDER BY value_text) AS ordered_objects`,
		`CREATE TABLE runtime_dialect_auto (id BIGINT AUTO_INCREMENT PRIMARY KEY)`,
		`CREATE INDEX runtime_dialect_value_index ON runtime_dialect_values(value_text)`,
		`DROP INDEX runtime_dialect_value_index ON runtime_dialect_values`,
		`EXPLAIN FORMAT=JSON SELECT id FROM runtime_dialect_values WHERE value_text = ?`,
		`WITH terminal AS (SELECT id FROM runtime_dialect_values WHERE state = 'open') SELECT COUNT(*) FROM runtime_dialect_values WHERE id IN (SELECT id FROM terminal)`,
		"SELECT `use` FROM runtime_dialect_use",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := [][]any{
		nil,
		{1, "open", "first", "2026-11-23 15:00:00.123456"},
		{1, "closed", "second", "2026-11-23 15:00:00.123456"},
		{1, "closed", "third", "2026-11-23 15:00:00.123456"},
		{"/suffix", 1},
		nil,
		{nil, 1},
		{"2026-11-23T15:00:00.123456789Z"},
		{"2026-11-23T15:00:00.123456789Z"},
		{"[\"a\", \"b\"]"},
		nil,
		nil,
		nil,
		nil,
		nil,
		{"first"},
		nil,
		nil,
	}
	for i, statement := range statements {
		if _, err := db.ExecContext(ctx, statement, args[i]...); err != nil {
			t.Fatalf("MySQL 8.4 rejected equivalent statement %d: %v", i, err)
		}
	}
}

func gatewayMySQLRuntimeSQLLiterals(t *testing.T) []gatewayMySQLRuntimeSQLLiteral {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(currentFile)
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	var literals []gatewayMySQLRuntimeSQLLiteral
	for _, path := range paths {
		base := filepath.Base(path)
		// The native schema, copier, MySQL connection helper, and embedded upgrade
		// shell are separate boundaries. They do not contain Gateway runtime SQL
		// sent through database/sql and are covered by their own tests.
		if strings.HasSuffix(base, "_test.go") || base == "mysql_runtime_dialect.go" || base == "gateway_mysql_copy.go" || base == "gateway_mysql_schema.go" || base == "gateway_store_mysql.go" || base == "self_upgrade_scripts.go" {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", base, err)
		}
		for _, decl := range file.Decls {
			name := ""
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name != nil {
				name = fn.Name.Name
			}
			ast.Inspect(decl, func(node ast.Node) bool {
				literal, ok := node.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(literal.Value)
				if err != nil || !gatewayMySQLLooksLikeSQL(value) {
					return true
				}
				position := fset.Position(literal.Pos())
				literals = append(literals, gatewayMySQLRuntimeSQLLiteral{file: base, line: position.Line, query: value, funcName: name})
				return true
			})
		}
	}
	return literals
}

var gatewayMySQLSQLStart = regexp.MustCompile(`(?i)^\s*(?:SELECT|INSERT|UPDATE|DELETE|WITH|CREATE|ALTER|DROP|PRAGMA)\b`)

func gatewayMySQLLooksLikeSQL(value string) bool {
	if gatewayMySQLSQLStart.MatchString(value) {
		return true
	}
	for _, rule := range gatewayMySQLRuntimeIssueRules {
		switch rule.code {
		case gatewayMySQLIssueSQLiteConcat, gatewayMySQLIssueReservedUse:
			continue
		}
		if rule.pattern.MatchString(value) {
			return true
		}
	}
	return false
}

func openGatewayMySQLRuntimeTestDB(t *testing.T) *sql.DB {
	t.Helper()
	port := strings.TrimSpace(os.Getenv("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT"))
	if port == "" {
		t.Skip("set DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT to run the disposable MySQL 8.4 syntax test")
	}
	addr := net.JoinHostPort("127.0.0.1", port)
	adminConfig := mysql.Config{User: "root", Net: "tcp", Addr: addr, AllowNativePasswords: true, Timeout: 5 * time.Second}
	admin, err := sql.Open("mysql", adminConfig.FormatDSN())
	if err != nil {
		t.Fatalf("open MySQL syntax test connection: %v", err)
	}
	t.Cleanup(func() { admin.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		t.Skipf("disposable MySQL 8.4 server is unavailable: %v", err)
	}
	database := "gateway_runtime_" + strings.ToLower(randomHex(8))
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE `"+database+"`"); err != nil {
		t.Fatalf("create MySQL syntax test database: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = admin.ExecContext(cleanupCtx, "DROP DATABASE `"+database+"`")
	})
	config := mysql.Config{User: "root", Net: "tcp", Addr: addr, DBName: database, AllowNativePasswords: true, Timeout: 5 * time.Second}
	db, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatalf("open MySQL syntax test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping MySQL syntax test database: %v", err)
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE runtime_dialect_use (`use` VARCHAR(16) NOT NULL)"); err != nil {
		t.Fatalf("create reserved-column fixture: %v", err)
	}
	return db
}
