package server

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func withShortSlowProgressHeartbeat(t *testing.T, interval time.Duration) {
	t.Helper()
	previous := slowProgressHeartbeatInterval
	slowProgressHeartbeatInterval = interval
	t.Cleanup(func() {
		slowProgressHeartbeatInterval = previous
	})
}

func TestSlowProgressHeartbeatIntervalDefault(t *testing.T) {
	if slowProgressHeartbeatInterval != 5*time.Second {
		t.Fatalf("slowProgressHeartbeatInterval = %s, want 5s", slowProgressHeartbeatInterval)
	}
}

func TestSlowProgressHeartbeatEmitsProgressKindNotRaw(t *testing.T) {
	withShortSlowProgressHeartbeat(t, 15*time.Millisecond)

	doneCh := make(chan struct{})
	gotCh := make(chan notificationEvent, 4)
	go slowProgressHeartbeat(doneCh, func(evt notificationEvent) {
		select {
		case gotCh <- evt:
		default:
		}
	})

	var first notificationEvent
	select {
	case first = <-gotCh:
	case <-time.After(time.Second):
		close(doneCh)
		t.Fatal("slowProgressHeartbeat did not emit a progress event")
	}
	close(doneCh)

	if first.Kind != "progress" {
		t.Fatalf("slowProgressHeartbeat kind = %q, want %q (must stay separated from raw stream)", first.Kind, "progress")
	}
	if !strings.Contains(first.Data, "命令后台执行中") {
		t.Fatalf("slowProgressHeartbeat data = %q, want progress label", first.Data)
	}
}

func assertStreamedCommandSuccessEnvelope(t *testing.T, response map[string]interface{}) {
	t.Helper()
	if response["error"] != nil {
		t.Fatalf("expected success response, got RPC error: %#v", response)
	}
	result, ok := response["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing result map: %#v", response)
	}
	if result["isError"] == true {
		t.Fatalf("expected success result, got tool error: %#v", result)
	}
	if got := resultContentText(result); got != "Operation complete." {
		t.Fatalf("content[0].text = %q, want legacy-compatible %q", got, "Operation complete.")
	}
	structured, ok := result["structuredContent"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing structuredContent on streamed command success: %#v", result)
	}
	if structured["kind"] != commandResultStructuredKind {
		t.Fatalf("structuredContent.kind = %#v, want %q", structured["kind"], commandResultStructuredKind)
	}
	if structured["exitCode"] != float64(0) {
		t.Fatalf("structuredContent.exitCode = %#v, want 0", structured["exitCode"])
	}
	if structured["streamed"] != true {
		t.Fatalf("structuredContent.streamed = %#v, want true", structured["streamed"])
	}
	if !resultHasStreamedCommandContent(result) {
		t.Fatalf("resultHasStreamedCommandContent returned false for %#v", result)
	}
}

func TestStreamedCommandHandlersReturnStructuredCommandResult(t *testing.T) {
	t.Setenv("DOOPS_WORKSPACE_ROOT", t.TempDir())
	withShortSlowProgressHeartbeat(t, 20*time.Millisecond)

	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	t.Run("doops_shell emits progress heartbeat and structured streamed result", func(t *testing.T) {
		rawArgs, _ := json.Marshal(map[string]interface{}{
			"session_id": "proto-shell",
			"command":    "sleep 0.07; printf '{\"ok\":true}'",
		})
		if err := conn.WriteJSON(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      101,
			"method":  "tools/call",
			"params": map[string]interface{}{
				"name":      "doops_shell",
				"arguments": json.RawMessage(rawArgs),
			},
		}); err != nil {
			t.Fatalf("call doops_shell: %v", err)
		}

		sawProgress := false
		var rawData strings.Builder
		var terminal map[string]interface{}
		for {
			var msg map[string]interface{}
			if err := conn.ReadJSON(&msg); err != nil {
				t.Fatalf("read ws message: %v", err)
			}
			if method, _ := msg["method"].(string); method == "notifications/message" {
				params, _ := msg["params"].(map[string]interface{})
				kind, _ := params["kind"].(string)
				data, _ := params["data"].(string)
				switch kind {
				case "progress":
					sawProgress = true
				case "raw":
					rawData.WriteString(data)
				default:
					t.Fatalf("unexpected notification kind %q: %#v", kind, params)
				}
				continue
			}
			if msg["id"] == float64(101) {
				terminal = msg
				break
			}
		}
		if !sawProgress {
			t.Fatal("expected at least one kind=progress heartbeat notification")
		}
		if rawData.String() != `{"ok":true}` {
			t.Fatalf("raw stream contaminated: got %q, want %q", rawData.String(), `{"ok":true}`)
		}
		assertStreamedCommandSuccessEnvelope(t, terminal)
	})

	t.Run("doops_shell silent command returns structured streamed result", func(t *testing.T) {
		resp := callToolResult(t, conn, "doops_shell", map[string]interface{}{
			"session_id": "proto-shell-silent",
			"command":    "true",
		})
		assertStreamedCommandSuccessEnvelope(t, resp)
	})

	t.Run("doops_node_info default shell path returns structured streamed result", func(t *testing.T) {
		resp := callToolResult(t, conn, "doops_node_info", map[string]interface{}{
			"session_id": "proto-node-info",
		})
		assertStreamedCommandSuccessEnvelope(t, resp)
	})
}

func TestHandleToolSSHSuccessReturnsStructuredCommandResult(t *testing.T) {
	isolateSSHExitTestClient(t)
	options := startSSHExitTestServer(t, func(server *ssh.ServerConn, channel ssh.Channel, command string) {
		cmd := exec.Command("sh", "-c", command)
		cmd.Stdout, cmd.Stderr = channel, channel.Stderr()
		if err := cmd.Run(); err != nil {
			t.Errorf("loopback ssh command failed: %v", err)
			return
		}
		if _, err := channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0})); err != nil {
			t.Error(err)
		}
	})

	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	startSSHExitTestAgent(t, f)
	client := f.client(t, f.admin)
	lifecycleSend(t, client, 2, "doops_ssh", "ssh-structured-success", map[string]interface{}{
		"host":    "test@127.0.0.1",
		"options": options,
		"command": []string{"printf", `{"ssh":"ok"}`},
	})
	response := lifecycleResponse(t, client, 2, "")
	assertStreamedCommandSuccessEnvelope(t, response)
}

func TestRunInternalToolCallStreamedProtocolAndLegacyCompatibility(t *testing.T) {
	t.Run("real agent slow shell strips progress and placeholder via structuredContent", func(t *testing.T) {
		t.Setenv("DOOPS_WORKSPACE_ROOT", t.TempDir())
		withShortSlowProgressHeartbeat(t, 20*time.Millisecond)

		f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
		startSSHExitTestAgent(t, f)

		got, err := f.hub.RunInternalToolCall(context.Background(), "dev", "local", "doops_shell", map[string]interface{}{
			"session_id": "internal-streamed-json",
			"command":    `sleep 0.07; printf '{"status":"pass"}'`,
		})
		if err != nil {
			t.Fatalf("RunInternalToolCall failed: %v", err)
		}
		if got != `{"status":"pass"}` {
			t.Fatalf("RunInternalToolCall output = %q, want pure %q", got, `{"status":"pass"}`)
		}

		emptyOut, err := f.hub.RunInternalToolCall(context.Background(), "dev", "local", "doops_shell", map[string]interface{}{
			"session_id": "internal-streamed-empty",
			"command":    "true",
		})
		if err != nil {
			t.Fatalf("RunInternalToolCall silent failed: %v", err)
		}
		if emptyOut != "" {
			t.Fatalf("RunInternalToolCall silent output = %q, want empty", emptyOut)
		}
	})

	t.Run("fake agent verifies custom progress filtering and legacy fallback", func(t *testing.T) {
		f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
		agent := f.agent(t, "internal-compat-agent")

		// 1. Modern Agent: custom kind="progress" + raw chunk + structuredContent.streamed=true.
		type callRes struct {
			out string
			err error
		}
		resCh := make(chan callRes, 1)
		go func() {
			out, err := f.hub.RunInternalToolCall(context.Background(), "dev", "local", "doops_shell", map[string]interface{}{
				"session_id": "compat-modern",
				"command":    "modern",
			})
			resCh <- callRes{out, err}
		}()
		call := agent.call(t, "doops_shell", "compat-modern")
		var mu sync.Mutex
		send := func(v interface{}) {
			mu.Lock()
			defer mu.Unlock()
			if err := agent.conn.WriteJSON(v); err != nil {
				t.Errorf("write fake agent message: %v", err)
			}
		}
		send(map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "notifications/message",
			"params": map[string]interface{}{
				"request_id": call.id,
				"kind":       "progress",
				"data":       "[agent] custom non-regex heartbeat",
			},
		})
		send(map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "notifications/message",
			"params": map[string]interface{}{
				"request_id": call.id,
				"kind":       "raw",
				"data":       `{"artifact":"pure"}`,
			},
		})
		send(buildStreamedCommandSuccessResponse(call.id))
		got := <-resCh
		if got.err != nil {
			t.Fatalf("modern internal call failed: %v", got.err)
		}
		if got.out != `{"artifact":"pure"}` {
			t.Fatalf("modern internal call output = %q, want %q", got.out, `{"artifact":"pure"}`)
		}

		// 2. Legacy Agent: no structuredContent -> RunInternalToolCall still appends
		// "Operation complete." and stripShellCompletionSuffix strips it cleanly.
		go func() {
			out, err := f.hub.RunInternalToolCall(context.Background(), "dev", "local", "doops_shell", map[string]interface{}{
				"session_id": "compat-legacy",
				"command":    "legacy",
			})
			resCh <- callRes{out, err}
		}()
		legacyCall := agent.call(t, "doops_shell", "compat-legacy")
		send(map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "notifications/message",
			"params": map[string]interface{}{
				"request_id": legacyCall.id,
				"kind":       "raw",
				"data":       "{\"legacy\":true}\n",
			},
		})
		send(buildSuccessResponse(legacyCall.id, "Operation complete."))
		legacyGot := <-resCh
		if legacyGot.err != nil {
			t.Fatalf("legacy internal call failed: %v", legacyGot.err)
		}
		if !strings.HasSuffix(legacyGot.out, "Operation complete.") {
			t.Fatalf("expected legacy path to retain Operation complete. before stripping, got %q", legacyGot.out)
		}
		cleaned := stripShellCompletionSuffix(legacyGot.out)
		if cleaned != `{"legacy":true}` {
			t.Fatalf("stripShellCompletionSuffix(legacy) = %q, want %q", cleaned, `{"legacy":true}`)
		}
	})
}
