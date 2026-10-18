package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkspaceInterruptionMissingTerminalBlocksMutation(t *testing.T) {
	for _, tool := range []string{
		"doops_file_write", "doops_clean_workspace",
		"doops_workspace_begin", "doops_workspace_chunk", "doops_workspace_commit",
	} {
		for _, interruption := range []string{"disconnect", "cancel_disconnect", "explicit_unknown"} {
			t.Run(tool+"/"+interruption, func(t *testing.T) {
				f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
				agent := f.agent(t, "workspace-writer")
				client := f.client(t, f.admin)
				lifecycleSend(t, client, 2, tool, "workspace-writer", map[string]interface{}{
					"path": "config.txt", "content": "test", "workspace": "workspace-writer",
					"url": "https://example.invalid/repo.git", "commit": strings.Repeat("a", 64),
					"upload_id": "upl_" + strings.Repeat("a", 32), "seq": 0, "data_b64": "YQ==",
				})
				call := agent.call(t, tool, "workspace-writer")
				op := f.operation(t, "workspace-writer", "running")
				if interruption == "cancel_disconnect" {
					f.cancel(t, op.ID)
					select {
					case id := <-agent.cancels:
						if id != call.id {
							t.Fatalf("cancel id = %d, want %d", id, call.id)
						}
					case <-time.After(3 * time.Second):
						t.Fatal("cancel did not reach Agent")
					}
				}
				if interruption == "explicit_unknown" {
					agent.complete(t, call, errExecutionOutcomeUnknown.Error())
				} else {
					_ = agent.conn.Close()
				}
				lifecycleResponse(t, client, 2, errExecutionOutcomeUnknown.Error())
				requireEventually(t, 3*time.Second, func() bool { return len(f.hub.listActiveOperations()) == 0 })
				if event := executionAuditTestEvent(t, f.store, op.AuditID); event.Status != "outcome_unknown" {
					t.Fatalf("missing terminal audit = %#v", event)
				}
				replacement := f.agent(t, "replacement-workspace-writer")
				probe := f.client(t, f.admin)
				lifecycleSend(t, probe, 2, "doops_file_write", "different-workspace", map[string]interface{}{
					"path": "config.txt", "content": "recovered",
				})
				replacement.complete(t, replacement.call(t, "doops_file_write", "different-workspace"), "")
				lifecycleResponse(t, probe, 2, "")
				lifecycleSend(t, probe, 3, "doops_k8s", "diagnostics", map[string]interface{}{"operation": "GET", "kind": "pods"})
				replacement.complete(t, replacement.call(t, "doops_k8s", "diagnostics"), "")
				lifecycleResponse(t, probe, 3, "")
			})
		}
	}
}

func TestWorkspaceInterruptionReadOnlyDoesNotQuarantine(t *testing.T) {
	for _, tool := range []string{
		"doops_file_read", "doops_node_info", "doops_k8s", "doops_task_status",
		"doops_workspace_pull_begin", "doops_workspace_pull_chunk",
	} {
		t.Run(tool, func(t *testing.T) {
			f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
			agent := f.agent(t, "workspace-reader")
			client := f.client(t, f.admin)
			lifecycleSend(t, client, 2, tool, "workspace-reader", map[string]interface{}{
				"path": "config.txt", "operation": " GET ", "kind": "pods", "task_id": "task",
			})
			agent.call(t, tool, "workspace-reader")
			op := f.operation(t, "workspace-reader", "running")
			_ = agent.conn.Close()
			lifecycleResponse(t, client, 2, errGatewayAgentDisconnected.Error())
			if event := executionAuditTestEvent(t, f.store, op.AuditID); event.Status != "error" {
				t.Fatalf("read-only disconnect audit = %#v", event)
			}
			replacement := f.agent(t, "replacement-workspace-reader")
			lifecycleSend(t, client, 3, "doops_file_write", "after-query", map[string]interface{}{
				"path": "config.txt", "content": "allowed",
			})
			replacement.complete(t, replacement.call(t, "doops_file_write", "after-query"), "")
			lifecycleResponse(t, client, 3, "")
		})
	}
}

func TestWorkspaceInterruptionCancelAcceptsRealLocalTerminal(t *testing.T) {
	for _, tool := range []string{"doops_file_write", "doops_clean_workspace"} {
		t.Run(tool, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("DOOPS_WORKSPACE_ROOT", root)
			session := "local-terminal-" + randomHex(8)
			path := filepath.Join(root, session, "config.txt")
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
				t.Fatal(err)
			}
			f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
			agent := f.agent(t, "local-terminal-agent")
			client := f.client(t, f.admin)
			args := map[string]interface{}{"path": "config.txt", "content": "after", "workspace": session}
			lifecycleSend(t, client, 2, tool, session, args)
			call := agent.call(t, tool, session)
			op := f.operation(t, session, "running")
			f.cancel(t, op.ID)
			select {
			case <-agent.cancels:
			case <-time.After(3 * time.Second):
				t.Fatal("Agent did not receive cancellation")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			raw, err := json.Marshal(args)
			if err != nil {
				t.Fatal(err)
			}
			// Run the actual local handler after cancellation, not a fabricated success.
			(&Gateway{}).handleToolCallOverWS(ctx, call.id, tool, raw, func(v interface{}) {
				agent.writeMu.Lock()
				defer agent.writeMu.Unlock()
				if err := agent.conn.WriteJSON(v); err != nil {
					t.Error(err)
				}
			})
			if err := client.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			response := readUntilID(t, client, 2)
			if err := client.SetReadDeadline(time.Time{}); err != nil {
				t.Fatal(err)
			}
			if response["result"] == nil && !executionResponseCanceled(response) {
				t.Fatalf("trusted local terminal became an unrelated failure: %#v", response)
			}
			requireEventually(t, 3*time.Second, func() bool { return len(f.hub.listActiveOperations()) == 0 })
			if events, err := f.store.UnresolvedExecutions("dev", "local"); err != nil || len(events) != 0 {
				t.Fatalf("trusted local terminal left blockers: %#v, %v", events, err)
			}
			if tool == "doops_file_write" {
				if content, err := os.ReadFile(path); err != nil || string(content) != "after" {
					t.Fatalf("local write did not finish: %q, %v", content, err)
				}
			} else if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("local clean did not finish: %v", err)
			}
			lifecycleSend(t, client, 3, "doops_file_write", session, map[string]interface{}{
				"path": "config.txt", "content": "next",
			})
			agent.complete(t, agent.call(t, "doops_file_write", session), "")
			lifecycleResponse(t, client, 3, "")
		})
	}
}

func TestWorkspaceInterruptionCancelGraceExpiresWithoutTerminal(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	agent := f.agent(t, "unresponsive-workspace-writer")
	client := f.client(t, f.admin)
	lifecycleSend(t, client, 2, "doops_file_write", "cancel-grace", map[string]interface{}{
		"path": "config.txt", "content": "pending",
	})
	agent.call(t, "doops_file_write", "cancel-grace")
	op := f.operation(t, "cancel-grace", "running")
	f.cancel(t, op.ID)
	select {
	case <-agent.cancels:
	case <-time.After(3 * time.Second):
		t.Fatal("Agent did not receive cancellation")
	}
	// Keep Agent connected and acknowledge cancellation, but never send the
	// original terminal reply. Exercise the real grace timer without shortening it.
	if err := client.SetReadDeadline(time.Now().Add(executionCancellationTimeout + 3*time.Second)); err != nil {
		t.Fatal(err)
	}
	response := readUntilID(t, client, 2)
	if !executionResponseUnknown(response) {
		t.Fatalf("grace expiry lost mutation uncertainty: %#v", response)
	}
	if err := client.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	if event := executionAuditTestEvent(t, f.store, op.AuditID); event.Status != "outcome_unknown" {
		t.Fatalf("grace expiry audit = %#v", event)
	}
	replacement := f.agent(t, "after-cancel-grace")
	lifecycleSend(t, client, 3, "doops_file_write", "after-grace", map[string]interface{}{
		"path": "config.txt", "content": "recovered",
	})
	replacement.complete(t, replacement.call(t, "doops_file_write", "after-grace"), "")
	lifecycleResponse(t, client, 3, "")
	lifecycleSend(t, client, 4, "doops_node_info", "grace-query", map[string]interface{}{})
	replacement.complete(t, replacement.call(t, "doops_node_info", "grace-query"), "")
	lifecycleResponse(t, client, 4, "")
}

// Inject a kernel write timeout on a real accepted WS socket. Without a finite
// deadline the write remains blocked until cleanup, exposing the old hang.
type workspaceDeadlineConn struct {
	net.Conn
	block    atomic.Bool
	deadline atomic.Int64
	writes   chan time.Time
	closed   chan struct{}
	once     sync.Once
}

func (c *workspaceDeadlineConn) SetWriteDeadline(deadline time.Time) error {
	value := int64(0)
	if !deadline.IsZero() {
		value = deadline.UnixNano()
	}
	c.deadline.Store(value)
	return c.Conn.SetWriteDeadline(deadline)
}

func (c *workspaceDeadlineConn) Write(p []byte) (int, error) {
	if !c.block.Load() {
		return c.Conn.Write(p)
	}
	deadline := time.Time{}
	if value := c.deadline.Load(); value != 0 {
		deadline = time.Unix(0, value)
	}
	select {
	case c.writes <- deadline:
	default:
	}
	if deadline.IsZero() {
		<-c.closed
		return 0, net.ErrClosed
	}
	return 0, &net.OpError{Op: "write", Net: "tcp", Err: os.ErrDeadlineExceeded}
}

func (c *workspaceDeadlineConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

type workspaceDeadlineListener struct {
	net.Listener
	accepted chan *workspaceDeadlineConn
}

func (l *workspaceDeadlineListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	wrapped := &workspaceDeadlineConn{Conn: conn, writes: make(chan time.Time, 1), closed: make(chan struct{})}
	l.accepted <- wrapped
	return wrapped, nil
}

func TestWorkspaceInterruptionClientWriteDeadlineClosesConnection(t *testing.T) {
	for _, path := range []string{"writeJSON", "writeRaw"} {
		t.Run(path, func(t *testing.T) {
			f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
			agent := f.agent(t, "slow-client-agent")
			finished := make(chan struct{})
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(finished)
				f.hub.client.HandleClientRPC(w, r)
			}))
			listener := &workspaceDeadlineListener{Listener: server.Listener, accepted: make(chan *workspaceDeadlineConn, 1)}
			server.Listener = listener
			server.Start()
			t.Cleanup(server.Close)
			client := dialGatewayRPCClient(t, server.URL, f.admin.Plaintext, "dev", "local")
			t.Cleanup(func() { _ = client.Close() })
			wire := <-listener.accepted
			t.Cleanup(func() { _ = wire.Close() })
			lifecycleSend(t, client, 2, "doops_file_write", "slow-client", map[string]interface{}{
				"path": "config.txt", "content": "pending",
			})
			call := agent.call(t, "doops_file_write", "slow-client")
			op := f.operation(t, "slow-client", "running")
			wire.block.Store(true)
			if path == "writeJSON" {
				writeGatewayJSON(t, client, map[string]interface{}{"jsonrpc": "2.0", "id": 3, "method": "unknown"})
			} else if err := agent.write(map[string]interface{}{
				"jsonrpc": "2.0", "method": "notifications/message",
				"params": map[string]interface{}{"request_id": call.id, "data": "progress"},
			}); err != nil {
				t.Fatal(err)
			}
			select {
			case deadline := <-wire.writes:
				remaining := time.Until(deadline)
				if deadline.IsZero() || remaining <= 0 || remaining > agentWSDataWriteTimeout {
					t.Fatalf("%s did not install the existing bounded deadline: %v", path, deadline)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("client write was not attempted")
			}
			select {
			case <-wire.closed:
			case <-time.After(3 * time.Second):
				t.Fatal("timed-out client connection remained open")
			}
			select {
			case id := <-agent.cancels:
				if id != call.id {
					t.Fatalf("canceled %d, want %d", id, call.id)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("write timeout did not cancel the active operation")
			}
			if held := f.operation(t, "slow-client", "running"); held.ID != op.ID {
				t.Fatalf("cancel ACK released the original operation: %#v", held)
			}
			_ = agent.conn.Close() // ACK is not terminal evidence.
			select {
			case <-finished:
			case <-time.After(3 * time.Second):
				t.Fatal("RPC handler remained stuck after the write timeout and Agent disconnect")
			}
			if event := executionAuditTestEvent(t, f.store, op.AuditID); event.Status != "outcome_unknown" {
				t.Fatalf("write timeout lost mutation uncertainty: %#v", event)
			}
			replacement := f.agent(t, "after-slow-client")
			probe := f.client(t, f.admin)
			lifecycleSend(t, probe, 2, "doops_file_write", "after-timeout", map[string]interface{}{
				"path": "config.txt", "content": "recovered",
			})
			replacement.complete(t, replacement.call(t, "doops_file_write", "after-timeout"), "")
			lifecycleResponse(t, probe, 2, "")
		})
	}
}
