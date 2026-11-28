package server

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestGatewayMySQLTableExistsProbeExecutes proves the two shapes of MySQL
// runtime SQL that used to read sqlite_master: the doorbell probe and the
// asset_monitor_series bridge probe behind PersistMonitorRunResults.
//
// The empty database is the interesting one: the probe must answer false
// without raising 1146, because the callers use that answer to skip optional
// writes. A test that only ran against an installed schema would pass even if
// the rendered SQL still said sqlite_master and merely got lucky.
func TestGatewayMySQLTableExistsProbeExecutes(t *testing.T) {
	db := gatewayMySQLTestOpenEmpty(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	store := &GatewayStore{db: db, dialect: gatewayDialectMySQL, backend: "mysql"}
	names := []string{"asset_monitor_series", "asset_monitor_samples", "monitor_resource_snapshots", "inspection_metric_points"}

	gatewayUseDialect(gatewayDialectMySQL, func() {
		mysqlProbe := gatewayTableExistsSQL(gatewayDialectMySQL)
		var exists bool
		if err := db.QueryRowContext(ctx, mysqlProbe, names[0]).Scan(&exists); err != nil {
			t.Fatalf("probe on the empty MySQL database: %v (the probe must read information_schema, not a SQLite catalog)", err)
		}
		if !strings.Contains(mysqlProbe, "information_schema.tables") || !strings.Contains(mysqlProbe, "DATABASE()") {
			t.Fatalf("MySQL table probe = %q, want information_schema.tables scoped by DATABASE()", mysqlProbe)
		}
		if sqliteProbe := gatewayTableExistsSQL(gatewayDialectSQLite); !strings.Contains(sqliteProbe, "sqlite_master") {
			t.Fatalf("SQLite table probe = %q, want sqlite_master", sqliteProbe)
		}
		if exists {
			t.Fatalf("probe on the empty MySQL database reported %s exists", names[0])
		}
		for _, name := range names {
			if store.tableExists(ctx, name) {
				t.Fatalf("empty MySQL database reports %s exists before the schema is installed", name)
			}
		}

		// The bridge writer is the statement that follows the probe in
		// PersistMonitorRunResults. Without the probe gate it would run against
		// the missing table and fail with 1146; with the gate it is skipped.
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin bridge probe: %v", err)
		}
		value := 1.0
		_, bridgeErr := storeMonitorPoint(ctx, tx, monitorRequest{Cluster: "c1", Instance: "i1"},
			monitorInput{monitorAsset: monitorAsset{Type: "machine", AssetID: "m1", Namespace: "default"}, Metric: "cpu", Unit: "percent", monitorPoint: monitorPoint{At: 1700000000000, Status: "ok", Value: &value}}, "exporter", "")
		tx.Rollback()
		if bridgeErr == nil || !strings.Contains(bridgeErr.Error(), "1146") {
			t.Fatalf("bridge writer on a schema-less database = %v, want MySQL error 1146 so the probe gate is what prevents it", bridgeErr)
		}

		if _, err := installGatewayMySQLSchema(ctx, db); err != nil {
			t.Fatalf("install MySQL schema: %v", err)
		}
		if err := db.QueryRowContext(ctx, mysqlProbe, names[0]).Scan(&exists); err != nil {
			t.Fatalf("probe on the installed MySQL schema: %v", err)
		}
		if !exists {
			t.Fatalf("probe on the installed MySQL schema reported %s missing", names[0])
		}
		for _, name := range names {
			if !store.tableExists(ctx, name) {
				t.Fatalf("installed MySQL schema reports %s missing", name)
			}
		}

		tx, err = db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin bridge: %v", err)
		}
		if _, err := storeMonitorPoint(ctx, tx, monitorRequest{Cluster: "c1", Instance: "i1"},
			monitorInput{monitorAsset: monitorAsset{Type: "machine", AssetID: "m1", Namespace: "default"}, Metric: "cpu", Unit: "percent", monitorPoint: monitorPoint{At: 1700000000000, Status: "ok", Value: &value}}, "exporter", ""); err != nil {
			tx.Rollback()
			t.Fatalf("bridge writer on the installed schema: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit bridge: %v", err)
		}
		var series, samples int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM asset_monitor_series").Scan(&series); err != nil {
			t.Fatalf("count series: %v", err)
		}
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM asset_monitor_samples").Scan(&samples); err != nil {
			t.Fatalf("count samples: %v", err)
		}
		if series != 1 || samples != 1 {
			t.Fatalf("bridge wrote series=%d samples=%d, want 1 and 1", series, samples)
		}
	})
}

// TestGatewayMySQLTableExistsProbeOnSQLite keeps the same probe on the default
// store: migrate() creates both core tables (audit_events) and asset tables
// (asset_monitor_series), and the probe must answer true for existing tables
// and false for absent ones without raising "no such table".
func TestGatewayMySQLTableExistsProbeOnSQLite(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	gatewayUseDialect(gatewayDialectSQLite, func() {
		if !store.tableExists(ctx, "audit_events") {
			t.Fatalf("migrated SQLite store reports audit_events missing")
		}
		if !store.tableExists(ctx, "asset_monitor_series") {
			t.Fatalf("migrated SQLite store reports auto-migrated asset_monitor_series missing")
		}
		if store.tableExists(ctx, "nonexistent_probe_table") {
			t.Fatalf("SQLite probe invented nonexistent_probe_table")
		}
	})
}
