package main

import (
	"database/sql"
	"slices"
	"testing"
)

func TestGatewayDoesNotRegisterOracleOrSQLServer(t *testing.T) {
	drivers := sql.Drivers()
	for _, name := range []string{"sqlserver", "oracle"} {
		if slices.Contains(drivers, name) {
			t.Errorf("Gateway unexpectedly registers Agent-only DoSQL driver %q", name)
		}
	}
}
