package server

// INT-40: the Agent resource sync ages observations with a two-argument scalar
// MIN, which MySQL does not have. These tests run the shipped statement -- not a
// re-written copy -- against real SQLite and real MySQL (mysql:8.4 when
// DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT is set) and assert the same rows, the same
// cap, and the same NULL behaviour.

import (
	"database/sql"
	"reflect"
	"testing"
)

func scalarMinMaxSQLiteDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(`CREATE TABLE agent_observations (cluster TEXT NOT NULL, instance TEXT NOT NULL, kind TEXT NOT NULL, key TEXT NOT NULL, misses INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (cluster, instance, kind, key))`); err != nil {
		t.Fatal(err)
	}
	return db
}

func scalarMinMaxMySQLDB(t *testing.T) *sql.DB {
	t.Helper()
	db := gatewayCopyTestDestination(t)
	if _, err := db.Exec("CREATE TABLE agent_observations (cluster VARCHAR(64) NOT NULL, instance VARCHAR(64) NOT NULL, kind VARCHAR(16) NOT NULL, " + gatewayMySQLIdentifier("key") + " VARCHAR(128) NOT NULL, misses BIGINT NOT NULL DEFAULT 0, PRIMARY KEY (cluster, instance, kind, " + gatewayMySQLIdentifier("key") + ")) ENGINE=InnoDB"); err != nil {
		t.Fatal(err)
	}
	return db
}

func scalarMinMaxSeed(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, row := range []struct{ kind, key string }{{"machine", "host-a"}, {"machine", "host-b"}, {"cluster", "cluster-a"}} {
		if _, err := db.Exec("INSERT INTO agent_observations(cluster,instance,kind,"+gatewayMySQLIdentifier("key")+",misses) VALUES (?,?,?,?,0)", "mx", "mx1", row.kind, row.key); err != nil {
			t.Fatal(err)
		}
	}
}

func scalarMinMaxMisses(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query("SELECT kind," + gatewayMySQLIdentifier("key") + ",misses FROM agent_observations ORDER BY kind," + gatewayMySQLIdentifier("key"))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var kind, key string
		var misses int64
		if err = rows.Scan(&kind, &key, &misses); err != nil {
			t.Fatal(err)
		}
		out = append(out, kind+"/"+key+"="+itoa(misses))
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func itoa(value int64) string {
	if value == 0 {
		return "0"
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

// TestAgentObservationMissesAcrossEngines runs the shipped statement three times
// with one reported kind, threshold 2, and three rows. The reported kind's two
// rows climb 1, 2 and then stay capped at 2; the cluster row, whose kind was not
// reported this round, is not touched because it is not in the IN list. Both
// engines must return exactly that.
func TestAgentObservationMissesAcrossEngines(t *testing.T) {
	sqlite := scalarMinMaxSQLiteDB(t)
	mysqlDB := scalarMinMaxMySQLDB(t)
	for _, engine := range []struct {
		db      *sql.DB
		dialect gatewaySQLDialect
	}{{sqlite, gatewayDialectSQLite}, {mysqlDB, gatewayDialectMySQL}} {
		scalarMinMaxSeed(t, engine.db)
		for round := 0; round < 3; round++ {
			if _, err := engine.db.Exec(agentObservationMissesSQL(engine.dialect), 2, "mx", "mx1", `["machine"]`); err != nil {
				t.Fatalf("%s round %d: %v", engine.dialect, round, err)
			}
		}
	}
	gotSQLite, gotMySQL := scalarMinMaxMisses(t, sqlite), scalarMinMaxMisses(t, mysqlDB)
	want := []string{"cluster/cluster-a=0", "machine/host-a=2", "machine/host-b=2"}
	if !reflect.DeepEqual(gotSQLite, want) {
		t.Errorf("SQLite misses = %v, want %v", gotSQLite, want)
	}
	if !reflect.DeepEqual(gotMySQL, want) {
		t.Errorf("MySQL misses = %v, want %v", gotMySQL, want)
	}
	t.Logf("SQLite misses: %v", gotSQLite)
	t.Logf("MySQL  misses: %v", gotMySQL)
}

// TestScalarMinMaxNullSemanticsAcrossEngines pins the property the rewrite relies
// on: SQLite's scalar MIN(a, b)/MAX(a, b) and MySQL's LEAST/GREATEST all return
// NULL when either argument is NULL. A comparison or COALESCE would not, which is
// why neither may replace the scalar function.
func TestScalarMinMaxNullSemanticsAcrossEngines(t *testing.T) {
	sqlite := scalarMinMaxSQLiteDB(t)
	mysqlDB := scalarMinMaxMySQLDB(t)
	for _, tc := range []struct {
		name string
		a, b any
		want int64
		null bool
	}{
		{name: "lower", a: 5, b: 3, want: 3},
		{name: "first null", a: nil, b: 3, null: true},
		{name: "second null", a: 5, b: nil, null: true},
		{name: "both null", a: nil, b: nil, null: true},
	} {
		for _, engine := range []struct {
			name    string
			db      *sql.DB
			dialect gatewaySQLDialect
		}{{"SQLite", sqlite, gatewayDialectSQLite}, {"MySQL", mysqlDB, gatewayDialectMySQL}} {
			var got sql.NullInt64
			query := "SELECT " + gatewayScalarLeastSQL(engine.dialect, "?", "?")
			if err := engine.db.QueryRow(query, tc.a, tc.b).Scan(&got); err != nil {
				t.Fatalf("%s %s: %v", engine.name, tc.name, err)
			}
			if got.Valid == tc.null {
				t.Errorf("%s %s: LEAST/MIN = %v (valid=%v), want null=%v", engine.name, tc.name, got.Int64, got.Valid, tc.null)
			}
			if !tc.null && got.Int64 != tc.want {
				t.Errorf("%s %s: LEAST/MIN = %d, want %d", engine.name, tc.name, got.Int64, tc.want)
			}
		}
	}
}
