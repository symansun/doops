package server

import (
	"testing"
)

// TestGatewayDialectFollowsOpenedStore pins D16: the dialect is process-global,
// so a store must set it on open rather than inherit whatever the previous
// store left behind. Migration runs PRAGMA/AUTOINCREMENT statements, which only
// parse on SQLite, so a stale MySQL dialect would break the open itself.
func TestGatewayDialectFollowsOpenedStore(t *testing.T) {
	previous := gatewayDialect()
	t.Cleanup(func() { gatewaySetDialect(previous) })

	gatewaySetDialect(gatewayDialectMySQL)
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open sqlite store while the ambient dialect is mysql: %v", err)
	}
	defer store.Close()
	if got := gatewayDialect(); got != gatewayDialectSQLite {
		t.Fatalf("opening a sqlite store left the ambient dialect at %v, want sqlite", got)
	}
	if store.dialect != gatewayDialectSQLite {
		t.Fatalf("store.dialect = %v, want sqlite", store.dialect)
	}
	// The ambient value must also reach the renderers the store's own statements
	// use, not just the struct field.
	if rendered := gatewayEpochNowSQL(); rendered != "unixepoch()" {
		t.Fatalf("gatewayEpochNowSQL() = %q after opening a sqlite store, want unixepoch()", rendered)
	}
}
