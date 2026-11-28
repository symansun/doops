package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// deadSymbolsRemovedByINT78 lists identifiers that had no production caller and
// were removed from this package (INT-78, dead-code shelf of the 11-22 ledger).
// A production reference coming back is either a resurrected dead path or a
// deliberate revival - either way it must not arrive silently, so the guard
// fails and names the file and line.
//
// test-only helpers that were moved into _test.go files keep their names; this
// guard only reads production sources, so those stay legal by construction.
var deadSymbolsRemovedByINT78 = []string{
	"gatewayMySQLDuplicateKey",
	"knowledgeBaseCapabilityCompatible",
	"TouchContext",
	"TouchAgentContext",
	"tokenIssuedVia",
	"clientCapabilities",
	"runK8SMCPStdioContext",
	"Stalled",
	"LiveCount",
	"shouldCloseWrite",
	"decodeEdgeClosePayload",
	"isResidualProcessesTerminated",
	"parseCOSSecret",
	"hasUnresolvedMutation",
	"doSQLVersionUnfinished",
}

func TestRemovedDeadSymbolsStayOutOfProductionSources(t *testing.T) {
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the guard source file")
	}
	dir := filepath.Dir(self)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	dead := make(map[string]bool, len(deadSymbolsRemovedByINT78))
	for _, name := range deadSymbolsRemovedByINT78 {
		dead[name] = true
	}
	scanned := 0
	var findings []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		scanned++
		ast.Inspect(parsed, func(node ast.Node) bool {
			ident, isIdent := node.(*ast.Ident)
			if !isIdent || !dead[ident.Name] {
				return true
			}
			position := fset.Position(ident.Pos())
			findings = append(findings, name+":"+strconv.Itoa(position.Line)+" "+ident.Name)
			return true
		})
	}
	if scanned <= 100 {
		t.Fatalf("scanned only %d production files; the walk is broken and would pass vacuously", scanned)
	}
	if len(findings) > 0 {
		t.Fatalf("production sources name symbols removed by INT-78 (dead code came back):\n  %s", strings.Join(findings, "\n  "))
	}
}
