package server

import (
	"context"
	"database/sql"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// gatewayBakedDialectCalls lists the renderers whose output depends on the
// process dialect. A package-level var initialized from any of them is frozen
// at init time in the default (SQLite) spelling; INT-28 found two such vars in
// asset_inspection_read.go and a MySQL Gateway then failed with
// "FUNCTION ... unixepoch does not exist".
var gatewayBakedDialectCalls = map[string]bool{
	"gatewayDialect":               true,
	"gatewayIdentifier":            true,
	"gatewayIdentifierFor":         true,
	"gatewayEpochNowSQL":           true,
	"gatewayEpochNowFor":           true,
	"gatewayEpochSecondsFor":       true,
	"gatewayConcatFor":             true,
	"gatewayNullSafeEqualsSQL":     true,
	"gatewayFilteredCountSQL":      true,
	"gatewayJSONEachValuesSQL":     true,
	"gatewayJSONEachKeysSQL":       true,
	"gatewayJSONObjectJoinSQL":     true,
	"gatewayJSONOrderedArraySQL":   true,
	"gatewayUpdateIgnoreSQL":       true,
	"gatewayUpsertSQL":             true,
	"gatewayUpsertBatchSQL":        true,
	"gatewayUpsertBatchSQLFor":     true,
	"gatewayExistingTableCountSQL": true,
	"gatewayListBaseTablesSQLFor":  true,
	"gatewayTableColumnsSQLFor":    true,
	"inventoryGrantsSQL":           true,
	"inventoryTokenFreshSQL":       true,
}

// gatewayVarScan is one package-level var whose initializer renders SQL for a
// dialect at init time.
type gatewayVarScan struct {
	file string
	line int
	name string
}

func gatewayDialectCallName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	}
	return ""
}

// gatewayScanPackageVars returns every package-level var initializer that calls
// a dialect renderer, plus the total number of var specs it inspected. The
// count is the anti-vacuity check: a filter that silently skipped every file
// would otherwise look green.
func gatewayScanPackageVars(t *testing.T, dir string) ([]gatewayVarScan, int) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	var hits []gatewayVarScan
	total := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				total++
				for _, expr := range value.Values {
					hit := false
					ast.Inspect(expr, func(node ast.Node) bool {
						call, ok := node.(*ast.CallExpr)
						if !ok {
							return true
						}
						if gatewayBakedDialectCalls[gatewayDialectCallName(call.Fun)] {
							hit = true
							return false
						}
						return true
					})
					if hit {
						for _, ident := range value.Names {
							hits = append(hits, gatewayVarScan{file: name, line: fset.Position(value.Pos()).Line, name: ident.Name})
						}
					}
				}
			}
		}
	}
	return hits, total
}

// TestGatewayMySQLNoDialectSQLBakedAtInit is the regression guard for the
// INT-28 defect: SQL rendered for a dialect must not be stored at package init.
func TestGatewayMySQLNoDialectSQLBakedAtInit(t *testing.T) {
	hits, total := gatewayScanPackageVars(t, ".")
	if total < 100 {
		// The package currently declares clearly more than a hundred package
		// level vars; a smaller count means the scan lost its files.
		t.Fatalf("scanned only %d package-level vars, want at least 100 (the scan is not looking at the package)", total)
	}
	for _, hit := range hits {
		t.Errorf("%s:%d: package-level var %s is initialized from dialect-rendered SQL; evaluate it per call instead, or a MySQL Gateway inherits the SQLite spelling", hit.file, hit.line, hit.name)
	}
	t.Logf("scanned %d package-level vars in internal/server, %d dialect-initialized", total, len(hits))
}

// TestGatewayBakedSQLGuardDetectsHistoricalDefect proves the guard is not a
// no-op: it must flag the exact initializer INT-28 removed.
func TestGatewayBakedSQLGuardDetectsHistoricalDefect(t *testing.T) {
	dir := t.TempDir()
	source := "package server\n\nvar inspectState = inspectionStateAt(gatewayEpochNowSQL())\nvar inspectReason = inspectionReasonAt(gatewayEpochNowSQL())\nvar harmless = 1 + 2\n"
	if err := os.WriteFile(filepath.Join(dir, "baked.go"), []byte(source), 0o644); err != nil {
		t.Fatalf("write guard fixture: %v", err)
	}
	hits, total := gatewayScanPackageVars(t, dir)
	if total != 3 {
		t.Fatalf("guard fixture var count = %d, want 3", total)
	}
	var names []string
	for _, hit := range hits {
		names = append(names, hit.name)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "inspectReason,inspectState" {
		t.Fatalf("guard flagged %v, want inspectReason and inspectState", names)
	}
	if hits[0].file != "baked.go" {
		t.Fatalf("guard attributed the defect to %s, want baked.go", hits[0].file)
	}
}

func gatewayInspectionSQLiteDDL() []string {
	return []string{
		"CREATE TABLE IF NOT EXISTS users (id TEXT PRIMARY KEY, name TEXT NOT NULL, disabled INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, password_hash TEXT NOT NULL, is_admin INTEGER NOT NULL DEFAULT 0)",
		"CREATE TABLE IF NOT EXISTS tokens (id TEXT PRIMARY KEY, kind TEXT NOT NULL, user_id TEXT, name TEXT NOT NULL, prefix TEXT NOT NULL, hash TEXT NOT NULL, cluster TEXT NOT NULL DEFAULT '', instance TEXT NOT NULL DEFAULT '', expires_at TEXT NOT NULL, revoked_at TEXT NOT NULL, created_at TEXT NOT NULL)",
		"CREATE TABLE IF NOT EXISTS grants (id INTEGER PRIMARY KEY AUTOINCREMENT, user_id TEXT NOT NULL, cluster TEXT NOT NULL, instance TEXT NOT NULL, created_at TEXT NOT NULL)",
		"CREATE TABLE IF NOT EXISTS inventory_machines (asset_id TEXT PRIMARY KEY, name TEXT NOT NULL, kind TEXT NOT NULL, address TEXT NOT NULL, cluster TEXT NOT NULL DEFAULT '', instance TEXT NOT NULL DEFAULT '')",
		"CREATE TABLE IF NOT EXISTS agent_status (cluster TEXT NOT NULL, instance TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'offline', archived_at TEXT NOT NULL DEFAULT '', connected_at TEXT NOT NULL, last_seen TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY (cluster, instance))",
		"CREATE TABLE IF NOT EXISTS cluster_inventory_sources (asset_id TEXT PRIMARY KEY, cluster TEXT NOT NULL, instance TEXT NOT NULL)",
		"CREATE TABLE IF NOT EXISTS inventory_business_systems (asset_id TEXT PRIMARY KEY, active INTEGER NOT NULL DEFAULT 1)",
		"CREATE TABLE IF NOT EXISTS inventory_business_deployments (asset_id TEXT NOT NULL, cluster TEXT NOT NULL, instance TEXT NOT NULL)",
		"CREATE TABLE IF NOT EXISTS inspection_plans (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, owner TEXT NOT NULL, executor TEXT NOT NULL, timezone TEXT NOT NULL, weekday INTEGER NOT NULL, clock TEXT NOT NULL, next_due INTEGER NOT NULL, active INTEGER NOT NULL DEFAULT 1)",
		"CREATE TABLE IF NOT EXISTS inspection_runs (id INTEGER PRIMARY KEY AUTOINCREMENT, plan_id INTEGER NOT NULL, due INTEGER NOT NULL, frozen INTEGER NOT NULL, trigger_kind TEXT NOT NULL)",
		"CREATE TABLE IF NOT EXISTS inspection_items (id INTEGER PRIMARY KEY AUTOINCREMENT, run_id INTEGER NOT NULL, asset_id TEXT NOT NULL, cluster TEXT NOT NULL, instance TEXT NOT NULL, cluster_id TEXT NOT NULL, check_id TEXT NOT NULL, generation INTEGER NOT NULL DEFAULT 1, opened INTEGER NOT NULL, deadline INTEGER NOT NULL, status TEXT NOT NULL, reason TEXT NOT NULL, repair TEXT NOT NULL, repair_by TEXT NOT NULL DEFAULT '', repair_at INTEGER NOT NULL DEFAULT 0)",
		"CREATE TABLE IF NOT EXISTS inspection_receipts (item_id INTEGER NOT NULL, generation INTEGER NOT NULL, actor TEXT NOT NULL, received INTEGER NOT NULL, status TEXT NOT NULL, reason TEXT NOT NULL, evidence TEXT NOT NULL, digest TEXT NOT NULL, PRIMARY KEY (item_id, generation))",
	}
}

func gatewayInspectionText(value any) string {
	switch v := value.(type) {
	case []byte:
		return string(v)
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

func gatewayInspectionSeed(t *testing.T, db *sql.DB, now int64) {
	t.Helper()
	statements := []string{
		"INSERT INTO users (id, name, created_at, password_hash, is_admin) VALUES ('u1', 'user one', '2026-01-01T00:00:00Z', 'x', 1)",
		"INSERT INTO users (id, name, disabled, created_at, password_hash, is_admin) VALUES ('u2', 'user two', 1, '2026-01-01T00:00:00Z', 'x', 0)",
		"INSERT INTO tokens (id, kind, user_id, name, prefix, hash, expires_at, revoked_at, created_at) VALUES ('t1', 'user', 'u1', 'token one', 'p', 'h', '2099-01-01T00:00:00Z', '', '2026-01-01T00:00:00Z')",
		"INSERT INTO inventory_machines (asset_id, name, kind, address, cluster, instance) VALUES ('a1', 'A1', 'vm', '10.0.0.1', 'c1', 'i1'), ('a2', 'A2', 'vm', '10.0.0.2', 'c1', 'i1'), ('a3', 'A3', 'vm', '10.0.0.3', 'c1', 'i1'), ('a4', 'A4', 'vm', '10.0.0.4', 'c1', 'i1'), ('a5', 'A5', 'vm', '10.0.0.5', 'c1', 'i1')",
		"INSERT INTO agent_status (cluster, instance, status, archived_at, connected_at, last_seen, updated_at) VALUES ('c1', 'i1', 'online', '', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')",
		"INSERT INTO inspection_plans (name, owner, executor, timezone, weekday, clock, next_due) VALUES ('weekly', 'u1', 'u1', 'UTC', 0, '09:00', 0)",
		"INSERT INTO inspection_runs (plan_id, due, frozen, trigger_kind) VALUES (1, 0, 0, 'manual')",
		"INSERT INTO inspection_plans (name, owner, executor, timezone, weekday, clock, next_due) VALUES ('blocked', 'u1', 'u2', 'UTC', 0, '09:00', 0)",
		"INSERT INTO inspection_runs (plan_id, due, frozen, trigger_kind) VALUES (2, 0, 0, 'manual')",
		"INSERT INTO inspection_items (run_id, asset_id, cluster, instance, cluster_id, check_id, opened, deadline, status, reason, repair) VALUES " +
			"(1, 'a1', 'c1', 'i1', 'c1', 'cpu', " + strconv.FormatInt(now-100, 10) + ", " + strconv.FormatInt(now+3600, 10) + ", 'pending', '', ''), " +
			"(1, 'a2', 'c1', 'i1', 'c1', 'mem', " + strconv.FormatInt(now-100, 10) + ", " + strconv.FormatInt(now-60, 10) + ", 'pending', '', ''), " +
			"(1, 'a3', 'c1', 'i1', 'c1', 'net', " + strconv.FormatInt(now-100, 10) + ", " + strconv.FormatInt(now+3600, 10) + ", 'pass', 'all_good', ''), " +
			"(1, 'a4', 'c1', 'i1', 'c1', 'disk', 0, 100, 'pending', '', ''), " +
			"(2, 'a5', 'c1', 'i1', 'c1', 'cpu', " + strconv.FormatInt(now-100, 10) + ", " + strconv.FormatInt(now+3600, 10) + ", 'pending', '', '')",
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed inspection fixture (%s): %v", statement, err)
		}
	}
}

// gatewayInspectionStates calls the real report endpoint and returns
// asset_id -> "status|reason" from the rendered item rows.
func gatewayInspectionStates(t *testing.T, store *GatewayStore) map[string]string {
	t.Helper()
	return gatewayInspectionStatesForRun(t, store, 1)
}

// gatewayInspectionStatesForRun reads one run so a fixture can carry a second
// plan whose executor is blocked without disturbing the run-1 expectations.
func gatewayInspectionStatesForRun(t *testing.T, store *GatewayStore, runID int64) map[string]string {
	t.Helper()
	request := httptest.NewRequest("GET", "/api/asset/inspections?view=report&run_id="+strconv.FormatInt(runID, 10), nil)
	payload, err := store.inspectionRead(request, TokenAuth{UserID: "u1", TokenID: "t1", Kind: TokenKindUser})
	if err != nil {
		t.Fatalf("inspection report read: %v", err)
	}
	body, ok := payload.(map[string]any)
	if !ok {
		t.Fatalf("inspection report payload = %T, want map", payload)
	}
	raw, ok := body["items"].([]map[string]any)
	if !ok {
		t.Fatalf("inspection items = %T, want rows", body["items"])
	}
	states := map[string]string{}
	for _, row := range raw {
		asset := gatewayInspectionText(row["asset_id"])
		states[asset] = gatewayInspectionText(row["status"]) + "|" + gatewayInspectionText(row["reason"])
	}
	return states
}

// TestGatewayMySQLInspectionListExecutes runs the inspection report query on
// MySQL with a rendered clock, and requires the same state/reason values as the
// SQLite run. Before INT-28 the query carried unixepoch() baked at init and
// MySQL rejected it.
func TestGatewayMySQLInspectionListExecutes(t *testing.T) {
	db := gatewayMySQLTestOpenEmpty(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := installGatewayMySQLSchema(ctx, db); err != nil {
		t.Fatalf("install MySQL schema: %v", err)
	}
	now := time.Now().Unix()
	gatewayInspectionSeed(t, db, now)

	store := &GatewayStore{db: db, dialect: gatewayDialectMySQL, backend: "mysql"}
	var mysqlStates map[string]string
	gatewayUseDialect(gatewayDialectMySQL, func() {
		mysqlStates = gatewayInspectionStates(t, store)
	})
	want := map[string]string{
		"a1": "pending|",
		"a2": "missing|collection_deadline_elapsed",
		"a3": "pass|all_good",
		"a4": "missing|collection_deadline_elapsed",
	}
	for asset, expected := range want {
		if got := mysqlStates[asset]; got != expected {
			t.Errorf("MySQL inspection row %s = %q, want %q (all rows: %v)", asset, got, expected, mysqlStates)
		}
	}

	sqlite := gatewayInspectionSQLiteStore(t)
	gatewayUseDialect(gatewayDialectSQLite, func() {
		sqliteStates := gatewayInspectionStates(t, sqlite)
		for asset, expected := range want {
			if got := sqliteStates[asset]; got != expected {
				t.Errorf("SQLite inspection row %s = %q, want %q (all rows: %v)", asset, got, expected, sqliteStates)
			}
		}
	})

	// Run 2 is planned by u2, who is disabled, so the block expression turns the
	// pending item into a gap with the executor reason. Deadline is still in the
	// future, so the deadline branch cannot be what produced "missing".
	wantBlocked := map[string]string{"a5": "missing|executor_grant_revoked"}
	gatewayUseDialect(gatewayDialectMySQL, func() {
		got := gatewayInspectionStatesForRun(t, store, 2)
		if got["a5"] != wantBlocked["a5"] {
			t.Errorf("MySQL blocked-executor row = %q, want %q (all rows: %v)", got["a5"], wantBlocked["a5"], got)
		}
	})
	gatewayUseDialect(gatewayDialectSQLite, func() {
		got := gatewayInspectionStatesForRun(t, sqlite, 2)
		if got["a5"] != wantBlocked["a5"] {
			t.Errorf("SQLite blocked-executor row = %q, want %q (all rows: %v)", got["a5"], wantBlocked["a5"], got)
		}
	})
}

func gatewayInspectionSQLiteStore(t *testing.T) *GatewayStore {
	t.Helper()
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "inspection.db"))
	if err != nil {
		t.Fatalf("open SQLite store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	now := time.Now().Unix()
	for _, statement := range gatewayInspectionSQLiteDDL() {
		if _, err := store.db.Exec(statement); err != nil {
			t.Fatalf("create SQLite fixture table: %v", err)
		}
	}
	gatewayInspectionSeed(t, store.db, now)
	return store
}

// TestGatewayInspectionStateBoundary pins the database clock at the deadline so
// the <= boundary is decided by the rendered SQL, not by wall-clock timing.
func TestGatewayInspectionStateBoundary(t *testing.T) {
	mysqlDB := gatewayMySQLTestOpenEmpty(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := installGatewayMySQLSchema(ctx, mysqlDB); err != nil {
		t.Fatalf("install MySQL schema: %v", err)
	}
	gatewayInspectionSeed(t, mysqlDB, time.Now().Unix())
	sqliteStore := gatewayInspectionSQLiteStore(t)
	probe := "SELECT " + inspectionStateAt("100") + " FROM inspection_items s JOIN inspection_runs r ON r.id=s.run_id JOIN inspection_plans p ON p.id=r.plan_id WHERE s.id=?"
	for name, db := range map[string]*sql.DB{"mysql": mysqlDB, "sqlite": sqliteStore.db} {
		var state string
		if err := db.QueryRow(probe, 4).Scan(&state); err != nil {
			t.Fatalf("%s boundary probe: %v (probe: %s)", name, err, probe)
		}
		if state != "missing" {
			t.Errorf("%s item with deadline == clock renders %q, want missing (deadline<=clock, so equality is overdue)", name, state)
		}
	}
}
