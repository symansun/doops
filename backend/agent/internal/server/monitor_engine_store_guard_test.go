package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// monitorStrategiesJSONDDLDefault is the frozen monitor_schedules DDL line the
// guard accepts verbatim. Pinning the whole line means an edit that drops the
// default fails here instead of silently changing what a fresh SQLite row gets.
const monitorStrategiesJSONDDLDefault = "strategies_json TEXT NOT NULL DEFAULT '[\"vm_host\",\"k8s_cluster\",\"container_runtime\",\"app_service\",\"database_service\"]'"

// TestMonitorStrategiesJSONHasNoRuntimeSQL pins task M-02 of the 11-22 security
// ledger (dead code / dead flag): SF-09 retired the native strategy selector, so
// monitor_schedules.strategies_json may survive only in the frozen DDL. No
// runtime SELECT, UPDATE, DELETE or WHERE clause may name it, and the only
// remaining write is the INSERT column the frozen MySQL DDL forces: that column
// is NOT NULL without a default, so MySQL 8.4 strict mode answers an INSERT that
// omits it with error 1364 (TestGatewayMySQLInsertDefaultsMatchSchema owns that
// rule; SQLite fills its DDL default, MySQL has none to fill).
//
// The guard parses every non-test Go file in this package and inspects the SQL
// string literals themselves, so a re-added read is reported with file:line and
// the statement head. It also floors the number of files scanned so a broken
// walk cannot pass by finding nothing.
func TestMonitorStrategiesJSONHasNoRuntimeSQL(t *testing.T) {
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the package directory")
	}
	dir := filepath.Dir(self)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("cannot list %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	scanned := 0
	ddlDefaults := 0
	insertSites := 0
	var failures []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("cannot parse %s: %v", name, err)
		}
		scanned++
		ast.Inspect(file, func(node ast.Node) bool {
			literal, isLiteral := node.(*ast.BasicLit)
			if !isLiteral || literal.Kind != token.STRING {
				return true
			}
			value := literal.Value
			if !strings.Contains(value, "strategies_json") {
				return true
			}
			// Report the line the retired column name sits on, not the line the
			// literal opens on: multi-line SQL puts them on different lines.
			line := fset.Position(literal.Pos()).Line + strings.Count(value[:strings.Index(value, "strategies_json")], "\n")
			location := name + ":" + strconv.Itoa(line)
			trimmed := strings.Trim(value, "\"\u0060")
			switch {
			case name == "gateway_store_schema.go":
				if !strings.Contains(value, monitorStrategiesJSONDDLDefault) {
					failures = append(failures, location+" declares the column without the frozen default; restore the DDL text or update this guard with the reason")
					return true
				}
				ddlDefaults++
			case name == "monitor_engine_store.go" && strings.HasPrefix(strings.TrimSpace(trimmed), "INSERT INTO monitor_schedules("):
				// The only write the frozen MySQL DDL leaves possible. It must stay
				// confined to the INSERT column list.
				// Keywords match with their trailing space so "updated_at" cannot
				// trip the UPDATE check.
				upper := strings.ToUpper(trimmed)
				if strings.Contains(upper, "SELECT ") || strings.Contains(upper, "UPDATE ") ||
					strings.Contains(upper, "DELETE ") || strings.Contains(upper, " WHERE") {
					failures = append(failures, location+" INSERT carries a read/update keyword; the retired column may only be listed for the INSERT: "+statementHead(trimmed))
					return true
				}
				insertSites++
			default:
				failures = append(failures, location+" runtime SQL still names strategies_json: "+statementHead(trimmed))
			}
			return true
		})
	}
	sort.Strings(failures)
	if len(failures) != 0 {
		t.Fatalf("strategies_json is retired from runtime SQL (SF-09 / ledger M-02); only the frozen DDL and the INSERT column the frozen MySQL DDL requires may name it:\n  %s", strings.Join(failures, "\n  "))
	}
	// Floor: the package has well over 100 non-test Go files today. A walk that
	// silently stops scanning (moved directory, changed suffix) must fail loudly
	// instead of passing with zero matches.
	if scanned <= 100 {
		t.Fatalf("only %d non-test Go files were scanned in %s; the guard is not covering the package", scanned, dir)
	}
	if ddlDefaults != 1 {
		t.Fatalf("expected exactly one frozen strategies_json DDL declaration in gateway_store_schema.go, saw %d", ddlDefaults)
	}
	if insertSites != 1 {
		t.Fatalf("expected exactly one monitor_schedules INSERT that names strategies_json, saw %d", insertSites)
	}
}

// statementHead keeps the red output readable without dumping a whole SQL body.
func statementHead(statement string) string {
	head := strings.Join(strings.Fields(statement), " ")
	if len(head) > 120 {
		head = head[:120] + "..."
	}
	return head
}
