package server

// INT-82b: an Agent restart must not re-run an addressable execution.
//
// The Agent's claim table (agentExecutionStore) is process memory: after a
// restart it is empty, so a redelivery of the same execution_id looks like a
// brand-new execution and the command runs a second time. The Gateway record
// survives (execution_records), and the Agent identifies itself with a boot id,
// so the Gateway can tell "same process, reattach" from "different process,
// the execution is gone" and refuse instead of re-running.

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// executionBootQuery fetches the stored record through the HTTP read path.
func executionBootRecord(t *testing.T, f *executionLifecycleFixture, token CreatedToken, executionID string) executionResumeRecord {
	t.Helper()
	status, body := executionResumeQuery(t, f, token, executionID, 5000)
	if status != 200 {
		t.Fatalf("GET /v1/executions/%s = HTTP %d: %s", executionID, status, body)
	}
	return decodeExecutionResumeRecord(t, body)
}

// TestExecutionAgentRestartDoesNotRerunCommand simulates an Agent restart in the
// one way a test process can: the tunnel drops mid-execution and the Agent-side
// claim table is emptied, exactly what a new Agent process starts with. The
// Gateway then redelivers the same execution ID to the new Agent.
//
// The side-effect log is the evidence: one "run" line means the command really
// ran once; two mean the restart re-ran it.
func TestExecutionAgentRestartDoesNotRerunCommand(t *testing.T) {
	f, operator := executionResumeEnv(t, 20*time.Second)
	client := f.client(t, operator)
	marker := filepath.Join(t.TempDir(), "boot-marker")
	command := fmt.Sprintf("printf 'run\n' >> %q; sleep 4; printf 'done\n' >> %q", marker, marker)
	executionID := "exec-boot-restart"

	agent := newExecutionResumeE2EAgent(t, f)
	writeGatewayJSON(t, client, executionResumeShellCall(2, command, executionID))
	requireEventually(t, 15*time.Second, func() bool {
		return executionResumeMarkerCount(t, marker, "run") == 1
	})

	// Restart: the tunnel dies and the process memory that held the claim is
	// gone. The command itself keeps running in this process, which is the
	// worst case for the Gateway: the old run is genuinely still alive.
	agent.drop(t, f)
	// A new process: empty claim table and a new boot generation.
	agentExecutionStore = newAgentExecutionRegistry(defaultTimeouts().AgentExecutionTTL)
	agentBootID = newAgentBootID()
	restarted := newExecutionResumeE2EAgent(t, f)

	terminal := readExecutionTerminal(t, client, 2, 30*time.Second)
	if _, failed := terminal["transport_error"]; failed {
		t.Fatalf("client never got a bounded answer after the Agent restart: %v", terminal)
	}
	t.Logf("terminal after the restart: %v", terminal)

	record := executionBootRecord(t, f, operator, executionID)
	t.Logf("record after the restart: status=%s error=%s", record.Execution.Status, record.Execution.Error)

	if got := executionResumeMarkerCount(t, marker, "run"); got != 1 {
		t.Fatalf("the command ran %d times across the Agent restart, want exactly once", got)
	}
	if record.Execution.Status != executionStatusFailed {
		t.Fatalf("record status = %q, want failed: the Gateway cannot vouch for an execution whose Agent process is gone", record.Execution.Status)
	}
	if restarted == nil {
		t.Fatalf("restarted Agent is nil")
	}
}
