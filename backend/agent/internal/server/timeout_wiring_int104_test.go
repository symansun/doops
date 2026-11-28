package server

// INT-104 / INT-105 guards: the Agent tunnel read deadline and the blocking
// ?wait_ms ceiling are table rows, not per-call environment reads and not a
// private literal. Each case drives the same function the runtime calls, so a
// call site that keeps its old value turns red instead of passing on a document
// that merely mentions the variable.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestTunnelReadTimeoutComesFromTheTable(t *testing.T) {
	if got, want := agentTunnelReadTimeout(), defaultTimeouts().TunnelReadTimeout; got != want {
		t.Fatalf("unwired tunnel read timeout = %s, want the table default %s", formatTimeoutValue(got), formatTimeoutValue(want))
	}

	withTimeoutField(t, func(td *timeoutDefaults) { td.TunnelReadTimeout = 3 * time.Second })
	if got := agentTunnelReadTimeout(); got != 3*time.Second {
		t.Fatalf("tunnel read timeout = %s, want the 3s table value", formatTimeoutValue(got))
	}

	// The operator path: DOOPS_TUNNEL_READ_TIMEOUT=3s reaches the table and the
	// call site keeps the bare-number-is-seconds compatibility it always had.
	resolved, err := loadTimeouts(timeoutEnv(map[string]string{
		"DOOPS_TUNNEL_READ_TIMEOUT":   "3s",
		"DOOPS_AGENT_LEASE":           "9s", // ping period 3s, so a 3s deadline is legal
		"DOOPS_AGENT_HEARTBEAT_FLUSH": "1s",
	}))
	if err != nil {
		t.Fatalf("3s was rejected: %v", err)
	}
	if resolved.TunnelReadTimeout != 3*time.Second {
		t.Fatalf("resolved tunnel read timeout = %s, want 3s", formatTimeoutValue(resolved.TunnelReadTimeout))
	}
	bare, err := loadTimeouts(timeoutEnv(map[string]string{"DOOPS_TUNNEL_READ_TIMEOUT": "45"}))
	if err != nil {
		t.Fatalf("bare seconds were rejected: %v", err)
	}
	if bare.TunnelReadTimeout != 45*time.Second {
		t.Fatalf("bare 45 resolved to %s, want 45s", formatTimeoutValue(bare.TunnelReadTimeout))
	}
}

func TestExecutionResumeWaitBudgetComesFromTheTable(t *testing.T) {
	// Default: a ?wait_ms=5000 request may block the full five seconds.
	if got := executionWaitBudget("5000"); got != 5*time.Second {
		t.Fatalf("wait budget = %s, want the 5s table default", formatTimeoutValue(got))
	}
	// Shorter requests are never extended.
	if got := executionWaitBudget("1200"); got != 1200*time.Millisecond {
		t.Fatalf("wait budget = %s, want 1.2s", formatTimeoutValue(got))
	}
	for _, raw := range []string{"", "nope", "-1"} {
		if got := executionWaitBudget(raw); got != 0 {
			t.Fatalf("%q produced a %s budget, want no blocking", raw, formatTimeoutValue(got))
		}
	}

	withTimeoutField(t, func(td *timeoutDefaults) { td.ExecutionResumeWaitMax = 50 * time.Millisecond })
	if got := executionWaitBudget("5000"); got != 50*time.Millisecond {
		t.Fatalf("wait budget = %s, want the 50ms table ceiling", formatTimeoutValue(got))
	}
	if got := executionWaitBudget("20"); got != 20*time.Millisecond {
		t.Fatalf("wait budget = %s, want the requested 20ms below the ceiling", formatTimeoutValue(got))
	}
}

func TestTunnelAndWaitMaxInvalidValuesStopStartup(t *testing.T) {
	cases := []struct{ name, env, raw string }{
		{"tunnel read garbage", "DOOPS_TUNNEL_READ_TIMEOUT", "5q"},
		{"tunnel read zero", "DOOPS_TUNNEL_READ_TIMEOUT", "0s"},
		{"wait max garbage", "DOOPS_EXECUTION_RESUME_WAIT_MAX", "soon"},
		{"wait max above range", "DOOPS_EXECUTION_RESUME_WAIT_MAX", "1h"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadTimeouts(timeoutEnv(map[string]string{c.env: c.raw}))
			if err == nil {
				t.Fatalf("%s=%q was accepted; the old reader fell back to the default here, which is exactly the bug", c.env, c.raw)
			}
			text := err.Error()
			for _, want := range []string{"gateway_timeout_config_invalid: ", c.env, "allowed range"} {
				if !strings.Contains(text, want) {
					t.Fatalf("startup error does not mention %q: %v", want, err)
				}
			}
		})
	}
}

func TestTunnelAndWaitMaxOrderingIsChecked(t *testing.T) {
	// A read deadline shorter than the ping period (a third of the lease) would
	// cut a healthy tunnel between two pings.
	_, err := loadTimeouts(timeoutEnv(map[string]string{"DOOPS_TUNNEL_READ_TIMEOUT": "2s"}))
	if err == nil {
		t.Fatal("a 2s tunnel read deadline under the 30s ping period was accepted")
	}
	for _, want := range []string{"tunnelReadTimeout", "DOOPS_AGENT_LEASE"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error does not name %q: %v", want, err)
		}
	}

	// One HTTP wait longer than the window the record keeps waiting is pointless.
	_, err = loadTimeouts(timeoutEnv(map[string]string{
		"DOOPS_EXECUTION_RESUME_WAIT_MAX": "2m",
		"DOOPS_EXECUTION_RESUME_GRACE":    "1m",
	}))
	if err == nil {
		t.Fatal("a 2m wait ceiling over a 1m grace was accepted")
	}
	for _, want := range []string{"executionResumeWaitMax", "DOOPS_EXECUTION_RESUME_GRACE"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error does not name %q: %v", want, err)
		}
	}
}

// TestExecutionResumeHandlerUsesTheBudgetHelper pins the only call site: the
// helper above is worthless if HandleExecution still clamps with a literal.
func TestExecutionResumeHandlerUsesTheBudgetHelper(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source file")
	}
	path := filepath.Join(filepath.Dir(thisFile), "execution_resume.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read execution_resume.go: %v", err)
	}
	src := string(data)
	want := "executionWaitBudget(r.URL.Query().Get(\"wait_ms\"))"
	if !strings.Contains(src, want) {
		t.Fatalf("execution_resume.go no longer calls %q; the ?wait_ms ceiling would bypass the table", want)
	}
	if strings.Contains(src, "executionResumeWaitMax") {
		t.Fatal("execution_resume.go still names executionResumeWaitMax; the retired constant is back")
	}
}
