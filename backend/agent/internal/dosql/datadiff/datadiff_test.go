package datadiff

import (
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestCompareIntKeyTablesBisectionAndFixSQL(t *testing.T) {
	src, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()

	for _, db := range []*sql.DB{src, dst} {
		if _, err := db.Exec(`CREATE TABLE sys_dict (id INTEGER PRIMARY KEY, code TEXT, label TEXT)`); err != nil {
			t.Fatal(err)
		}
	}
	// Populate 120 rows across both DBs, then introduce 1 missing, 1 extra, 1 mismatch.
	for i := 1; i <= 120; i++ {
		if _, err := src.Exec(`INSERT INTO sys_dict VALUES (?, ?, ?)`, i, "k", "val"); err != nil {
			t.Fatal(err)
		}
		if i != 42 { // row 42 missing in target
			label := "val"
			if i == 88 {
				label = "stale" // row 88 mismatched
			}
			if _, err := dst.Exec(`INSERT INTO sys_dict VALUES (?, ?, ?)`, i, "k", label); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Row 125 extra in target
	if _, err := dst.Exec(`INSERT INTO sys_dict VALUES (125, 'extra', 'rogue')`); err != nil {
		t.Fatal(err)
	}

	res, err := CompareIntKeyTables(t.Context(), src, dst, Options{
		Engine:      "sqlite",
		Table:       "sys_dict",
		KeyColumn:   "id",
		Columns:     []string{"id", "code", "label"},
		ChunkSize:   50,
		LeafSize:    15,
		GenerateFix: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Identical || res.DiffCount != 3 || res.ChunksChecked < 4 {
		t.Fatalf("unexpected datadiff result: %+v", res)
	}
	// Apply generated fix SQL to target and verify tables become 100% identical!
	for _, d := range res.Diffs {
		if d.FixSQL == "" {
			t.Fatalf("missing fix SQL for %+v", d)
		}
		if _, err := dst.Exec(d.FixSQL); err != nil {
			t.Fatalf("apply fix SQL %q: %v", d.FixSQL, err)
		}
	}
	after, err := CompareIntKeyTables(t.Context(), src, dst, Options{
		Engine: "sqlite", Table: "sys_dict", KeyColumn: "id", Columns: []string{"id", "code", "label"}, ChunkSize: 50, LeafSize: 15,
	})
	if err != nil || !after.Identical || after.DiffCount != 0 {
		t.Fatalf("expected identical tables after applying fix SQL: %+v err=%v", after, err)
	}
}

func TestCompareIntKeyTablesNullQuotesEmptyAndTruncation(t *testing.T) {
	src, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()

	// Invalid options fail closed.
	if _, err := CompareIntKeyTables(t.Context(), src, dst, Options{}); err == nil || !strings.Contains(err.Error(), "dosql_datadiff_invalid") {
		t.Fatalf("expected dosql_datadiff_invalid, got %v", err)
	}

	for _, db := range []*sql.DB{src, dst} {
		if _, err := db.Exec(`CREATE TABLE cfg (id INTEGER PRIMARY KEY, val TEXT)`); err != nil {
			t.Fatal(err)
		}
	}
	// Empty tables are identical.
	empty, err := CompareIntKeyTables(t.Context(), src, dst, Options{
		Engine: "sqlite", Table: "cfg", KeyColumn: "id", Columns: []string{"id", "val"},
	})
	if err != nil || !empty.Identical || empty.DiffCount != 0 {
		t.Fatalf("empty tables failed: %+v err=%v", empty, err)
	}

	// Test NULL values and single quotes escaping in generated fix SQL.
	if _, err := src.Exec(`INSERT INTO cfg VALUES (1, NULL), (2, 'O''Reilly'), (3, 'a'), (4, 'b')`); err != nil {
		t.Fatal(err)
	}
	if _, err := dst.Exec(`INSERT INTO cfg VALUES (1, 'not-null'), (2, 'plain')`); err != nil {
		t.Fatal(err)
	}
	// Truncation caps returned Diffs at MaxDiffs=2 while DiffCount reports full 4.
	trunc, err := CompareIntKeyTables(t.Context(), src, dst, Options{
		Engine: "sqlite", Table: "cfg", KeyColumn: "id", Columns: []string{"id", "val"}, MaxDiffs: 2, GenerateFix: true,
	})
	if err != nil || !trunc.Truncated || trunc.DiffCount != 4 || len(trunc.Diffs) != 2 {
		t.Fatalf("expected truncated diff: %+v err=%v", trunc, err)
	}
	// Full run with fix SQL round-trips NULL and escaped quotes cleanly.
	full, err := CompareIntKeyTables(t.Context(), src, dst, Options{
		Engine: "sqlite", Table: "cfg", KeyColumn: "id", Columns: []string{"id", "val"}, GenerateFix: true,
	})
	if err != nil || full.DiffCount != 4 {
		t.Fatalf("full diff failed: %+v err=%v", full, err)
	}
	for _, d := range full.Diffs {
		if _, err := dst.Exec(d.FixSQL); err != nil {
			t.Fatalf("exec fix SQL %q: %v", d.FixSQL, err)
		}
	}
	after, err := CompareIntKeyTables(t.Context(), src, dst, Options{
		Engine: "sqlite", Table: "cfg", KeyColumn: "id", Columns: []string{"id", "val"},
	})
	if err != nil || !after.Identical {
		t.Fatalf("expected identical after NULL/quote fix SQL: %+v err=%v", after, err)
	}
}

func TestDataDiffMySQLBackslashEscapingByteKeyAndOmittedKeyColumn(t *testing.T) {
	// MySQL escapes backslashes in string literals; PG/SQLite keep them literal.
	if got := sqlLiteral("mysql", `C:\tmp\'a`); got != `'C:\\tmp\\''a'` {
		t.Fatalf("mysql sqlLiteral = %q, want %q", got, `'C:\\tmp\\''a'`)
	}
	if got := sqlLiteral("postgres", `C:\tmp\'a`); got != `'C:\tmp\''a'` {
		t.Fatalf("postgres sqlLiteral = %q, want %q", got, `'C:\tmp\''a'`)
	}
	// toInt64 accepts []byte and string (MySQL interpolateParams text protocol).
	if got, err := toInt64([]byte("123456")); err != nil || got != 123456 {
		t.Fatalf("toInt64([]byte) = %d, %v", got, err)
	}
	if got, err := toInt64("789"); err != nil || got != 789 {
		t.Fatalf("toInt64(string) = %d, %v", got, err)
	}
	// renderInsertSQL includes KeyColumn even if caller omitted it from Columns.
	stmt := renderInsertSQL(Options{Engine: "mysql", Table: "cfg", KeyColumn: "id", Columns: []string{"val"}}, orderedRow{
		Key:    42,
		Values: map[string]string{"val": `a\b`},
	})
	want := "INSERT INTO `cfg` (`id`, `val`) VALUES (42, 'a\\\\b');"
	if stmt != want {
		t.Fatalf("renderInsertSQL = %q, want %q", stmt, want)
	}
}
