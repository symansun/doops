package server

import (
	"database/sql"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// gatewayBootstrapProjection names the columns each bootstrap row is compared
// on. A new bootstrap row must appear here, so the two-backend comparison has
// a visible contract instead of comparing whatever happens to be selected.
var gatewayBootstrapProjection = map[string][]string{
	"gateway_schema_migrations": {"version"},
	"edge_route_revision":       {"id", "version"},
}

func gatewayReadBootstrapRowSet(t *testing.T, db *sql.DB, dialect string) map[string][][]any {
	t.Helper()
	out := map[string][][]any{}
	for _, row := range gatewayStoreBootstrapRows() {
		columns, ok := gatewayBootstrapProjection[row.table]
		if !ok {
			t.Fatalf("bootstrap table %s has no projection in gatewayBootstrapProjection", row.table)
		}
		query := "SELECT " + strings.Join(columns, ", ") + " FROM " + row.table + " ORDER BY 1"
		rows, err := db.Query(query)
		if err != nil {
			t.Fatalf("%s: read %s: %v", dialect, row.table, err)
		}
		var table [][]any
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				t.Fatalf("%s: scan %s: %v", dialect, row.table, err)
			}
			for i, value := range values {
				if raw, ok := value.([]byte); ok {
					values[i] = string(raw)
				}
			}
			table = append(table, values)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatalf("%s: read %s: %v", dialect, row.table, err)
		}
		rows.Close()
		out[row.table] = table
	}
	return out
}

// TestGatewayBootstrapRowsMatchAcrossBackends is the INT-33 parity guard: it
// boots a real SQLite store (migrate) and a real MySQL store (install + shared
// seeds), then requires the bootstrap row set to be identical. A seed added to
// one backend only turns this red.
func TestGatewayBootstrapRowsMatchAcrossBackends(t *testing.T) {
	sqlitePath := filepath.Join(t.TempDir(), "gateway.db")
	sqliteStore, err := OpenGatewayStore(sqlitePath)
	if err != nil {
		t.Fatalf("open SQLite store: %v", err)
	}
	t.Cleanup(func() { sqliteStore.Close() })

	mysqlDB := gatewayCopyTestDestination(t)
	var database string
	if err := mysqlDB.QueryRow("SELECT DATABASE()").Scan(&database); err != nil {
		t.Fatalf("read fixture database name: %v", err)
	}
	port := strings.TrimSpace(os.Getenv("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT"))
	dsn := fmt.Sprintf("root@tcp(%s)/%s?parseTime=true", net.JoinHostPort("127.0.0.1", port), database)
	t.Setenv("DOOPS_GATEWAY_STORE", "mysql")
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", dsn)
	t.Setenv("DOOPS_GATEWAY_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	previous := gatewayDialect()
	t.Cleanup(func() { gatewaySetDialect(previous) })
	mysqlStore, err := OpenGatewayStore("")
	if err != nil {
		t.Fatalf("open MySQL store: %v", err)
	}
	t.Cleanup(func() { mysqlStore.Close() })

	if got := gatewayReadBootstrapRowSet(t, sqliteStore.db, "sqlite"); !reflect.DeepEqual(got, gatewayReadBootstrapRowSet(t, mysqlStore.db, "mysql")) {
		// Report per table so a diff names the row that drifted.
		sq := gatewayReadBootstrapRowSet(t, sqliteStore.db, "sqlite")
		my := gatewayReadBootstrapRowSet(t, mysqlStore.db, "mysql")
		for table := range sq {
			if !reflect.DeepEqual(sq[table], my[table]) {
				t.Errorf("bootstrap table %s: sqlite=%v mysql=%v", table, sq[table], my[table])
			}
		}
		t.Fatal("the MySQL install and the SQLite migrate seed different bootstrap rows")
	}
	for table, rows := range gatewayReadBootstrapRowSet(t, mysqlStore.db, "mysql") {
		if len(rows) != 1 {
			t.Errorf("MySQL bootstrap table %s holds %d rows, want exactly 1", table, len(rows))
		}
	}
}
