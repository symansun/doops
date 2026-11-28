package server

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// The three SQLite-only helpers are called from migrate(), which only runs on the
// SQLite branch of OpenGatewayStore. A comment cannot prove that stays true, so each
// entry now refuses to run while the store dialect is MySQL: callers get a named
// error instead of the 1064 that PRAGMA or EXPLAIN would raise on those engines.
func TestSQLiteOnlyPathRefusesMySQLDialect(t *testing.T) {
	db := gatewayMySQLTestOpenEmpty(t)
	store := &GatewayStore{db: db, dialect: gatewayDialectMySQL, backend: "mysql"}
	gatewayUseDialect(gatewayDialectMySQL, func() {
		def := "TEXT NOT NULL DEFAULT ''"
		if err := store.ensureColumn("audit_events", "probe_column", def); !errors.Is(err, errGatewaySQLiteOnlyPath) {
			t.Fatalf("ensureColumn error = %v, want gateway_sqlite_only_path", err)
		}
		has, err := store.tableHasColumn("audit_events", "probe_column")
		if has || !errors.Is(err, errGatewaySQLiteOnlyPath) {
			t.Fatalf("tableHasColumn = %v, %v, want false, gateway_sqlite_only_path", has, err)
		}
		plans, err := store.optimizeAuditIndexes(context.Background())
		if plans != nil || !errors.Is(err, errGatewaySQLiteOnlyPath) {
			t.Fatalf("optimizeAuditIndexes = %v, %v, want nil, gateway_sqlite_only_path", plans, err)
		}
		err = store.ensureColumn("audit_events", "probe_column", def)
		if err == nil || !strings.Contains(err.Error(), "runs only in the SQLite migration path") {
			t.Fatalf("guard message %q does not name the contract", err)
		}
	})
	// Outside the MySQL scope the guard is inactive; this store is nevertheless bound
	// to a MySQL connection, so the PRAGMA branch now fails with the engine's own
	// 1064. That is exactly the failure the guard replaces while the dialect is MySQL.
	err := store.ensureColumn("audit_events", "probe_column", "TEXT")
	if errors.Is(err, errGatewaySQLiteOnlyPath) {
		t.Fatalf("guard stayed active after the dialect scope exited: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "1064") {
		t.Fatalf("unguarded PRAGMA on MySQL = %v, want the 1064 the guard avoids", err)
	}
}

func TestSQLiteOnlyPathStillRunsOnSQLite(t *testing.T) {
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	gatewayUseDialect(gatewayDialectSQLite, func() {
		if err := store.ensureColumn("audit_events", "int32_probe", "TEXT NOT NULL DEFAULT ''"); err != nil {
			t.Fatalf("ensureColumn on SQLite: %v", err)
		}
		has, err := store.tableHasColumn("audit_events", "int32_probe")
		if err != nil || !has {
			t.Fatalf("tableHasColumn after ensureColumn = %v, %v, want true, nil", has, err)
		}
		plans, err := store.optimizeAuditIndexes(context.Background())
		if err != nil {
			t.Fatalf("optimizeAuditIndexes on SQLite: %v", err)
		}
		if len(plans) != 13 {
			t.Fatalf("optimizeAuditIndexes returned %d plans, want 13", len(plans))
		}
	})
}
