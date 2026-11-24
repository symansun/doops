package server

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// TestGatewayMySQLTargetPairArgsOrder pins the constructor that owns the bind
// order of every catalog statement. The ?N to ? rewrite is safe only while the
// values reach the driver in the same order the text binds them, and that order
// is invisible to MySQL: a swapped pair is still valid SQL and still returns
// 200, it just returns the wrong rows. So the order is asserted value by value,
// not merely counted.
func TestGatewayMySQLTargetPairArgsOrder(t *testing.T) {
	got := fmt.Sprint(targetPairArgs("alpha", "beta", 4, true))
	want := "[alpha beta alpha beta alpha beta alpha beta true]"
	if got != want {
		t.Fatalf("list bindings = %s, want %s (cluster first, then instance, once per placeholder)", got, want)
	}
	got = fmt.Sprint(targetPairArgs("alpha", "beta", 4, "machine", "m1"))
	want = "[alpha beta alpha beta alpha beta alpha beta machine m1]"
	if got != want {
		t.Fatalf("existence bindings = %s, want %s (the two trailing probes stay last)", got, want)
	}
	got = fmt.Sprint(targetPairArgs("alpha", "beta", 7))
	want = "[alpha beta alpha beta alpha beta alpha beta alpha beta alpha beta alpha beta]"
	if got != want {
		t.Fatalf("declared-resource bindings = %s, want %s (seven pairs)", got, want)
	}
	if got := len(targetPairArgs("alpha", "beta", 0)); got != 0 {
		t.Fatalf("zero pairs produced %d bindings, want 0", got)
	}
}

// TestGatewayMySQLCatalogCallSitesBindThroughConstructor is the guard the INT-19
// tests lacked: they built their own argument list, so flipping cluster and
// instance at a production call site changed nothing they could see. This walks
// the source, finds every statement whose SQL text starts from workspaceCatalog
// or declaredAgentResourcesSQL, and requires the call to pass exactly one
// constructor call — then checks the constructor's pair count against the
// number of ? in that statement, so a new placeholder cannot slip through.
func TestGatewayMySQLCatalogCallSitesBindThroughConstructor(t *testing.T) {
	fset := token.NewFileSet()
	files := []string{"agent_asset_scope.go", "agent_resource_store.go"}
	guarded := 0
	for _, name := range files {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "Query", "QueryRow", "QueryContext", "QueryRowContext", "Exec", "ExecContext":
			default:
				return true
			}
			// The *Context forms take the context first, so the statement is not
			// always the first argument.
			sqlIndex := 0
			if strings.HasSuffix(sel.Sel.Name, "Context") {
				sqlIndex = 1
			}
			if len(call.Args) <= sqlIndex {
				return true
			}
			sqlText := printNode(fset, call.Args[sqlIndex])
			if !strings.Contains(sqlText, "workspaceCatalog") && !strings.Contains(sqlText, "declaredAgentResourcesSQL()") {
				return true
			}
			guarded++
			where := fset.Position(call.Pos()).String()
			if !call.Ellipsis.IsValid() || len(call.Args) != sqlIndex+2 {
				t.Errorf("%s binds a catalog statement with %d explicit arguments, want a single targetPairArgs(...) spread so the order has one owner", where, len(call.Args)-sqlIndex-1)
				return true
			}
			inner, ok := call.Args[sqlIndex+1].(*ast.CallExpr)
			if !ok {
				t.Errorf("%s passes %s as call arguments, want targetPairArgs(...)", where, printNode(fset, call.Args[sqlIndex+1]))
				return true
			}
			fun, ok := inner.Fun.(*ast.Ident)
			if !ok || fun.Name != "targetPairArgs" {
				t.Errorf("%s builds catalog bindings with %s, want targetPairArgs(...)", where, printNode(fset, inner.Fun))
				return true
			}
			if len(inner.Args) < 3 {
				t.Errorf("%s calls targetPairArgs with %d arguments, want cluster, instance, pairs", where, len(inner.Args))
				return true
			}
			// The constructor fixes the pair order, but a call site can still
			// hand it the instance where the cluster belongs — the exact
			// mutation this guard exists for — so the two source values are
			// checked too.
			clusterArg := printNode(fset, inner.Args[0])
			instanceArg := printNode(fset, inner.Args[1])
			if !strings.HasSuffix(clusterArg, "Cluster") && clusterArg != "cluster" {
				t.Errorf("%s passes %s as the first binding, want the cluster", where, clusterArg)
			}
			if !strings.HasSuffix(instanceArg, "Instance") && instanceArg != "instance" {
				t.Errorf("%s passes %s as the second binding, want the instance", where, instanceArg)
			}
			pairsLit, ok := inner.Args[2].(*ast.BasicLit)
			if !ok || pairsLit.Kind != token.INT {
				t.Errorf("%s passes a non-literal pair count to targetPairArgs, so the placeholder count cannot be checked", where)
				return true
			}
			pairs, err := strconv.Atoi(pairsLit.Value)
			if err != nil {
				t.Errorf("%s has an unparsable pair count %s", where, pairsLit.Value)
				return true
			}
			extras := len(inner.Args) - 3
			// The identifier stands for the constant; count what the driver sees.
			resolved := strings.ReplaceAll(sqlText, "workspaceCatalog", workspaceCatalog)
			resolved = strings.ReplaceAll(resolved, "declaredAgentResourcesSQL()", declaredAgentResourcesSQL())
			placeholders := strings.Count(resolved, "?")
			if want := pairs*2 + extras; placeholders != want {
				t.Errorf("%s binds %d placeholders with %d values (%d pairs plus %d extras)", where, placeholders, want, pairs, extras)
			}
			return true
		})
	}
	if guarded != 3 {
		t.Fatalf("guarded %d catalog call sites, want 3 (list, existence, declared resources); a renamed helper would otherwise skip the guard silently", guarded)
	}
}

func printNode(fset *token.FileSet, n ast.Node) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, n); err != nil {
		return ""
	}
	return buf.String()
}
