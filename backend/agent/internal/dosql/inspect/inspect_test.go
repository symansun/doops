package inspect

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type stubQueryResult struct {
	match string
	cols  []string
	rows  [][]driver.Value
}

type stubCatalogDriver struct {
	mu      sync.Mutex
	results []stubQueryResult
}

var (
	registerStubOnce sync.Once
	activeStub       = &stubCatalogDriver{}
)

func openStubDB(t *testing.T, results []stubQueryResult) *sql.DB {
	t.Helper()
	registerStubOnce.Do(func() {
		sql.Register("dosql_inspect_stub", activeStub)
	})
	activeStub.mu.Lock()
	activeStub.results = results
	activeStub.mu.Unlock()
	db, err := sql.Open("dosql_inspect_stub", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func (d *stubCatalogDriver) Open(name string) (driver.Conn, error) {
	return &stubCatalogConn{d: d}, nil
}

type stubCatalogConn struct{ d *stubCatalogDriver }

func (c *stubCatalogConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c *stubCatalogConn) Close() error                        { return nil }
func (c *stubCatalogConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }

func (c *stubCatalogConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	for _, r := range c.d.results {
		if strings.Contains(query, r.match) {
			return &stubCatalogRows{cols: r.cols, rows: r.rows}, nil
		}
	}
	return &stubCatalogRows{}, nil
}

type stubCatalogRows struct {
	cols []string
	rows [][]driver.Value
	idx  int
}

func (r *stubCatalogRows) Columns() []string { return r.cols }
func (r *stubCatalogRows) Close() error      { return nil }
func (r *stubCatalogRows) Next(dest []driver.Value) error {
	if r.idx >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.idx])
	r.idx++
	return nil
}

func TestInspectMySQLCatalogTablesColumnsIndexesAndFKs(t *testing.T) {
	db := openStubDB(t, []stubQueryResult{
		{
			match: "information_schema.TABLES",
			cols:  []string{"TABLE_NAME", "TABLE_TYPE", "ENGINE", "TABLE_COLLATION", "TABLE_ROWS", "TABLE_COMMENT"},
			rows: [][]driver.Value{
				{"orders", "BASE TABLE", "InnoDB", "utf8mb4_0900_ai_ci", int64(1200), "order table"},
				{"orders_v", "VIEW", "", "", int64(0), ""},
			},
		},
		{
			match: "information_schema.COLUMNS",
			cols:  []string{"TABLE_NAME", "COLUMN_NAME", "COLUMN_TYPE", "IS_NULLABLE", "COLUMN_DEFAULT", "EXTRA", "COLUMN_COMMENT"},
			rows: [][]driver.Value{
				{"orders", "id", "BIGINT", "NO", "", "auto_increment", ""},
				{"orders", "user_id", "BIGINT", "NO", "", "", ""},
				{"orders", "status", "VARCHAR(32)", "YES", "new", "", "order status"},
			},
		},
		{
			match: "information_schema.STATISTICS",
			cols:  []string{"TABLE_NAME", "INDEX_NAME", "NON_UNIQUE", "COLUMN_NAME", "INDEX_TYPE"},
			rows: [][]driver.Value{
				{"orders", "PRIMARY", int64(0), "id", "BTREE"},
				{"orders", "idx_user_status", int64(1), "user_id", "BTREE"},
				{"orders", "idx_user_status", int64(1), "status", "BTREE"},
			},
		},
		{
			match: "information_schema.KEY_COLUMN_USAGE",
			cols:  []string{"TABLE_NAME", "CONSTRAINT_NAME", "COLUMN_NAME", "REFERENCED_TABLE_NAME", "REFERENCED_COLUMN_NAME", "UPDATE_RULE", "DELETE_RULE"},
			rows: [][]driver.Value{
				{"orders", "fk_orders_user", "user_id", "users", "id", "RESTRICT", "CASCADE"},
			},
		},
	})

	ir, err := InspectSQL(t.Context(), db, "mysql", "shop", Options{})
	if err != nil {
		t.Fatalf("InspectSQL mysql failed: %v", err)
	}
	if ir.TableCount != 2 || ir.SchemaHash == "" || ir.Tables[0].Charset != "utf8mb4" || ir.Tables[1].Kind != "VIEW" {
		t.Fatalf("unexpected mysql SchemaIR: %+v", ir)
	}
	ord := ir.Tables[0]
	if len(ord.Columns) != 3 || ord.Columns[0].Type != "bigint" || !ord.Columns[2].Nullable {
		t.Fatalf("unexpected mysql columns: %+v", ord.Columns)
	}
	if len(ord.Indexes) != 2 || !ord.Indexes[0].Primary || len(ord.Indexes[1].Columns) != 2 {
		t.Fatalf("unexpected mysql indexes: %+v", ord.Indexes)
	}
	if len(ord.ForeignKeys) != 1 || ord.ForeignKeys[0].ReferencedTable != "users" || ord.ForeignKeys[0].OnDelete != "CASCADE" {
		t.Fatalf("unexpected mysql FKs: %+v", ord.ForeignKeys)
	}

	// Table filter + SummaryOnly skips detail queries and returns only requested table.
	filtered, err := InspectSQL(t.Context(), db, "mysql", "shop", Options{Tables: []string{"orders"}, SummaryOnly: true})
	if err != nil || filtered.TableCount != 1 || len(filtered.Tables[0].Columns) != 0 {
		t.Fatalf("unexpected filtered summary: %+v err=%v", filtered, err)
	}
}

func TestInspectPostgresCatalogIncludesIndexesAndForeignKeys(t *testing.T) {
	db := openStubDB(t, []stubQueryResult{
		{
			match: "FROM pg_index ix",
			cols:  []string{"relname", "idxname", "indisunique", "indisprimary", "attname", "amname"},
			rows: [][]driver.Value{
				{"orders", "orders_pkey", true, true, "id", "btree"},
				{"orders", "idx_orders_tenant_code", true, false, "tenant_id", "btree"},
				{"orders", "idx_orders_tenant_code", true, false, "code", "btree"},
			},
		},
		{
			match: "FROM pg_constraint c",
			cols:  []string{"src", "conname", "la", "ref", "ra", "upd", "del"},
			rows: [][]driver.Value{
				{"orders", "fk_orders_tenant", "tenant_id", "tenants", "id", "a", "c"},
			},
		},
		{
			match: "JOIN pg_attribute a ON a.attrelid = c.oid",
			cols:  []string{"relname", "attname", "type", "nullable", "def", "comment"},
			rows: [][]driver.Value{
				{"orders", "id", "bigint", false, "", ""},
				{"orders", "tenant_id", "bigint", false, "", ""},
				{"orders", "code", "character varying(64)", true, "", "order code"},
			},
		},
		{
			match: "FROM pg_class c",
			cols:  []string{"relname", "relkind", "reltuples", "comment"},
			rows: [][]driver.Value{
				{"orders", "r", int64(-1), "orders table"},
				{"orders_mv", "m", int64(50), ""},
			},
		},
	})

	ir, err := InspectSQL(t.Context(), db, "postgres", "app", Options{})
	if err != nil {
		t.Fatalf("InspectSQL postgres failed: %v", err)
	}
	if ir.TableCount != 2 || ir.Tables[0].EstimatedRows != 0 || ir.Tables[1].Kind != "VIEW" {
		t.Fatalf("unexpected pg SchemaIR: %+v", ir)
	}
	ord := ir.Tables[0]
	if len(ord.Columns) != 3 {
		t.Fatalf("unexpected pg columns: %+v", ord.Columns)
	}
	if len(ord.Indexes) != 2 || !ord.Indexes[0].Primary || ord.Indexes[1].Type != "BTREE" || len(ord.Indexes[1].Columns) != 2 {
		t.Fatalf("unexpected pg indexes: %+v", ord.Indexes)
	}
	if len(ord.ForeignKeys) != 1 || ord.ForeignKeys[0].ReferencedTable != "tenants" ||
		ord.ForeignKeys[0].OnUpdate != "NO ACTION" || ord.ForeignKeys[0].OnDelete != "CASCADE" {
		t.Fatalf("unexpected pg FKs: %+v", ord.ForeignKeys)
	}
	if pgFKActionName("r") != "RESTRICT" || pgFKActionName("n") != "SET NULL" || pgFKActionName("d") != "SET DEFAULT" {
		t.Fatal("pgFKActionName mapping broken")
	}

	if _, err := InspectSQL(t.Context(), db, "oracle", "orcl", Options{}); err == nil || !strings.Contains(err.Error(), "dosql_inspect_unsupported") {
		t.Fatalf("expected unsupported engine error, got %v", err)
	}
}

func TestComputeHashAndAtomicSnapshotStore(t *testing.T) {
	ir1 := &SchemaIR{
		Engine: "mysql", Database: "orders",
		Tables: []TableIR{
			{Name: "b", EstimatedRows: 10, Columns: []ColumnIR{{Name: "id", Type: "int"}}},
			{Name: "a", EstimatedRows: 99999, Columns: []ColumnIR{{Name: "id", Type: "int"}}},
		},
	}
	ir2 := &SchemaIR{
		Engine: "mysql", Database: "orders",
		Tables: []TableIR{
			{Name: "a", EstimatedRows: 0, Columns: []ColumnIR{{Name: "id", Type: "int"}}},
			{Name: "b", EstimatedRows: 500, Columns: []ColumnIR{{Name: "id", Type: "int"}}},
		},
	}
	if ir1.ComputeHash() != ir2.ComputeHash() {
		t.Fatalf("hash must ignore EstimatedRows and table order: %s vs %s", ir1.ComputeHash(), ir2.ComputeHash())
	}

	root := t.TempDir()
	// Canonical address normalization produces identical snapshot directory.
	sA := NewSnapshotStore(root, "MYSQL", "DB-B.TEST:3306,db-a.test:3306", "orders")
	sB := NewSnapshotStore(root, "mysql", "db-a.test:3306,db-b.test:3306", "orders")
	if sA.Dir != sB.Dir {
		t.Fatalf("canonical snapshot dirs differ: %q vs %q", sA.Dir, sB.Dir)
	}

	p1, err := sA.Save(1, ir1)
	if err != nil {
		t.Fatalf("Save v1 failed: %v", err)
	}
	if info, err := os.Stat(p1); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("snapshot permissions = %v err=%v", info, err)
	}
	if _, err := sA.Save(3, ir2); err != nil {
		t.Fatalf("Save v3 failed: %v", err)
	}
	vers, err := sB.ListVersions()
	if err != nil || len(vers) != 2 || vers[0] != 1 || vers[1] != 3 {
		t.Fatalf("ListVersions = %v err=%v", vers, err)
	}
	loaded, err := sB.Load(1)
	if err != nil || loaded.SchemaHash != ir1.ComputeHash() {
		t.Fatalf("Load(1) = %+v err=%v", loaded, err)
	}
	latest, err := sB.LoadLatest()
	if err != nil || latest.SchemaHash != ir2.ComputeHash() {
		t.Fatalf("LoadLatest = %+v err=%v", latest, err)
	}
	// Verify no atomic temp files were left behind.
	entries, err := os.ReadDir(sA.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("leftover temp file in snapshot dir: %s", filepath.Join(sA.Dir, e.Name()))
		}
	}
}
