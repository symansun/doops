package server

// INT-94: the timeout table is the only source of these budgets. These tests
// are the falsifiable half of the wiring: each one drives a real call site with
// a non-default table value and asserts the observed behavior, so a call site
// that quietly keeps its old literal turns red. The source scan is the
// structural half: the retired constants must not come back at all.

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// withTimeoutField swaps one field of the process snapshot for the duration of
// a test. Every call site reads that snapshot, so mutating it here is exactly
// what an operator setting the env var does at startup.
func withTimeoutField(t *testing.T, apply func(*timeoutDefaults)) {
	t.Helper()
	saved := timeouts
	apply(&timeouts)
	t.Cleanup(func() { timeouts = saved })
}

// retiredTimeoutNames are the package-private constants that used to hold these
// budgets. Their absence from production sources is the structural half of the
// wiring: a re-added literal cannot reach a call site without a fresh review,
// and the behavioral tests below would then disagree with it.
var retiredTimeoutNames = []string{
	"helmSettleReserve",
	"helmSettleTimeout",
	"maxHelmWaitTimeout",
	"helmTerminationGrace",
	"mcpToolCallDeadline",
	"mcpProbeTimeout",
	"defaultEdgeDialTimeout",
	"monitorRetentionMinInterval",
	"modelProviderDeliveryTimeout",
	"registryAuthDeliveryTimeout",
	"toolDeadlineMargin",
	"doopsMCPToolTimeoutSeconds",
	// INT-104/INT-105: the tunnel read deadline and the ?wait_ms ceiling are now
	// table fields with accessors, so their old private names must stay gone.
	"defaultAgentTunnelReadTimeout",
	"executionResumeWaitMax",
	// INT-106 moved the three Edge budgets onto the table and deleted their
	// private defaults, so the literals cannot come back unnoticed. The Edge
	// websocket deadline changed from a constant to edgeWSReadTimeout(), a
	// function name the scan must allow, which is why it is not listed here.
	"defaultEdgeWriteStall",
	"defaultEdgeConnIDQuarantine",
	"defaultEdgeHelloTimeout",
}

// TestRetiredTimeoutConstantsStayOutOfProductionSources scans this package for
// the retired identifiers. The file-count floor keeps a broken scan (wrong
// directory, empty listing) from passing vacuously.
func TestRetiredTimeoutConstantsStayOutOfProductionSources(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source file")
	}
	dir := filepath.Dir(thisFile)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	retired := make(map[string]bool, len(retiredTimeoutNames))
	for _, name := range retiredTimeoutNames {
		retired[name] = true
	}
	fset := token.NewFileSet()
	scanned := 0
	var hits []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		scanned++
		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			ident, isIdent := node.(*ast.Ident)
			if !isIdent || !retired[ident.Name] {
				return true
			}
			position := fset.Position(ident.Pos()).String()
			hits = append(hits, strings.TrimPrefix(position, dir+string(os.PathSeparator))+" "+ident.Name)
			return true
		})
	}
	if scanned < 100 {
		t.Fatalf("scanned only %d production Go files in %s; the scan must cover the package", scanned, dir)
	}
	if len(hits) > 0 {
		sort.Strings(hits)
		t.Fatalf("a retired timeout constant is back in production sources (%d hits in %d files):%s%s", len(hits), scanned, string(rune(10)), strings.Join(hits, string(rune(10))))
	}
}

// TestToolDeadlineMarginComesFromTheTable drives toolDeadline with a 10s gateway
// budget: the default table margin leaves 5s, and a 1s table margin must leave
// 9s. A call site reading a private 5s literal would stay at 5s under the swap.
func TestToolDeadlineMarginComesFromTheTable(t *testing.T) {
	t.Setenv(ToolTimeoutEnv, "")
	_, cancel, effective := toolDeadline(context.Background(), 10000)
	cancel()
	if want := 10*time.Second - timeouts.ToolDeadlineMargin; effective != want {
		t.Fatalf("effective deadline %s, want the table margin to leave %s", effective, want)
	}
	withTimeoutField(t, func(d *timeoutDefaults) { d.ToolDeadlineMargin = time.Second })
	_, cancelSwapped, swapped := toolDeadline(context.Background(), 10000)
	cancelSwapped()
	if swapped != 9*time.Second {
		t.Fatalf("with a 1s table margin the 10s budget must leave 9s, got %s", swapped)
	}
}

// TestMonitorRetentionThrottleComesFromTheTable pins the throttle window to the
// table: with a 1s window a 500ms retry is skipped and a 1.5s retry scans. The
// default 15m literal would throttle both, so this test fails if the call site
// stops reading the table.
func TestMonitorRetentionThrottleComesFromTheTable(t *testing.T) {
	withTimeoutField(t, func(d *timeoutDefaults) { d.MonitorRetentionMinInterval = time.Second })
	resetMonitorRetentionGate()
	store := openMonitorArtifactStore(t)
	now := time.Now().UTC()
	seedMonitorRetentionRun(t, store, "run_ret_wiring", "sched_ret_wiring", now.Add(-40*24*time.Hour).UnixMilli(), "success", false)
	policy := retentionTestPolicy(30, 90, 1)
	if first := store.PruneMonitorRetention(now, policy, false); first.SkippedThrottled {
		t.Fatalf("the first scan must not be throttled, got %s", first.summary())
	}
	skip := store.PruneMonitorRetention(now.Add(500*time.Millisecond), policy, false)
	if !skip.SkippedThrottled {
		t.Fatalf("a 500ms retry inside the 1s table window must be throttled, got %s", skip.summary())
	}
	if skip.ThrottleWindow != time.Second {
		t.Fatalf("the skip must report the table window 1s, got %s", skip.ThrottleWindow)
	}
	if next := store.PruneMonitorRetention(now.Add(1500*time.Millisecond), policy, false); next.SkippedThrottled {
		t.Fatalf("after the 1s table window a 1.5s gap must scan, got %s", next.summary())
	}
}

// TestMCPProbeTimeoutComesFromTheTable drives probeHTTPMCP against a fake MCP
// server that answers initialize and tools/list. With a 30ms table timeout the
// 300ms server is aborted fast; with a 2s one the same probe succeeds.
func TestMCPProbeTimeoutComesFromTheTable(t *testing.T) {
	var slow atomic.Bool
	slow.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if slow.Load() {
			time.Sleep(300 * time.Millisecond)
		}
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		method, _ := request["method"].(string)
		var result map[string]any
		switch method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "fake", "version": "1"},
			}
		case "tools/list":
			result = map[string]any{"tools": []any{}}
		default:
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": result})
	}))
	defer server.Close()
	entry := agentMCPServerEntry{Name: "fake", Transport: "http", URL: server.URL}

	withTimeoutField(t, func(d *timeoutDefaults) { d.MCPProbeTimeout = 30 * time.Millisecond })
	started := time.Now()
	if _, _, err := probeHTTPMCP(context.Background(), entry); err == nil {
		t.Fatal("a 30ms table probe timeout must fail against a 300ms server")
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("the probe waited %s; the 30ms table timeout must abort it long before the 12s default", elapsed)
	}

	slow.Store(false)
	withTimeoutField(t, func(d *timeoutDefaults) { d.MCPProbeTimeout = 2 * time.Second })
	info, tools, err := probeHTTPMCP(context.Background(), entry)
	if err != nil {
		t.Fatalf("a fast server must probe successfully inside the table timeout: %v", err)
	}
	if info == nil || info.Name != "fake" {
		t.Fatalf("initialize result lost its serverInfo, got %+v", info)
	}
	if len(tools) != 0 {
		t.Fatalf("tools/list returned no tools, got %d", len(tools))
	}
}

// TestMySQLConfigTakesConnectTimeoutFromTheTable pins the driver config to the
// table: all three MySQL entry points build it, so a private 8s literal would
// survive only here.
func TestMySQLConfigTakesConnectTimeoutFromTheTable(t *testing.T) {
	config := gatewayMySQLConfig{Host: "db.example", Port: 3306, User: "u", Password: "p", Database: "d"}
	cfg, err := config.driverConfig()
	if err != nil {
		t.Fatalf("driverConfig: %v", err)
	}
	if cfg.Timeout != timeouts.MySQLConnectTimeout {
		t.Fatalf("driver timeout %s, want the table value %s", cfg.Timeout, timeouts.MySQLConnectTimeout)
	}
	withTimeoutField(t, func(d *timeoutDefaults) { d.MySQLConnectTimeout = 50 * time.Millisecond })
	cfg, err = config.driverConfig()
	if err != nil {
		t.Fatalf("driverConfig after the swap: %v", err)
	}
	if cfg.Timeout != 50*time.Millisecond {
		t.Fatalf("the driver timeout ignored the table swap, got %s", cfg.Timeout)
	}
}
