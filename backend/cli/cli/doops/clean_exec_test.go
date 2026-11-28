package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestResolveCleanWorkspace(t *testing.T) {
	tests := []struct {
		name            string
		workspace       string
		explicitSession string
		want            string
		wantErr         bool
	}{
		{
			name:            "explicit workspace wins over explicit session",
			workspace:       "target-ws",
			explicitSession: "outer-session",
			want:            "target-ws",
		},
		{
			name:            "explicit workspace alone succeeds",
			workspace:       "only-ws",
			explicitSession: "",
			want:            "only-ws",
		},
		{
			name:            "explicit session reused when workspace omitted",
			workspace:       "",
			explicitSession: "release-42",
			want:            "release-42",
		},
		{
			name:            "fails closed when neither workspace nor session provided",
			workspace:       "",
			explicitSession: "",
			wantErr:         true,
		},
		{
			name:            "fails closed on whitespace only inputs",
			workspace:       "   ",
			explicitSession: "\t ",
			wantErr:         true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveCleanWorkspace(tc.workspace, tc.explicitSession)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got workspace=%q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("workspace = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCLICleanReusesExplicitSessionAndFailsClosedWithoutBoth(t *testing.T) {
	var mu sync.Mutex
	var cleanedWorkspaces []string
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
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
					"result":  agentInitializeResult(),
				})
			case "tools/call":
				params, _ := req["params"].(map[string]interface{})
				args, _ := params["arguments"].(map[string]interface{})
				ws, _ := args["workspace"].(string)
				mu.Lock()
				cleanedWorkspaces = append(cleanedWorkspaces, ws)
				mu.Unlock()
				_ = conn.WriteJSON(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result": map[string]interface{}{
						"content": []map[string]interface{}{{"type": "text", "text": "cleaned " + ws}},
					},
				})
			}
		}
	}))
	defer gateway.Close()

	t.Setenv("DOOPS_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	if err := saveServers([]Server{{Name: "test", Gateway: gateway.URL, Cluster: "dev", Instance: "node"}}); err != nil {
		t.Fatal(err)
	}
	auth := &AuthStore{}
	auth.SetGateway(gateway.URL, "synthetic-token")
	if err := auth.Save(); err != nil {
		t.Fatal(err)
	}

	bin := buildDoopsCLIBinary(t)
	run := func(args ...string) (string, string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		return stdout.String(), stderr.String(), err
	}

	// 1. Global -session without --workspace reuses -session.
	if out, errOut, err := run("-session", "rel-global", "clean", "--target", "test"); err != nil {
		t.Fatalf("clean with global -session failed: %v\nstdout=%s\nstderr=%s", err, out, errOut)
	}

	// 2. Subcommand --session without --workspace reuses --session.
	if out, errOut, err := run("clean", "--target", "test", "--session", "rel-sub"); err != nil {
		t.Fatalf("clean with subcommand --session failed: %v\nstdout=%s\nstderr=%s", err, out, errOut)
	}

	// 3. Explicit --workspace takes precedence over -session.
	if out, errOut, err := run("-session", "outer-session", "clean", "--target", "test", "--workspace", "explicit-ws"); err != nil {
		t.Fatalf("clean with explicit --workspace failed: %v\nstdout=%s\nstderr=%s", err, out, errOut)
	}

	// 4. Explicit --workspace without -session still succeeds.
	if out, errOut, err := run("clean", "--target", "test", "--workspace", "only-ws"); err != nil {
		t.Fatalf("clean with only --workspace failed: %v\nstdout=%s\nstderr=%s", err, out, errOut)
	}

	// 5. Neither -session nor --workspace fails closed without calling Gateway.
	out, errOut, err := run("clean", "--target", "test")
	if err == nil {
		t.Fatalf("clean without -session or --workspace must fail closed; stdout=%s stderr=%s", out, errOut)
	}

	mu.Lock()
	got := append([]string(nil), cleanedWorkspaces...)
	mu.Unlock()
	want := []string{"rel-global", "rel-sub", "explicit-ws", "only-ws"}
	if len(got) != len(want) {
		t.Fatalf("cleaned workspaces = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cleaned workspace[%d] = %q, want %q (all=%v)", i, got[i], want[i], got)
		}
	}
}

func TestCLIExecRawAndQuietSuppressBannerHeartbeatAndTerminalText(t *testing.T) {
	const jsonPayload = "{\"status\":\"ok\",\"items\":[1,2]}\n"
	const heartbeat5s = "\r\x1b[K[agent] ⏳ 命令后台执行中... (耗时 5s)"
	const heartbeat10s = "\r\x1b[K[agent] ⏳ 命令后台执行中... (耗时 10s)"

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
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
					"result":  agentInitializeResult(),
				})
			case "tools/call":
				params, _ := req["params"].(map[string]interface{})
				args, _ := params["arguments"].(map[string]interface{})
				command, _ := args["command"].(string)

				sendRaw := func(data string) {
					_ = conn.WriteJSON(map[string]interface{}{
						"jsonrpc": "2.0",
						"method":  "notifications/message",
						"params": map[string]interface{}{
							"request_id": req["id"],
							"kind":       "raw",
							"data":       data,
						},
					})
				}

				switch command {
				case "emit-json":
					sendRaw(heartbeat5s)
					sendRaw(jsonPayload)
					sendRaw(heartbeat10s)
					_ = conn.WriteJSON(map[string]interface{}{
						"jsonrpc": "2.0",
						"id":      req["id"],
						"result": map[string]interface{}{
							"content": []map[string]interface{}{{"type": "text", "text": "Operation complete."}},
						},
					})
				case "silent-ok":
					sendRaw(heartbeat5s)
					_ = conn.WriteJSON(map[string]interface{}{
						"jsonrpc": "2.0",
						"id":      req["id"],
						"result": map[string]interface{}{
							"content": []map[string]interface{}{{"type": "text", "text": "Operation complete."}},
						},
					})
				case "fail-cmd":
					sendRaw(heartbeat5s)
					_ = conn.WriteJSON(map[string]interface{}{
						"jsonrpc": "2.0",
						"id":      req["id"],
						"result": map[string]interface{}{
							"content": []map[string]interface{}{{"type": "text", "text": "Command failed with error: exit status 2"}},
							"isError": true,
						},
					})
				}
			}
		}
	}))
	defer gateway.Close()

	t.Setenv("DOOPS_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	if err := saveServers([]Server{{Name: "test", Gateway: gateway.URL, Cluster: "dev", Instance: "node"}}); err != nil {
		t.Fatal(err)
	}
	auth := &AuthStore{}
	auth.SetGateway(gateway.URL, "synthetic-token")
	if err := auth.Save(); err != nil {
		t.Fatal(err)
	}

	bin := buildDoopsCLIBinary(t)
	run := func(args ...string) (string, string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		return stdout.String(), stderr.String(), err
	}

	// Default interactive mode still emits [TARGETING] banner and heartbeat.
	defaultOut, _, err := run("-session", "exec-default", "exec", "--target", "test", "--cmd", "emit-json")
	if err != nil {
		t.Fatalf("default exec failed: %v", err)
	}
	if !strings.Contains(defaultOut, "[TARGETING]") {
		t.Fatalf("default exec must keep [TARGETING] banner, got: %q", defaultOut)
	}
	if !strings.Contains(defaultOut, "命令后台执行中") {
		t.Fatalf("default exec must keep interactive heartbeat, got: %q", defaultOut)
	}

	// --raw and --quiet both output pure JSON consumable by json.Unmarshal.
	for _, flagName := range []string{"--raw", "--quiet"} {
		t.Run("flag_"+flagName, func(t *testing.T) {
			stdout, stderr, err := run("-session", "exec-raw", "exec", flagName, "--target", "test", "--cmd", "emit-json")
			if err != nil {
				t.Fatalf("exec %s failed: %v (stderr=%q)", flagName, err, stderr)
			}
			if stdout != jsonPayload {
				t.Fatalf("exec %s stdout = %q, want exact %q", flagName, stdout, jsonPayload)
			}
			var parsed struct {
				Status string `json:"status"`
				Items  []int  `json:"items"`
			}
			if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
				t.Fatalf("exec %s stdout is not valid JSON: %v (%q)", flagName, err, stdout)
			}
			if parsed.Status != "ok" || len(parsed.Items) != 2 {
				t.Fatalf("unexpected parsed JSON: %+v", parsed)
			}
		})
	}

	// --raw with -verbose keeps verbose diagnostics on stderr, leaving stdout pure JSON.
	stdout, stderr, err := run("-verbose", "-session", "exec-raw-verbose", "exec", "--raw", "--target", "test", "--cmd", "emit-json")
	if err != nil {
		t.Fatalf("verbose exec --raw failed: %v", err)
	}
	if stdout != jsonPayload {
		t.Fatalf("verbose exec --raw polluted stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "[CLIENT:") {
		t.Fatalf("verbose exec --raw expected client logs on stderr, got: %q", stderr)
	}

	// --raw with silent command suppresses "Operation complete." wrapper text.
	silentOut, silentErr, err := run("-session", "exec-silent", "exec", "--raw", "--target", "test", "--cmd", "silent-ok")
	if err != nil {
		t.Fatalf("silent exec --raw failed: %v (stderr=%q)", err, silentErr)
	}
	if silentOut != "" {
		t.Fatalf("silent exec --raw must keep stdout empty, got: %q", silentOut)
	}

	// --raw error goes to stderr, leaving stdout clean.
	failOut, failErr, err := run("-session", "exec-fail", "exec", "--raw", "--target", "test", "--cmd", "fail-cmd")
	if err == nil {
		t.Fatal("expected failing remote command to exit non-zero")
	}
	if failOut != "" {
		t.Fatalf("failing exec --raw must not pollute stdout, got: %q", failOut)
	}
	if !strings.Contains(failErr, "exit status 2") {
		t.Fatalf("failing exec --raw must report error on stderr, got: %q", failErr)
	}
}

func TestCallAndCaptureStructuredStreamedCommandAndProgressProtocol(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
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
					"result":  agentInitializeResult(),
				})
			case "tools/call":
				params, _ := req["params"].(map[string]interface{})
				args, _ := params["arguments"].(map[string]interface{})
				mode, _ := args["command"].(string)

				sendNotice := func(kind, data string) {
					_ = conn.WriteJSON(map[string]interface{}{
						"jsonrpc": "2.0",
						"method":  "notifications/message",
						"params": map[string]interface{}{
							"request_id": req["id"],
							"kind":       kind,
							"data":       data,
						},
					})
				}

				switch mode {
				case "json-no-newline":
					// Custom progress payload that does NOT match legacy regex:
					// must still be filtered strictly by kind == "progress".
					sendNotice("progress", "[agent] custom progress tick 1")
					sendNotice("raw", `{"status":"ok","count":3}`)
					sendNotice("progress", "\r\x1b[K[agent] ⏳ 命令后台执行中... (耗时 5s)")
					_ = conn.WriteJSON(map[string]interface{}{
						"jsonrpc": "2.0",
						"id":      req["id"],
						"result": map[string]interface{}{
							"content": []map[string]interface{}{{"type": "text", "text": "Operation complete."}},
							"structuredContent": map[string]interface{}{
								"kind":     "doops.command-result",
								"exitCode": 0,
								"streamed": true,
							},
						},
					})
				case "empty-stdout":
					sendNotice("progress", "[agent] waiting on silent command")
					_ = conn.WriteJSON(map[string]interface{}{
						"jsonrpc": "2.0",
						"id":      req["id"],
						"result": map[string]interface{}{
							"content": []map[string]interface{}{{"type": "text", "text": "Operation complete."}},
							"structuredContent": map[string]interface{}{
								"kind":     "doops.command-result",
								"exitCode": 0,
								"streamed": true,
							},
						},
					})
				case "legacy-ssh-heartbeat":
					sendNotice("raw", "\r\x1b[K[agent] ⏳ 命令后台执行中... (耗时 5s)")
					sendNotice("raw", "ssh-body\n")
					_ = conn.WriteJSON(map[string]interface{}{
						"jsonrpc": "2.0",
						"id":      req["id"],
						"result": map[string]interface{}{
							"content": []map[string]interface{}{{"type": "text", "text": "SSH command finished."}},
						},
					})
				}
			}
		}
	}))
	defer gateway.Close()

	client := newGatewayTestClient(t, gateway.URL, "structured-capture")
	defer client.Close()

	t.Run("progress skipped and Operation complete not appended on no-newline stdout", func(t *testing.T) {
		got, err := client.CallAndCapture("doops_shell", map[string]interface{}{"command": "json-no-newline"})
		if err != nil {
			t.Fatalf("CallAndCapture failed: %v", err)
		}
		const want = `{"status":"ok","count":3}`
		if got != want {
			t.Fatalf("CallAndCapture = %q, want exact %q", got, want)
		}
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(got), &parsed); err != nil {
			t.Fatalf("captured JSON corrupted: %v (%q)", err, got)
		}
	})

	t.Run("empty stdout with streamed structuredContent returns empty string without error", func(t *testing.T) {
		got, err := client.CallAndCapture("doops_shell", map[string]interface{}{"command": "empty-stdout"})
		if err != nil {
			t.Fatalf("empty streamed command must not return error, got: %v", err)
		}
		if got != "" {
			t.Fatalf("empty streamed command output = %q, want empty", got)
		}
	})

	t.Run("progress kind skipped regardless of tool name", func(t *testing.T) {
		got, err := client.CallAndCapture("doops_node_info", map[string]interface{}{"command": "json-no-newline"})
		if err != nil {
			t.Fatalf("CallAndCapture doops_node_info failed: %v", err)
		}
		const want = `{"status":"ok","count":3}`
		if got != want {
			t.Fatalf("CallAndCapture doops_node_info = %q, want %q", got, want)
		}
	})

	t.Run("legacy ssh raw heartbeat filtered while preserving legacy terminal text", func(t *testing.T) {
		got, err := client.CallAndCapture("doops_ssh", map[string]interface{}{"command": "legacy-ssh-heartbeat"})
		if err != nil {
			t.Fatalf("CallAndCapture legacy ssh failed: %v", err)
		}
		const want = "ssh-body\nSSH command finished."
		if got != want {
			t.Fatalf("CallAndCapture legacy ssh = %q, want %q", got, want)
		}
	})
}

func TestCLIExecProgressKindInteractiveVsRawAndNoTrailingNewline(t *testing.T) {
	const customProgress = "[agent] custom progress tick"
	const payloadNoNewline = `{"pure":true}`

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
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
					"result":  agentInitializeResult(),
				})
			case "tools/call":
				_ = conn.WriteJSON(map[string]interface{}{
					"jsonrpc": "2.0",
					"method":  "notifications/message",
					"params": map[string]interface{}{
						"request_id": req["id"],
						"kind":       "progress",
						"data":       customProgress,
					},
				})
				_ = conn.WriteJSON(map[string]interface{}{
					"jsonrpc": "2.0",
					"method":  "notifications/message",
					"params": map[string]interface{}{
						"request_id": req["id"],
						"kind":       "raw",
						"data":       payloadNoNewline,
					},
				})
				_ = conn.WriteJSON(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result": map[string]interface{}{
						"content": []map[string]interface{}{{"type": "text", "text": "Operation complete."}},
						"structuredContent": map[string]interface{}{
							"kind":     "doops.command-result",
							"exitCode": 0,
							"streamed": true,
						},
					},
				})
			}
		}
	}))
	defer gateway.Close()

	t.Setenv("DOOPS_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	if err := saveServers([]Server{{Name: "test", Gateway: gateway.URL, Cluster: "dev", Instance: "node"}}); err != nil {
		t.Fatal(err)
	}
	auth := &AuthStore{}
	auth.SetGateway(gateway.URL, "synthetic-token")
	if err := auth.Save(); err != nil {
		t.Fatal(err)
	}

	bin := buildDoopsCLIBinary(t)
	run := func(args ...string) (string, string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		return stdout.String(), stderr.String(), err
	}

	// Interactive mode keeps kind="progress" visible.
	defaultOut, _, err := run("-session", "prog-default", "exec", "--target", "test", "--cmd", "printf json")
	if err != nil {
		t.Fatalf("interactive exec failed: %v", err)
	}
	if !strings.Contains(defaultOut, customProgress) {
		t.Fatalf("interactive exec must display kind=progress notification, got: %q", defaultOut)
	}

	// --raw suppresses kind="progress" and preserves exact bytes without injecting a newline.
	rawOut, rawErr, err := run("-session", "prog-raw", "exec", "--raw", "--target", "test", "--cmd", "printf json")
	if err != nil {
		t.Fatalf("exec --raw failed: %v (stderr=%q)", err, rawErr)
	}
	if rawOut != payloadNoNewline {
		t.Fatalf("exec --raw stdout = %q, want exact %q", rawOut, payloadNoNewline)
	}
}
