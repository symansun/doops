package server

import (
	"database/sql"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// gatewayMySQLUnlistedInsertSites must stay empty. Every statement that used to
// rely on the physical column order now names its columns, and a new one is not
// allowed to come back: positional values bind by position, so a reorder of the
// table (a SQLite ALTER TABLE rebuild or a hand edit of the native DDL) writes
// every value into the wrong column without any test noticing. Keeping the map
// (instead of deleting the concept) documents that the answer to a failing
// statement is a column list, not a new exemption.
var gatewayMySQLUnlistedInsertSites = map[string]int{}

// gatewayMySQLPositionalInsertSites freezes the six statements that were
// rewritten from INSERT INTO t VALUES (...) into an explicit column list, keyed
// by table with the number of sites. Their column list must equal the frozen
// export's physical order: that is what makes the rewrite provably
// behavior-preserving, because each value still lands in the column it landed in
// before. Dropping or reordering a name now changes the binding and fails here.
var gatewayMySQLPositionalInsertSites = map[string]int{
	"inspection_receipts":        2, // 8 values: item, generation, actor, received, status, reason, evidence, digest
	"inspection_scope":           1, // 6 values: plan, asset, cluster, instance, cluster_id, check
	"inventory_cluster_profiles": 3, // 6 values: asset, name, environment, contact, created_by, revision
}

// TestGatewayMySQLInsertsNameColumns is the arity and column-order guard for the
// runtime INSERTs. It enforces three things:
//
//  1. no runtime INSERT omits its column list — the suites fail with the file and
//     line of any statement that tries, instead of skipping it;
//  2. for the six statements converted from positional form, the named columns
//     still equal the frozen export's physical column order and the MySQL DDL
//     declares that same order, so the conversion did not move a value;
//  3. the frozen site counts still match, so a second write to the same table
//     cannot appear unnoticed.
//
// The value count is compared with the column count as well, so a placeholder
// added or dropped by hand fails here rather than at execution time. Question
// marks inside quoted literals are not counted, and literal values (the two
// empty strings and the literal 1 in cluster_profiles.go) count as values.
func TestGatewayMySQLInsertsNameColumns(t *testing.T) {
	export := gatewayMySQLTestSQLiteExport(t)
	ddl := string(gatewayMySQLSchemaSQL)
	var unlisted []string
	positional := map[string]int{}
	for _, literal := range gatewayMySQLRuntimeSQLLiterals(t) {
		if table, ok := gatewayMySQLUnlistedInsertTable(literal.query); ok {
			unlisted = append(unlisted, fmt.Sprintf("%s:%d (INSERT INTO %s)", literal.file, literal.line, table))
			continue
		}
		table, columns, ok := gatewayMySQLInsertColumns(literal.query)
		if !ok {
			continue
		}
		if _, frozen := gatewayMySQLPositionalInsertSites[table]; !frozen {
			continue
		}
		positional[table]++
		exportColumns := gatewayMySQLSQLiteColumnOrder(t, export, table)
		ddlColumns := gatewayMySQLDDLColumnOrder(t, ddl, table)
		if len(exportColumns) != len(ddlColumns) {
			t.Fatalf("%s: the frozen SQLite export and the MySQL DDL declare %d and %d columns:\n      export: %v\n       mysql: %v",
				table, len(exportColumns), len(ddlColumns), exportColumns, ddlColumns)
		}
		for i := range exportColumns {
			if exportColumns[i] != ddlColumns[i] {
				t.Fatalf("%s: column %d is %q in the frozen SQLite export and %q in the MySQL DDL; re-capture both from the same source",
					table, i+1, exportColumns[i], ddlColumns[i])
			}
		}
		if len(columns) != len(exportColumns) {
			t.Fatalf("%s: %s:%d names %d columns, but the table has %d:\n      named: %v\n     export: %v",
				table, literal.file, literal.line, len(columns), len(exportColumns), columns, exportColumns)
		}
		for i := range columns {
			if columns[i] == exportColumns[i] {
				continue
			}
			t.Fatalf("%s: %s:%d names %q as column %d, but the frozen export's column %d is %q; the positional statement this replaced bound the %d-th value to %q, so the column list must keep the export order (named: %v, export: %v)",
				table, literal.file, literal.line, columns[i], i+1, i+1, exportColumns[i], i+1, exportColumns[i], columns, exportColumns)
		}
		values := gatewayMySQLInsertValues(t, literal)
		placeholders := 0
		for _, value := range values {
			if value == "?" {
				placeholders++
				continue
			}
			if !gatewayMySQLValueLiteral.MatchString(value) {
				t.Fatalf("%s:%d: cannot parse the value %q of a runtime INSERT; only ?, quoted literals, numbers, NULL and booleans are supported",
					literal.file, literal.line, value)
			}
		}
		if len(values) != len(columns) {
			t.Fatalf("%s: %s:%d binds %d values (%d placeholders) but names %d columns; each placeholder and each literal value fills exactly one column",
				table, literal.file, literal.line, len(values), placeholders, len(columns))
		}
	}
	if len(unlisted) != 0 {
		t.Fatalf("INSERT without a column list is forbidden; name the columns: %s", strings.Join(unlisted, ", "))
	}
	if len(gatewayMySQLUnlistedInsertSites) != 0 {
		t.Fatalf("gatewayMySQLUnlistedInsertSites must stay empty, found %v; a positional INSERT binds by physical column order and cannot be made safe by an exemption", gatewayMySQLUnlistedInsertSites)
	}
	if !reflect.DeepEqual(positional, gatewayMySQLPositionalInsertSites) {
		t.Fatalf("the INSERT statements that must name every column in export order changed: measured %v, frozen %v.\nName the columns explicitly; if a new write is legitimate, add its table and site count to gatewayMySQLPositionalInsertSites and say why",
			positional, gatewayMySQLPositionalInsertSites)
	}
	t.Logf("checked %d INSERT statements across %d tables for explicit column lists, export order, DDL order and arity", sumCounts(positional), len(positional))
}

func sumCounts(counts map[string]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}

// gatewayMySQLUnlistedInsertTable returns the target table when the statement is
// an INSERT that goes straight from the table name to VALUES, i.e. names no
// column list. Column-list statements return ok=false and are handled by
// TestGatewayMySQLInsertDefaultsMatchSchema and the order check above.
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
		t.Fatalf("%s:%d: the statement matched an INSERT but has no VALUES keyword: %s",
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
		t.Fatalf("%s:%d: the value tuple is not closed: %s", literal.file, literal.line, literal.query)
	}
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			trimmed = append(trimmed, value)
		}
	}
	if len(trimmed) == 0 {
		t.Fatalf("%s:%d: the INSERT has an empty value tuple", literal.file, literal.line)
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
		t.Fatalf("%s is missing from the frozen export, but the runtime inserts into it; migrate the table before guarding the INSERT", table)
	}
	return columns
}

// gatewayMySQLDDLColumnOrder returns the MySQL column order as the DDL declares
// it (gatewayMySQLDDLColumns keeps file order; the name helper sorts and must
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
