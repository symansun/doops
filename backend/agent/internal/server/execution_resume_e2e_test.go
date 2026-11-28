package server

// INT-72: 断线续接 (disconnect/resume) end-to-end acceptance.
//
// The ledger entry (docs/reviews/2026-11-22-remediation-status.md:241) asks for
// exactly this: a client starts an execution, the Agent tunnel drops, the Agent
// reconnects, and the Gateway can still return the execution's terminal state
// from its execution ID. The tests here use a real GatewayStore, a real
// httptest Gateway, and the real Agent runtime (ServeGatewayWebSocketConnWithReady)
// that cmd/agent drives, so a red result is a red result in production terms.
//
// The execution ID is a client-supplied resume key carried next to the tools/call
// arguments. GET /v1/executions/<id> reads the terminal state back; the wire
// contract is documented in backend/docs/DOOPS_GATEWAY_TUNNEL.md.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const (
	executionResumeCluster  = "dev"
	executionResumeInstance = "local"
)

// executionResumeEnv boots the real store + Gateway with enrollment required, an
// operator granted on dev/local, and no Agent yet. The resume grace comes from
// the timeout table, which is a startup snapshot: the case installs its own
// snapshot instead of mutating the environment after it was resolved.
func executionResumeEnv(t *testing.T, grace time.Duration) (*executionLifecycleFixture, CreatedToken) {
	t.Helper()
	// Both roots are set: an unset workspace root makes the Agent write to
	// /root/... and fail with skill_sync_failed on a developer machine.
	t.Setenv("DOOPS_AGENT_SKILLS_ROOT", t.TempDir())
	t.Setenv("DOOPS_WORKSPACE_ROOT", t.TempDir())
	// A local runner has no AI endpoint: a silent fallback must fail here.
	t.Setenv("DO_AGENT_URL", "http://127.0.0.1:1")
	withTimeoutField(t, func(d *timeoutDefaults) {
		d.ExecutionResumeGrace = grace
		if d.ExecutionResumeTTL < grace {
			d.ExecutionResumeTTL = grace
		}
	})
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{RequireAgentEnrollment: true})
	f.httpTimeout = 120 * time.Second
	operator := f.user(t, "resume-e2e-op", ScopeGrant{Cluster: executionResumeCluster, Instance: executionResumeInstance})
	return f, operator
}

// executionResumeWindow is the tunnel downtime the acceptance case exercises.
// The ledger names 30s; candidate runs may shorten it through -short or
// DOOPS_TEST_RESUME_WINDOW so the package stays usable in a quick loop.
func executionResumeWindow(t *testing.T) time.Duration {
	t.Helper()
	if testing.Short() {
		return 2 * time.Second
	}
	if raw := strings.TrimSpace(os.Getenv("DOOPS_TEST_RESUME_WINDOW")); raw != "" {
		window, err := time.ParseDuration(raw)
		if err != nil || window <= 0 {
			t.Fatalf("DOOPS_TEST_RESUME_WINDOW=%q is not a positive duration", raw)
		}
		return window
	}
	return 30 * time.Second
}

// executionResumeE2EAgent is one Agent process (one *Gateway) whose tunnel socket
// can be dropped and re-dialled. Reconnecting the same *Gateway is what the
// production reconnect loop does; a new *Gateway would be a process restart.
type executionResumeE2EAgent struct {
	gw   *Gateway
	conn *websocket.Conn
	done chan struct{}
}

func newExecutionResumeE2EAgent(t *testing.T, f *executionLifecycleFixture) *executionResumeE2EAgent {
	t.Helper()
	agent := &executionResumeE2EAgent{gw: NewGateway()}
	agent.connect(t, f)
	t.Cleanup(func() { agent.disconnect(t) })
	return agent
}

func (a *executionResumeE2EAgent) connect(t *testing.T, f *executionLifecycleFixture) {
	t.Helper()
	conn := dialEnrolledTestAgent(t, f, executionResumeCluster, executionResumeInstance)
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.gw.ServeGatewayWebSocketConnWithReady(conn, "resume-e2e", nil)
	}()
	a.conn, a.done = conn, done
	waitForGatewayAgent(t, f.hub, executionResumeCluster, executionResumeInstance)
}

func (a *executionResumeE2EAgent) disconnect(t *testing.T) {
	t.Helper()
	if a.conn == nil {
		return
	}
	_ = a.conn.Close()
	select {
	case <-a.done:
	case <-time.After(10 * time.Second):
		t.Fatalf("Agent runtime did not stop after the tunnel socket closed")
	}
	a.conn, a.done = nil, nil
}

// drop closes the tunnel socket and waits until the Gateway no longer holds the
// Agent, so the reconnect that follows is a real reconnect and not a no-op.
func (a *executionResumeE2EAgent) drop(t *testing.T, f *executionLifecycleFixture) {
	t.Helper()
	if a.conn == nil {
		t.Fatalf("drop called on a closed Agent tunnel")
	}
	_ = a.conn.Close()
	select {
	case <-a.done:
	case <-time.After(10 * time.Second):
		t.Fatalf("Agent runtime did not notice the dropped tunnel")
	}
	a.conn, a.done = nil, nil
	requireEventually(t, 10*time.Second, func() bool {
		return f.hub.getAgent(executionResumeCluster, executionResumeInstance) == nil
	})
}

func executionResumeShellCall(id int, command, executionID string) map[string]interface{} {
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "doops_shell",
			"arguments": map[string]interface{}{
				"session_id": "resume-e2e",
				"command":    command,
			},
			"execution_id": executionID,
		},
	}
}

func readExecutionTerminal(t *testing.T, conn *websocket.Conn, id int, timeout time.Duration) map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		_ = conn.SetReadDeadline(deadline)
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			return map[string]interface{}{"transport_error": err.Error()}
		}
		if got, ok := msg["id"]; ok && fmt.Sprint(got) == fmt.Sprint(id) {
			return msg
		}
	}
}

func executionTerminalSucceeded(msg map[string]interface{}) (bool, string) {
	if errObj, ok := msg["error"]; ok {
		return false, fmt.Sprintf("JSON-RPC error %v", errObj)
	}
	result, ok := msg["result"].(map[string]interface{})
	if !ok {
		return false, fmt.Sprintf("terminal carries no result: %v", msg)
	}
	if isErr, ok := result["isError"]; ok && fmt.Sprint(isErr) == "true" {
		return false, fmt.Sprintf("tool result is an error: %v", result)
	}
	return true, ""
}

func executionResumeQuery(t *testing.T, f *executionLifecycleFixture, token CreatedToken, executionID string, waitMS int) (int, []byte) {
	t.Helper()
	path := "/v1/executions/" + url.PathEscape(executionID)
	if waitMS > 0 {
		path += fmt.Sprintf("?wait_ms=%d", waitMS)
	}
	req, err := http.NewRequest(http.MethodGet, f.server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token.Plaintext)
	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("GET %s: read body: %v", path, err)
	}
	return resp.StatusCode, body
}

type executionResumeRecord struct {
	Execution struct {
		ExecutionID string          `json:"execution_id"`
		Status      string          `json:"status"`
		Error       string          `json:"error"`
		Result      json.RawMessage `json:"result"`
	} `json:"execution"`
}

func decodeExecutionResumeRecord(t *testing.T, body []byte) executionResumeRecord {
	t.Helper()
	var record executionResumeRecord
	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatalf("decode execution record: %v; %s", err, body)
	}
	return record
}

// executionResumeMarkerCount counts the lines a side-effect log got, which is how
// these tests prove "the command ran exactly once" without trusting the reply.
func executionResumeMarkerCount(t *testing.T, path, want string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read side-effect log %s: %v", path, err)
	}
	count := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == want {
			count++
		}
	}
	return count
}

// TestExecutionResumeAfterTunnelDrop is the ledger's acceptance case: the Agent
// tunnel is down for the whole window, the command keeps running, the Agent
// reconnects, and the same execution ID returns the terminal state.
func TestExecutionResumeAfterTunnelDrop(t *testing.T) {
	window := executionResumeWindow(t)
	f, operator := executionResumeEnv(t, window+20*time.Second)
	agent := newExecutionResumeE2EAgent(t, f)
	conn := f.client(t, operator)

	marker := filepath.Join(t.TempDir(), "side-effects.log")
	command := fmt.Sprintf("printf 'run\\n' >> %q; sleep %d; printf 'done\\n' >> %q",
		marker, int(window.Seconds())+4, marker)
	executionID := "exec-resume-drop"

	writeGatewayJSON(t, conn, executionResumeShellCall(2, command, executionID))
	requireEventually(t, 20*time.Second, func() bool { return executionResumeMarkerCount(t, marker, "run") == 1 })

	agent.drop(t, f)
	t.Logf("tunnel down for %s of a %s execution", window, window+4*time.Second)
	time.Sleep(window)
	agent.connect(t, f)

	msg := readExecutionTerminal(t, conn, 2, window+120*time.Second)
	if ok, why := executionTerminalSucceeded(msg); !ok {
		t.Errorf("INT-72 red: a tunnel drop still ends the execution for the client: %s", why)
	}
	status, body := executionResumeQuery(t, f, operator, executionID, 10000)
	if status != http.StatusOK {
		t.Errorf("INT-72 red: GET /v1/executions/%s after the reconnect = HTTP %d: %s", executionID, status, body)
	} else {
		record := decodeExecutionResumeRecord(t, body)
		if record.Execution.Status != "succeeded" {
			t.Errorf("execution %s status = %q, want succeeded: %s", executionID, record.Execution.Status, body)
		}
		if len(record.Execution.Result) == 0 || string(record.Execution.Result) == "null" {
			t.Errorf("execution %s carries no terminal result: %s", executionID, body)
		}
	}
	if runs := executionResumeMarkerCount(t, marker, "run"); runs != 1 {
		t.Errorf("the command started %d times, want exactly once", runs)
	}
	if dones := executionResumeMarkerCount(t, marker, "done"); dones != 1 {
		t.Errorf("the command finished %d times, want exactly once", dones)
	}
}

// TestExecutionResumeWithoutReconnectFailsBounded proves the new wait has an
// upper bound: no reconnect means a definite "the result is not recoverable"
// answer, never a query that hangs.
func TestExecutionResumeWithoutReconnectFailsBounded(t *testing.T) {
	grace := 3 * time.Second
	f, operator := executionResumeEnv(t, grace)
	agent := newExecutionResumeE2EAgent(t, f)
	conn := f.client(t, operator)

	marker := filepath.Join(t.TempDir(), "side-effects.log")
	command := fmt.Sprintf("printf 'run\\n' >> %q; sleep 30; printf 'done\\n' >> %q", marker, marker)
	executionID := "exec-resume-gone"

	writeGatewayJSON(t, conn, executionResumeShellCall(2, command, executionID))
	requireEventually(t, 20*time.Second, func() bool { return executionResumeMarkerCount(t, marker, "run") == 1 })

	agent.drop(t, f)
	started := time.Now()
	msg := readExecutionTerminal(t, conn, 2, 45*time.Second)
	elapsed := time.Since(started)
	if elapsed > 30*time.Second {
		t.Errorf("the client waited %s for a terminal answer, want a bounded failure near the %s grace", elapsed, grace)
	}
	if _, ok := msg["error"]; !ok {
		t.Errorf("want an explicit failure after the grace, got %v", msg)
	}

	status, body := executionResumeQuery(t, f, operator, executionID, 5000)
	if status != http.StatusOK {
		t.Fatalf("GET /v1/executions/%s = HTTP %d: %s", executionID, status, body)
	}
	record := decodeExecutionResumeRecord(t, body)
	if record.Execution.Status != "failed" {
		t.Errorf("execution %s status = %q, want failed (result unavailable)", executionID, record.Execution.Status)
	}
	if !strings.Contains(record.Execution.Error, "execution_result_unavailable") {
		t.Errorf("execution %s error = %q, want the stable execution_result_unavailable category", executionID, record.Execution.Error)
	}
}

// TestExecutionResumeCompletedBeforeReconnect: the execution finishes while the
// tunnel is down. The reconnect must return that stored terminal state and must
// not start the command a second time.
func TestExecutionResumeCompletedBeforeReconnect(t *testing.T) {
	f, operator := executionResumeEnv(t, 30*time.Second)
	agent := newExecutionResumeE2EAgent(t, f)
	conn := f.client(t, operator)

	marker := filepath.Join(t.TempDir(), "side-effects.log")
	command := fmt.Sprintf("printf 'run\\n' >> %q; sleep 1; printf 'done\\n' >> %q", marker, marker)
	executionID := "exec-resume-finished"

	writeGatewayJSON(t, conn, executionResumeShellCall(2, command, executionID))
	requireEventually(t, 20*time.Second, func() bool { return executionResumeMarkerCount(t, marker, "run") == 1 })
	time.Sleep(200 * time.Millisecond)

	agent.drop(t, f)
	requireEventually(t, 20*time.Second, func() bool { return executionResumeMarkerCount(t, marker, "done") == 1 })
	agent.connect(t, f)

	msg := readExecutionTerminal(t, conn, 2, 60*time.Second)
	if ok, why := executionTerminalSucceeded(msg); !ok {
		t.Errorf("a finished execution did not come back after the reconnect: %s", why)
	}
	if runs := executionResumeMarkerCount(t, marker, "run"); runs != 1 {
		t.Errorf("the command ran %d times across the reconnect, want exactly once", runs)
	}

	status, body := executionResumeQuery(t, f, operator, executionID, 5000)
	if status != http.StatusOK {
		t.Fatalf("GET /v1/executions/%s = HTTP %d: %s", executionID, status, body)
	}
	record := decodeExecutionResumeRecord(t, body)
	if record.Execution.Status != "succeeded" {
		t.Errorf("execution %s status = %q, want succeeded: %s", executionID, record.Execution.Status, body)
	}
}

// TestExecutionResumeIsIdempotent: repeated reads return the same terminal
// state, and re-sending the same execution ID with different arguments resumes
// the stored execution instead of running something new.
func TestExecutionResumeIsIdempotent(t *testing.T) {
	f, operator := executionResumeEnv(t, 10*time.Second)
	_ = newExecutionResumeE2EAgent(t, f)
	conn := f.client(t, operator)

	marker := filepath.Join(t.TempDir(), "side-effects.log")
	command := fmt.Sprintf("printf 'run\\n' >> %q; printf 'first\\n' >> %q", marker, marker)
	executionID := "exec-resume-idempotent"

	writeGatewayJSON(t, conn, executionResumeShellCall(2, command, executionID))
	first := readExecutionTerminal(t, conn, 2, 30*time.Second)
	if ok, why := executionTerminalSucceeded(first); !ok {
		t.Fatalf("baseline execution failed: %s", why)
	}

	statusA, bodyA := executionResumeQuery(t, f, operator, executionID, 0)
	statusB, bodyB := executionResumeQuery(t, f, operator, executionID, 0)
	if statusA != http.StatusOK || statusB != http.StatusOK {
		t.Fatalf("repeated GET /v1/executions/%s = %d and %d: %s / %s", executionID, statusA, statusB, bodyA, bodyB)
	}
	if string(bodyA) != string(bodyB) {
		t.Errorf("repeated reads differ:\n%s\n%s", bodyA, bodyB)
	}

	replay := fmt.Sprintf("printf 'second\\n' >> %q", marker)
	writeGatewayJSON(t, conn, executionResumeShellCall(3, replay, executionID))
	second := readExecutionTerminal(t, conn, 3, 30*time.Second)
	if ok, why := executionTerminalSucceeded(second); !ok {
		t.Fatalf("replayed execution failed: %s", why)
	}
	if runs := executionResumeMarkerCount(t, marker, "run"); runs != 1 {
		t.Errorf("the original command ran %d times, want exactly once", runs)
	}
	if extra := executionResumeMarkerCount(t, marker, "second"); extra != 0 {
		t.Errorf("the replayed execution ran its new command %d times; an execution ID must resume, not re-execute", extra)
	}
}
