package server

import (
	"testing"
	"time"
)

// gatewaySchemaMigrationsTestStore opens a fresh SQLite store in a temporary
// directory. OpenGatewayStore already runs migrate(), so the ledger is present
// before the test looks at it.
func gatewaySchemaMigrationsTestStore(t *testing.T) *GatewayStore {
	t.Helper()
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open gateway store: %v", err)
	}
	t.Cleanup(func() { store.db.Close() })
	return store
}

func gatewaySchemaMigrationsRows(t *testing.T, store *GatewayStore) []string {
	t.Helper()
	rows, err := store.db.Query("SELECT version, applied_at FROM gateway_schema_migrations ORDER BY version")
	if err != nil {
		t.Fatalf("read migration ledger: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var version, applied string
		if err := rows.Scan(&version, &applied); err != nil {
			t.Fatalf("scan migration ledger: %v", err)
		}
		if _, err := time.Parse(time.RFC3339, applied); err != nil {
			t.Fatalf("applied_at %q is not RFC3339: %v", applied, err)
		}
		out = append(out, version+"|"+applied)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migration ledger: %v", err)
	}
	return out
}

// TestGatewaySchemaMigrationsLedgerBaseline proves a fresh database ends up at
// exactly one baseline row. A ledger that is empty cannot tell a first start
// from a legacy database, and a ledger with several rows would misreport the
// schema revision.
func TestGatewaySchemaMigrationsLedgerBaseline(t *testing.T) {
	store := gatewaySchemaMigrationsTestStore(t)
	rows := gatewaySchemaMigrationsRows(t, store)
	if len(rows) != 1 {
		t.Fatalf("fresh store migration ledger = %v, want exactly one baseline row", rows)
	}
	version, applied := splitLedgerRow(t, rows[0])
	if version != gatewaySchemaBaselineVersion {
		t.Fatalf("baseline version = %q, want %q", version, gatewaySchemaBaselineVersion)
	}
	if _, err := time.Parse(time.RFC3339, applied); err != nil {
		t.Fatalf("baseline applied_at %q is not RFC3339: %v", applied, err)
	}
	if applied[len(applied)-1] != 'Z' {
		t.Fatalf("baseline applied_at = %q, want a UTC (Z) timestamp", applied)
	}
}

// TestGatewaySchemaMigrationsLedgerIdempotent proves a second migrate() on the
// same database writes nothing. The stored timestamp is replaced with a
// sentinel first: if the baseline insert ran again, the sentinel would be
// overwritten, and with the primary key removed a second row would appear.
func TestGatewaySchemaMigrationsLedgerIdempotent(t *testing.T) {
	store := gatewaySchemaMigrationsTestStore(t)
	const sentinel = "2000-01-01T00:00:00Z"
	if _, err := store.db.Exec("UPDATE gateway_schema_migrations SET applied_at = ?", sentinel); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}
	if err := store.migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	rows := gatewaySchemaMigrationsRows(t, store)
	if len(rows) != 1 {
		t.Fatalf("migration ledger after a second migrate = %v, want the single baseline row", rows)
	}
	version, applied := splitLedgerRow(t, rows[0])
	if version != gatewaySchemaBaselineVersion || applied != sentinel {
		t.Fatalf("second migrate rewrote the ledger row to %q|%q, want %q|%q", version, applied, gatewaySchemaBaselineVersion, sentinel)
	}
}

// TestGatewaySchemaMigrationsLedgerRebuilds proves a dropped ledger is rebuilt
// together with its baseline row: a database restored from a partial copy or a
// hand-edited file must not stay at an unknown revision.
func TestGatewaySchemaMigrationsLedgerRebuilds(t *testing.T) {
	store := gatewaySchemaMigrationsTestStore(t)
	if _, err := store.db.Exec("DROP TABLE gateway_schema_migrations"); err != nil {
		t.Fatalf("drop ledger: %v", err)
	}
	if err := store.migrate(); err != nil {
		t.Fatalf("migrate after drop: %v", err)
	}
	rows := gatewaySchemaMigrationsRows(t, store)
	if len(rows) != 1 {
		t.Fatalf("rebuilt migration ledger = %v, want exactly one baseline row", rows)
	}
	version, _ := splitLedgerRow(t, rows[0])
	if version != gatewaySchemaBaselineVersion {
		t.Fatalf("rebuilt baseline version = %q, want %q", version, gatewaySchemaBaselineVersion)
	}
}

func splitLedgerRow(t *testing.T, row string) (string, string) {
	t.Helper()
	for i := 0; i < len(row); i++ {
		if row[i] == '|' {
			return row[:i], row[i+1:]
		}
	}
	t.Fatalf("ledger row %q has no separator", row)
	return "", ""
}
