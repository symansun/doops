package server

import (
	"database/sql"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestGatewayMySQLColumnsMatchExport is the column-level guard for every table
// that exists on both sides. A column that only one side defines breaks the
// online path in both directions: too few on MySQL and the runtime INSERT hits
// error 1364 (or the copier omits production data), too many on MySQL and the
// copier writes a column the SQLite runtime cannot serve.
//
// The table set is taken from the frozen export, so the guard grows
// automatically when the export gains a table; DDL-only tables stay covered by
// TestGatewayMySQLSchemaCoversRuntimeDDL and TestGatewayMySQLTablesMatchExport.
func TestGatewayMySQLColumnsMatchExport(t *testing.T) {
	export := gatewayMySQLTestSQLiteExport(t)
	ddl := string(gatewayMySQLSchemaSQL)
	for _, table := range gatewayMySQLExportTables(t, export) {
		want := gatewayMySQLSQLiteColumnNames(t, export, table)
		got := gatewayMySQLDDLColumnNames(t, ddl, table)
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("%s columns differ between the frozen SQLite export and the MySQL DDL:\n only in export: %v\n  only in mysql: %v\n      export: %v\n       mysql: %v",
				table, gatewayMySQLColumnDifference(want, got), gatewayMySQLColumnDifference(got, want), want, got)
		}
	}
}

// TestGatewayMySQLInsertDefaultsMatchSchema fails when a runtime INSERT omits a
// column that the MySQL DDL declares NOT NULL without a default. SQLite accepts
// the omission because its columns carry defaults; MySQL 8.4 in strict mode
// rejects it with error 1364, which only shows up against a real server. The
// column lists are extracted from the SQL literals in the package source, so a
// new INSERT is guarded without editing this test.
//
// Statements without a column list are skipped: INSERT INTO t VALUES (...)
// depends on the physical column order, which this guard cannot see.
func TestGatewayMySQLInsertDefaultsMatchSchema(t *testing.T) {
	export := gatewayMySQLTestSQLiteExport(t)
	ddl := string(gatewayMySQLSchemaSQL)
	transient := gatewayMySQLTransientTableSet()
	statements := gatewayMySQLRuntimeSQLLiterals(t)
	seen := map[string]bool{}
	guarded := 0
	for _, literal := range statements {
		upper := strings.ToUpper(literal.query)
		table, columns, ok := gatewayMySQLInsertColumns(literal.query)
		if !ok {
			if strings.Contains(upper, "INSERT") && strings.Contains(upper, "INTO") {
				t.Logf("skip %s:%d: INSERT without a parsable column list cannot be checked against the schema", literal.file, literal.line)
			}
			continue
		}
		// A transient table exists only inside one migrate* function: the
		// SQLite upgrade creates it, fills it, and renames or drops it in the same
		// transaction, so it never persists and MySQL (offline DDL + copier, no
		// migrate()) never sees it. The exemption is deliberately narrow: an
		// INSERT into the same name from a live-path function still fails.
		if transient[table] {
			if !strings.HasPrefix(literal.funcName, "migrate") {
				t.Fatalf("%s is a transient one-shot migration table, but %s:%d inserts into it from %q; only a migrate* function may do that",
					table, literal.file, literal.line, literal.funcName)
			}
			if !seen[table] {
				seen[table] = true
				t.Logf("skip %s: transient table created and renamed/dropped inside %s (never persisted)", table, literal.funcName)
			}
			continue
		}
		// A table the runtime writes but the frozen export does not describe is a
		// real gap: MySQL cannot serve it and the guard cannot see its columns.
		if !gatewayMySQLTableInExport(t, export, table) {
			t.Fatalf("%s is missing from the frozen export and the MySQL DDL, but %s:%d inserts into it; migrate it before guarding INSERTs",
				table, literal.file, literal.line)
		}
		present := make(map[string]bool, len(columns))
		for _, column := range columns {
			present[column] = true
		}
		for _, column := range gatewayMySQLDDLColumns(t, ddl, table) {
			if present[column.name] || !column.notNull || column.hasDefault {
				continue
			}
			t.Fatalf("%s.%s is NOT NULL without a default in the MySQL DDL, but %s:%d inserts into %s without it (MySQL error 1364); give the column a VARCHAR default or add it to the INSERT",
				table, column.name, literal.file, literal.line, table)
		}
		guarded++
	}
	if guarded == 0 {
		t.Fatalf("no INSERT statements were checked; the guard is not covering the runtime")
	}
	t.Logf("checked %d INSERT statements against the MySQL DDL", guarded)
}

type gatewayMySQLColumnSpec struct {
	name       string
	notNull    bool
	hasDefault bool
}

// gatewayMySQLDDLColumns parses the column definitions of one CREATE TABLE
// statement. Items are split at top-level commas only, so CHECK expressions and
// multi-line defaults stay inside their item, and comment lines are stripped so
// a "-- ..." remark cannot inject a bogus column.
func gatewayMySQLDDLColumns(t *testing.T, ddl, table string) []gatewayMySQLColumnSpec {
	t.Helper()
	body := gatewayMySQLDDLBody(t, ddl, table)
	body = gatewayMySQLCommentLine.ReplaceAllString(body, "\n")
	var columns []gatewayMySQLColumnSpec
	for _, item := range gatewayMySQLTopLevelItems(body) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if gatewayMySQLNonColumnItem.MatchString(item) {
			continue
		}
		name, rest := gatewayMySQLSplitColumn(item)
		if name == "" {
			t.Fatalf("%s: cannot parse DDL column item %q", table, item)
		}
		if !gatewayMySQLColumnType.MatchString(rest) {
			t.Fatalf("%s: cannot parse the DDL type of column %q from item %q", table, name, item)
		}
		columns = append(columns, gatewayMySQLColumnSpec{
			name:       name,
			notNull:    gatewayMySQLNotNull.MatchString(rest),
			hasDefault: gatewayMySQLHasDefault.MatchString(rest),
		})
	}
	if len(columns) == 0 {
		t.Fatalf("%s: the MySQL DDL defines no parsable columns", table)
	}
	return columns
}

// gatewayMySQLColumnDifference returns the values of a that b does not contain.
func gatewayMySQLColumnDifference(a, b []string) []string {
	present := make(map[string]bool, len(b))
	for _, value := range b {
		present[value] = true
	}
	var diff []string
	for _, value := range a {
		if !present[value] {
			diff = append(diff, value)
		}
	}
	return diff
}

func gatewayMySQLDDLColumnNames(t *testing.T, ddl, table string) []string {
	t.Helper()
	specs := gatewayMySQLDDLColumns(t, ddl, table)
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.name)
	}
	sort.Strings(names)
	return names
}

func gatewayMySQLDDLBody(t *testing.T, ddl, table string) string {
	t.Helper()
	prefix := "CREATE TABLE " + table + " ("
	start := strings.Index(ddl, prefix)
	if start < 0 {
		t.Fatalf("%s is missing from the MySQL DDL", table)
	}
	rest := ddl[start+len(prefix):]
	end := strings.Index(rest, ") ENGINE=InnoDB")
	if end < 0 {
		t.Fatalf("%s has no ENGINE=InnoDB terminator", table)
	}
	return rest[:end]
}

// gatewayMySQLTopLevelItems splits a CREATE TABLE body at commas that are not
// nested in parentheses or quotes.
func gatewayMySQLTopLevelItems(body string) []string {
	var items []string
	depth := 0
	var quote rune
	start := 0
	for i, r := range body {
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"', '\x60':
			quote = r
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				items = append(items, body[start:i])
				start = i + 1
			}
		}
	}
	items = append(items, body[start:])
	return items
}

func gatewayMySQLSplitColumn(item string) (string, string) {
	item = strings.TrimSpace(item)
	if item == "" {
		return "", ""
	}
	name := item
	rest := ""
	if space := strings.IndexAny(item, " \t\n"); space >= 0 {
		name, rest = item[:space], item[space:]
	}
	return strings.Trim(name, "\x60"), strings.ToUpper(strings.TrimSpace(rest))
}

func gatewayMySQLExportTables(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		t.Fatalf("list the frozen export tables: %v", err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan export table: %v", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list the frozen export tables: %v", err)
	}
	if len(tables) == 0 {
		t.Fatalf("the frozen export has no tables")
	}
	return tables
}

func gatewayMySQLTableInExport(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()
	var name string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name = ?", table).Scan(&name)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		t.Fatalf("look up %s in the frozen export: %v", table, err)
	}
	return true
}

func gatewayMySQLSQLiteColumnNames(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query("SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		t.Fatalf("read %s columns from the frozen export: %v", table, err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan %s column: %v", table, err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read %s columns from the frozen export: %v", table, err)
	}
	sort.Strings(columns)
	return columns
}

func gatewayMySQLTransientTableSet() map[string]bool {
	set := make(map[string]bool, len(gatewayMySQLTestTransientTables))
	for _, table := range gatewayMySQLTestTransientTables {
		set[table] = true
	}
	return set
}

// gatewayMySQLInsertColumns extracts the target table and column list of an
// INSERT statement. It accepts the variants the package actually uses:
// INSERT INTO t (...), INSERT OR IGNORE INTO t (...) and INSERT INTO t (...)
// SELECT .... Statements without a column list return ok=false.
func gatewayMySQLInsertColumns(query string) (string, []string, bool) {
	match := gatewayMySQLInsertPattern.FindStringSubmatch(query)
	if match == nil {
		return "", nil, false
	}
	var columns []string
	for _, column := range strings.Split(match[2], ",") {
		column = strings.Trim(strings.TrimSpace(column), "\x60")
		if column != "" {
			columns = append(columns, column)
		}
	}
	if len(columns) == 0 {
		return "", nil, false
	}
	return match[1], columns, true
}

var (
	gatewayMySQLInsertPattern = regexp.MustCompile("(?is)\\bINSERT\\s+(?:OR\\s+\\w+\\s+)?INTO\\s+([A-Za-z_][A-Za-z0-9_]*)\\s*\\(([^)]*)\\)")
	gatewayMySQLCommentLine   = regexp.MustCompile("(?m)^\\s*--.*$")
	gatewayMySQLNonColumnItem = regexp.MustCompile("(?i)^(?:PRIMARY\\s+KEY|KEY\\b|UNIQUE\\b|CONSTRAINT\\b|INDEX\\b|FOREIGN\\b|CHECK\\b)")
	gatewayMySQLColumnType    = regexp.MustCompile("^(?:BIGINT|INT|INTEGER|SMALLINT|TINYINT|MEDIUMINT|VARCHAR|CHAR|TEXT|MEDIUMTEXT|LONGTEXT|TINYTEXT|BLOB|MEDIUMBLOB|LONGBLOB|TINYBLOB|VARBINARY|BINARY|DECIMAL|NUMERIC|DOUBLE|FLOAT|REAL|BOOLEAN|BOOL|DATE|DATETIME|TIMESTAMP|TIME|JSON|ENUM|SET|BIT)\\b")
	gatewayMySQLNotNull       = regexp.MustCompile("(?i)\\bNOT\\s+NULL\\b")
	gatewayMySQLHasDefault    = regexp.MustCompile("(?i)\\bDEFAULT\\b")
)
