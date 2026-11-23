package server

import (
	"database/sql"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// gatewayMySQLUnlistedInsertSites freezes every INSERT in the runtime that names
// no column list, keyed by table with the number of sites. Those statements bind
// values purely by position: SQLite rejects a wrong arity, but neither SQLite nor
// MySQL complains when the physical column order drifts, so a reordered column
// silently writes every value into the wrong column. The map is the escape hatch,
// not a formality: adding an entry means accepting that risk for a table, and the
// default is to name the columns instead.
//
// Site counts, not file:line, because line numbers move every time a neighbour
// changes and a stale pin would fail for the wrong reason.
var gatewayMySQLUnlistedInsertSites = map[string]int{
	// inspection_receipts: 8 values (item, generation, actor, received, status,
	// reason, evidence, digest); one write is an idempotent "received" row, one is
	// the terminal verdict. Column order is identical on both sides, so the
	// positional binding is currently correct.
	"inspection_receipts": 2,
	// inspection_scope: 6 values (plan, asset, cluster, instance, cluster_id,
	// check); the site sits in a loop that attaches each authorized check.
	"inspection_scope": 1,
	// inventory_cluster_profiles: 6 values (asset, name, environment, contact,
	// created_by, revision); one of the three writes passes two literal empty
	// strings and the literal revision 1 instead of placeholders.
	"inventory_cluster_profiles": 3,
}

// TestGatewayMySQLInsertWithoutColumnListOrdersColumns guards INSERT INTO t
// VALUES (...). The statement is only correct while three things hold, and this
// test asserts all three per site:
//
//  1. the frozen SQLite export and the MySQL DDL declare the table's columns in
//     the same order, column by column — the one property the positional binding
//     depends on and the one nobody else checks;
//  2. the number of bound values equals the table's column count, so a missing or
//     extra placeholder is a failure instead of a silently shifted row;
//  3. the set of tables written this way equals gatewayMySQLUnlistedInsertSites,
//     so a new unlisted INSERT cannot land without a deliberate decision.
//
// Without this test the previous guard merely logged these statements, which made
// them the only place where a schema change could break the runtime with CI green.
func TestGatewayMySQLInsertWithoutColumnListOrdersColumns(t *testing.T) {
	export := gatewayMySQLTestSQLiteExport(t)
	ddl := string(gatewayMySQLSchemaSQL)
	seen := map[string]int{}
	sites := 0
	for _, literal := range gatewayMySQLRuntimeSQLLiterals(t) {
		table, ok := gatewayMySQLUnlistedInsertTable(literal.query)
		if !ok {
			continue
		}
		seen[table]++
		sites++
		exportColumns := gatewayMySQLSQLiteColumnOrder(t, export, table)
		ddlColumns := gatewayMySQLDDLColumnOrder(t, ddl, table)
		if len(exportColumns) != len(ddlColumns) {
			t.Fatalf("%s: %s:%d inserts without a column list, but the table has %d columns in the frozen SQLite export and %d in the MySQL DDL:\n      export: %v\n       mysql: %v\nname the columns in the INSERT or fix the exported schema",
				table, literal.file, literal.line, len(exportColumns), len(ddlColumns), exportColumns, ddlColumns)
		}
		for i := range exportColumns {
			if exportColumns[i] == ddlColumns[i] {
				continue
			}
			t.Fatalf("%s: %s:%d inserts without a column list, so every value is bound by position, but column %d is %q in the frozen SQLite export and %q in the MySQL DDL:\n      export: %v\n       mysql: %v\nre-capture the export and the native DDL from the same source, or name the columns in the INSERT",
				table, literal.file, literal.line, i+1, exportColumns[i], ddlColumns[i], exportColumns, ddlColumns)
		}
		values := gatewayMySQLInsertValues(t, literal)
		placeholders := 0
		for _, value := range values {
			if value == "?" {
				placeholders++
				continue
			}
			if !gatewayMySQLValueLiteral.MatchString(value) {
				t.Fatalf("%s:%d: cannot parse the value %q of an INSERT without a column list; only ?, quoted literals, numbers, NULL and booleans are supported",
					literal.file, literal.line, value)
			}
		}
		if len(values) != len(ddlColumns) {
			t.Fatalf("%s: %s:%d inserts %d values (%d placeholders) into %s, which has %d columns; SQLite only refuses the wrong arity at execution time and MySQL would accept it as positional data\nname the columns in the INSERT",
				table, literal.file, literal.line, len(values), placeholders, table, len(ddlColumns))
		}
	}
	if !reflect.DeepEqual(seen, gatewayMySQLUnlistedInsertSites) {
		t.Fatalf("the INSERT statements without a column list changed: measured %v, frozen %v.\nBefore adding a column-less INSERT, name the columns; to keep one, add the table to gatewayMySQLUnlistedInsertSites with the site count and say why in the comment",
			seen, gatewayMySQLUnlistedInsertSites)
	}
	t.Logf("checked %d INSERT statements without a column list across %d tables against the export order, the DDL order and their arity", sites, len(seen))
}

// gatewayMySQLUnlistedInsertTable returns the target table when the statement is
// an INSERT that goes straight from the table name to VALUES, i.e. names no
// column list. INSERT INTO t (a, b) VALUES (...) returns ok=false and stays with
// TestGatewayMySQLInsertDefaultsMatchSchema.
func gatewayMySQLUnlistedInsertTable(query string) (string, bool) {
	match := gatewayMySQLUnlistedInsertPattern.FindStringSubmatch(query)
	if match == nil {
		return "", false
	}
	return match[1], true
}

// gatewayMySQLInsertValues extracts the parenthesized value tuple after VALUES
// and splits it at top-level commas, so a quoted literal containing a comma or a
// parenthesis stays one value instead of corrupting the count.
func gatewayMySQLInsertValues(t *testing.T, literal gatewayMySQLRuntimeSQLLiteral) []string {
	t.Helper()
	index := gatewayMySQLValuesKeyword.FindStringIndex(literal.query)
	if index == nil {
		t.Fatalf("%s:%d: the statement matched an INSERT without a column list but has no VALUES keyword: %s",
			literal.file, literal.line, literal.query)
	}
	rest := literal.query[index[1]:]
	open := strings.Index(rest, "(")
	if open < 0 {
		t.Fatalf("%s:%d: VALUES is not followed by a value tuple: %s", literal.file, literal.line, literal.query)
	}
	var values []string
	depth := 0
	var quote rune
	start := open + 1
	closed := false
	for i, r := range rest[open:] {
		position := open + i
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				values = append(values, rest[start:position])
				closed = true
			}
		case ',':
			if depth == 1 {
				values = append(values, rest[start:position])
				start = position + 1
			}
		}
		if closed {
			break
		}
	}
	if !closed {
		t.Fatalf("%s:%d: the value tuple of the INSERT without a column list is not closed: %s",
			literal.file, literal.line, literal.query)
	}
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			trimmed = append(trimmed, value)
		}
	}
	if len(trimmed) == 0 {
		t.Fatalf("%s:%d: the INSERT without a column list has an empty value tuple", literal.file, literal.line)
	}
	return trimmed
}

// gatewayMySQLSQLiteColumnOrder reads the column order the SQLite runtime binds
// against. pragma_table_info reports the physical order, so unlike
// gatewayMySQLSQLiteColumnNames this helper must not sort.
func gatewayMySQLSQLiteColumnOrder(t *testing.T, db *sql.DB, table string) []string {
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
	if len(columns) == 0 {
		t.Fatalf("%s is missing from the frozen export, but the runtime inserts into it without a column list; migrate the table before guarding the INSERT", table)
	}
	return columns
}

// gatewayMySQLDDLColumnOrder returns the MySQL column order as the DDL declares
// it (gatewayMySQLDDLColumns keeps files order; the name helper sorts and must
// not be used here).
func gatewayMySQLDDLColumnOrder(t *testing.T, ddl, table string) []string {
	t.Helper()
	specs := gatewayMySQLDDLColumns(t, ddl, table)
	columns := make([]string, 0, len(specs))
	for _, spec := range specs {
		columns = append(columns, spec.name)
	}
	return columns
}

var (
	gatewayMySQLUnlistedInsertPattern = regexp.MustCompile("(?is)\\bINSERT\\s+(?:OR\\s+\\w+\\s+)?INTO\\s+([A-Za-z_][A-Za-z0-9_]*)\\s+VALUES\\b")
	gatewayMySQLValuesKeyword         = regexp.MustCompile("(?is)\\bVALUES\\b")
	gatewayMySQLValueLiteral          = regexp.MustCompile("(?i)^(?:NULL|TRUE|FALSE|[-+]?[0-9]+(?:\\.[0-9]+)?(?:[eE][-+]?[0-9]+)?|0x[0-9A-Fa-f]+|'(?:[^']|'')*'|\"(?:[^\"]|\"\")*\")$")
)
