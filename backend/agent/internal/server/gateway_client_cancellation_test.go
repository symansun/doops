package server

import (
	"errors"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestGatewayClientCancellationIsConnectionScopedAndWaitsForTerminal(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	remote := f.agent(t, "client-cancellation")
	token := f.user(t, "executor", ScopeGrant{
		Cluster: "dev", Instance: "local",
	})
	client, other := f.client(t, token), f.client(t, token)
	lifecycleSend(t, client, 2, "doops_shell", "client-cancel", map[string]interface{}{"command": "sleep 60"})
	call := remote.call(t, "doops_shell", "client-cancel")

	writeGatewayJSON(t, other, map[string]interface{}{
		"jsonrpc": "2.0", "id": 3, "method": "tools/cancel", "params": map[string]interface{}{"id": 2},
	})
	if response := lifecycleResponse(t, other, 3, ""); response["result"].(map[string]interface{})["canceled"] != false {
		t.Fatalf("another connection canceled the request: %#v", response)
	}
	select {
	case id := <-remote.cancels:
		t.Fatalf("cross-connection cancellation reached Agent: %d", id)
	default:
	}

	writeGatewayJSON(t, client, map[string]interface{}{
		"jsonrpc": "2.0", "id": 3, "method": "tools/cancel", "params": map[string]interface{}{"id": 2},
	})
	if response := lifecycleResponse(t, client, 3, ""); response["result"].(map[string]interface{})["canceled"] != true {
		t.Fatalf("own cancellation was not acknowledged: %#v", response)
	}
	select {
	case id := <-remote.cancels:
		if id != call.id {
			t.Fatalf("canceled wrong Agent request: %d != %d", id, call.id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not reach Agent")
	}
	op := f.operation(t, "client-cancel", "running")
	if got := executionAuditTestEvent(t, f.store, op.AuditID); got.Status != "executing" {
		t.Fatalf("cancel ACK prematurely finalized execution: %#v", got)
	}
	remote.complete(t, call, "operation canceled")
	lifecycleResponse(t, client, 2, "execution_outcome_unknown")
	lifecycleSend(t, client, 4, "doops_shell", "client-cancel", map[string]interface{}{"command": "true"})
	remote.complete(t, remote.call(t, "doops_shell", "client-cancel"), "")
	lifecycleResponse(t, client, 4, "")
}

func TestGatewayClientCancellationStopsRealAgentProcess(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	agent := NewGateway()
	conn := dialFakeGatewayAgent(t, f.server.URL, "dev", "local")
	done := make(chan struct{})
	go func() {
		defer close(done)
		agent.ServeGatewayWebSocketConnWithReady(conn, "real-cancel-agent", nil)
	}()
	t.Cleanup(func() {
		_ = conn.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Agent cleanup did not finish")
		}
	})
	waitForGatewayAgent(t, f.hub, "dev", "local")
	client := f.client(t, f.admin)
	pidFile := filepath.Join(root, "child.pid")
	lifecycleSend(t, client, 2, "doops_shell", "real-client-cancel", map[string]interface{}{
		"command": cancellationTestResistantCommand(pidFile),
	})
	_, group := awaitCancellationTestPID(t, pidFile)
	writeGatewayJSON(t, client, map[string]interface{}{
		"jsonrpc": "2.0", "id": 3, "method": "tools/cancel", "params": map[string]interface{}{"id": 2},
	})
	response := lifecycleResponse(t, client, 3, "")
	if response["result"].(map[string]interface{})["canceled"] != true {
		t.Fatalf("real cancellation rejected: %#v", response)
	}
	lifecycleResponse(t, client, 2, "execution_outcome_unknown")
	if err := syscall.Kill(-group, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("terminal response arrived with process group %d still alive: %v", group, err)
	}
	lifecycleSend(t, client, 4, "doops_shell", "real-client-cancel", map[string]interface{}{
		"command": "printf 'AFTER_CANCEL_OK\\n'",
	})
	lifecycleResponse(t, client, 4, "")
}
