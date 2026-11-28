package diff

import (
	"strings"
	"testing"

	"github.com/user/doops/agent/internal/dosql/inspect"
)

func TestCompareSchemasTopologicalOrderAndNormalization(t *testing.T) {
	current := &inspect.SchemaIR{
		Engine:   "mysql",
		Database: "prod",
		Tables: []inspect.TableIR{
			{
				Name: "orders",
				Columns: []inspect.ColumnIR{
					{Name: "id", Type: "int(11)", Nullable: false},
					{Name: "legacy_code", Type: "varchar(32)", Nullable: true},
				},
			},
		},
	}
	desired := &inspect.SchemaIR{
		Engine:   "mysql",
		Database: "staging",
		Tables: []inspect.TableIR{
			// Child table listed BEFORE parent table; topo sort must emit parent first.
			{
				Name: "order_items",
				Columns: []inspect.ColumnIR{
					{Name: "id", Type: "bigint", Nullable: false},
					{Name: "user_id", Type: "bigint", Nullable: false},
				},
				ForeignKeys: []inspect.ForeignKeyIR{
					{Name: "fk_user", Columns: []string{"user_id"}, ReferencedTable: "users", ReferencedColumns: []string{"id"}},
				},
			},
			{
				Name: "users",
				Columns: []inspect.ColumnIR{
					{Name: "id", Type: "bigint", Nullable: false},
				},
			},
			{
				Name: "orders",
				Columns: []inspect.ColumnIR{
					{Name: "id", Type: "int", Nullable: false}, // int == int(11) in MySQL 8
					{Name: "order_code", Type: "varchar(32)", Nullable: true},
				},
				Indexes: []inspect.IndexIR{
					{Name: "idx_code", Columns: []string{"order_code"}},
				},
			},
		},
	}

	d := CompareSchemas(current, desired, nil)
	if d.Identical || !d.HasDestructive {
		t.Fatalf("expected non-identical destructive diff: %+v", d)
	}
	// Verify users is created before order_items.
	if len(d.SuggestedDDL) < 2 || !strings.Contains(d.SuggestedDDL[0], "CREATE TABLE `users`") || !strings.Contains(d.SuggestedDDL[1], "CREATE TABLE `order_items`") {
		t.Fatalf("FK topological ordering failed: %#v", d.SuggestedDDL)
	}
	// Verify int(11) vs int did NOT generate a spurious MODIFY COLUMN id.
	for _, c := range d.Changes {
		if c.Table == "orders" && c.Object == "id" {
			t.Fatalf("spurious int(11) vs int diff: %+v", c)
		}
	}
	// Verify possible rename detection for legacy_code -> order_code.
	if len(d.PossibleRenames) != 1 {
		t.Fatalf("expected 1 possible rename hint, got %+v", d.PossibleRenames)
	}
}

func TestCompareSchemasPostgresIndexesFKsAndReverseDropOrder(t *testing.T) {
	current := &inspect.SchemaIR{
		Engine:   "postgres",
		Database: "prod",
		Tables: []inspect.TableIR{
			{
				Name: "users",
				Columns: []inspect.ColumnIR{
					{Name: "id", Type: "bigint", Nullable: false},
					{Name: "email", Type: "character varying(255)", Nullable: true},
				},
				Indexes: []inspect.IndexIR{
					{Name: "idx_old_email", Columns: []string{"email"}},
				},
			},
			{
				Name: "legacy_parent",
				Columns: []inspect.ColumnIR{
					{Name: "id", Type: "bigint", Nullable: false},
				},
			},
			{
				Name: "legacy_child",
				Columns: []inspect.ColumnIR{
					{Name: "id", Type: "bigint", Nullable: false},
					{Name: "parent_id", Type: "bigint", Nullable: false},
				},
				ForeignKeys: []inspect.ForeignKeyIR{
					{Name: "fk_child_parent", Columns: []string{"parent_id"}, ReferencedTable: "legacy_parent", ReferencedColumns: []string{"id"}},
				},
			},
		},
	}
	desired := &inspect.SchemaIR{
		Engine:   "postgres",
		Database: "staging",
		Tables: []inspect.TableIR{
			{
				Name: "users",
				Columns: []inspect.ColumnIR{
					{Name: "id", Type: "bigint", Nullable: false},
					{Name: "email", Type: "character varying(128)", Nullable: false}, // narrowed + NOT NULL -> destructive
				},
				Indexes: []inspect.IndexIR{
					{Name: "idx_users_email_uniq", Unique: true, Columns: []string{"email"}},
				},
				ForeignKeys: []inspect.ForeignKeyIR{
					{Name: "fk_users_org", Columns: []string{"id"}, ReferencedTable: "orgs", ReferencedColumns: []string{"id"}, OnDelete: "CASCADE"},
				},
			},
		},
	}

	d := CompareSchemas(current, desired, nil)
	if d.Identical || !d.HasDestructive {
		t.Fatalf("expected destructive PG diff: %+v", d)
	}
	joined := strings.Join(d.SuggestedDDL, "\n")
	if !strings.Contains(joined, `CREATE UNIQUE INDEX CONCURRENTLY "idx_users_email_uniq"`) {
		t.Fatalf("expected PG CONCURRENTLY index DDL, got:\n%s", joined)
	}
	if !strings.Contains(joined, `DROP INDEX CONCURRENTLY "idx_old_email"`) {
		t.Fatalf("expected PG DROP INDEX CONCURRENTLY DDL, got:\n%s", joined)
	}
	if !strings.Contains(joined, `ADD CONSTRAINT "fk_users_org" FOREIGN KEY`) {
		t.Fatalf("expected PG ADD FK DDL, got:\n%s", joined)
	}
	childDrop := strings.Index(joined, `DROP TABLE "legacy_child"`)
	parentDrop := strings.Index(joined, `DROP TABLE "legacy_parent"`)
	if childDrop < 0 || parentDrop < 0 || childDrop > parentDrop {
		t.Fatalf("expected legacy_child dropped before legacy_parent, got:\n%s", joined)
	}
	// Table filter limits diff to requested table only.
	filtered := CompareSchemas(current, desired, []string{"users"})
	for _, c := range filtered.Changes {
		if c.Table != "users" {
			t.Fatalf("table filter leaked table %q", c.Table)
		}
	}
}

func TestSQL07DialectDDLFidelityForMySQLAndPostgres(t *testing.T) {
	// 1. MySQL: unquoted string defaults must be quoted, numeric defaults stay unquoted,
	//    and FK ON UPDATE / ON DELETE actions are preserved.
	mysqlDesired := &inspect.SchemaIR{
		Engine: "mysql", Database: "a",
		Tables: []inspect.TableIR{{
			Name: "orders",
			Columns: []inspect.ColumnIR{
				{Name: "id", Type: "bigint", Nullable: false},
				{Name: "status", Type: "varchar(32)", Nullable: false, Default: "active"},
				{Name: "amount", Type: "decimal(12,2)", Nullable: false, Default: "0.00"},
			},
			Indexes: []inspect.IndexIR{
				{Name: "PRIMARY", Primary: true, Unique: true, Columns: []string{"id"}},
				{Name: "idx_status", Columns: []string{"status"}},
			},
			ForeignKeys: []inspect.ForeignKeyIR{
				{Name: "fk_u", Columns: []string{"id"}, ReferencedTable: "users", ReferencedColumns: []string{"id"}, OnUpdate: "RESTRICT", OnDelete: "CASCADE"},
			},
		}},
	}
	md := CompareSchemas(&inspect.SchemaIR{Engine: "mysql", Database: "b"}, mysqlDesired, nil)
	mDDL := strings.Join(md.SuggestedDDL, "\n")
	if !strings.Contains(mDDL, "DEFAULT 'active'") || !strings.Contains(mDDL, "DEFAULT 0.00") {
		t.Fatalf("mysql default quoting wrong:\n%s", mDDL)
	}
	if !strings.Contains(mDDL, "KEY `idx_status` (`status`)") || !strings.Contains(mDDL, "ON UPDATE RESTRICT ON DELETE CASCADE") {
		t.Fatalf("mysql secondary index or FK actions missing:\n%s", mDDL)
	}

	// 2. Postgres: CREATE TABLE must use CONSTRAINT ... UNIQUE (never MySQL UNIQUE KEY),
	//    emit secondary indexes as separate CREATE INDEX statements, skip Primary index in diff,
	//    and emit SET DEFAULT + SET NOT NULL in ALTER COLUMN.
	pgDesired := &inspect.SchemaIR{
		Engine: "postgres", Database: "a",
		Tables: []inspect.TableIR{{
			Name: "orders",
			Columns: []inspect.ColumnIR{
				{Name: "id", Type: "bigint", Nullable: false},
				{Name: "email", Type: "character varying(128)", Nullable: false},
				{Name: "status", Type: "character varying(32)", Nullable: false, Default: "'new'::character varying"},
			},
			Indexes: []inspect.IndexIR{
				{Name: "orders_pkey", Primary: true, Unique: true, Columns: []string{"id"}},
				{Name: "uq_email", Unique: true, Columns: []string{"email"}},
				{Name: "idx_status", Columns: []string{"status"}},
			},
		}},
	}
	pd := CompareSchemas(&inspect.SchemaIR{Engine: "postgres", Database: "b"}, pgDesired, nil)
	pDDL := strings.Join(pd.SuggestedDDL, "\n")
	if strings.Contains(pDDL, "UNIQUE KEY") {
		t.Fatalf("postgres CREATE TABLE emitted MySQL-only UNIQUE KEY:\n%s", pDDL)
	}
	if !strings.Contains(pDDL, `CONSTRAINT "uq_email" UNIQUE ("email")`) || !strings.Contains(pDDL, `CREATE INDEX "idx_status" ON "orders" ("status");`) {
		t.Fatalf("postgres unique constraint or secondary index missing:\n%s", pDDL)
	}
	// Modifying nullable + default on PG emits both SET DEFAULT and SET NOT NULL and ignores orders_pkey.
	pgBefore := &inspect.SchemaIR{
		Engine: "postgres", Database: "b",
		Tables: []inspect.TableIR{{
			Name: "orders",
			Columns: []inspect.ColumnIR{
				{Name: "id", Type: "bigint", Nullable: false},
				{Name: "email", Type: "character varying(128)", Nullable: false},
				{Name: "status", Type: "character varying(32)", Nullable: true},
			},
			Indexes: []inspect.IndexIR{
				{Name: "orders_pkey", Primary: true, Unique: true, Columns: []string{"id"}},
				{Name: "uq_email", Unique: true, Columns: []string{"email"}},
				{Name: "idx_status", Columns: []string{"status"}},
			},
		}},
	}
	pInc := CompareSchemas(pgBefore, pgDesired, nil)
	pIncDDL := strings.Join(pInc.SuggestedDDL, "\n")
	if !strings.Contains(pIncDDL, `SET DEFAULT 'new'::character varying`) || !strings.Contains(pIncDDL, `SET NOT NULL`) || strings.Contains(pIncDDL, "orders_pkey") {
		t.Fatalf("postgres incremental ALTER COLUMN or PK skip broken:\n%s", pIncDDL)
	}
}
