package main

import (
	"database/sql"
	"slices"
	"testing"
)

func TestAgentRegistersDoSQLDrivers(t *testing.T) {
	drivers := sql.Drivers()
	for _, name := range []string{"mysql", "pgx", "sqlserver", "oracle"} {
		if !slices.Contains(drivers, name) {
			t.Errorf("Agent missing DoSQL driver %q; registered: %v", name, drivers)
		}
	}
}
