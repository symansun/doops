package server

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// This file owns the bootstrap rows: the singleton rows a Gateway store must
// carry before it serves its first read. They are not catalogue data (Skill and
// MCP packages) and not migration copies (rows moved from a superseded table):
// a missing bootstrap row is a startup failure, not a degraded feature.
//
// INT-33 is why the list is shared. SQLite seeded these rows inside
// migrate()/migrateEdge(), and the MySQL install path only created tables, so a
// real MySQL 8.4 Gateway started, then panicked at tunnel_hub.go:127 because
// NewEdgeRegistry re-reads edge_route_revision and the row was absent. Both
// backends now apply this one list, so the two paths cannot drift: adding a seed
// here reaches SQLite (migrate) and MySQL (gatewayMySQLPrepareRuntimeSchema) in
// the same change. TestGatewayBootstrapRowsMatchAcrossBackends compares the rows
// the two backends end up with, and TestMigrateDoesNotSeedRowsOutsideTheSharedList
// fails when a migrate* function grows an INSERT that this list does not own.

// gatewayStoreBootstrapRow is one singleton row, spelled once.
type gatewayStoreBootstrapRow struct {
	// table names the singleton table in the error an operator sees.
	table string
	// seed is the complete do-nothing upsert, spelled as one composite literal
	// so the insert text stays visible to the MySQL upsert guard in
	// gateway_mysql_runtime_test.go: that guard reads the INSERT off the AST and
	// treats an insert assembled from another field as unreadable. The conflict
	// target is the primary key that makes the seed idempotent, so a restart does
	// not advance the edge revision, does not add a second ledger row, and two
	// racing startups cannot fail on the primary key.
	seed gatewayUpsert
	// args evaluates when the row is applied, never at package init: the ledger
	// timestamp belongs to the startup that wrote it.
	args func() []any
	// probe re-reads the row with the statement the runtime uses first, so a
	// store that failed to seed fails as a named startup error instead of a bare
	// panic inside the hub. nil is a bug; verifyGatewayStoreBootstrapRows says so.
	probe func(ctx context.Context, q gatewayBootstrapQuerier) error
}

// gatewayBootstrapQuerier is the read surface of *sql.DB and *sql.Tx.
type gatewayBootstrapQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// gatewayBootstrapExecutor is the write surface of *sql.DB and *sql.Tx.
type gatewayBootstrapExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func gatewayStoreBootstrapRows() []gatewayStoreBootstrapRow {
	return []gatewayStoreBootstrapRow{
		{
			// The migration ledger carries the schema revision this database is at.
			// A fresh database gets exactly one baseline row; a restart must not add
			// a second one, and two startups racing must not fail.
			table: "gateway_schema_migrations",
			seed: gatewayUpsert{
				insert:    "INSERT INTO gateway_schema_migrations (version, applied_at) VALUES (?, ?)",
				target:    []string{"version"},
				doNothing: true,
			},
			args: func() []any {
				return []any{gatewaySchemaBaselineVersion, time.Now().UTC().Format(time.RFC3339)}
			},
			probe: func(ctx context.Context, q gatewayBootstrapQuerier) error {
				var version string
				return q.QueryRowContext(ctx, "SELECT version FROM gateway_schema_migrations WHERE version = ?", gatewaySchemaBaselineVersion).Scan(&version)
			},
		},
		{
			// id is pinned to 1 by the CHECK constraint and is the primary key, so
			// the bootstrap row is inserted once and every later open leaves the
			// revision counter alone. The edge registry reads exactly this row while
			// the hub is constructed (tunnel_hub.go:125-128).
			table: "edge_route_revision",
			seed: gatewayUpsert{
				insert:    "INSERT INTO edge_route_revision (id, version) VALUES (1, 1)",
				target:    []string{"id"},
				doNothing: true,
			},
			args: func() []any { return nil },
			probe: func(ctx context.Context, q gatewayBootstrapQuerier) error {
				var version int64
				return q.QueryRowContext(ctx, "SELECT version FROM edge_route_revision WHERE id = 1").Scan(&version)
			},
		},
	}
}

// gatewayStoreBootstrapTables returns the tables the shared list owns. The
// static guard uses it to name the table a migrate* function must not seed.
func gatewayStoreBootstrapTables() map[string]bool {
	tables := make(map[string]bool, len(gatewayStoreBootstrapRows()))
	for _, row := range gatewayStoreBootstrapRows() {
		tables[row.table] = true
	}
	return tables
}

// applyGatewayStoreBootstrapRows writes every bootstrap row for dialect. It is
// idempotent: the insert renders as a do-nothing upsert, so a restart keeps the
// existing row and a racing startup cannot fail on the primary key. The error
// names the table, because "which row" is the only useful fact at startup.
func applyGatewayStoreBootstrapRows(exec gatewayBootstrapExecutor, dialect gatewaySQLDialect) error {
	for _, row := range gatewayStoreBootstrapRows() {
		statement := row.seed.render(dialect)
		if _, err := exec.Exec(statement, row.args()...); err != nil {
			return fmt.Errorf("gateway_bootstrap_seed_failed: %s: %w", row.table, err)
		}
	}
	return nil
}

// verifyGatewayStoreBootstrapRows reads every seeded row back. Without it a
// store that lost a seed fails later as "panic: sql: no rows in result set" in
// the middle of hub construction; with it the operator reads the table name. It
// is the MySQL startup half of the guarantee the shared list provides.
func verifyGatewayStoreBootstrapRows(ctx context.Context, q gatewayBootstrapQuerier) error {
	for _, row := range gatewayStoreBootstrapRows() {
		if row.probe == nil {
			return fmt.Errorf("gateway_bootstrap_probe_missing: %s", row.table)
		}
		if err := row.probe(ctx, q); err != nil {
			return fmt.Errorf("gateway_bootstrap_row_missing: %s: %w", row.table, err)
		}
	}
	return nil
}
