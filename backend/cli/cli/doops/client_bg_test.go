package main

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func agentInitializeResult() map[string]interface{} {
	return map[string]interface{}{
		"capabilities": map[string]interface{}{
			"protocols": map[string]interface{}{
				"executionLifecycle": executionLifecycleCapability(),
			},
		},
	}
}

func TestExecBgRejectsInvalidLifecycleWithoutGatingForegroundCalls(t *testing.T) {
	incomplete := executionLifecycleCapability()
	delete(incomplete, "promptCancellation")
	disabled := executionLifecycleCapability()
	disabled["backgroundLifetime"] = false
	for _, tc := range []struct {
		name       string
		capability interface{}
	}{
		{"missing", nil},
		{"false", false},
		{"malformed", "doops.sh/execution-lifecycle/v1"},
		{"incomplete", incomplete},
		{"disabled", disabled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls, connections atomic.Int32
			upgrader := websocket.Upgrader{}
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				connections.Add(1)
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Errorf("upgrade: %v", err)
					return
				}
				defer conn.Close()
				for {
					var req map[string]interface{}
					if err := conn.ReadJSON(&req); err != nil {
						return
					}
					result := map[string]interface{}{
						"content": []map[string]string{{"type": "text", "text": "foreground complete"}},
					}
					if req["method"] == "initialize" {
						protocols := map[string]interface{}{}
						if tc.capability != nil {
							protocols["executionLifecycle"] = tc.capability
						}
						result = map[string]interface{}{
							"capabilities": map[string]interface{}{"protocols": protocols},
						}
					} else {
						calls.Add(1)
						params, _ := req["params"].(map[string]interface{})
						if req["method"] != "tools/call" || params["name"] == "doops_bg" {
							t.Errorf("unexpected tool request: %#v", req)
						}
					}
					_ = conn.WriteJSON(map[string]interface{}{
						"jsonrpc": "2.0", "id": req["id"], "result": result,
					})
				}
			}))
			defer gateway.Close()
			client := newGatewayTestClient(t, gateway.URL, "lifecycle-gate")
			defer client.Close()
			err := client.ExecBg("sleep 1")
			if err == nil || !strings.Contains(err.Error(), `does not advertise protocol "executionLifecycle"`) {
				t.Fatalf("invalid lifecycle capability was not rejected: %v", err)
			}
			if calls.Load() != 0 {
				t.Fatalf("rejected background task sent %d tools/call requests", calls.Load())
			}
			for _, tool := range []string{"doops_shell", "doops_ssh", "doops_agent_prompt"} {
				if err := client.Call(tool, map[string]interface{}{}); err != nil {
					t.Fatalf("foreground %s must not require executionLifecycle: %v", tool, err)
				}
			}
			if calls.Load() != 3 || connections.Load() != 1 {
				t.Fatalf("unexpected foreground calls or retries: calls=%d connections=%d", calls.Load(), connections.Load())
			}
		})
	}
}

func TestForegroundCLIExecWorksWithoutLifecycleCapability(t *testing.T) {
	var calls atomic.Int32
	upgrader := websocket.Upgrader{}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		for {
			var req map[string]interface{}
			if err := conn.ReadJSON(&req); err != nil {
				return
			}
			switch req["method"] {
			case "initialize":
				_ = conn.WriteJSON(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result": map[string]interface{}{
						"capabilities": map[string]interface{}{
							"protocols": map[string]interface{}{},
						},
					},
				})
			case "tools/call":
				calls.Add(1)
				params, _ := req["params"].(map[string]interface{})
				args, _ := params["arguments"].(map[string]interface{})
				if params["name"] != "doops_shell" ||
					args["session_id"] != "foreground-no-lifecycle" ||
					args["command"] != "printf foreground-ok" {
					t.Errorf("unexpected foreground CLI call: %#v", req)
				}
				_ = conn.WriteJSON(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result": map[string]interface{}{
						"content": []map[string]string{{"type": "text", "text": "foreground-ok"}},
					},
				})
			default:
				t.Errorf("unexpected RPC method: %#v", req["method"])
			}
		}
	}))
	defer gateway.Close()

	root := t.TempDir()
	t.Setenv("DOOPS_CONFIG", filepath.Join(root, "config.json"))
	if err := saveServers([]Server{{
		Name: "test", Gateway: gateway.URL, Cluster: "prod", Instance: "node",
	}}); err != nil {
		t.Fatal(err)
	}
	auth := &AuthStore{}
	auth.SetGateway(gateway.URL, "test-token")
	if err := auth.Save(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, buildDoopsCLIBinary(t),
		"-session", "foreground-no-lifecycle",
		"exec", "--target", "test", "--cmd", "printf foreground-ok",
	)
	command.Env = append(os.Environ(), "DOOPS_CONFIG="+filepath.Join(root, "config.json"))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("foreground CLI exec failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "foreground-ok") {
		t.Fatalf("foreground CLI output missing result: %s", output)
	}
	if calls.Load() != 1 {
		t.Fatalf("foreground CLI dispatched %d calls, want 1", calls.Load())
	}
}

func TestExecBgCLIStreamsOneRPCUntilStopped(t *testing.T) {
	stopped := make(chan struct{})
	var stopOnce sync.Once
	release := func() { stopOnce.Do(func() { close(stopped) }) }
	defer release()
	var calls, connections atomic.Int32
	upgrader := websocket.Upgrader{}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connections.Add(1)
		if r.URL.Path != "/v1/rpc" || r.URL.Query().Get("cluster") != "prod" ||
			r.URL.Query().Get("instance") != "node" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("unexpected RPC route: %s headers=%v", r.URL, r.Header)
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		for {
			var req map[string]interface{}
			if err := conn.ReadJSON(&req); err != nil {
				return
			}
			if req["method"] == "initialize" {
				_ = conn.WriteJSON(map[string]interface{}{
					"jsonrpc": "2.0", "id": req["id"],
					"result": agentInitializeResult(),
				})
				continue
			}
			calls.Add(1)
			params, _ := req["params"].(map[string]interface{})
			args, _ := params["arguments"].(map[string]interface{})
			if req["method"] != "tools/call" || params["name"] != "doops_bg" ||
				args["command"] != "sleep 1" || args["session_id"] != "bg-stream" {
				t.Errorf("unexpected tool request: %#v", req)
			}
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0", "method": "notifications/message",
				"params": map[string]interface{}{"request_id": req["id"], "sessionID": "bg-stream", "data": "TaskID: bg-1\nworking\n"},
			})
			<-stopped
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]interface{}{
					"content": []map[string]string{{"type": "text", "text": "Task complete.\nTaskID: bg-1\nExitCode: 0"}},
				},
			})
		}
	}))
	defer gateway.Close()

	binary := buildDoopsCLIBinary(t)
	t.Setenv("DOOPS_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	if err := saveServers([]Server{{Name: "test", Gateway: gateway.URL, Cluster: "prod", Instance: "node"}}); err != nil {
		t.Fatal(err)
	}
	auth := &AuthStore{}
	auth.SetGateway(gateway.URL, "test-token")
	if err := auth.Save(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "-session", "bg-stream", "exec", "--target", "test", "--bg", "--cmd", "sleep 1")
	command.Env = append(os.Environ(), "DOOPS_CLI_CALL_TIMEOUT=2s")
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = command.Stdout
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 32)
	done := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
		done <- command.Wait()
	}()
	var output strings.Builder
	seenTask, seenProgress := false, false
	for !seenTask || !seenProgress {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("CLI returned before stopped: %s", output.String())
			}
			output.WriteString(line + "\n")
			seenTask = seenTask || line == "TaskID: bg-1"
			seenProgress = seenProgress || line == "working"
			if strings.Contains(line, "Task complete") {
				t.Fatalf("completion was reported before stopped: %s", output.String())
			}
		case <-ctx.Done():
			t.Fatalf("progress was not streamed before final result: %s", output.String())
		}
	}
	select {
	case err := <-done:
		t.Fatalf("CLI returned on progress instead of waiting for stopped: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	release()
	for line := range lines {
		output.WriteString(line + "\n")
	}
	if err := <-done; err != nil {
		t.Fatalf("streaming CLI: %v\n%s", err, output.String())
	}
	for _, want := range []string{"Task complete.", "ExitCode: 0"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("final result hidden after progress, missing %q:\n%s", want, output.String())
		}
	}
	if calls.Load() != 1 || connections.Load() != 1 {
		t.Fatalf("background task polled or retried: calls=%d connections=%d", calls.Load(), connections.Load())
	}
}

func TestExecBgReturnsStreamingErrorsWithoutPollingOrRetry(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
	}{
		{"tool_error", "Task failed.\nTaskID: bg-1\nExitCode: 17"},
		{"rpc_error", "remote error: execution_outcome_unknown"},
		{"canceled", "remote error: operation canceled"},
		{"disconnect", "connection lost: WS connection lost"},
		{"timeout", "remote error: execution_outcome_unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls, connections atomic.Int32
			upgrader := websocket.Upgrader{}
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				connections.Add(1)
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Errorf("upgrade: %v", err)
					return
				}
				defer conn.Close()
				for {
					var req map[string]interface{}
					if err := conn.ReadJSON(&req); err != nil {
						return
					}
					if req["method"] == "initialize" {
						_ = conn.WriteJSON(map[string]interface{}{
							"jsonrpc": "2.0", "id": req["id"],
							"result": agentInitializeResult(),
						})
						continue
					}
					if req["method"] == "tools/cancel" {
						if tc.name != "timeout" {
							t.Errorf("unexpected cancellation request: %#v", req)
							return
						}
						params, _ := req["params"].(map[string]interface{})
						_ = conn.WriteJSON(map[string]interface{}{
							"jsonrpc": "2.0", "id": req["id"],
							"result": map[string]interface{}{"canceled": true},
						})
						_ = conn.WriteJSON(map[string]interface{}{
							"jsonrpc": "2.0", "id": params["id"],
							"error": map[string]interface{}{"code": -32008, "message": "execution_outcome_unknown"},
						})
						continue
					}
					calls.Add(1)
					params, _ := req["params"].(map[string]interface{})
					if req["method"] != "tools/call" || params["name"] != "doops_bg" {
						t.Errorf("unexpected polling call: %#v", params)
					}
					_ = conn.WriteJSON(map[string]interface{}{
						"jsonrpc": "2.0", "method": "notifications/message",
						"params": map[string]interface{}{"request_id": req["id"], "sessionID": "bg-error", "data": "TaskID: bg-1\n"},
					})
					switch tc.name {
					case "tool_error":
						_ = conn.WriteJSON(map[string]interface{}{
							"jsonrpc": "2.0", "id": req["id"],
							"result": map[string]interface{}{
								"isError": true, "content": []map[string]string{{"type": "text", "text": tc.want}},
							},
						})
					case "rpc_error":
						_ = conn.WriteJSON(map[string]interface{}{
							"jsonrpc": "2.0", "id": req["id"],
							"error": map[string]interface{}{"code": -32603, "message": "execution_outcome_unknown"},
						})
					case "canceled":
						_ = conn.WriteJSON(map[string]interface{}{
							"jsonrpc": "2.0", "id": req["id"],
							"error": map[string]interface{}{"code": -32007, "message": "operation canceled"},
						})
					case "disconnect":
						return
					}
				}
			}))
			defer gateway.Close()
			client := newGatewayTestClient(t, gateway.URL, "bg-error")
			defer client.Close()
			client.CallTimeout = 100 * time.Millisecond
			err := client.ExecBg("sleep 1")
			if err == nil || err.Error() != tc.want {
				t.Fatalf("background error changed: got %v want %q", err, tc.want)
			}
			if calls.Load() != 1 || connections.Load() != 1 {
				t.Fatalf("background task polled or retried: calls=%d connections=%d", calls.Load(), connections.Load())
			}
		})
	}
}
