package server

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// packageSourceDir is the directory of this package's sources, resolved from the
// test binary's own file so the scan cannot be pointed at another tree.
func packageSourceDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate the package sources")
	}
	return filepath.Dir(file)
}

// productionGoFiles lists every non-test Go file of the package.
func productionGoFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var files []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files = append(files, filepath.Join(dir, name))
	}
	sort.Strings(files)
	return files
}

// TestRetiredEnvReadersStayOutOfTheSources is the anti-regression gate for
// INT-106. positiveEnvDuration and durationOverride were the two remaining
// "read the environment on every call, fall back silently on a typo" readers.
// Both are gone; a call site that comes back must name its file and line here.
func TestRetiredEnvReadersStayOutOfTheSources(t *testing.T) {
	dir := packageSourceDir(t)
	files := productionGoFiles(t, dir)
	if len(files) <= 50 {
		t.Fatalf("scanned only %d Go files under %s; the scan is not looking at the package", len(files), dir)
	}
	retired := map[string]bool{"positiveEnvDuration": true, "durationOverride": true}
	var hits []string
	for _, path := range files {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok || !retired[id.Name] {
				return true
			}
			pos := fset.Position(id.Pos())
			hits = append(hits, fmt.Sprintf("%s:%d %s", filepath.Base(pos.Filename), pos.Line, id.Name))
			return true
		})
	}
	if len(hits) > 0 {
		sort.Strings(hits)
		t.Fatalf("a retired per-call environment reader is back in the runtime sources:%s  %s",
			string(rune(10)), strings.Join(hits, string(rune(10))+"  "))
	}
}

// TestEveryEnvSpecIsReadThroughTheSnapshot is the wiring half of the gate: a row
// with an Env must be reachable from the resolved snapshot. A field that is added
// to the table but only ever read as defaultTimeouts().<Field> would resolve the
// environment, validate it, and then ignore it, which is exactly the silent
// fallback this table exists to remove. The mapping below is read out of
// timeoutSpecs() itself, so a new row is covered without editing the test.
func TestEveryEnvSpecIsReadThroughTheSnapshot(t *testing.T) {
	dir := packageSourceDir(t)
	files := productionGoFiles(t, dir)
	if len(files) <= 50 {
		t.Fatalf("scanned only %d Go files under %s; the scan is not looking at the package", len(files), dir)
	}

	fset := token.NewFileSet()
	tablePath := filepath.Join(dir, "timeout_defaults.go")
	table, err := parser.ParseFile(fset, tablePath, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", tablePath, err)
	}
	envField := map[string]string{}
	ast.Inspect(table, func(n ast.Node) bool {
		spec, ok := n.(*ast.CompositeLit)
		if !ok || len(spec.Elts) == 0 {
			return true
		}
		var env, field string
		for _, elt := range spec.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			switch key.Name {
			case "Env":
				if lit, ok := kv.Value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					env = strings.Trim(lit.Value, string(rune(34)))
				}
			case "set":
				fn, ok := kv.Value.(*ast.FuncLit)
				if !ok || fn.Body == nil || len(fn.Body.List) != 1 {
					continue
				}
				assign, ok := fn.Body.List[0].(*ast.AssignStmt)
				if !ok || len(assign.Lhs) != 1 {
					continue
				}
				if sel, ok := assign.Lhs[0].(*ast.SelectorExpr); ok {
					field = sel.Sel.Name
				}
			}
		}
		if env != "" && field != "" {
			envField[env] = field
		}
		return true
	})
	if len(envField) < 20 {
		t.Fatalf("read only %d environment rows out of timeoutSpecs(); the parse is broken", len(envField))
	}

	read := map[string]bool{}
	for _, path := range files {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "timeouts" {
				read[sel.Sel.Name] = true
			}
			return true
		})
	}

	var missing []string
	for env, field := range envField {
		if !read[field] {
			missing = append(missing, env+" resolves into "+field+" but no runtime source reads timeouts."+field)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("a timeout row is resolved but never used:%s  %s", string(rune(10)), strings.Join(missing, string(rune(10))+"  "))
	}
}

// TestEdgeBudgetsComeFromTheTable proves the three Edge budgets and the Edge
// websocket deadline follow the resolved snapshot instead of the environment.
func TestEdgeBudgetsComeFromTheTable(t *testing.T) {
	if got := edgeWriteStall(); got != 30*time.Second {
		t.Fatalf("edgeWriteStall = %s, want the shipped 30s default", formatTimeoutValue(got))
	}
	if got := edgeConnIDQuarantine(); got != 30*time.Second {
		t.Fatalf("edgeConnIDQuarantine = %s, want the shipped 30s default", formatTimeoutValue(got))
	}
	if got := edgeHelloTimeout(); got != 5*time.Second {
		t.Fatalf("edgeHelloTimeout = %s, want the shipped 5s default", formatTimeoutValue(got))
	}
	if got := edgeWSReadTimeout(); got != 20*time.Second {
		t.Fatalf("edgeWSReadTimeout = %s, want the shipped 20s default", formatTimeoutValue(got))
	}
	withTimeoutField(t, func(t *timeoutDefaults) {
		t.EdgeWriteStall = time.Second
		t.EdgeConnIDQuarantine = 7 * time.Second
		t.EdgeHelloTimeout = 2 * time.Second
		t.EdgeWSReadTimeout = 11 * time.Second
	})
	if got := edgeWriteStall(); got != time.Second {
		t.Fatalf("edgeWriteStall = %s, want the 1s table value", formatTimeoutValue(got))
	}
	if got := edgeConnIDQuarantine(); got != 7*time.Second {
		t.Fatalf("edgeConnIDQuarantine = %s, want the 7s table value", formatTimeoutValue(got))
	}
	if got := edgeHelloTimeout(); got != 2*time.Second {
		t.Fatalf("edgeHelloTimeout = %s, want the 2s table value", formatTimeoutValue(got))
	}
	if got := edgeWSReadTimeout(); got != 11*time.Second {
		t.Fatalf("edgeWSReadTimeout = %s, want the 11s table value", formatTimeoutValue(got))
	}
	// A zero option falls back to the table default (the scheduler always passes
	// the resolved value explicitly), and never to a private literal.
	table := newEdgeConnIDTable(nil, 4, 0, 0)
	if want := defaultTimeouts().EdgeConnIDQuarantine; table.quarantineFor != want {
		t.Fatalf("edge conn id quarantine fallback = %s, want the table default %s", formatTimeoutValue(table.quarantineFor), formatTimeoutValue(want))
	}
	if table.openingFor != 5*time.Second+edgeOpeningAckSlack {
		t.Fatalf("edge conn id opening fallback = %s, want the dial budget plus the advertised slack", formatTimeoutValue(table.openingFor))
	}
}

// TestToolExecutionBudgetsComeFromTheSnapshot pins the two getters that used to
// re-read the environment on every call.
func TestToolExecutionBudgetsComeFromTheSnapshot(t *testing.T) {
	withTimeoutField(t, func(t *timeoutDefaults) {
		t.ToolExecutionTimeout = 45 * time.Minute
		t.CompletedTaskMaxAge = 3 * time.Hour
	})
	if got := maxToolExecutionDuration(); got != 45*time.Minute {
		t.Fatalf("maxToolExecutionDuration = %s, want the 45m table value", formatTimeoutValue(got))
	}
	if got := maxCompletedTaskAge(); got != 3*time.Hour {
		t.Fatalf("maxCompletedTaskAge = %s, want the 3h table value", formatTimeoutValue(got))
	}
	t.Setenv(ToolTimeoutEnv, "90m")
	if got := maxToolExecutionDuration(); got != 45*time.Minute {
		t.Fatalf("setting %s after start moved the budget to %s; the getter must read the startup snapshot", ToolTimeoutEnv, formatTimeoutValue(got))
	}
}

// TestEdgeTimeoutValuesStopStartup makes the new rows fail closed: a typo must
// stop the process and name the variable and its range, not fall back.
func TestEdgeTimeoutValuesStopStartup(t *testing.T) {
	cases := []struct {
		env  string
		raw  string
		want string
	}{
		{"DOOPS_EDGE_WRITE_STALL", "5q", "not a Go duration"},
		{"DOOPS_EDGE_CONN_ID_QUARANTINE", "0s", "outside the allowed range"},
		{"DOOPS_EDGE_HELLO_TIMEOUT", "soon", "not a Go duration"},
		{"DOOPS_EDGE_WS_READ_TIMEOUT", "1h", "allowed range"},
	}
	for _, c := range cases {
		_, err := loadTimeouts(timeoutEnv(map[string]string{c.env: c.raw}))
		if err == nil {
			t.Fatalf("%s=%q was accepted; an invalid timeout must stop the process", c.env, c.raw)
		}
		msg := err.Error()
		if !strings.HasPrefix(msg, "gateway_timeout_config_invalid: ") {
			t.Fatalf("%s=%q failed without the startup prefix: %s", c.env, c.raw, msg)
		}
		if !strings.Contains(msg, c.env) || !strings.Contains(msg, c.want) {
			t.Fatalf("%s=%q failed with %s, want %q and the variable name", c.env, c.raw, msg, c.want)
		}
		if !strings.Contains(msg, "allowed range") {
			t.Fatalf("%s=%q failed without a range: %s", c.env, c.raw, msg)
		}
	}
}

// TestEdgeTimeoutOrderingIsChecked pins I12 and I13.
func TestEdgeTimeoutOrderingIsChecked(t *testing.T) {
	_, err := loadTimeouts(timeoutEnv(map[string]string{
		"DOOPS_EDGE_DIAL_TIMEOUT":       "5s",
		"DOOPS_EDGE_CONN_ID_QUARANTINE": "3s",
	}))
	if err == nil {
		t.Fatal("a 3s quarantine under a 5s dial with 1s slack was accepted; a released connection id could be reused mid-handshake")
	}
	if !strings.Contains(err.Error(), "edgeConnIDQuarantine") || !strings.Contains(err.Error(), "DOOPS_EDGE_DIAL_TIMEOUT") {
		t.Fatalf("quarantine failure = %s, want both variables named", err)
	}
	_, err = loadTimeouts(timeoutEnv(map[string]string{"DOOPS_EDGE_WS_READ_TIMEOUT": "2s"}))
	if err == nil {
		t.Fatal("a 2s Edge websocket deadline under a 5s ping interval was accepted; a healthy session would be cut")
	}
	if !strings.Contains(err.Error(), "edgeWSReadTimeout") || !strings.Contains(err.Error(), "agentWSPingInterval") {
		t.Fatalf("Edge websocket failure = %s, want the deadline and the ping interval named", err)
	}
}
