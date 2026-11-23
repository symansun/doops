package server

import (
	"database/sql"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestGatewayMySQLAgentStatusColumnsMatchExport is the column-level guard for
// agent_status, the one table the resource sync extends at runtime.
//
// The frozen export is a production capture, not a rebuild: re-capturing it
// from a fresh migration would delete legacy production indexes the migration
// no longer creates, so a whole-schema column diff would turn into a second
// re-capture. The guard is therefore scoped to the table that gains columns
// outside the production dump. A column missing on either side breaks the
// online path: too few on MySQL and MarkAgentOnline hits error 1364; too many
// and the copier writes a column the runtime cannot serve.
func TestGatewayMySQLAgentStatusColumnsMatchExport(t *testing.T) {
	const table = "agent_status"
	exportColumns := gatewayMySQLAgentStatusSQLiteColumns(t, gatewayMySQLTestSQLiteExport(t), table)
	sort.Strings(exportColumns)
	ddlColumns := gatewayMySQLAgentStatusDDLColumns(t, string(gatewayMySQLSchemaSQL), table)
	if !reflect.DeepEqual(exportColumns, ddlColumns) {
		t.Fatalf("agent_status columns differ between the frozen SQLite export and the MySQL DDL:\n export: %v\n   mysql: %v", exportColumns, ddlColumns)
	}
}

// TestGatewayMySQLAgentStatusInsertMatchesSchema replays the INSERT that
// MarkAgentOnline executes against a disposable MySQL 8.4 install, so the
// MySQL DDL is checked against the real statement shape instead of a
// hand-written minimal row. It fails when MySQL refuses a value the runtime
// leaves to the schema, and when a column the runtime writes as "unknown"
// (NULL) is declared NOT NULL.
func TestGatewayMySQLAgentStatusInsertMatchesSchema(t *testing.T) {
	const table = "agent_status"
	insertColumns := gatewayMySQLAgentStatusInsertColumns(t)
	db := gatewayCopyTestDestination(t)
	if _, err := installGatewayMySQLSchema(t.Context(), db); err != nil {
		t.Fatalf("install MySQL schema: %v", err)
	}

	columns := gatewayMySQLAgentStatusMySQLColumns(t, db, table)
	// The Go side writes kubernetes_available from a *bool, so an unreported
	// mode is NULL rather than false. The reflection check keeps the replay
	// honest if that field ever becomes a plain bool.
	field, ok := reflect.TypeOf((*GatewayAgent)(nil)).Elem().FieldByName("KubernetesAvailable")
	if !ok || field.Type.Kind() != reflect.Ptr {
		t.Fatalf("GatewayAgent.KubernetesAvailable = %v, want a pointer so NULL means unsupported", field.Type)
	}

	present := make(map[string]bool, len(insertColumns))
	for _, column := range insertColumns {
		if _, ok := columns[column]; !ok {
			t.Fatalf("MarkAgentOnline inserts %q, which the MySQL schema does not define", column)
		}
		present[column] = true
	}
	for name, meta := range columns {
		if present[name] || meta.nullable || meta.hasDefault {
			continue
		}
		t.Fatalf("MarkAgentOnline omits %q, but the MySQL column is NOT NULL without a default (insert error 1364)", name)
	}

	placeholders := make([]string, len(insertColumns))
	args := make([]any, len(insertColumns))
	for i, column := range insertColumns {
		placeholders[i] = "?"
		meta := columns[column]
		switch {
		case column == "kubernetes_available":
			args[i] = nil
		case meta.numeric():
			args[i] = 0
		default:
			args[i] = ""
		}
	}
	insert := "INSERT INTO " + table + " (" + strings.Join(insertColumns, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
	if _, err := db.Exec(insert, args...); err != nil {
		t.Fatalf("MarkAgentOnline INSERT shape fails on MySQL: %v", err)
	}
	var kubernetesAvailable sql.NullBool
	if err := db.QueryRow("SELECT kubernetes_available FROM " + table).Scan(&kubernetesAvailable); err != nil {
		t.Fatalf("read back kubernetes_available: %v", err)
	}
	if kubernetesAvailable.Valid {
		t.Fatalf("kubernetes_available = %v, want NULL for an Agent that did not report it", kubernetesAvailable.Bool)
	}
}

type gatewayMySQLAgentStatusColumn struct {
	dataType   string
	nullable   bool
	hasDefault bool
}

func (c gatewayMySQLAgentStatusColumn) numeric() bool {
	switch c.dataType {
	case "tinyint", "smallint", "mediumint", "int", "bigint", "decimal", "double", "float":
		return true
	}
	return false
}

func gatewayMySQLAgentStatusSQLiteColumns(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query("SELECT name FROM pragma_table_info(?) ORDER BY cid", table)
	if err != nil {
		t.Fatalf("read %s from the frozen export: %v", table, err)
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
		t.Fatalf("read %s columns: %v", table, err)
	}
	return columns
}

func gatewayMySQLAgentStatusDDLColumns(t *testing.T, ddl, table string) []string {
	t.Helper()
	body := gatewayMySQLAgentStatusDDLBody(t, ddl, table)
	nonColumn := regexp.MustCompile("^(?:PRIMARY KEY|KEY|UNIQUE|CONSTRAINT|INDEX|FOREIGN|CHECK)\\b")
	var columns []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || nonColumn.MatchString(strings.ToUpper(line)) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		columns = append(columns, fields[0])
	}
	sort.Strings(columns)
	return columns
}

func gatewayMySQLAgentStatusDDLBody(t *testing.T, ddl, table string) string {
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

func gatewayMySQLAgentStatusMySQLColumns(t *testing.T, db *sql.DB, table string) map[string]gatewayMySQLAgentStatusColumn {
	t.Helper()
	query := "SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE, COLUMN_DEFAULT, EXTRA FROM information_schema.COLUMNS" +
		" WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?"
	rows, err := db.Query(query, table)
	if err != nil {
		t.Fatalf("read %s columns from MySQL: %v", table, err)
	}
	defer rows.Close()
	columns := make(map[string]gatewayMySQLAgentStatusColumn)
	for rows.Next() {
		var name, dataType, nullable, extra string
		var defaultValue sql.NullString
		if err := rows.Scan(&name, &dataType, &nullable, &defaultValue, &extra); err != nil {
			t.Fatalf("scan %s column: %v", table, err)
		}
		columns[name] = gatewayMySQLAgentStatusColumn{
			dataType:   strings.ToLower(dataType),
			nullable:   strings.EqualFold(nullable, "YES"),
			hasDefault: defaultValue.Valid || strings.Contains(strings.ToUpper(extra), "DEFAULT_GENERATED"),
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read %s columns from MySQL: %v", table, err)
	}
	return columns
}

// gatewayMySQLAgentStatusInsertColumns extracts the column list of the INSERT
// MarkAgentOnline runs, so the replay follows the statement in the source.
func gatewayMySQLAgentStatusInsertColumns(t *testing.T) []string {
	t.Helper()
	pattern := regexp.MustCompile("(?s)INSERT INTO agent_status\\s*\\(([^)]*)\\)")
	for _, literal := range gatewayMySQLRuntimeSQLLiterals(t) {
		if literal.file != "gateway_store_agents.go" {
			continue
		}
		match := pattern.FindStringSubmatch(literal.query)
		if match == nil {
			continue
		}
		var columns []string
		for _, column := range strings.Split(match[1], ",") {
			columns = append(columns, strings.TrimSpace(column))
		}
		return columns
	}
	t.Fatalf("MarkAgentOnline INSERT INTO agent_status was not found")
	return nil
}
