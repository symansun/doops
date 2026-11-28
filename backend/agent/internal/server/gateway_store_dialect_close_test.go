package server

import "testing"

// TestGatewayStoreCloseRestoresTheOpeningDialect pins the ambient dialect to the
// live store: opening a MySQL store publishes the MySQL spelling, and closing
// that store puts the previous value back.
//
// This is the leak that made TestGatewayMySQLIdentifierRendering fail whenever
// TestGatewayMySQLHashColumnsAcceptIssuedHashes ran first: the hash-width test
// opens a real MySQL store and closes it, and before this fix the closed store
// kept the process rendering MySQL SQL, so the later guard - which asserts the
// production wiring still picks the SQLite catalog - read the wrong backend. CI
// never saw it because the MySQL tests skip when DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT
// is unset, so nothing published the MySQL dialect there.
func TestGatewayStoreCloseRestoresTheOpeningDialect(t *testing.T) {
	gatewayUseDialect(gatewayDialectSQLite, func() {
		store := &GatewayStore{backend: "mysql", dialect: gatewayDialectMySQL, previousDialect: gatewayDialectSQLite}
		gatewaySetDialect(gatewayDialectMySQL)
		if err := store.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if got, want := gatewayDialect(), gatewayDialectSQLite; got != want {
			t.Fatalf("dialect after Close = %q, want the value that was current when the store opened (%q)", got, want)
		}
		// A second Close must be a no-op: restoring twice would overwrite whatever
		// the process switched to after the first one.
		gatewaySetDialect(gatewayDialectMySQL)
		if err := store.Close(); err != nil {
			t.Fatalf("second Close: %v", err)
		}
		if got, want := gatewayDialect(), gatewayDialectMySQL; got != want {
			t.Fatalf("second Close changed the dialect to %q, want %q", got, want)
		}
	})

	// An SQLite store leaves the ambient value alone; opening it is what set it.
	gatewayUseDialect(gatewayDialectSQLite, func() {
		store := &GatewayStore{backend: "sqlite", dialect: gatewayDialectSQLite}
		if err := store.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if got, want := gatewayDialect(), gatewayDialectSQLite; got != want {
			t.Fatalf("closing an SQLite store changed the dialect to %q, want %q", got, want)
		}
	})

	// A MySQL store with no recorded previous value still lands on the documented
	// default rather than on an empty dialect no renderer understands.
	gatewayUseDialect(gatewayDialectMySQL, func() {
		store := &GatewayStore{backend: "mysql", dialect: gatewayDialectMySQL}
		if err := store.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if got, want := gatewayDialect(), gatewayDialectSQLite; got != want {
			t.Fatalf("dialect after closing a store with no recorded previous value = %q, want %q", got, want)
		}
	})
}
