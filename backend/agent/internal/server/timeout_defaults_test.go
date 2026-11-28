package server

// Guards for the B-13 timeout table (backend/docs/RUNTIME_LIMITS.md):
//   - the documentation and the table must agree in both directions,
//   - the constants that still own a value elsewhere must equal the table,
//   - invalid values are a startup failure, never a silent fallback,
//   - the ordering inequalities I1 / I2 / I3 are checked and reject a broken chain.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// docNewline keeps the reader from having to quote a newline escape; the doc is
// split on this byte.
var docNewline = string(rune(10))

// timeoutEnv builds a getenv function from a map so a case never depends on the
// environment of the test process.
func timeoutEnv(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func timeoutSpecByEnv(t *testing.T, env string) timeoutSpec {
	t.Helper()
	for _, spec := range timeoutSpecs() {
		if spec.Env == env {
			return spec
		}
	}
	t.Fatalf("no timeout spec for %s", env)
	return timeoutSpec{}
}

func readRuntimeLimitsDoc(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}
	path := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "docs", "RUNTIME_LIMITS.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// docTimeoutRows parses table 1 of backend/docs/RUNTIME_LIMITS.md into
// env -> {default, min, max}.
func docTimeoutRows(t *testing.T) map[string][3]time.Duration {
	t.Helper()
	rows := map[string][3]time.Duration{}
	for _, line := range strings.Split(readRuntimeLimitsDoc(t), docNewline) {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "| `DOOPS_") {
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) < 5 {
			t.Fatalf("malformed timeout row in RUNTIME_LIMITS.md: %q", line)
		}
		env := strings.Trim(strings.TrimSpace(cells[1]), "`")
		bounds := strings.Split(strings.TrimSpace(cells[3]), "..")
		if len(bounds) != 2 {
			t.Fatalf("row %s: bounds %q is not min..max", env, strings.TrimSpace(cells[3]))
		}
		def, err := time.ParseDuration(strings.TrimSpace(cells[2]))
		if err != nil {
			t.Fatalf("row %s: default %q: %v", env, strings.TrimSpace(cells[2]), err)
		}
		min, err := time.ParseDuration(bounds[0])
		if err != nil {
			t.Fatalf("row %s: min %q: %v", env, bounds[0], err)
		}
		max, err := time.ParseDuration(bounds[1])
		if err != nil {
			t.Fatalf("row %s: max %q: %v", env, bounds[1], err)
		}
		if _, seen := rows[env]; seen {
			t.Fatalf("RUNTIME_LIMITS.md lists %s twice", env)
		}
		rows[env] = [3]time.Duration{def, min, max}
	}
	if len(rows) == 0 {
		t.Fatal("RUNTIME_LIMITS.md has no timeout rows; the parser is scanning the wrong text")
	}
	return rows
}

func TestTimeoutDefaultsMatchDoc(t *testing.T) {
	rows := docTimeoutRows(t)
	var failures []string
	documented := map[string]bool{}
	for _, spec := range timeoutSpecs() {
		if spec.Env == "" {
			continue
		}
		documented[spec.Env] = true
		row, ok := rows[spec.Env]
		if !ok {
			failures = append(failures, fmt.Sprintf("%s is in the code table but missing from RUNTIME_LIMITS.md", spec.Env))
			continue
		}
		if row[0] != spec.Default {
			failures = append(failures, fmt.Sprintf("%s: RUNTIME_LIMITS.md says default %s, the table says %s",
				spec.Env, formatTimeoutValue(row[0]), formatTimeoutValue(spec.Default)))
		}
		if row[1] != spec.Min || row[2] != spec.Max {
			failures = append(failures, fmt.Sprintf("%s: RUNTIME_LIMITS.md says %s..%s, the table says %s..%s",
				spec.Env, formatTimeoutValue(row[1]), formatTimeoutValue(row[2]), formatTimeoutValue(spec.Min), formatTimeoutValue(spec.Max)))
		}
	}
	for env := range rows {
		if !documented[env] {
			failures = append(failures, fmt.Sprintf("%s is documented in RUNTIME_LIMITS.md but has no row in the code table", env))
		}
	}
	if len(failures) > 0 {
		t.Fatalf("RUNTIME_LIMITS.md and timeout_defaults.go disagree:%s  %s", docNewline, strings.Join(failures, docNewline+"  "))
	}
	// The document must state that the call sites read the table, and list them.
	doc := readRuntimeLimitsDoc(t)
	for _, want := range []string{"## 接线点清单", "已接线"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("RUNTIME_LIMITS.md no longer states %q; the wiring would go undocumented", want)
		}
	}
}

// TestTimeoutDefaultsKeepPreWiringValues is the wiring contract: each value the
// call sites used to own as a private constant must still be the table default,
// so an operator who sets nothing sees exactly the old behaviour. A one-second
// difference here is a regression, not a tuning.
func TestTimeoutDefaultsKeepPreWiringValues(t *testing.T) {
	for _, spec := range timeoutSpecs() {
		if spec.Env != "" && os.Getenv(spec.Env) != "" {
			t.Fatalf("this case measures the unset-environment defaults, but %s is set", spec.Env)
		}
	}
	cases := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		// Resolved from an empty environment: this is what a default install runs.
		{"ToolExecutionTimeout", timeouts.ToolExecutionTimeout, 30 * time.Minute},
		{"CompletedTaskMaxAge", timeouts.CompletedTaskMaxAge, 24 * time.Hour},
		{"ToolDeadlineMargin", timeouts.ToolDeadlineMargin, 5 * time.Second},
		{"HelmSettleReserve", timeouts.HelmSettleReserve, 90 * time.Second},
		{"HelmSettleTimeout", timeouts.HelmSettleTimeout, 45 * time.Second},
		{"HelmMaxWait", timeouts.HelmMaxWait, 25 * time.Minute},
		{"HelmTerminationGrace", timeouts.HelmTerminationGrace, 30 * time.Second},
		{"MCPToolTimeout", timeouts.MCPToolTimeout, 30 * time.Minute},
		{"MCPCallSettle", timeouts.MCPCallSettle, 30 * time.Second},
		{"MCPProbeTimeout", timeouts.MCPProbeTimeout, 12 * time.Second},
		{"EdgeDialTimeout", timeouts.EdgeDialTimeout, 5 * time.Second},
		// INT-106 moved the last per-call env readers (the three Edge budgets
		// and the Edge websocket read deadline) onto the same table.
		{"EdgeWriteStall", timeouts.EdgeWriteStall, 30 * time.Second},
		{"EdgeConnIDQuarantine", timeouts.EdgeConnIDQuarantine, 30 * time.Second},
		{"EdgeHelloTimeout", timeouts.EdgeHelloTimeout, 5 * time.Second},
		{"EdgeWSReadTimeout", timeouts.EdgeWSReadTimeout, 20 * time.Second},
		{"MySQLConnectTimeout", timeouts.MySQLConnectTimeout, 8 * time.Second},
		{"CredentialDeliveryTimeout", timeouts.CredentialDeliveryTimeout, 3 * time.Minute},
		{"MonitorRetentionMinInterval", timeouts.MonitorRetentionMinInterval, 15 * time.Minute},
		// INT-96 moved the last literals (tunnel_hub.go:82 / :232 and
		// pkg/gatewaycmd/gatewaycmd.go:146) onto the same table.
		{"AgentLease", timeouts.AgentLease, 90 * time.Second},
		{"AgentHeartbeatFlush", timeouts.AgentHeartbeatFlush, 30 * time.Second},
		{"PlatformLoadSampleRetention", timeouts.PlatformLoadSampleRetention, 30 * 24 * time.Hour},
		{"TargetQueueTimeout", timeouts.TargetQueueTimeout, 10 * time.Minute},
		{"AuditRetentionInterval", timeouts.AuditRetentionInterval, 6 * time.Hour},
		{"ExpiredTokenSweepInterval", timeouts.ExpiredTokenSweepInterval, 6 * time.Hour},
		// INT-98 (B-13 step four) moved the flag defaults, the reaper period,
		// the offline retention window and the audit purge pause onto the table.
		{"OperationTimeout", timeouts.OperationTimeout, 60 * time.Minute},
		{"DetachedTaskTimeout", timeouts.DetachedTaskTimeout, 24 * time.Hour},
		{"TargetReconnectGrace", timeouts.TargetReconnectGrace, 10 * time.Second},
		{"ShutdownDrainTimeout", timeouts.ShutdownDrainTimeout, 2 * time.Minute},
		{"MonitorTick", timeouts.MonitorTick, 15 * time.Second},
		{"HTTPReadHeaderTimeout", timeouts.HTTPReadHeaderTimeout, 10 * time.Second},
		{"HTTPIdleTimeout", timeouts.HTTPIdleTimeout, 2 * time.Minute},
		{"OfflineReaperInterval", timeouts.OfflineReaperInterval, time.Hour},
		{"OfflineInstanceRetention", timeouts.OfflineInstanceRetention, 14 * 24 * time.Hour},
		{"AuditPurgeBatchPause", timeouts.AuditPurgeBatchPause, 100 * time.Millisecond},
		// INT-103 moved the resume windows, the Agent-side execution TTL and the
		// HTTP shutdown budget off their literals and off the silent env readers.
		{"ExecutionResumeGrace", timeouts.ExecutionResumeGrace, 60 * time.Second},
		{"ExecutionResumeTTL", timeouts.ExecutionResumeTTL, 30 * time.Minute},
		{"AgentExecutionTTL", timeouts.AgentExecutionTTL, 30 * time.Minute},
		{"HTTPShutdownTimeout", timeouts.HTTPShutdownTimeout, 10 * time.Second},
		// INT-104/INT-105 moved the tunnel read deadline and the ?wait_ms ceiling
		// onto the same table without changing either value.
		{"TunnelReadTimeout", timeouts.TunnelReadTimeout, 90 * time.Second},
		{"ExecutionResumeWaitMax", timeouts.ExecutionResumeWaitMax, 5 * time.Second},
		{"MCP tool budget advertised to doagent", time.Duration(mcpToolTimeoutSeconds()) * time.Second, 30 * time.Minute},
		{"helm call deadline", timeouts.MCPToolTimeout - timeouts.HelmSettleReserve, 28*time.Minute + 30*time.Second},
		{"mcp call deadline", timeouts.MCPToolTimeout - timeouts.MCPCallSettle, 29*time.Minute + 30*time.Second},
	}
	var failures []string
	for _, c := range cases {
		if c.got != c.want {
			failures = append(failures, fmt.Sprintf("%s: table %s, code %s", c.name, formatTimeoutValue(c.got), formatTimeoutValue(c.want)))
		}
	}
	if len(failures) > 0 {
		t.Fatalf("timeout table and code constants disagree:%s  %s", docNewline, strings.Join(failures, docNewline+"  "))
	}
}

// timeoutField reads the field a spec resolves into.
func timeoutField(t timeoutDefaults, env string) time.Duration {
	switch env {
	case ToolTimeoutEnv:
		return t.ToolExecutionTimeout
	case "DOOPS_COMPLETED_TASK_MAX_AGE":
		return t.CompletedTaskMaxAge
	case "DOOPS_TOOL_DEADLINE_MARGIN":
		return t.ToolDeadlineMargin
	case "DOOPS_HELM_SETTLE_RESERVE":
		return t.HelmSettleReserve
	case "DOOPS_HELM_SETTLE_TIMEOUT":
		return t.HelmSettleTimeout
	case "DOOPS_HELM_MAX_WAIT":
		return t.HelmMaxWait
	case "DOOPS_AGENT_LEASE":
		return t.AgentLease
	case "DOOPS_AGENT_HEARTBEAT_FLUSH":
		return t.AgentHeartbeatFlush
	case "DOOPS_EDGE_DIAL_TIMEOUT":
		return t.EdgeDialTimeout
	case "DOOPS_EDGE_WRITE_STALL":
		return t.EdgeWriteStall
	case "DOOPS_EDGE_CONN_ID_QUARANTINE":
		return t.EdgeConnIDQuarantine
	case "DOOPS_EDGE_HELLO_TIMEOUT":
		return t.EdgeHelloTimeout
	case "DOOPS_EDGE_WS_READ_TIMEOUT":
		return t.EdgeWSReadTimeout
	case "DOOPS_MYSQL_CONNECT_TIMEOUT":
		return t.MySQLConnectTimeout
	case "DOOPS_MCP_PROBE_TIMEOUT":
		return t.MCPProbeTimeout
	case "DOOPS_CREDENTIAL_DELIVERY_TIMEOUT":
		return t.CredentialDeliveryTimeout
	case "DOOPS_MONITOR_RETENTION_MIN_INTERVAL":
		return t.MonitorRetentionMinInterval
	case "DOOPS_PLATFORM_LOAD_SAMPLE_RETENTION":
		return t.PlatformLoadSampleRetention
	case "DOOPS_TARGET_QUEUE_TIMEOUT":
		return t.TargetQueueTimeout
	case "DOOPS_AUDIT_RETENTION_INTERVAL":
		return t.AuditRetentionInterval
	case "DOOPS_EXPIRED_TOKEN_SWEEP_INTERVAL":
		return t.ExpiredTokenSweepInterval
	case "DOOPS_OPERATION_TIMEOUT":
		return t.OperationTimeout
	case "DOOPS_DETACHED_TASK_TIMEOUT":
		return t.DetachedTaskTimeout
	case "DOOPS_TARGET_RECONNECT_GRACE":
		return t.TargetReconnectGrace
	case "DOOPS_SHUTDOWN_DRAIN_TIMEOUT":
		return t.ShutdownDrainTimeout
	case "DOOPS_MONITOR_TICK":
		return t.MonitorTick
	case "DOOPS_HTTP_READ_HEADER_TIMEOUT":
		return t.HTTPReadHeaderTimeout
	case "DOOPS_HTTP_IDLE_TIMEOUT":
		return t.HTTPIdleTimeout
	case "DOOPS_OFFLINE_REAPER_INTERVAL":
		return t.OfflineReaperInterval
	case "DOOPS_OFFLINE_INSTANCE_RETENTION":
		return t.OfflineInstanceRetention
	case "DOOPS_AUDIT_PURGE_BATCH_PAUSE":
		return t.AuditPurgeBatchPause
	case "DOOPS_EXECUTION_RESUME_GRACE":
		return t.ExecutionResumeGrace
	case "DOOPS_EXECUTION_RESUME_TTL":
		return t.ExecutionResumeTTL
	case "DOOPS_AGENT_EXECUTION_TTL":
		return t.AgentExecutionTTL
	case "DOOPS_HTTP_SHUTDOWN_TIMEOUT":
		return t.HTTPShutdownTimeout
	case "DOOPS_TUNNEL_READ_TIMEOUT":
		return t.TunnelReadTimeout
	case "DOOPS_EXECUTION_RESUME_WAIT_MAX":
		return t.ExecutionResumeWaitMax
	}
	return 0
}

func TestTimeoutDefaultsResolveValidValues(t *testing.T) {
	overrides := map[string]string{
		ToolTimeoutEnv:                         "40m",
		"DOOPS_COMPLETED_TASK_MAX_AGE":         "12h",
		"DOOPS_TOOL_DEADLINE_MARGIN":           "7s",
		"DOOPS_HELM_SETTLE_RESERVE":            "2m",
		"DOOPS_HELM_SETTLE_TIMEOUT":            "1m",
		"DOOPS_HELM_MAX_WAIT":                  "20m",
		"DOOPS_AGENT_LEASE":                    "2m",
		"DOOPS_AGENT_HEARTBEAT_FLUSH":          "20s",
		"DOOPS_EDGE_DIAL_TIMEOUT":              "9s",
		"DOOPS_MYSQL_CONNECT_TIMEOUT":          "10s",
		"DOOPS_MCP_PROBE_TIMEOUT":              "20s",
		"DOOPS_CREDENTIAL_DELIVERY_TIMEOUT":    "5m",
		"DOOPS_MONITOR_RETENTION_MIN_INTERVAL": "1h",
		"DOOPS_PLATFORM_LOAD_SAMPLE_RETENTION": "48h",
		"DOOPS_TARGET_QUEUE_TIMEOUT":           "4m",
		"DOOPS_AUDIT_RETENTION_INTERVAL":       "90m",
		"DOOPS_EXPIRED_TOKEN_SWEEP_INTERVAL":   "3h",
		"DOOPS_OPERATION_TIMEOUT":              "45m",
		"DOOPS_DETACHED_TASK_TIMEOUT":          "20h",
		"DOOPS_TARGET_RECONNECT_GRACE":         "7s",
		"DOOPS_SHUTDOWN_DRAIN_TIMEOUT":         "90s",
		"DOOPS_MONITOR_TICK":                   "20s",
		"DOOPS_HTTP_READ_HEADER_TIMEOUT":       "6s",
		"DOOPS_HTTP_IDLE_TIMEOUT":              "90s",
		"DOOPS_OFFLINE_REAPER_INTERVAL":        "2h",
		"DOOPS_OFFLINE_INSTANCE_RETENTION":     "720h",
		"DOOPS_AUDIT_PURGE_BATCH_PAUSE":        "250ms",
		"DOOPS_EXECUTION_RESUME_GRACE":         "90s",
		"DOOPS_EXECUTION_RESUME_TTL":           "2h",
		"DOOPS_AGENT_EXECUTION_TTL":            "90m",
		"DOOPS_HTTP_SHUTDOWN_TIMEOUT":          "30s",
		"DOOPS_TUNNEL_READ_TIMEOUT":            "4m",
		"DOOPS_EXECUTION_RESUME_WAIT_MAX":      "30s",
		"DOOPS_EDGE_WRITE_STALL":               "45s",
		"DOOPS_EDGE_CONN_ID_QUARANTINE":        "25s",
		"DOOPS_EDGE_HELLO_TIMEOUT":             "3s",
		"DOOPS_EDGE_WS_READ_TIMEOUT":           "12s",
	}
	specs := 0
	for _, spec := range timeoutSpecs() {
		if spec.Env != "" {
			specs++
		}
	}
	if len(overrides) != specs {
		t.Fatalf("this case covers %d variables but the table has %d; add the missing one", len(overrides), specs)
	}
	resolved, err := loadTimeouts(timeoutEnv(overrides))
	if err != nil {
		t.Fatalf("valid overrides rejected: %v", err)
	}
	for env, raw := range overrides {
		spec := timeoutSpecByEnv(t, env)
		want, err := time.ParseDuration(raw)
		if err != nil {
			t.Fatalf("%s: %v", env, err)
		}
		got := timeoutField(resolved, env)
		if got != want {
			t.Fatalf("%s=%q resolved to %s, want %s", env, raw, formatTimeoutValue(got), formatTimeoutValue(want))
		}
		if spec.Default == want {
			t.Fatalf("%s: the override equals the default, so this case proves nothing", env)
		}
	}
}

func TestTimeoutDefaultsRejectInvalidValues(t *testing.T) {
	covered := 0
	for _, spec := range timeoutSpecs() {
		if spec.Env == "" {
			continue
		}
		covered++
		cases := []struct{ name, raw string }{
			{"not a duration", "soon"},
			{"zero", "0s"},
			{"negative", "-5s"},
			{"above the maximum", (spec.Max * 2).String()},
		}
		for _, c := range cases {
			t.Run(spec.Env+" "+c.name, func(t *testing.T) {
				_, err := loadTimeouts(timeoutEnv(map[string]string{spec.Env: c.raw}))
				if err == nil {
					t.Fatalf("%s=%q was accepted; an invalid timeout must stop the process", spec.Env, c.raw)
				}
				text := err.Error()
				if !strings.Contains(text, "gateway_timeout_config_invalid: ") {
					t.Fatalf("error does not use the documented prefix: %v", err)
				}
				if !strings.Contains(text, spec.Env) {
					t.Fatalf("error does not name the variable: %v", err)
				}
				if !strings.Contains(text, strconv.Quote(c.raw)) {
					t.Fatalf("error does not quote the value %q: %v", c.raw, err)
				}
				if !strings.Contains(text, "allowed range") {
					t.Fatalf("error does not state the allowed range: %v", err)
				}
			})
		}
	}
	if covered == 0 {
		t.Fatal("no overridable timeout in the table; the guard is scanning nothing")
	}
}

func TestTimeoutDefaultsReportEveryBadValue(t *testing.T) {
	_, err := loadTimeouts(timeoutEnv(map[string]string{
		"DOOPS_EDGE_DIAL_TIMEOUT": "soon",
		"DOOPS_AGENT_LEASE":       "0s",
	}))
	if err == nil {
		t.Fatal("two invalid values were accepted")
	}
	for _, want := range []string{"DOOPS_EDGE_DIAL_TIMEOUT", "DOOPS_AGENT_LEASE"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error does not name %s: %v", want, err)
		}
	}
}

func TestTimeoutOrderRejectsBrokenChains(t *testing.T) {
	cases := []struct {
		name  string
		env   map[string]string
		wantA []string
	}{
		{
			name:  "settle timeout does not fit in the reserve",
			env:   map[string]string{"DOOPS_HELM_SETTLE_TIMEOUT": "1m10s"},
			wantA: []string{"DOOPS_HELM_SETTLE_RESERVE", "DOOPS_HELM_SETTLE_TIMEOUT"},
		},
		{
			name:  "reserve too small for grace plus settle",
			env:   map[string]string{"DOOPS_HELM_SETTLE_RESERVE": "1m10s"},
			wantA: []string{"DOOPS_HELM_SETTLE_RESERVE", "DOOPS_HELM_SETTLE_TIMEOUT"},
		},
		{
			name:  "max helm wait does not fit in the helm budget",
			env:   map[string]string{"DOOPS_HELM_MAX_WAIT": "30m"},
			wantA: []string{"DOOPS_HELM_MAX_WAIT", "DOOPS_HELM_SETTLE_RESERVE"},
		},
		{
			name:  "deadline margin is not smaller than the tool timeout",
			env:   map[string]string{"DOOPS_TOOL_DEADLINE_MARGIN": "45m"},
			wantA: []string{"DOOPS_TOOL_DEADLINE_MARGIN", "DOOPS_TOOL_TIMEOUT"},
		},
		{
			name:  "heartbeat flush outruns a third of the lease",
			env:   map[string]string{"DOOPS_AGENT_LEASE": "1s", "DOOPS_AGENT_HEARTBEAT_FLUSH": "1s"},
			wantA: []string{"DOOPS_AGENT_HEARTBEAT_FLUSH", "DOOPS_AGENT_LEASE"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadTimeouts(timeoutEnv(c.env))
			if err == nil {
				t.Fatalf("broken chain %v was accepted at startup", c.env)
			}
			for _, want := range c.wantA {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error does not name %s: %v", want, err)
				}
			}
		})
	}
}

// TestTimeoutOrderAcceptsTheShippedDefaults is the falsifiable other half: the
// values this build actually ships must pass the startup check.
func TestTimeoutOrderAcceptsTheShippedDefaults(t *testing.T) {
	resolved, err := loadTimeouts(timeoutEnv(nil))
	if err != nil {
		t.Fatalf("the shipped defaults fail the startup check: %v", err)
	}
	if resolved != defaultTimeouts() {
		t.Fatal("resolving an empty environment changed a default")
	}
}

// TestToolTimeoutKeepsBareSeconds keeps the documented compatibility: a bare
// number means seconds, a duration string means itself, and both read 30m.
func TestToolTimeoutKeepsBareSeconds(t *testing.T) {
	for _, raw := range []string{"1800", "30m"} {
		resolved, err := loadTimeouts(timeoutEnv(map[string]string{ToolTimeoutEnv: raw}))
		if err != nil {
			t.Fatalf("%s=%q rejected: %v", ToolTimeoutEnv, raw, err)
		}
		if resolved.ToolExecutionTimeout != 30*time.Minute {
			t.Fatalf("%s=%q resolved to %s, want 30m", ToolTimeoutEnv, raw, formatTimeoutValue(resolved.ToolExecutionTimeout))
		}
	}
	// INT-106 removed the per-call environment read: the getters now return the
	// startup snapshot. Setting the variable after start must change nothing, and a
	// typo can no longer fall back silently because mustLoadTimeouts refused it.
	withTimeoutField(t, func(t *timeoutDefaults) {
		t.ToolExecutionTimeout = 45 * time.Minute
		t.CompletedTaskMaxAge = 90 * time.Second
	})
	if got := maxToolExecutionDuration(); got != 45*time.Minute {
		t.Fatalf("maxToolExecutionDuration = %s, want the 45m table value", formatTimeoutValue(got))
	}
	if got := maxCompletedTaskAge(); got != 90*time.Second {
		t.Fatalf("maxCompletedTaskAge = %s, want the 90s table value", formatTimeoutValue(got))
	}
	t.Setenv(ToolTimeoutEnv, "1800")
	if got := maxToolExecutionDuration(); got != 45*time.Minute {
		t.Fatalf("setting %s after start moved the budget to %s; the getter must read the startup snapshot", ToolTimeoutEnv, formatTimeoutValue(got))
	}
}
