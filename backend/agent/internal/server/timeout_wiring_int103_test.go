package server

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// INT-103 moved the credential/execution resume windows, the Agent-side
// execution TTL, the HTTP shutdown budget and the remaining MySQL probe sites
// off their literals (and off the silent env readers). These cases pin the
// table as the only source and keep the MySQL sites honest about it.

// TestMySQLCharsetProbeTakesTheBudgetFromTheTable drives the charset probe
// (gateway_store_mysql_runtime.go:376) directly: a handshake that never arrives
// must end on the table budget, not on a 10s literal.
func TestMySQLCharsetProbeTakesTheBudgetFromTheTable(t *testing.T) {
	port := silentMySQLListener(t)
	withTimeoutField(t, func(d *timeoutDefaults) { d.MySQLConnectTimeout = 50 * time.Millisecond })
	cfg := gatewayMySQLConfig{Host: "127.0.0.1", Port: port, User: "probe", Password: "probe", Database: "probe"}
	driverCfg, err := cfg.driverConfig()
	if err != nil {
		t.Fatalf("driver config: %v", err)
	}
	start := time.Now()
	_, _, err = gatewayMySQLDatabaseCharset(context.Background(), driverCfg)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("a silent MySQL listener must not answer the charset probe")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("the charset probe ran %s: it ignored the 50ms table budget and still used the old 10s literal", elapsed)
	}
	t.Logf("charset probe with a 50ms table budget failed after %s: %v", elapsed, err)
}

// TestAuditScrubPingTakesTheBudgetFromTheTable drives audit_scrub.go:170 through
// the real entry point: the DSN points at a socket that never speaks, so the
// ping can only finish on its own deadline.
func TestAuditScrubPingTakesTheBudgetFromTheTable(t *testing.T) {
	port := silentMySQLListener(t)
	withTimeoutField(t, func(d *timeoutDefaults) { d.MySQLConnectTimeout = 50 * time.Millisecond })
	dsn := "root@tcp(" + net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))) + ")/probe"
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", dsn)
	opts := AuditScrubOptions{
		Rollback:        true,
		BackupPath:      filepath.Join(t.TempDir(), "preimage.jsonl"),
		ConfirmDatabase: "probe",
	}
	start := time.Now()
	code, err := RunAuditScrub(context.Background(), opts, io.Discard)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("a silent MySQL listener must not pass the audit scrub ping")
	}
	if code != AuditScrubExitFailure {
		t.Fatalf("exit code = %d, want the failure code %d (%v)", code, AuditScrubExitFailure, err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("the audit scrub ping ran %s: it ignored the 50ms table budget and still used the old 10s literal", elapsed)
	}
	t.Logf("audit scrub ping with a 50ms table budget failed after %s: %v", elapsed, err)
}

// TestResumeWindowsComeFromTheTable covers the three values that used to be read
// from the environment by executionResumeGraceFromEnv / executionResumeTTLFromEnv
// / durationFromEnv, and the HTTP shutdown budget.
func TestResumeWindowsComeFromTheTable(t *testing.T) {
	withTimeoutField(t, func(d *timeoutDefaults) {
		d.ExecutionResumeGrace = 7 * time.Second
		d.ExecutionResumeTTL = 11 * time.Minute
		d.AgentExecutionTTL = 13 * time.Minute
		d.HTTPShutdownTimeout = 3 * time.Second
	})
	if got := executionResumeGrace(); got != 7*time.Second {
		t.Fatalf("execution resume grace = %s, want the 7s table value", got)
	}
	if got := executionResumeTTL(); got != 11*time.Minute {
		t.Fatalf("execution resume ttl = %s, want the 11m table value", got)
	}
	if got := agentExecutionTTL(); got != 13*time.Minute {
		t.Fatalf("agent execution ttl = %s, want the 13m table value", got)
	}
	if got := HTTPShutdownTimeout(); got != 3*time.Second {
		t.Fatalf("HTTP shutdown budget = %s, want the 3s table value", got)
	}
	if got := newExecutionRegistry(0).ttl; got != 11*time.Minute {
		t.Fatalf("newExecutionRegistry(0) ttl = %s, want the 11m table value", got)
	}
	if got := newAgentExecutionRegistry(0).ttl; got != 13*time.Minute {
		t.Fatalf("newAgentExecutionRegistry(0) ttl = %s, want the 13m table value", got)
	}
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	hub := NewGatewayHub(store, GatewayHubOptions{})
	if got := hub.opts.ExecutionResumeGrace; got != 7*time.Second {
		t.Fatalf("hub resume grace = %s, want the 7s table value", got)
	}
	if got := hub.opts.ExecutionResumeTTL; got != 11*time.Minute {
		t.Fatalf("hub resume ttl = %s, want the 11m table value", got)
	}
}

// TestResumeAndShutdownBudgetsFallBackWhenTheTableIsZero keeps a zeroed table
// from becoming a zero-deadline context.
func TestResumeAndShutdownBudgetsFallBackWhenTheTableIsZero(t *testing.T) {
	withTimeoutField(t, func(d *timeoutDefaults) {
		d.ExecutionResumeGrace = 0
		d.ExecutionResumeTTL = 0
		d.AgentExecutionTTL = 0
		d.HTTPShutdownTimeout = 0
	})
	defaults := defaultTimeouts()
	if got := executionResumeGrace(); got != defaults.ExecutionResumeGrace {
		t.Fatalf("zeroed resume grace fell back to %s, want %s", got, defaults.ExecutionResumeGrace)
	}
	if got := executionResumeTTL(); got != defaults.ExecutionResumeTTL {
		t.Fatalf("zeroed resume ttl fell back to %s, want %s", got, defaults.ExecutionResumeTTL)
	}
	if got := agentExecutionTTL(); got != defaults.AgentExecutionTTL {
		t.Fatalf("zeroed agent ttl fell back to %s, want %s", got, defaults.AgentExecutionTTL)
	}
	if got := HTTPShutdownTimeout(); got != defaults.HTTPShutdownTimeout {
		t.Fatalf("zeroed shutdown budget fell back to %s, want %s", got, defaults.HTTPShutdownTimeout)
	}
}

// TestResumeBudgetsRejectInvalidEnvValues is the startup contract: a typo must
// stop the process with the shared prefix instead of silently pinning a default.
func TestResumeBudgetsRejectInvalidEnvValues(t *testing.T) {
	cases := []struct {
		name string
		env  string
		raw  string
	}{
		{"resume grace is not a duration", "DOOPS_EXECUTION_RESUME_GRACE", "soon"},
		{"resume ttl is negative", "DOOPS_EXECUTION_RESUME_TTL", "-5s"},
		{"agent execution ttl has a unit typo", "DOOPS_AGENT_EXECUTION_TTL", "60x"},
		{"http shutdown budget is zero", "DOOPS_HTTP_SHUTDOWN_TIMEOUT", "0s"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadTimeouts(func(name string) string {
				if name == c.env {
					return c.raw
				}
				return ""
			})
			if err == nil {
				t.Fatalf("%s=%q was accepted; an invalid timeout must stop the process", c.env, c.raw)
			}
			if !strings.Contains(err.Error(), "gateway_timeout_config_invalid: ") {
				t.Fatalf("error %q lacks the shared prefix", err.Error())
			}
			if !strings.Contains(err.Error(), c.env) {
				t.Fatalf("error %q does not name %s", err.Error(), c.env)
			}
		})
	}
}

// TestResumeGraceLongerThanItsTTLIsRejected is I9: waiting past the expiry of
// the record being waited on is a configuration error, not a tuning choice.
func TestResumeGraceLongerThanItsTTLIsRejected(t *testing.T) {
	_, err := loadTimeouts(func(name string) string {
		switch name {
		case "DOOPS_EXECUTION_RESUME_GRACE":
			return "8m"
		case "DOOPS_EXECUTION_RESUME_TTL":
			return "5m"
		}
		return ""
	})
	if err == nil {
		t.Fatal("an 8m resume grace above a 5m TTL was accepted")
	}
	for _, want := range []string{"gateway_timeout_config_invalid: ", "executionResumeGrace", "must not exceed executionResumeTTL"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q lacks %q", err.Error(), want)
		}
	}
}

// TestMySQLProbeSitesKeepTheTableBudget is the structural half for the two
// openers the silent-listener fixture cannot reach: they only ping after the
// charset probe succeeded, so a source scan is what keeps them on the table.
func TestMySQLProbeSitesKeepTheTableBudget(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source")
	}
	dir := filepath.Dir(file)
	sites := map[string]int{
		"gateway_store_mysql.go":         1,
		"gateway_store_mysql_runtime.go": 2,
		"gateway_mysql_copy_command.go":  1,
		"audit_scrub.go":                 1,
	}
	checked := 0
	for name, want := range sites {
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		lines := strings.Split(string(src), string(rune(10)))
		found := 0
		for i, line := range lines {
			if !strings.Contains(line, "PingContext(") && !strings.Contains(line, "QueryRowContext(probeCtx") {
				continue
			}
			found++
			budgeted := false
			for back := i - 1; back >= 0 && back >= i-4; back-- {
				if strings.Contains(lines[back], "mysqlPingTimeout()") {
					budgeted = true
					break
				}
			}
			if !budgeted {
				t.Errorf("%s:%d probes without mysqlPingTimeout(); the ping budget must come from the timeouts table", name, i+1)
			}
		}
		if found != want {
			t.Errorf("%s: found %d probe sites, the scan is pinned at %d", name, found, want)
		}
		checked += found
	}
	if checked < 5 {
		t.Fatalf("only %d probe sites were checked; the scan must cover all five", checked)
	}
}
