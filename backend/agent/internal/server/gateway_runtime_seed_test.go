package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestGatewayRuntimeSeedStepsMatchContract pins the startup seed list. Adding,
// removing or reordering a seed is allowed, but it must be a deliberate edit
// here too: that is what makes "a new seed silently skipped by one backend"
// impossible, because both backends run exactly this slice.
func TestGatewayRuntimeSeedStepsMatchContract(t *testing.T) {
	want := []string{"bootstrap-rows", "builtin-skills", "builtin-mcp-servers", "audit-indexes"}
	var got []string
	seen := map[string]bool{}
	for _, step := range gatewayRuntimeSeedSteps() {
		if step.name == "" {
			t.Fatalf("seed step %d has no name", len(got))
		}
		if seen[step.name] {
			t.Fatalf("seed step %q is listed twice", step.name)
		}
		seen[step.name] = true
		if step.run == nil {
			t.Fatalf("seed step %q has no run function", step.name)
		}
		got = append(got, step.name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("startup seed steps = %v, want %v (update this list and gateway_runtime_seed.go together)", got, want)
	}
}

type gatewayParsedSource struct {
	name string
	file *ast.File
}

func gatewayParsePackageSources(t *testing.T) []gatewayParsedSource {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	fset := token.NewFileSet()
	var sources []gatewayParsedSource
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		sources = append(sources, gatewayParsedSource{name: name, file: file})
	}
	if len(sources) == 0 {
		t.Fatal("found no package sources")
	}
	return sources
}

func gatewayFuncDecl(sources []gatewayParsedSource, name string) (gatewayParsedSource, *ast.FuncDecl) {
	for _, source := range sources {
		for _, decl := range source.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Name.Name == name {
				return source, fn
			}
		}
	}
	return gatewayParsedSource{}, nil
}

func gatewayCallsFunc(fn *ast.FuncDecl, name string) bool {
	found := false
	ast.Inspect(fn, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == name {
			found = true
		}
		return !found
	})
	return found
}

// TestGatewayStartupSeedsRunThroughOnePath is the structural half of the INT-33
// guard: both startup paths must call seedRuntimeDefaults, and no startup seed
// may be called from anywhere except the shared seed list. A new seed wired
// straight into migrate() (the old bug shape) turns this red.
func TestGatewayStartupSeedsRunThroughOnePath(t *testing.T) {
	sources := gatewayParsePackageSources(t)
	for _, name := range []string{"migrate", "gatewayMySQLPrepareRuntimeSchema"} {
		_, fn := gatewayFuncDecl(sources, name)
		if fn == nil {
			t.Fatalf("startup function %s is gone; the INT-33 guard cannot run", name)
		}
		if !gatewayCallsFunc(fn, "seedRuntimeDefaults") {
			t.Errorf("%s does not call seedRuntimeDefaults; a startup seed would reach only one backend", name)
		}
	}
	// Seed entry points are reachable only through gatewayRuntimeSeedSteps, which
	// both backends run. A call from migrate() or from the MySQL installer is the
	// drift this task removed.
	for _, source := range sources {
		if source.name == "gateway_runtime_seed.go" {
			continue
		}
		for _, decl := range source.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				callee := selector.Sel.Name
				if strings.HasPrefix(callee, "Seed") || callee == "optimizeAuditIndexes" {
					t.Errorf("%s calls startup seed %s outside gatewayRuntimeSeedSteps; add it to gateway_runtime_seed.go instead so both backends run it", source.name, callee)
				}
				return true
			})
		}
	}
}

var gatewayMigrationInsertPattern = regexp.MustCompile("(?i)\\bINSERT\\s+(?:OR\\s+[A-Z]+\\s+)?INTO\\s+([a-z_][a-z0-9_]*)")

// gatewayMigrationCopyTables are INSERTs inside migrate* functions that copy
// existing rows into a new table shape. They are migration steps, not seeded
// singletons, so they stay in the migration and the MySQL offline copier owns
// the equivalent data; a row seeded for a fresh database must not live here.
var gatewayMigrationCopyTables = map[string]string{
	"credential_bundle_items_v2":  "copies existing credential bundle items into the v2 table",
	"credential_verifications_v2": "copies existing credential verifications into the v2 table",
	"grants_v2":                   "copies existing grants into the v2 table",
}

// TestMigrateDoesNotSeedRowsOutsideTheSharedList turns red when a later change
// writes a singleton/bootstrap row from a migrate* function. Such a row would
// reach SQLite only, which is exactly the INT-33 failure.
func TestMigrateDoesNotSeedRowsOutsideTheSharedList(t *testing.T) {
	sources := gatewayParsePackageSources(t)
	bootstrapTables := gatewayStoreBootstrapTables()
	for _, source := range sources {
		for _, decl := range source.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if fn.Name.Name != "migrate" && !strings.HasPrefix(fn.Name.Name, "migrate") {
				continue
			}
			ast.Inspect(fn, func(node ast.Node) bool {
				literal, ok := node.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				for _, match := range gatewayMigrationInsertPattern.FindAllStringSubmatch(literal.Value, -1) {
					table := strings.ToLower(match[1])
					if bootstrapTables[table] {
						t.Errorf("%s inserts into bootstrap table %s; those rows come from gatewayStoreBootstrapRows, not from a migration", fn.Name.Name, table)
						continue
					}
					if _, ok := gatewayMigrationCopyTables[table]; ok {
						continue
					}
					t.Errorf("%s inserts a row into %s from the migration; if it is a seeded singleton, add it to gateway_runtime_seed.go so MySQL seeds it too, otherwise list it in gatewayMigrationCopyTables with the reason", fn.Name.Name, table)
				}
				return true
			})
		}
	}
}

// TestGatewayBootstrapTablesMatchContract pins which singleton rows a fresh
// database must hold. Dropping one backend's seed would otherwise leave the
// cross-backend comparison green (both sides empty) while the Gateway panics on
// the first read.
func TestGatewayBootstrapTablesMatchContract(t *testing.T) {
	want := []string{"edge_route_revision", "gateway_schema_migrations"}
	var got []string
	for _, row := range gatewayStoreBootstrapRows() {
		got = append(got, row.table)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bootstrap tables = %v, want %v", got, want)
	}
	required := gatewayStoreBootstrapTables()
	for _, table := range want {
		if !required[table] {
			t.Errorf("gatewayStoreBootstrapTables does not name %s", table)
		}
	}
}

// TestGatewayBootstrapRowsCarryProbes keeps the shared bootstrap list honest:
// each row it seeds must have a probe, so a missing row fails with the table
// name instead of a bare "sql: no rows in result set".
func TestGatewayBootstrapRowsCarryProbes(t *testing.T) {
	for _, row := range gatewayStoreBootstrapRows() {
		if strings.TrimSpace(row.table) == "" || strings.TrimSpace(row.seed.insert) == "" {
			t.Fatalf("bootstrap row %+v is incomplete", row)
		}
		if row.probe == nil {
			t.Errorf("bootstrap table %s has no probe; a missing row would surface as an opaque SQL error", row.table)
		}
		if len(row.seed.target) == 0 || !row.seed.doNothing {
			t.Errorf("bootstrap table %s has no conflict target; the seed would not be repeatable", row.table)
		}
	}
}
