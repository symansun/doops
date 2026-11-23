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

	// gatewayMySQLIssueOnConflict is absent on purpose: runtime upserts no longer
	// carry ON CONFLICT because they render through gatewayUpsertSQL. The rule
	// still fires for a raw literal (see the upsert sample in the dialect table)
	// and TestGatewayMySQLUpsertsRenderForBothDialects proves the MySQL rendering
	// is clean.
	want := map[gatewayMySQLRuntimeIssueCode]int{
		gatewayMySQLIssueNumberedParameter:   4,
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

// --- INT-13: runtime upsert translation ---

type gatewayMySQLUpsertSite struct {
	file   string
	line   int
	upsert gatewayUpsert
}

func (site gatewayMySQLUpsertSite) label() string {
	return site.file + ":" + strconv.Itoa(site.line)
}

// gatewayMySQLUpsertSites reads the package source and returns every
// gatewayUpsert composite literal, so the guard tests exercise the exact
// specifications the store uses instead of a copy that can drift.
func gatewayMySQLUpsertSites(t *testing.T) []gatewayMySQLUpsertSite {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	sites := make([]gatewayMySQLUpsertSite, 0, 32)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			lit, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			ident, ok := lit.Type.(*ast.Ident)
			if !ok || ident.Name != "gatewayUpsert" {
				return true
			}
			site := gatewayMySQLUpsertSite{file: name, line: fset.Position(lit.Pos()).Line}
			for _, element := range lit.Elts {
				keyValue, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := keyValue.Key.(*ast.Ident)
				if !ok {
					continue
				}
				switch key.Name {
				case "insert":
					site.upsert.insert = gatewayMySQLUpsertStringLiteral(t, keyValue.Value)
				case "target":
					site.upsert.target = gatewayMySQLUpsertStringSlice(t, keyValue.Value)
				case "doNothing":
					value, ok := keyValue.Value.(*ast.Ident)
					if !ok || value.Name != "true" {
						t.Fatalf("%s: doNothing must be the literal true", site.label())
					}
					site.upsert.doNothing = true
				case "set":
					site.upsert.set = gatewayMySQLUpsertStringSlice(t, keyValue.Value)
				case "where":
					site.upsert.where = gatewayMySQLUpsertStringLiteral(t, keyValue.Value)
				}
			}
			if strings.TrimSpace(site.upsert.insert) == "" {
				t.Fatalf("%s: gatewayUpsert without an insert statement", site.label())
			}
			sites = append(sites, site)
			return true
		})
	}
	sort.Slice(sites, func(left, right int) bool {
		if sites[left].file != sites[right].file {
			return sites[left].file < sites[right].file
		}
		return sites[left].line < sites[right].line
	})
	return sites
}

func gatewayMySQLUpsertStringLiteral(t *testing.T, expression ast.Expr) string {
	t.Helper()
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		t.Fatalf("gatewayUpsert field is not a string literal: %T", expression)
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		t.Fatalf("gatewayUpsert field is not a quoted string: %v", err)
	}
	return value
}

func gatewayMySQLUpsertStringSlice(t *testing.T, expression ast.Expr) []string {
	t.Helper()
	literal, ok := expression.(*ast.CompositeLit)
	if !ok {
		t.Fatalf("gatewayUpsert field is not a string slice literal: %T", expression)
	}
	values := make([]string, 0, len(literal.Elts))
	for _, element := range literal.Elts {
		values = append(values, gatewayMySQLUpsertStringLiteral(t, element))
	}
	return values
}

func TestGatewayMySQLUpsertsRenderForBothDialects(t *testing.T) {
	sites := gatewayMySQLUpsertSites(t)
	if len(sites) == 0 {
		t.Fatal("no gatewayUpsert sites found; runtime upserts must not go back to raw ON CONFLICT literals")
	}
	for _, site := range sites {
		t.Run(site.label(), func(t *testing.T) {
			sqlite := site.upsert.render(gatewayDialectSQLite)
			mysql := site.upsert.render(gatewayDialectMySQL)
			if !strings.Contains(sqlite, " ON CONFLICT") {
				t.Fatalf("SQLite rendering lost ON CONFLICT: %s", sqlite)
			}
			if len(site.upsert.target) > 0 {
				want := "ON CONFLICT(" + strings.Join(site.upsert.target, ",") + ")"
				if !strings.Contains(sqlite, want) {
					t.Fatalf("SQLite rendering lost the conflict target %q: %s", want, sqlite)
				}
			}
			if strings.Contains(sqlite, "ON DUPLICATE KEY") || strings.Contains(sqlite, "VALUES(\x60") {
				t.Fatalf("SQLite rendering leaked MySQL syntax: %s", sqlite)
			}
			for _, assignment := range site.upsert.set {
				_, expression, _ := strings.Cut(assignment, "=")
				if strings.Contains(strings.ToLower(expression), "excluded.") && !strings.Contains(sqlite, "excluded.") {
					t.Fatalf("SQLite rendering lost the excluded pseudo table: %s", sqlite)
				}
			}
			if !strings.Contains(mysql, " ON DUPLICATE KEY UPDATE ") {
				t.Fatalf("MySQL rendering did not translate the conflict clause: %s", mysql)
			}
			if strings.Contains(mysql, "ON CONFLICT") || strings.Contains(mysql, "excluded.") || strings.Contains(mysql, "excluded .") {
				t.Fatalf("MySQL rendering kept SQLite-only syntax: %s", mysql)
			}
			if strings.Contains(mysql, " AS doops_new") || strings.Contains(mysql, " doops_new.") {
				t.Fatalf("MySQL rendering used a row alias, which makes bare columns in the filter ambiguous: %s", mysql)
			}
			if strings.Count(sqlite, "?") != strings.Count(mysql, "?") {
				t.Fatalf("MySQL rendering changed the placeholder count:\n sqlite: %s\n mysql: %s", sqlite, mysql)
			}
			if site.upsert.doNothing && len(site.upsert.set) > 0 {
				t.Fatalf("gatewayUpsert sets assignments and DO NOTHING together")
			}
			assignments := gatewayMySQLUpsertAssignments(site.upsert)
			if len(assignments) != len(site.upsert.set) {
				t.Fatalf("MySQL rendering dropped assignments: %d of %d", len(assignments), len(site.upsert.set))
			}
			if site.upsert.where != "" {
				if !strings.Contains(mysql, "IF(") {
					t.Fatalf("MySQL rendering dropped the SQLite-only filter: %s", mysql)
				}
				for _, assignment := range assignments {
					if !strings.Contains(assignment, " = IF(") {
						t.Fatalf("a filtered update must leave the stored row untouched on a false predicate, so every assignment needs IF(): %s", assignment)
					}
				}
				first := ""
				for _, item := range site.upsert.set {
					name := strings.TrimSpace(strings.SplitN(item, "=", 2)[0])
					if !gatewayMySQLUpsertMentions(site.upsert.where, name) {
						first = name
						break
					}
				}
				if first != "" && !strings.HasPrefix(assignments[0], gatewayMySQLIdentifier(first)+" = IF(") {
					t.Fatalf("columns the predicate does not mention must be assigned before the predicate columns: %s", assignments[0])
				}
			}
			for _, issue := range gatewayMySQLRuntimeIssues(mysql) {
				if issue.code == gatewayMySQLIssueOnConflict || issue.code == gatewayMySQLIssueReservedUse {
					t.Fatalf("MySQL rendering still trips the %s audit: %s", issue.code, mysql)
				}
			}
		})
	}
}

func gatewayMySQLUpsertTable(upsert gatewayUpsert) string {
	match := gatewayMySQLUpsertInsertHead.FindStringSubmatch(strings.TrimSpace(upsert.insert))
	if match == nil {
		return ""
	}
	return strings.TrimSpace(match[2])
}

func gatewayMySQLUpsertSameColumns(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[string]int, len(left))
	for _, column := range left {
		counts[column]++
	}
	for _, column := range right {
		counts[column]--
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}

// gatewayMySQLTestUniqueKeys loads the frozen export into SQLite and returns
// the primary key and unique key columns of every table.
func gatewayMySQLTestUniqueKeys(t *testing.T) (map[string][][]string, map[string]bool) {
	t.Helper()
	data := gatewayMySQLTestExportText(t)
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	var tables, uniqueIndexes []string
	for _, statement := range strings.Split(data, ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		upper := strings.ToUpper(statement)
		switch {
		case strings.HasPrefix(upper, "CREATE TABLE"):
			tables = append(tables, statement)
		case strings.HasPrefix(upper, "CREATE UNIQUE INDEX"):
			uniqueIndexes = append(uniqueIndexes, statement)
		}
	}
	pending := tables
	for len(pending) > 0 {
		var failed []string
		progress := false
		for _, statement := range pending {
			if _, err := db.Exec(statement); err != nil {
				failed = append(failed, statement)
				continue
			}
			progress = true
		}
		if !progress {
			t.Fatalf("cannot load export tables: %d remaining", len(failed))
		}
		pending = failed
	}
	names := make(map[string]bool)
	keys := make(map[string][][]string)
	seen := make(map[string]map[string]bool)
	addKey := func(table string, columns []string) {
		if len(columns) == 0 {
			return
		}
		signature := strings.Join(columns, ",")
		if seen[table] == nil {
			seen[table] = make(map[string]bool)
		}
		if seen[table][signature] {
			return
		}
		seen[table][signature] = true
		keys[table] = append(keys[table], columns)
	}
	rows, err := db.Query("SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		names[name] = true
	}
	rows.Close()
	for table := range names {
		type primaryColumn struct {
			position int
			name     string
		}
		var primary []primaryColumn
		infoRows, err := db.Query("PRAGMA table_info(" + gatewayMySQLIdentifier(table) + ")")
		if err != nil {
			t.Fatal(err)
		}
		for infoRows.Next() {
			var cid, notNull, position int
			var name, columnType string
			var defaultValue any
			if err := infoRows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &position); err != nil {
				infoRows.Close()
				t.Fatal(err)
			}
			if position > 0 {
				primary = append(primary, primaryColumn{position: position, name: name})
			}
		}
		infoRows.Close()
		sort.Slice(primary, func(left, right int) bool { return primary[left].position < primary[right].position })
		columns := make([]string, 0, len(primary))
		for _, column := range primary {
			columns = append(columns, column.name)
		}
		addKey(table, columns)
	}
	for _, statement := range uniqueIndexes {
		rest := strings.TrimSpace(statement[len("CREATE UNIQUE INDEX"):])
		fields := strings.Fields(rest)
		if len(fields) < 3 || strings.ToUpper(fields[1]) != "ON" {
			t.Fatalf("cannot parse unique index statement: %s", statement)
		}
		table := strings.Trim(fields[2], "\"\x60[]")
		if cut := strings.Index(table, "("); cut >= 0 {
			table = table[:cut]
		}
		columns, ok := gatewayMySQLTestParenColumns(rest, strings.Index(rest, "("))
		if !ok {
			t.Fatalf("cannot parse unique index columns: %s", statement)
		}
		addKey(table, columns)
	}
	for _, statement := range tables {
		table := gatewayMySQLTestCreatedTable(statement)
		if table == "" {
			continue
		}
		upper := strings.ToUpper(statement)
		for offset := 0; offset < len(upper); {
			found := strings.Index(upper[offset:], "UNIQUE")
			if found < 0 {
				break
			}
			position := offset + found + len("UNIQUE")
			trimmed := strings.TrimLeft(statement[position:], " \t\n")
			if !strings.HasPrefix(trimmed, "(") {
				offset = position
				continue
			}
			open := position + len(statement[position:]) - len(trimmed)
			columns, ok := gatewayMySQLTestParenColumns(statement, open)
			if !ok {
				t.Fatalf("cannot parse inline unique constraint: %s", statement)
			}
			addKey(table, columns)
			offset = open + 1
		}
	}
	return keys, names
}

func gatewayMySQLTestCreatedTable(statement string) string {
	rest := strings.TrimSpace(statement[len("CREATE TABLE"):])
	upper := strings.ToUpper(rest)
	if strings.HasPrefix(upper, "IF NOT EXISTS") {
		rest = strings.TrimSpace(rest[len("IF NOT EXISTS"):])
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[0], "\"\x60[]")
}

func gatewayMySQLTestParenColumns(text string, open int) ([]string, bool) {
	if open < 0 || open >= len(text) || text[open] != '(' {
		return nil, false
	}
	close := strings.Index(text[open:], ")")
	if close < 0 {
		return nil, false
	}
	inner := text[open+1 : open+close]
	columns := make([]string, 0, 4)
	for _, entry := range strings.Split(inner, ",") {
		fields := strings.Fields(strings.TrimSpace(entry))
		if len(fields) == 0 {
			continue
		}
		column := strings.Trim(fields[0], "\"\x60[]")
		if column != "" {
			columns = append(columns, column)
		}
	}
	return columns, true
}

// gatewayMySQLUpsertResidualKeys freezes the sites whose SQLite conflict target
// names one unique key while the table has another. MySQL runs ON DUPLICATE KEY
// UPDATE for any unique key, so a collision on the other key would update a row
// where SQLite raises. Every entry must explain why that collision cannot happen;
// adding one is a reviewed decision, not a way to silence a failure.
var gatewayMySQLUpsertResidualKeys = map[string]string{
	"credential_grants":           "the primary key id is a random identifier minted per statement, so a duplicate id is unreachable",
	"credential_materializations": "the primary key id is a random identifier minted per statement, so a duplicate id is unreachable",
	"credential_verifications":    "the primary key id is a random identifier minted per statement, so a duplicate id is unreachable",
	"grants":                      "the primary key id is assigned by AUTO_INCREMENT and never supplied by the insert, so it cannot collide",
}

func TestGatewayMySQLUpsertConflictTargetsMatchUniqueKeys(t *testing.T) {
	keys, tables := gatewayMySQLTestUniqueKeys(t)
	seenResidual := make(map[string]bool)
	for _, site := range gatewayMySQLUpsertSites(t) {
		table := gatewayMySQLUpsertTable(site.upsert)
		if table == "" {
			t.Fatalf("%s: cannot name the target table", site.label())
		}
		if !tables[table] {
			// grants_v2 exists only inside its migration, which declares
			// UNIQUE(user_id, cluster, instance); the copy path skips transient tables.
			if table != "grants_v2" {
				t.Fatalf("%s: table %s is absent from the frozen export", site.label(), table)
			}
			continue
		}
		if len(site.upsert.target) == 0 {
			// DO NOTHING without a target is a no-op on any key in both dialects.
			if !site.upsert.doNothing {
				t.Fatalf("%s: DO UPDATE needs an explicit conflict target", site.label())
			}
			continue
		}
		tableKeys := keys[table]
		matched := false
		var residual [][]string
		for _, tableKey := range tableKeys {
			if gatewayMySQLUpsertSameColumns(tableKey, site.upsert.target) {
				matched = true
				continue
			}
			residual = append(residual, tableKey)
		}
		if !matched {
			t.Fatalf("%s: conflict target %v is not a unique key of %s (keys: %v)", site.label(), site.upsert.target, table, tableKeys)
		}
		if len(residual) == 0 {
			continue
		}
		reason, frozen := gatewayMySQLUpsertResidualKeys[table]
		if !frozen || strings.TrimSpace(reason) == "" {
			t.Fatalf("%s: %s has extra unique keys %v that MySQL would also conflict on; explain why that collision is unreachable", site.label(), table, residual)
		}
		seenResidual[table] = true
	}
	for table := range gatewayMySQLUpsertResidualKeys {
		if !seenResidual[table] {
			t.Fatalf("gatewayMySQLUpsertResidualKeys lists %s but no site needs it; drop the stale entry", table)
		}
	}
}

type gatewayMySQLTestColumn struct {
	name          string
	dataType      string
	columnType    string
	nullable      bool
	maxLength     int64
	autoIncrement bool
}

// gatewayMySQLTestDropChecks removes CHECK constraints from the disposable
// schema. The fixtures below generate type-correct values, not business-valid
// ones, and a CHECK verdict says nothing about whether ON DUPLICATE KEY UPDATE
// matches ON CONFLICT.
func gatewayMySQLTestDropChecks(t *testing.T, ctx context.Context, db *sql.DB) int {
	t.Helper()
	rows, err := db.QueryContext(ctx, "SELECT tc.TABLE_NAME, cc.CONSTRAINT_NAME FROM information_schema.TABLE_CONSTRAINTS tc JOIN information_schema.CHECK_CONSTRAINTS cc ON cc.CONSTRAINT_SCHEMA = tc.CONSTRAINT_SCHEMA AND cc.CONSTRAINT_NAME = tc.CONSTRAINT_NAME WHERE tc.TABLE_SCHEMA = DATABASE() AND tc.CONSTRAINT_TYPE = 'CHECK'")
	if err != nil {
		t.Fatalf("list CHECK constraints: %v", err)
	}
	type check struct{ table, name string }
	var checks []check
	for rows.Next() {
		var item check
		if err := rows.Scan(&item.table, &item.name); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		checks = append(checks, item)
	}
	rows.Close()
	for _, item := range checks {
		statement := "ALTER TABLE " + gatewayMySQLIdentifier(item.table) + " DROP CHECK " + gatewayMySQLIdentifier(item.name)
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("drop CHECK constraint %s: %v", item.name, err)
		}
	}
	return len(checks)
}

func gatewayMySQLTestDropTriggers(t *testing.T, ctx context.Context, db *sql.DB) int {
	t.Helper()
	rows, err := db.QueryContext(ctx, "SELECT TRIGGER_NAME FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = DATABASE()")
	if err != nil {
		t.Fatalf("list triggers: %v", err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		names = append(names, name)
	}
	rows.Close()
	for _, name := range names {
		if _, err := db.ExecContext(ctx, "DROP TRIGGER "+gatewayMySQLIdentifier(name)); err != nil {
			t.Fatalf("drop trigger %s: %v", name, err)
		}
	}
	return len(names)
}

// gatewayMySQLTestAddMissingUniqueKeys creates the unique keys the SQLite
// runtime declares but the native DDL does not, so a conflicting insert can
// actually reach ON DUPLICATE KEY UPDATE. Returns the statements it ran.
func gatewayMySQLTestAddMissingUniqueKeys(t *testing.T, ctx context.Context, db *sql.DB, columns map[string]map[string]gatewayMySQLTestColumn) ([]string, []string) {
	t.Helper()
	wanted, _ := gatewayMySQLTestUniqueKeys(t)
	present := gatewayMySQLTestTableUniqueKeys(t, ctx, db)
	var created, skipped []string
	for table, keys := range wanted {
		if len(columns[table]) == 0 {
			continue
		}
		for index, key := range keys {
			matched := false
			for _, existing := range present[table] {
				if gatewayMySQLUpsertSameColumns(existing, key) {
					matched = true
				}
			}
			if matched {
				continue
			}
			parts := make([]string, 0, len(key))
			representable := true
			for _, column := range key {
				metadata, found := columns[table][column]
				if !found {
					representable = false
					break
				}
				part := gatewayMySQLIdentifier(column)
				switch metadata.dataType {
				case "text", "tinytext", "mediumtext", "longtext", "blob", "tinyblob", "mediumblob", "longblob":
					part += "(191)"
				}
				parts = append(parts, part)
			}
			if !representable {
				skipped = append(skipped, table+"("+strings.Join(key, ",")+")")
				continue
			}
			statement := "CREATE UNIQUE INDEX " + gatewayMySQLIdentifier("doops_rehearsal_"+table+"_"+strconv.Itoa(index)) +
				" ON " + gatewayMySQLIdentifier(table) + " (" + strings.Join(parts, ", ") + ")"
			if _, err := db.ExecContext(ctx, statement); err != nil {
				t.Fatalf("create the missing unique key %v on %s: %v\n%s", key, table, err, statement)
			}
			created = append(created, table+"("+strings.Join(key, ",")+")")
		}
	}
	return created, skipped
}

func gatewayMySQLTestTableColumns(t *testing.T, ctx context.Context, db *sql.DB) map[string]map[string]gatewayMySQLTestColumn {
	t.Helper()
	rows, err := db.QueryContext(ctx, "SELECT TABLE_NAME, COLUMN_NAME, DATA_TYPE, COLUMN_TYPE, IS_NULLABLE, CHARACTER_MAXIMUM_LENGTH, EXTRA FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE()")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns := make(map[string]map[string]gatewayMySQLTestColumn)
	for rows.Next() {
		var table, name, dataType, columnType, nullable, extra string
		var maxLength sql.NullInt64
		if err := rows.Scan(&table, &name, &dataType, &columnType, &nullable, &maxLength, &extra); err != nil {
			t.Fatal(err)
		}
		if columns[table] == nil {
			columns[table] = make(map[string]gatewayMySQLTestColumn)
		}
		columns[table][name] = gatewayMySQLTestColumn{
			name:          name,
			dataType:      strings.ToLower(dataType),
			columnType:    strings.ToLower(columnType),
			nullable:      strings.EqualFold(nullable, "YES"),
			maxLength:     maxLength.Int64,
			autoIncrement: strings.Contains(strings.ToLower(extra), "auto_increment"),
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return columns
}

func gatewayMySQLTestTableUniqueKeys(t *testing.T, ctx context.Context, db *sql.DB) map[string][][]string {
	t.Helper()
	rows, err := db.QueryContext(ctx, "SELECT TABLE_NAME, INDEX_NAME, COLUMN_NAME FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND NON_UNIQUE = 0 ORDER BY TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	keys := make(map[string][][]string)
	for rows.Next() {
		var table, index, column string
		if err := rows.Scan(&table, &index, &column); err != nil {
			t.Fatal(err)
		}
		keys[table] = append(keys[table], []string{index, column})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	grouped := make(map[string][][]string, len(keys))
	for table, pairs := range keys {
		byIndex := make(map[string][]string)
		var names []string
		for _, pair := range pairs {
			if byIndex[pair[0]] == nil {
				names = append(names, pair[0])
			}
			byIndex[pair[0]] = append(byIndex[pair[0]], pair[1])
		}
		for _, name := range names {
			grouped[table] = append(grouped[table], byIndex[name])
		}
	}
	return grouped
}

func gatewayMySQLTestColumnValue(column gatewayMySQLTestColumn, pass int) any {
	switch column.dataType {
	case "tinyint", "smallint", "mediumint", "int", "integer", "bigint":
		return int64(10 * pass)
	case "decimal", "numeric", "float", "double", "real":
		return "1." + strconv.Itoa(pass)
	case "bit":
		return pass % 2
	case "date":
		return "2026-01-02"
	case "datetime", "timestamp":
		return "2026-01-02 03:04:0" + strconv.Itoa(pass)
	case "time":
		return "03:04:05"
	case "year":
		return 2026
	case "json":
		return "{\"pass\": " + strconv.Itoa(pass) + "}"
	case "blob", "tinyblob", "mediumblob", "longblob", "binary", "varbinary":
		return []byte("int13-" + strconv.Itoa(pass))
	case "enum", "set":
		options := gatewayMySQLTestEnumOptions(column.columnType)
		if len(options) > 0 {
			return options[(pass-1)%len(options)]
		}
		return "int13-" + strconv.Itoa(pass)
	}
	value := "int13-" + strconv.Itoa(pass)
	if column.maxLength > 0 && int64(len(value)) > column.maxLength {
		value = strconv.Itoa(pass)
	}
	return value
}

func gatewayMySQLTestEnumOptions(columnType string) []string {
	open := strings.Index(columnType, "(")
	if open < 0 {
		return nil
	}
	var options []string
	remaining := columnType[open+1:]
	for {
		start := strings.Index(remaining, "'")
		if start < 0 {
			break
		}
		remaining = remaining[start+1:]
		end := strings.Index(remaining, "'")
		if end < 0 {
			break
		}
		options = append(options, remaining[:end])
		remaining = remaining[end+1:]
	}
	return options
}

func gatewayMySQLTestInsertColumns(upsert gatewayUpsert) []string {
	match := gatewayMySQLUpsertInsertHead.FindStringSubmatch(strings.TrimSpace(upsert.insert))
	if match == nil {
		return nil
	}
	var columns []string
	for _, entry := range strings.Split(match[3], ",") {
		column := strings.Trim(strings.TrimSpace(entry), "\"\x60")
		if column != "" {
			columns = append(columns, column)
		}
	}
	return columns
}

type gatewayMySQLTestInsertSlot struct {
	column  string
	literal bool
	value   any
}

// gatewayMySQLTestInsertSlots pairs every insert column with its VALUES entry,
// because the Gateway's SQLite upserts mix ? placeholders with literals and a
// literal is not an argument.
func gatewayMySQLTestInsertSlots(t *testing.T, upsert gatewayUpsert) []gatewayMySQLTestInsertSlot {
	t.Helper()
	columns := gatewayMySQLTestInsertColumns(upsert)
	if len(columns) == 0 {
		t.Fatalf("cannot parse the insert column list: %s", upsert.insert)
	}
	values, ok := gatewayMySQLTestValuesClause(upsert.insert)
	if !ok {
		t.Fatalf("the upsert insert has no literal VALUES clause: %s", upsert.insert)
	}
	if len(values) != len(columns) {
		t.Fatalf("the insert names %d columns but supplies %d values: %s", len(columns), len(values), upsert.insert)
	}
	slots := make([]gatewayMySQLTestInsertSlot, 0, len(columns))
	for index, column := range columns {
		raw := strings.TrimSpace(values[index])
		if raw == "?" {
			slots = append(slots, gatewayMySQLTestInsertSlot{column: column})
			continue
		}
		slots = append(slots, gatewayMySQLTestInsertSlot{column: column, literal: true, value: gatewayMySQLTestLiteral(raw)})
	}
	return slots
}

func gatewayMySQLTestValuesClause(insert string) ([]string, bool) {
	upper := strings.ToUpper(insert)
	open := strings.Index(upper, "VALUES")
	if open < 0 {
		return nil, false
	}
	rest := insert[open+len("VALUES"):]
	start := strings.Index(rest, "(")
	if start < 0 {
		return nil, false
	}
	depth := 0
	var quote rune
	var current strings.Builder
	var values []string
	for _, character := range rest[start:] {
		switch {
		case quote != 0:
			current.WriteRune(character)
			if character == quote {
				quote = 0
			}
			continue
		case character == '\'' || character == '"':
			quote = character
			current.WriteRune(character)
			continue
		case character == '(':
			depth++
			if depth == 1 {
				continue
			}
		case character == ')':
			depth--
			if depth == 0 {
				values = append(values, current.String())
				return values, len(values) > 0
			}
		case character == ',' && depth == 1:
			values = append(values, current.String())
			current.Reset()
			continue
		}
		current.WriteRune(character)
	}
	return nil, false
}

func gatewayMySQLTestLiteral(raw string) any {
	raw = strings.TrimSpace(raw)
	if strings.EqualFold(raw, "NULL") {
		return nil
	}
	if strings.HasPrefix(raw, "'") && strings.HasSuffix(raw, "'") && len(raw) >= 2 {
		return strings.ReplaceAll(raw[1:len(raw)-1], "''", "'")
	}
	return raw
}

func gatewayMySQLTestPinColumns(upsert gatewayUpsert, columns []string, keys [][]string) []string {
	if len(upsert.target) > 0 {
		return upsert.target
	}
	inserted := make(map[string]bool, len(columns))
	for _, column := range columns {
		inserted[column] = true
	}
	for _, key := range keys {
		complete := true
		for _, column := range key {
			if !inserted[column] {
				complete = false
				break
			}
		}
		if complete {
			return key
		}
	}
	return nil
}

func gatewayMySQLTestRow(t *testing.T, ctx context.Context, tx *sql.Tx, table string, pinned []string, values map[string]any) map[string]sql.NullString {
	t.Helper()
	where := make([]string, 0, len(pinned))
	args := make([]any, 0, len(pinned))
	for _, column := range pinned {
		where = append(where, gatewayMySQLIdentifier(column)+" <=> ?")
		args = append(args, values[column])
	}
	query := "SELECT * FROM " + gatewayMySQLIdentifier(table) + " WHERE " + strings.Join(where, " AND ")
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		t.Fatalf("read %s: %v", table, err)
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	row := make(map[string]sql.NullString, len(names))
	count := 0
	for rows.Next() {
		count++
		raw := make([]sql.NullString, len(names))
		destinations := make([]any, len(names))
		for index := range raw {
			destinations[index] = &raw[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			t.Fatal(err)
		}
		for index, name := range names {
			row[name] = raw[index]
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("read %s: %d rows match %v, want 1", table, count, pinned)
	}
	return row
}

func gatewayMySQLTestValuesEqual(left, right sql.NullString) bool {
	if left.Valid != right.Valid {
		return false
	}
	if !left.Valid {
		return true
	}
	if left.String == right.String {
		return true
	}
	leftNumber, leftErr := strconv.ParseFloat(left.String, 64)
	rightNumber, rightErr := strconv.ParseFloat(right.String, 64)
	if leftErr == nil && rightErr == nil {
		return leftNumber == rightNumber
	}
	return false
}

var gatewayMySQLTestScalarExtreme = regexp.MustCompile("(?i)^(max|min|greatest|least)\\(\\s*(?:[A-Za-z_][A-Za-z0-9_]*\\.)?([A-Za-z_][A-Za-z0-9_]*)\\s*,\\s*excluded\\.([A-Za-z_][A-Za-z0-9_]*)\\s*\\)$")

func gatewayMySQLTestExpectedValue(upsert gatewayUpsert, assignment string, pass map[string]any, stored sql.NullString) (sql.NullString, bool) {
	name, expression, ok := strings.Cut(assignment, "=")
	if !ok {
		return sql.NullString{}, false
	}
	name = strings.TrimSpace(name)
	expression = strings.TrimSpace(expression)
	if source, found := strings.CutPrefix(expression, "excluded."); found {
		value, present := pass[strings.TrimSpace(source)]
		if !present {
			return sql.NullString{}, false
		}
		return gatewayMySQLTestNullString(value), true
	}
	if source, found := strings.CutPrefix(expression, gatewayMySQLUpsertNewRowPrefix); found {
		source = strings.TrimSuffix(strings.TrimSpace(source), ")")
		source = strings.Trim(strings.TrimSpace(source), "\x60")
		value, present := pass[source]
		if !present {
			return sql.NullString{}, false
		}
		return gatewayMySQLTestNullString(value), true
	}
	if match := gatewayMySQLTestScalarExtreme.FindStringSubmatch(expression); match != nil {
		value, present := pass[match[3]]
		if !present {
			return sql.NullString{}, false
		}
		candidate, err := strconv.ParseFloat(gatewayMySQLTestNullString(value).String, 64)
		if err != nil {
			return sql.NullString{}, false
		}
		keep := stored.Valid
		storedNumber := 0.0
		if keep {
			parsed, parseErr := strconv.ParseFloat(stored.String, 64)
			if parseErr != nil {
				return sql.NullString{}, false
			}
			storedNumber = parsed
		}
		switch strings.ToLower(match[1]) {
		case "max", "greatest":
			if keep && storedNumber > candidate {
				candidate = storedNumber
			}
		default:
			if keep && storedNumber < candidate {
				candidate = storedNumber
			}
		}
		return sql.NullString{String: strconv.FormatFloat(candidate, 'f', -1, 64), Valid: true}, true
	}
	if strings.EqualFold(expression, "NULL") {
		return sql.NullString{}, true
	}
	if expression == "''" {
		return sql.NullString{String: "", Valid: true}, true
	}
	if number, err := strconv.ParseFloat(expression, 64); err == nil {
		value := sql.NullString{String: strconv.FormatFloat(number, 'f', -1, 64), Valid: true}
		_ = name
		return value, true
	}
	_ = stored
	return sql.NullString{}, false
}

func gatewayMySQLTestNullString(value any) sql.NullString {
	switch typed := value.(type) {
	case nil:
		return sql.NullString{}
	case []byte:
		return sql.NullString{String: string(typed), Valid: true}
	case string:
		return sql.NullString{String: typed, Valid: true}
	case int64:
		return sql.NullString{String: strconv.FormatInt(typed, 10), Valid: true}
	case int:
		return sql.NullString{String: strconv.Itoa(typed), Valid: true}
	}
	return sql.NullString{String: "unsupported"}
}

// gatewayMySQLTestOpenEmpty creates a disposable schema that stays empty, which
// installGatewayMySQLSchema requires; the syntax-test helper already put a
// fixture table in its own schema.
func gatewayMySQLTestOpenEmpty(t *testing.T) *sql.DB {
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
	database := "gateway_upsert_" + strings.ToLower(randomHex(8))
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+gatewayMySQLIdentifier(database)); err != nil {
		t.Fatalf("create MySQL syntax test database: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = admin.ExecContext(cleanupCtx, "DROP DATABASE "+gatewayMySQLIdentifier(database))
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
	return db
}

func TestGatewayMySQLUpsertsExecuteOnMySQL(t *testing.T) {
	db := gatewayMySQLTestOpenEmpty(t)
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if _, err := db.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0"); err != nil {
		t.Fatalf("disable foreign key checks: %v", err)
	}
	if _, err := installGatewayMySQLSchema(ctx, db); err != nil {
		t.Fatalf("install the native MySQL schema: %v", err)
	}
	dropped := gatewayMySQLTestDropChecks(t, ctx, db)
	if dropped > 0 {
		t.Logf("dropped %d CHECK constraints: the fixture values are type-correct, not business-valid, and the subject is upsert semantics", dropped)
	}
	triggers := gatewayMySQLTestDropTriggers(t, ctx, db)
	if triggers > 0 {
		t.Logf("dropped %d triggers: they guard business invariants the generated fixtures cannot satisfy", triggers)
	}
	columns := gatewayMySQLTestTableColumns(t, ctx, db)
	keys := gatewayMySQLTestTableUniqueKeys(t, ctx, db)
	created, skipped := gatewayMySQLTestAddMissingUniqueKeys(t, ctx, db, columns)
	if len(created) > 0 {
		t.Logf("the native DDL omits %d unique keys the SQLite runtime declares; the rehearsal created them to reach the conflicting row: %v", len(created), created)
	}
	if len(skipped) > 0 {
		t.Logf("the native DDL cannot express %d partial or expression unique keys, which MySQL enforces with triggers instead: %v", len(skipped), skipped)
	}
	keys = gatewayMySQLTestTableUniqueKeys(t, ctx, db)
	sites := gatewayMySQLUpsertSites(t)
	if len(sites) == 0 {
		t.Fatal("no gatewayUpsert sites found")
	}
	for _, site := range sites {
		site := site
		t.Run(site.label(), func(t *testing.T) {
			table := gatewayMySQLUpsertTable(site.upsert)
			tableColumns := columns[table]
			if len(tableColumns) == 0 {
				if table == "grants_v2" {
					t.Skip("grants_v2 exists only inside its migration, which the copy path treats as transient")
				}
				t.Fatalf("the native schema has no table %s", table)
			}
			slots := gatewayMySQLTestInsertSlots(t, site.upsert)
			inserted := make([]string, 0, len(slots))
			for _, slot := range slots {
				inserted = append(inserted, slot.column)
			}
			pinned := gatewayMySQLTestPinColumns(site.upsert, inserted, keys[table])
			if len(pinned) == 0 {
				t.Fatalf("no unique key is fully supplied by the insert, so a conflict cannot be guaranteed")
			}
			if len(site.upsert.target) > 0 {
				found := false
				for _, key := range keys[table] {
					if gatewayMySQLUpsertSameColumns(key, site.upsert.target) {
						found = true
					}
				}
				if !found {
					t.Fatalf("conflict target %v is not a unique key of the native %s", site.upsert.target, table)
				}
			}
			predicate := make(map[string]bool)
			for _, column := range inserted {
				if site.upsert.where != "" && gatewayMySQLUpsertMentions(site.upsert.where, column) {
					predicate[column] = true
				}
			}
			buildPass := func(target int, predicatePass int, otherPass int) ([]any, map[string]any) {
				args := make([]any, 0, len(slots))
				values := make(map[string]any, len(slots))
				for _, slot := range slots {
					if slot.literal {
						values[slot.column] = slot.value
						continue
					}
					metadata := tableColumns[slot.column]
					pass := otherPass
					for _, pin := range pinned {
						if pin == slot.column {
							pass = target
						}
					}
					if predicate[slot.column] {
						pass = predicatePass
					}
					value := gatewayMySQLTestColumnValue(metadata, pass)
					args = append(args, value)
					values[slot.column] = value
				}
				return args, values
			}
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			firstArgs, firstValues := buildPass(1, 1, 1)
			if _, err := tx.ExecContext(ctx, site.upsert.render(gatewayDialectMySQL), firstArgs...); err != nil {
				t.Fatalf("the first insert failed on MySQL 8.4: %v\n%s", err, site.upsert.render(gatewayDialectMySQL))
			}
			baseline := gatewayMySQLTestRow(t, ctx, tx, table, pinned, firstValues)
			secondArgs, secondValues := buildPass(1, 2, 2)
			if _, err := tx.ExecContext(ctx, site.upsert.render(gatewayDialectMySQL), secondArgs...); err != nil {
				t.Fatalf("the conflicting insert failed on MySQL 8.4: %v\n%s", err, site.upsert.render(gatewayDialectMySQL))
			}
			updated := gatewayMySQLTestRow(t, ctx, tx, table, pinned, firstValues)
			if site.upsert.doNothing {
				for name, want := range baseline {
					if !gatewayMySQLTestValuesEqual(want, updated[name]) {
						t.Fatalf("DO NOTHING changed %s: stored %v, got %v", name, want, updated[name])
					}
				}
				return
			}
			checked := 0
			expected := make(map[string]sql.NullString, len(baseline))
			for name, stored := range baseline {
				expected[name] = stored
			}
			for _, assignment := range site.upsert.set {
				name := strings.TrimSpace(strings.SplitN(assignment, "=", 2)[0])
				value, simple := gatewayMySQLTestExpectedValue(site.upsert, assignment, secondValues, baseline[name])
				if !simple {
					expected[name] = updated[name]
					continue
				}
				expected[name] = value
				checked++
			}
			if checked == 0 {
				t.Fatalf("no assignment could be predicted, so the MySQL update is unverified")
			}
			for name, want := range expected {
				if !gatewayMySQLTestValuesEqual(want, updated[name]) {
					t.Fatalf("after the conflict %s is %v, want %v (stored %v)", name, updated[name], want, baseline[name])
				}
			}
			if len(predicate) == 0 {
				t.Logf("verified %d assignments on MySQL 8.4", checked)
				return
			}
			thirdArgs, _ := buildPass(1, 2, 3)
			if _, err := tx.ExecContext(ctx, site.upsert.render(gatewayDialectMySQL), thirdArgs...); err != nil {
				t.Fatalf("the filtered upsert failed on MySQL 8.4: %v", err)
			}
			unchanged := gatewayMySQLTestRow(t, ctx, tx, table, pinned, firstValues)
			changed := false
			for name, stored := range updated {
				if !gatewayMySQLTestValuesEqual(stored, unchanged[name]) {
					t.Fatalf("a false predicate updated %s to %v, but the row stored %v", name, unchanged[name], stored)
				}
			}
			for index := range thirdArgs {
				if thirdArgs[index] != secondArgs[index] {
					changed = true
				}
			}
			if !changed {
				t.Fatalf("the false-predicate pass differs from the applied pass in no column, so it proves nothing")
			}
			t.Logf("verified %d assignments, including an untouched row when the predicate is false", checked)
		})
	}
}
