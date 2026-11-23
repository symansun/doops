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
// Statements without a column list are rejected outright: INSERT INTO t VALUES
// (...) binds by physical column order, which no column-set comparison can see,
// so the runtime must name its columns. TestGatewayMySQLInsertsNameColumns owns
// the order, arity and site-count assertions for the same statements.
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
			if table, unlisted := gatewayMySQLUnlistedInsertTable(literal.query); unlisted {
				t.Fatalf("%s:%d: INSERT INTO %s without a column list is forbidden; name the columns so the statement does not depend on the physical column order", literal.file, literal.line, table)
			}
			if strings.Contains(upper, "INSERT") && strings.Contains(upper, "INTO") {
				t.Logf("skip %s:%d: SQL assembled from identifiers at runtime names its columns explicitly and is covered by its own tests", literal.file, literal.line)
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

// --- INT-14: unique key parity between the frozen export and the native DDL ---

// TestGatewayMySQLUniqueKeysMatchExport pins every unique key of the frozen
// SQLite export to the native MySQL DDL, and pins the three translations MySQL
// needs to stay faithful: the TEXT scope_json key becomes SHA2(scope_json, 256)
// rather than a prefix, the two partial indexes become IF(..., col, NULL) parts,
// and the catalog identity gains LENGTH-prefixed parts so the concatenation is
// injective. A missing key makes ON DUPLICATE KEY UPDATE never fire; a key that
// is close but not equal (a prefix, a plain column instead of IF(...)) merges
// rows SQLite keeps apart. Both failures name the table and the key.
func TestGatewayMySQLUniqueKeysMatchExport(t *testing.T) {
	tables, exported := gatewayMySQLTestExportUniqueKeys(t)
	declared := gatewayMySQLTestDDLUniqueKeys(t, tables)
	if len(exported) == 0 {
		t.Fatal("the frozen export declares no unique key, so this guard would be vacuous")
	}
	used := make([]bool, len(declared))
	seen := make(map[string]bool, len(exported))
	excluded := 0
	for _, key := range exported {
		identity := key.table + "|" + key.identity()
		seen[identity] = true
		if reason, ok := gatewayMySQLTestUniqueKeyExclusions[identity]; ok {
			excluded++
			t.Logf("%s is enforced by a trigger instead of an index: %s", key.describe(), reason)
			continue
		}
		wanted, translated := gatewayMySQLTestUniqueKeyTranslations[identity]
		matched := -1
		for i, candidate := range declared {
			if used[i] || candidate.table != key.table {
				continue
			}
			if translated {
				if candidate.hasExpression && gatewayMySQLTestNormalizeSQLPart(candidate.partsText()) == gatewayMySQLTestNormalizeSQLPart(wanted) {
					matched = i
					break
				}
				continue
			}
			if !candidate.hasExpression && gatewayMySQLTestColumnsEqualOrdered(candidate.parts, key.parts) {
				matched = i
				break
			}
		}
		if matched < 0 {
			if translated {
				t.Fatalf("%s: the native MySQL DDL must declare a unique key over (%s), and the translation says exactly (%s); %s declares %v", identity, key.partsText(), wanted, key.table, gatewayMySQLTestDDLKeyNames(declared, key.table))
			}
			t.Fatalf("%s: the native MySQL DDL declares no unique key over (%s); add it to backend/agent/internal/server/gateway_mysql_schema.sql, or to gatewayMySQLTestUniqueKeyTranslations when MySQL cannot express it literally", identity, key.partsText())
		}
		used[matched] = true
	}
	for i, key := range declared {
		if used[i] {
			continue
		}
		t.Fatalf("%s: the native MySQL DDL declares a unique key the frozen SQLite export does not; mirror it in backend/agent/internal/server/testdata/gateway-sqlite-schema.sql or drop it from the DDL", key.describe())
	}
	if len(declared) != len(exported)-excluded {
		t.Fatalf("the DDL declares %d unique keys and the export declares %d minus %d trigger-enforced key(s); the two sides drifted apart", len(declared), len(exported), excluded)
	}
	for identity := range gatewayMySQLTestUniqueKeyTranslations {
		if !seen[identity] {
			t.Fatalf("gatewayMySQLTestUniqueKeyTranslations names %s, which the frozen export no longer declares; drop the stale translation", identity)
		}
	}
	for identity := range gatewayMySQLTestUniqueKeyExclusions {
		if !seen[identity] {
			t.Fatalf("gatewayMySQLTestUniqueKeyExclusions names %s, which the frozen export no longer declares; drop the stale exclusion", identity)
		}
	}
	t.Logf("matched %d of %d exported unique keys, %d enforced by triggers", len(declared), len(exported), excluded)
}

// gatewayMySQLTestUniqueKeyTranslations maps the SQLite unique keys that MySQL
// cannot declare literally to the exact index parts the native DDL uses. Both
// sides are normalized with gatewayMySQLTestNormalizeSQLPart, so whitespace and
// identifier quoting do not matter while the expression itself must match:
// swapping SHA2(scope_json, 256) for a 191-character prefix, or IF(state='open',
// series_id, NULL) for a bare series_id, changes the key's meaning and fails.
var gatewayMySQLTestUniqueKeyTranslations = map[string]string{
	"credential_grants|(credential_id,grantee_id,scope_json)": "credential_id, grantee_id, (SHA2(scope_json, 256))",
	"asset_monitor_alerts|asset_monitor_one_open":             "(IF(state='open', series_id, NULL))",
	"credential_versions|idx_credential_versions_one_active":  "(IF(state='active', credential_id, NULL))",
	"inventory_resource_catalog|catalog_bound_identity":       "(IF(cluster<>'', CONCAT(LENGTH(kind), ':', kind, LENGTH(cluster), ':', cluster, LENGTH(instance), ':', instance, identity_key), NULL))",
	"inventory_resource_catalog|catalog_pending_identity":     "(IF(cluster='', CONCAT(LENGTH(kind), ':', kind, LENGTH(created_by), ':', created_by, identity_key), NULL))",
}

// gatewayMySQLTestUniqueKeyExclusions lists the SQLite unique keys the DDL
// deliberately does not declare, with the reason MySQL cannot express them as
// an index without changing their meaning.
var gatewayMySQLTestUniqueKeyExclusions = map[string]string{
	"inspection_runs|inspection_cycle": "due is TEXT, so a MySQL unique key would need a prefix and a prefix would merge distinct due values; the native DDL enforces the key with the inspection_cycle_insert and inspection_cycle_update triggers",
}

type gatewayMySQLUniqueKey struct {
	table         string
	name          string
	parts         []string
	hasExpression bool
	where         string
}

func (key gatewayMySQLUniqueKey) identity() string {
	if key.name != "" {
		return key.name
	}
	return "(" + strings.Join(key.parts, ",") + ")"
}

func (key gatewayMySQLUniqueKey) partsText() string {
	return strings.Join(key.parts, ", ")
}

func (key gatewayMySQLUniqueKey) describe() string {
	text := key.table + " " + key.identity() + " (" + key.partsText() + ")"
	if key.where != "" {
		text += " WHERE " + key.where
	}
	if key.hasExpression {
		text += " [functional]"
	}
	return text
}

func gatewayMySQLTestDDLKeyNames(keys []gatewayMySQLUniqueKey, table string) []string {
	var names []string
	for _, key := range keys {
		if key.table == table {
			names = append(names, key.identity())
		}
	}
	return names
}

// gatewayMySQLTestStripSQLComments drops the -- comment lines the frozen export
// carries. Some of them sit in the same ';' segment as the CREATE statement that
// follows, so a loader that only looks at the first statement of a segment
// silently loses those indexes.
func gatewayMySQLTestStripSQLComments(text string) string {
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// gatewayMySQLTestExportUniqueKeys rebuilds the frozen export in memory and
// returns its tables plus every UNIQUE index in declaration order. The export is
// a statement list, not a schema dump, so tables and triggers are applied before
// the indexes that may reference them.
func gatewayMySQLTestExportUniqueKeys(t *testing.T) ([]string, []gatewayMySQLUniqueKey) {
	t.Helper()
	var tables, indexes []string
	for _, statement := range strings.Split(gatewayMySQLTestStripSQLComments(gatewayMySQLTestExportText(t)), ";") {
		statement = strings.TrimSpace(statement)
		upper := strings.ToUpper(statement)
		switch {
		case strings.HasPrefix(upper, "CREATE TABLE"):
			tables = append(tables, statement)
		case strings.HasPrefix(upper, "CREATE UNIQUE INDEX"):
			indexes = append(indexes, statement)
		}
	}
	if len(tables) == 0 {
		t.Fatal("the frozen export carries no CREATE TABLE statement")
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open the in-memory SQLite export: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	load := func(kind string, statements []string) {
		pending := statements
		for len(pending) > 0 {
			var failed []string
			var last error
			for _, statement := range pending {
				if _, err := db.Exec(statement); err != nil {
					failed = append(failed, statement)
					last = err
					continue
				}
			}
			if len(failed) == len(pending) {
				t.Fatalf("load %d %s statement(s) from the frozen export: %v", len(pending), kind, last)
			}
			pending = failed
		}
	}
	load("table", tables)
	load("unique index", indexes)
	names := gatewayMySQLExportTables(t, db)
	var keys []gatewayMySQLUniqueKey
	for _, table := range names {
		keys = append(keys, gatewayMySQLTestSQLiteUniqueKeys(t, db, table)...)
	}
	return names, keys
}

func gatewayMySQLTestSQLiteUniqueKeys(t *testing.T, db *sql.DB, table string) []gatewayMySQLUniqueKey {
	t.Helper()
	rows, err := db.Query("PRAGMA index_list('" + strings.ReplaceAll(table, "'", "''") + "')")
	if err != nil {
		t.Fatalf("list the indexes of %s in the frozen export: %v", table, err)
	}
	headers, err := rows.Columns()
	if err != nil {
		t.Fatalf("read the index_list columns of %s: %v", table, err)
	}
	if len(headers) < 4 {
		t.Fatalf("PRAGMA index_list(%s) returned %d columns, expected at least 4", table, len(headers))
	}
	type listedIndex struct {
		name   string
		unique bool
		origin string
	}
	var listed []listedIndex
	for rows.Next() {
		values := make([]sql.RawBytes, len(headers))
		pointers := make([]any, len(headers))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatalf("scan the index_list row of %s: %v", table, err)
		}
		// PRAGMA index_list: seq, name, unique, origin, partial.
		listed = append(listed, listedIndex{name: string(values[1]), unique: string(values[2]) == "1", origin: strings.ToLower(string(values[3]))})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list the indexes of %s in the frozen export: %v", table, err)
	}
	rows.Close()
	var keys []gatewayMySQLUniqueKey
	for _, index := range listed {
		if !index.unique || (index.origin != "c" && index.origin != "u") {
			continue // not unique, or the primary key
		}
		key := gatewayMySQLUniqueKey{table: table, parts: gatewayMySQLTestSQLiteIndexColumns(t, db, index.name)}
		if index.origin == "u" {
			// An inline UNIQUE column constraint: the autoindex has no SQL of its own.
			keys = append(keys, key)
			continue
		}
		key.name = index.name
		for _, part := range key.parts {
			if !gatewayMySQLTestPlainColumn(part) {
				key.hasExpression = true
			}
		}
		key.where = gatewayMySQLTestSQLiteIndexWhere(t, db, index.name)
		keys = append(keys, key)
	}
	return keys
}

func gatewayMySQLTestSQLiteIndexColumns(t *testing.T, db *sql.DB, index string) []string {
	t.Helper()
	rows, err := db.Query("PRAGMA index_info('" + strings.ReplaceAll(index, "'", "''") + "')")
	if err != nil {
		t.Fatalf("read the columns of index %s: %v", index, err)
	}
	defer rows.Close()
	headers, err := rows.Columns()
	if err != nil {
		t.Fatalf("read the index_info columns of %s: %v", index, err)
	}
	if len(headers) < 3 {
		t.Fatalf("PRAGMA index_info(%s) returned %d columns, expected at least 3", index, len(headers))
	}
	var columns []string
	for rows.Next() {
		values := make([]sql.RawBytes, len(headers))
		pointers := make([]any, len(headers))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatalf("scan the index_info row of %s: %v", index, err)
		}
		if values[2] == nil {
			// An expression part: SQLite reports cid -2 and a NULL name. Keep a
			// placeholder so the key is still compared as functional, and let the
			// translation or exclusion table decide what the DDL must declare.
			columns = append(columns, "(expression)")
			continue
		}
		columns = append(columns, string(values[2]))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the columns of index %s: %v", index, err)
	}
	return columns
}

func gatewayMySQLTestSQLiteIndexWhere(t *testing.T, db *sql.DB, index string) string {
	t.Helper()
	var statement sql.NullString
	if err := db.QueryRow("SELECT sql FROM sqlite_schema WHERE type = 'index' AND name = ?", index).Scan(&statement); err != nil {
		t.Fatalf("read the SQL of index %s: %v", index, err)
	}
	normalized := gatewayMySQLTestNormalizeSQLPart(statement.String)
	cut := strings.Index(normalized, " WHERE ")
	if cut < 0 {
		return ""
	}
	return strings.TrimSpace(normalized[cut+len(" WHERE "):])
}

// gatewayMySQLTestDDLUniqueKeys parses the UNIQUE KEY items and the inline
// UNIQUE column constraints of the native DDL.
func gatewayMySQLTestDDLUniqueKeys(t *testing.T, tables []string) []gatewayMySQLUniqueKey {
	t.Helper()
	var keys []gatewayMySQLUniqueKey
	for _, table := range tables {
		for _, item := range gatewayMySQLTopLevelItems(gatewayMySQLDDLBody(t, gatewayMySQLSchemaSQL, table)) {
			item = strings.TrimSpace(item)
			upper := strings.ToUpper(item)
			switch {
			case strings.HasPrefix(upper, "UNIQUE KEY "):
				rest := item[len("UNIQUE KEY "):]
				open := strings.Index(rest, "(")
				if open < 0 || !strings.HasSuffix(rest, ")") {
					t.Fatalf("%s: cannot parse the unique key clause %q", table, item)
				}
				key := gatewayMySQLUniqueKey{table: table, name: strings.TrimSpace(rest[:open]), parts: gatewayMySQLTestDDLKeyParts(rest[open+1 : len(rest)-1])}
				for _, part := range key.parts {
					if !gatewayMySQLTestPlainColumn(part) {
						key.hasExpression = true
					}
				}
				keys = append(keys, key)
			case strings.HasSuffix(upper, " UNIQUE") && !strings.HasPrefix(upper, "UNIQUE"):
				fields := strings.Fields(item)
				if len(fields) < 2 {
					continue
				}
				keys = append(keys, gatewayMySQLUniqueKey{table: table, parts: []string{gatewayMySQLTestNormalizeSQLPart(fields[0])}})
			}
		}
	}
	return keys
}

func gatewayMySQLTestDDLKeyParts(inner string) []string {
	var parts []string
	for _, item := range gatewayMySQLTopLevelItems(inner) {
		if normalized := gatewayMySQLTestNormalizeSQLPart(item); normalized != "" {
			parts = append(parts, normalized)
		}
	}
	return parts
}

// gatewayMySQLTestDDLUniqueKeyClause returns the raw UNIQUE KEY clause so a test
// can re-add a key it dropped.
func gatewayMySQLTestDDLUniqueKeyClause(t *testing.T, table, name string) (string, bool) {
	t.Helper()
	for _, item := range gatewayMySQLTopLevelItems(gatewayMySQLDDLBody(t, gatewayMySQLSchemaSQL, table)) {
		item = strings.TrimSpace(item)
		if strings.HasPrefix(item, "UNIQUE KEY "+name+" ") {
			return item, true
		}
	}
	return "", false
}

func gatewayMySQLTestNormalizeSQLPart(part string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(part, "\x60", "")), " ")
}

func gatewayMySQLTestPlainColumn(part string) bool {
	if part == "" || strings.HasPrefix(part, "(") {
		return false
	}
	for i, r := range part {
		switch {
		case r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

func gatewayMySQLTestColumnsEqualOrdered(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if gatewayMySQLTestNormalizeSQLPart(got[i]) != gatewayMySQLTestNormalizeSQLPart(want[i]) {
			return false
		}
	}
	return true
}
