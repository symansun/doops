package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type temporaryHeartbeatError struct{}
type fatalHeartbeatTimeoutError struct{}

func runTestCommand(t *testing.T, directory, name string, arguments ...string) string {
	t.Helper()
	command := exec.Command(name, arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, arguments, err, output)
	}
	return string(output)
}

func newGatewayAuthorizedAgentServer(t *testing.T, gateway *Gateway) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		gateway.ServeGatewayWebSocketConnWithReady(conn, r.RemoteAddr, nil)
	}))
}

func (temporaryHeartbeatError) Error() string   { return "temporary heartbeat write timeout" }
func (temporaryHeartbeatError) Timeout() bool   { return true }
func (temporaryHeartbeatError) Temporary() bool { return true }

func (fatalHeartbeatTimeoutError) Error() string   { return "fatal heartbeat network write timeout" }
func (fatalHeartbeatTimeoutError) Timeout() bool   { return true }
func (fatalHeartbeatTimeoutError) Temporary() bool { return false }

type fakeWebSocketControlWriter struct {
	writeErr error
	closes   int
	onWrite  func()
}

func (f *fakeWebSocketControlWriter) WriteControl(int, []byte, time.Time) error {
	if f.onWrite != nil {
		f.onWrite()
	}
	return f.writeErr
}

func (f *fakeWebSocketControlWriter) Close() error {
	f.closes++
	return nil
}

func TestWriteHeartbeatPingClosesConnectionAfterUnexplainedWriteFailure(t *testing.T) {
	tests := []struct {
		name             string
		writeErr         error
		dataWriteActive  bool
		dataWriteOverlap bool
		wantKeep         bool
		wantCloses       int
	}{
		{
			name:       "temporary write timeout",
			writeErr:   temporaryHeartbeatError{},
			wantKeep:   false,
			wantCloses: 1,
		},
		{
			name:            "temporary write timeout during data write",
			writeErr:        temporaryHeartbeatError{},
			dataWriteActive: true,
			wantKeep:        true,
			wantCloses:      0,
		},
		{
			name:             "temporary write timeout overlapping completed data write",
			writeErr:         temporaryHeartbeatError{},
			dataWriteOverlap: true,
			wantKeep:         true,
			wantCloses:       0,
		},
		{
			name:             "fatal network timeout overlapping completed data write",
			writeErr:         fatalHeartbeatTimeoutError{},
			dataWriteOverlap: true,
			wantKeep:         false,
			wantCloses:       1,
		},
		{
			name:       "permanent write failure",
			writeErr:   fmt.Errorf("connection closed"),
			wantKeep:   false,
			wantCloses: 1,
		},
		{
			name:       "successful heartbeat",
			wantKeep:   true,
			wantCloses: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var dataWriteState websocketDataWriteState
			if tt.dataWriteActive {
				dataWriteState.begin()
				defer dataWriteState.end()
			}
			conn := &fakeWebSocketControlWriter{writeErr: tt.writeErr}
			if tt.dataWriteOverlap {
				conn.onWrite = func() {
					dataWriteState.begin()
					dataWriteState.end()
				}
			}
			if got := writeHeartbeatPing(conn, &dataWriteState, nil, time.Second); got != tt.wantKeep {
				t.Fatalf("keep connection = %t, want %t", got, tt.wantKeep)
			}
			if conn.closes != tt.wantCloses {
				t.Fatalf("close calls = %d, want %d", conn.closes, tt.wantCloses)
			}
		})
	}
}

type fakeWebSocketDataWriter struct {
	deadlines []time.Time
	writeErr  error
	closes    int
}

func (f *fakeWebSocketDataWriter) SetWriteDeadline(deadline time.Time) error {
	f.deadlines = append(f.deadlines, deadline)
	return nil
}

func (f *fakeWebSocketDataWriter) WriteJSON(interface{}) error {
	return f.writeErr
}

func (f *fakeWebSocketDataWriter) Close() error {
	f.closes++
	return nil
}

func TestWriteWebSocketJSONBoundsBlockedWrites(t *testing.T) {
	t.Run("successful write clears deadline", func(t *testing.T) {
		conn := &fakeWebSocketDataWriter{}
		var dataWriteState websocketDataWriteState
		if err := writeWebSocketJSON(conn, &sync.Mutex{}, &dataWriteState, map[string]string{"ok": "true"}, time.Second); err != nil {
			t.Fatalf("write JSON: %v", err)
		}
		if len(conn.deadlines) != 2 || conn.deadlines[0].IsZero() || !conn.deadlines[1].IsZero() {
			t.Fatalf("write deadlines = %v, want finite deadline followed by zero", conn.deadlines)
		}
		if conn.closes != 0 {
			t.Fatalf("close calls = %d, want 0", conn.closes)
		}
		if dataWriteState.sequence.Load()%2 != 0 {
			t.Fatal("data write state remained active after success")
		}
	})

	t.Run("write failure closes connection", func(t *testing.T) {
		conn := &fakeWebSocketDataWriter{writeErr: fmt.Errorf("write timeout")}
		var dataWriteState websocketDataWriteState
		if err := writeWebSocketJSON(conn, &sync.Mutex{}, &dataWriteState, map[string]string{"ok": "false"}, time.Second); err == nil {
			t.Fatal("write JSON succeeded, want failure")
		}
		if len(conn.deadlines) != 1 || conn.deadlines[0].IsZero() {
			t.Fatalf("write deadlines = %v, want one finite deadline", conn.deadlines)
		}
		if conn.closes != 1 {
			t.Fatalf("close calls = %d, want 1", conn.closes)
		}
		if dataWriteState.sequence.Load()%2 != 0 {
			t.Fatal("data write state remained active after failure")
		}
	})
}

func TestReadWebSocketMessageReportsProgressBeforeMessageCompletes(t *testing.T) {
	serverConn := make(chan *websocket.Conn, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		serverConn <- conn
	}))
	defer ts.Close()

	clientConn, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(ts.URL, "http"),
		nil,
	)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer clientConn.Close()
	receiver := <-serverConn
	defer receiver.Close()

	type readResult struct {
		messageType int
		body        []byte
		err         error
	}
	progress := make(chan struct{}, 1)
	result := make(chan readResult, 1)
	go func() {
		messageType, body, err := readWebSocketMessage(receiver, func() {
			select {
			case progress <- struct{}{}:
			default:
			}
		})
		result <- readResult{messageType: messageType, body: body, err: err}
	}()

	writer, err := clientConn.NextWriter(websocket.TextMessage)
	if err != nil {
		t.Fatalf("open message writer: %v", err)
	}
	body := bytes.Repeat([]byte("x"), 64<<10)
	if _, err := writer.Write(body); err != nil {
		t.Fatalf("write partial message: %v", err)
	}

	select {
	case <-progress:
	case <-time.After(time.Second):
		t.Fatal("reader did not report progress before the message completed")
	}
	select {
	case <-result:
		t.Fatal("reader completed before the message writer closed")
	default:
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("close message writer: %v", err)
	}
	select {
	case got := <-result:
		if got.err != nil {
			t.Fatalf("read message: %v", got.err)
		}
		if got.messageType != websocket.TextMessage {
			t.Fatalf("message type = %d, want text", got.messageType)
		}
		if !bytes.Equal(got.body, body) {
			t.Fatalf("message body has %d bytes, want %d", len(got.body), len(body))
		}
	case <-time.After(time.Second):
		t.Fatal("reader did not complete after the message writer closed")
	}
}

func TestAgentReverseTunnelReadyOnlyAfterGatewayRegistration(t *testing.T) {
	gw := NewGateway()
	ready := make(chan struct{})
	var readyOnce sync.Once
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		gw.ServeGatewayWebSocketConnWithReady(conn, r.RemoteAddr, func() {
			readyOnce.Do(func() { close(ready) })
		})
	}))
	defer ts.Close()

	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)
	select {
	case <-ready:
		t.Fatal("initialize must not mark reverse tunnel ready before registry acknowledgement")
	case <-time.After(50 * time.Millisecond):
	}
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "gateway/registered",
	}); err != nil {
		t.Fatalf("send gateway registration acknowledgement: %v", err)
	}
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("gateway registration acknowledgement did not mark reverse tunnel ready")
	}
}

func TestAgentWebSocketFileReadWrite(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()

	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	callTool(t, conn, "doops_file_write", map[string]interface{}{
		"session_id": "files",
		"path":       "hello.txt",
		"content":    "hello gateway",
	})
	result := callTool(t, conn, "doops_file_read", map[string]interface{}{
		"session_id": "files",
		"path":       "hello.txt",
	})
	if result != "hello gateway" {
		t.Fatalf("read content mismatch: %q", result)
	}
	if _, err := os.Stat(filepath.Join(root, "files", "hello.txt")); err != nil {
		t.Fatalf("expected file in session workspace: %v", err)
	}
}

func TestAgentWebSocketFileToolsRejectWorkspaceEscape(t *testing.T) {
	t.Setenv("DOOPS_WORKSPACE_ROOT", t.TempDir())
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if _, err := handleFileWrite(mustJSON(t, map[string]interface{}{
		"session_id": "files",
		"path":       outside,
		"content":    "nope",
	})); err == nil {
		t.Fatal("expected absolute file write path to be rejected")
	}
	if _, err := handleFileRead(mustJSON(t, map[string]interface{}{
		"session_id": "files",
		"path":       "../outside.txt",
	})); err == nil {
		t.Fatal("expected relative escape read path to be rejected")
	}
}

func TestGitCloneToolClonesIntoWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	repoPath := createLocalBareGitRepo(t)

	result, err := handleGitClone(mustJSON(t, map[string]interface{}{
		"session_id": "clone",
		"url":        repoPath,
		"branch":     "main",
		"directory":  "app",
	}))
	if err != nil {
		t.Fatalf("git clone: %v", err)
	}
	if !strings.Contains(result, "cloned") {
		t.Fatalf("unexpected clone result: %s", result)
	}
	if _, err := os.Stat(filepath.Join(root, "clone", "app", "README.md")); err != nil {
		t.Fatalf("expected cloned file: %v", err)
	}
}

func TestGitCloneToolRejectsDirectoryEscape(t *testing.T) {
	t.Setenv("DOOPS_WORKSPACE_ROOT", t.TempDir())
	_, err := handleGitClone(mustJSON(t, map[string]interface{}{
		"session_id": "clone",
		"url":        "https://example.com/repo.git",
		"branch":     "main",
		"directory":  "../escape",
	}))
	if err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("expected directory validation error, got %v", err)
	}
}

func TestAgentMetadataModeReturnsModelStatusWithoutPrompt(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv("DO_AGENT_SETTINGS", settingsPath)
	if err := os.WriteFile(settingsPath, []byte(`{
  "model": "minimax/MiniMax-M3",
  "provider": {
    "minimax": {
      "options": {"apiKey": "real-key", "baseURL": "https://api.minimaxi.com/anthropic", "kind": "anthropic"},
      "models": {"MiniMax-M3": {"name": "MiniMax-M3"}}
    }
  }
}`), 0600); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	doagent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rpc" {
			http.NotFound(w, r)
			return
		}
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode rpc: %v", err)
		}
		if req["method"] != "initialize" {
			t.Fatalf("metadata mode should only initialize doagent, got method %v", req["method"])
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"result": map[string]interface{}{
				"_meta": map[string]interface{}{"model": "MiniMax-M3"},
			},
		})
	}))
	defer doagent.Close()
	t.Setenv("DO_AGENT_URL", doagent.URL)

	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	got := callTool(t, conn, "doops_agent_prompt", map[string]interface{}{
		"session_id": "metadata-smoke",
		"mode":       "metadata",
	})
	var meta map[string]interface{}
	if err := json.Unmarshal([]byte(got), &meta); err != nil {
		t.Fatalf("metadata should be JSON, got %q: %v", got, err)
	}
	if meta["status"] != "connected" || meta["provider"] != "minimax" || meta["model"] != "minimax/MiniMax-M3" {
		t.Fatalf("metadata mismatch: %+v", meta)
	}
}

func TestAgentHistoryModeWithoutSessionReturnsEmptyHistory(t *testing.T) {
	doagent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("history mode without a bound session should not call doagent: %s", r.URL.Path)
	}))
	defer doagent.Close()
	t.Setenv("DO_AGENT_URL", doagent.URL)

	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	got := callTool(t, conn, "doops_agent_prompt", map[string]interface{}{
		"session_id": "empty-history",
		"mode":       "history",
	})
	var history map[string]interface{}
	if err := json.Unmarshal([]byte(got), &history); err != nil {
		t.Fatalf("history should be JSON, got %q: %v", got, err)
	}
	messages, _ := history["messages"].([]interface{})
	if history["source"] != "doagent" || history["sessionId"] != "empty-history" || len(messages) != 0 {
		t.Fatalf("history mismatch: %+v", history)
	}
}

func TestAgentPromptNotificationIncludesAssistantDeltaKind(t *testing.T) {
	prompted := make(chan struct{})
	receivedPrompt := make(chan string, 1)
	doagent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rpc":
			var req map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode rpc: %v", err)
			}
			switch req["method"] {
			case "initialize":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result": map[string]interface{}{
						"capabilities": map[string]interface{}{"cicdStructuredReport": true},
					},
				})
			case "session/new":
				params, _ := req["params"].(map[string]interface{})
				if params["permissionMode"] != "restricted" {
					t.Fatalf("doagent sessions must fail closed outside apply: %#v", params)
				}
				if strings.Contains(fmt.Sprint(params["systemPrompt"]), "Current DoOps session_id") {
					t.Fatalf("runtime session leaked into model prompt: %#v", params)
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result":  map[string]interface{}{"sessionId": "doagent-kind"},
				})
			case "session/setMode":
				params, _ := req["params"].(map[string]interface{})
				if params["modeId"] != "auto" {
					t.Fatalf("generic prompt must use native auto mode: %#v", req)
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result":  map[string]interface{}{},
				})
			case "session/prompt":
				params, _ := req["params"].(map[string]interface{})
				prompt, _ := params["prompt"].(string)
				allowed, _ := params["allowedTools"].([]interface{})
				if strings.Join(interfaceStringValues(allowed), ",") != "Read,Glob,Grep,Skill,mcp_doops_k8s_read" {
					t.Fatalf("ordinary prompt must have an exact read-only tool allowlist: %#v", params)
				}
				if params["responseMode"] != "stream" {
					t.Fatalf("prompt must request SSE terminal delivery: %#v", params)
				}
				receivedPrompt <- prompt
				close(prompted)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result": map[string]interface{}{
						"status":   "accepted",
						"delivery": "sse",
						"turnId":   "turn-kind",
					},
				})
			default:
				t.Fatalf("unexpected rpc method: %v", req["method"])
			}
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			<-prompted
			fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","turnId":"turn-kind","content":{"type":"text","text":"hello"}}}}`)
			fmt.Fprintln(w)
			fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message","turnId":"turn-kind","content":{"type":"text","text":"done"}}}}`)
			fmt.Fprintln(w)
			fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"turn_finished","turnId":"turn-kind","status":"completed","stopReason":"end_turn"}}}`)
			fmt.Fprintln(w)
		default:
			http.NotFound(w, r)
		}
	}))
	defer doagent.Close()
	t.Setenv("DO_AGENT_URL", doagent.URL)

	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	rawArgs, _ := json.Marshal(map[string]interface{}{
		"session_id":  "kind-prompt",
		"instruction": "hello",
	})
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      42,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "doops_agent_prompt",
			"arguments": json.RawMessage(rawArgs),
		},
	}); err != nil {
		t.Fatalf("call prompt: %v", err)
	}

	msg := readNotification(t, conn)
	params, _ := msg["params"].(map[string]interface{})
	if params["kind"] != "assistant_delta" {
		t.Fatalf("expected assistant_delta notification kind, got %#v", params)
	}
	if params["data"] != "hello" {
		t.Fatalf("expected first assistant chunk, got %#v", params["data"])
	}

	for {
		var result map[string]interface{}
		if err := conn.ReadJSON(&result); err != nil {
			t.Fatalf("read prompt result: %v", err)
		}
		if _, ok := result["method"]; ok {
			continue
		}
		toolResult, _ := result["result"].(map[string]interface{})
		content, _ := toolResult["content"].([]interface{})
		if len(content) != 1 {
			t.Fatalf("expected text-only prompt result, got %#v", result)
		}
		if _, ok := toolResult["structuredContent"]; ok {
			t.Fatalf("doops_agent_prompt must not return structured content: %#v", toolResult)
		}
		break
	}

	if prompt := <-receivedPrompt; strings.Contains(prompt, "deploy.sh") {
		t.Fatalf("agent prompt must not inject a deploy.sh generation contract: %s", prompt)
	}
}

func interfaceStringValues(values []interface{}) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.(string))
	}
	return result
}

func TestAgentInitializeDoesNotExposeDedicatedCICDReconciliation(t *testing.T) {
	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()

	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
	}); err != nil {
		t.Fatalf("send initialize: %v", err)
	}

	var response map[string]interface{}
	if err := conn.ReadJSON(&response); err != nil {
		t.Fatalf("read initialize: %v", err)
	}
	result, _ := response["result"].(map[string]interface{})
	capabilities, _ := result["capabilities"].(map[string]interface{})
	for capability := range capabilities {
		if strings.Contains(strings.ToLower(capability), "deployment") {
			t.Fatalf("CI/CD must enter doagent through Ask, not a dedicated tool: %#v", capabilities)
		}
	}
}

func TestAgentSystemPromptDoesNotRequireDeploymentScripts(t *testing.T) {
	prompt, err := os.ReadFile(filepath.Join("..", "..", "skills", "system_prompt.md"))
	if err != nil {
		t.Fatalf("read system prompt: %v", err)
	}
	text := string(prompt)
	for _, marker := range []string{
		"doagent",
		"多 Agent",
		"Skill",
		"自然语言任务",
	} {
		if !strings.Contains(text, marker) {
			t.Fatalf("system prompt must describe the Agent-native boundary %q: %s", marker, text)
		}
	}
	for _, forbidden := range []string{
		"deploy.sh",
		"build.sh",
		"buildctl --addr",
		"kubectl",
		"Harbor",
		"registry.example.com",
		"last known good Helm",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("system prompt must not hard-code orchestration detail %q: %s", forbidden, text)
		}
	}
}

func TestConfiguredDoagentURLUsesEntrypointPort(t *testing.T) {
	t.Setenv("DO_AGENT_URL", "")
	t.Setenv("DO_AGENT_PORT", "7749")
	if got := configuredDoagentURL(); got != "http://127.0.0.1:7749" {
		t.Fatalf("configured doagent URL = %q", got)
	}

	t.Setenv("DO_AGENT_URL", "http://127.0.0.1:9010")
	if got := configuredDoagentURL(); got != "http://127.0.0.1:9010" {
		t.Fatalf("explicit doagent URL = %q", got)
	}
}

func TestShellNotificationIncludesRawKind(t *testing.T) {
	t.Setenv("DOOPS_WORKSPACE_ROOT", t.TempDir())
	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	rawArgs, _ := json.Marshal(map[string]interface{}{
		"session_id": "kind-shell",
		"command":    "printf 'raw-kind\\n'",
	})
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      43,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "doops_shell",
			"arguments": json.RawMessage(rawArgs),
		},
	}); err != nil {
		t.Fatalf("call shell: %v", err)
	}

	msg := readNotification(t, conn)
	params, _ := msg["params"].(map[string]interface{})
	if params["kind"] != "raw" {
		t.Fatalf("expected raw notification kind, got %#v", params)
	}
	if params["data"] != "raw-kind\n" {
		t.Fatalf("expected shell output, got %#v", params["data"])
	}

	for {
		var response map[string]interface{}
		if err := conn.ReadJSON(&response); err != nil {
			t.Fatalf("read shell response: %v", err)
		}
		if response["id"] != float64(43) {
			continue
		}
		if errObj, ok := response["error"]; ok {
			t.Fatalf("shell returned error: %#v", errObj)
		}
		break
	}
}

func TestSubscribeDoagentSSEReturnsErrorWhenStreamEndsWithoutFinalEvent(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"partial"}}}}`)
		fmt.Fprintln(w)
	}))
	defer ts.Close()

	var progress strings.Builder
	err := subscribeDoagentSSE(t.Context(), ts.URL, "sse-no-final", func(evt notificationEvent) {
		progress.WriteString(evt.Data)
	})
	if err == nil {
		t.Fatal("expected error when SSE ends before final event")
	}
	if !strings.Contains(err.Error(), "ended before final") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := progress.String(); got != "" {
		t.Fatalf("progress mismatch: %q", got)
	}
}

func TestSubscribeDoagentSSEScopesUntaggedEventsToFreshMachineSession(t *testing.T) {
	for _, test := range []struct {
		name          string
		allowUntagged bool
		wantTypes     []string
	}{
		{
			name:          "fresh machine session accepts current untagged event",
			allowUntagged: true,
			wantTypes:     []string{"agent_message", "turn_finished"},
		},
		{
			name:          "reused session rejects untagged event",
			allowUntagged: false,
			wantTypes:     []string{"turn_finished"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/events" {
					t.Fatalf("unexpected path: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message","replay":true,"content":{"type":"text","text":"stale"}}}}`)
				fmt.Fprintln(w)
				fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message","content":{"type":"text","text":"current"}}}}`)
				fmt.Fprintln(w)
				fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"turn_finished","turnId":"turn-current","status":"completed","stopReason":"end_turn"}}}`)
				fmt.Fprintln(w)
			}))
			defer ts.Close()

			turnIDReady := make(chan string, 1)
			turnIDReady <- "turn-current"
			close(turnIDReady)
			var gotTypes []string
			err := subscribeDoagentSSEWithCollectorReady(
				t.Context(),
				ts.URL,
				"scope-test",
				func(notificationEvent) {},
				func(update map[string]interface{}) error {
					gotTypes = append(gotTypes, fmt.Sprint(update["sessionUpdate"]))
					return nil
				},
				turnIDReady,
				test.allowUntagged,
				true,
				nil,
			)
			if err != nil {
				t.Fatalf("subscribe: %v", err)
			}
			if !reflect.DeepEqual(gotTypes, test.wantTypes) {
				t.Fatalf("collected update types = %#v, want %#v", gotTypes, test.wantTypes)
			}
		})
	}
}

func TestSubscribeDoagentSSERejectsLiveUntaggedContentOnlyToolEvidence(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"tool_call_update","replay":true,"toolCallId":"stale","toolName":"mcp_doops_helm","status":"completed","input":{"operation":"rollback"},"content":[{"type":"content","content":{"type":"text","text":"stale"}}]}}}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"tool_call_update","toolCallId":"current","toolName":"mcp_doops_helm","status":"in_progress","input":{"operation":"upgrade-install","release":"doops-agent","chart":"deploy/helm/doops-agent","namespace":"kz-ops","confirm":true}}}}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"tool_call_update","toolCallId":"current","toolName":"mcp_doops_helm","status":"completed","content":[{"type":"content","content":{"type":"text","text":"Helm operation complete."}}]}}}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"turn_finished","turnId":"turn-current","status":"completed","stopReason":"end_turn"}}}`)
		fmt.Fprintln(w)
	}))
	defer ts.Close()

	turnIDReady := make(chan string, 1)
	turnIDReady <- "turn-current"
	close(turnIDReady)
	collector := newDoagentToolTraceCollector(false)
	err := subscribeDoagentSSEWithCollectorReady(
		t.Context(),
		ts.URL,
		"apply-evidence",
		func(notificationEvent) {},
		collector.collect,
		turnIDReady,
		true,
		true,
		nil,
	)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := collector.requireCompletedToolEvidence(); err == nil ||
		!strings.Contains(err.Error(), "raw_output_missing") {
		t.Fatalf("content-only tool evidence accepted: %v", err)
	}
	if traces := collector.completed(); len(traces) != 0 {
		t.Fatalf("content-only traces accepted as completed evidence: %#v", traces)
	}
}

func TestSubscribeDoagentSSEDoesNotForwardMalformedRawData(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: Authorization: Bearer synthetic-canary`)
		fmt.Fprintln(w)
	}))
	defer ts.Close()

	var progress strings.Builder
	err := subscribeDoagentSSE(t.Context(), ts.URL, "malformed-sse", func(evt notificationEvent) {
		progress.WriteString(evt.Data)
	})
	if err == nil || !strings.Contains(err.Error(), "doagent_sse_protocol_invalid") {
		t.Fatalf("malformed SSE error = %v", err)
	}
	if strings.Contains(progress.String(), "synthetic-canary") ||
		!strings.Contains(progress.String(), "doagent_sse_protocol_invalid") {
		t.Fatalf("malformed SSE raw data leaked to progress: %q", progress.String())
	}
}

func TestSubscribeDoagentSSEReturnsErrorWhenStreamGoesIdle(t *testing.T) {
	t.Setenv("DOOPS_DOAGENT_SSE_IDLE_TIMEOUT", "20ms")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer ts.Close()

	err := subscribeDoagentSSE(t.Context(), ts.URL, "sse-idle", func(notificationEvent) {})
	if err == nil {
		t.Fatal("expected idle timeout error")
	}
	if !strings.Contains(err.Error(), "idle timeout") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSubscribeDoagentSSEReturnsTurnFinishedError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"failed-turn","update":{"sessionUpdate":"turn_finished","stopReason":"error","error":"Agent error: API error (404 Not Found): vip expired"}}}`)
		fmt.Fprintln(w)
	}))
	defer ts.Close()

	started := time.Now()
	err := subscribeDoagentSSE(t.Context(), ts.URL, "failed-turn", func(notificationEvent) {})
	if err == nil {
		t.Fatal("expected failed turn to return an error")
	}
	if !strings.Contains(err.Error(), "vip expired") {
		t.Fatalf("turn failure must preserve the actual doagent error, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("terminal turn failure must return immediately, took %s", elapsed)
	}
}

func TestSubscribeDoagentSSEWaitsForAuthoritativeFailedTurn(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message","content":{"type":"text","text":"looks good"}}}}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"usage_update","inputTokens":10,"outputTokens":5}}}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"turn_finished","status":"interrupted","stopReason":"context_limit"}}}`)
		fmt.Fprintln(w)
	}))
	defer ts.Close()

	var progress strings.Builder
	err := subscribeDoagentSSE(t.Context(), ts.URL, "interrupted-turn", func(evt notificationEvent) {
		progress.WriteString(evt.Data)
	})
	if err == nil || !strings.Contains(err.Error(), "context_limit") {
		t.Fatalf("authoritative failed turn must win over agent_message, got %v", err)
	}
	if !strings.Contains(progress.String(), "looks good") {
		t.Fatalf("agent message should remain visible as progress: %q", progress.String())
	}
}

func TestAgentPromptReturnsAdmissionErrorImmediately(t *testing.T) {
	prompted := make(chan struct{})
	doagent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rpc":
			var req map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode rpc: %v", err)
				return
			}
			switch req["method"] {
			case "session/new":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result":  map[string]interface{}{"sessionId": "admission-error"},
				})
			case "session/setMode":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result":  map[string]interface{}{},
				})
			case "session/prompt":
				close(prompted)
				w.WriteHeader(http.StatusAccepted)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"error":   map[string]interface{}{"code": -32003, "message": "queue full"},
				})
			default:
				t.Errorf("unexpected rpc method: %v", req["method"])
			}
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			<-prompted
		default:
			http.NotFound(w, r)
		}
	}))
	defer doagent.Close()
	t.Setenv("DO_AGENT_URL", doagent.URL)

	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	started := time.Now()
	result := callToolResult(t, conn, "doops_agent_prompt", map[string]interface{}{
		"session_id":  "admission-error",
		"instruction": "inspect",
	})
	if !strings.Contains(fmt.Sprint(result), "doagent_prompt_failed") ||
		strings.Contains(fmt.Sprint(result), "queue full") {
		t.Fatalf("prompt admission error must return only a stable category: %#v", result)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("prompt admission error must not wait for SSE timeout, took %s", elapsed)
	}
}

func TestAgentWebSocketCancelKillsShellCommand(t *testing.T) {
	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()

	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	rawArgs, _ := json.Marshal(map[string]interface{}{
		"command":    "sleep 30",
		"session_id": "cancel-shell",
	})
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      float64(42),
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "doops_shell",
			"arguments": json.RawMessage(rawArgs),
		},
	}); err != nil {
		t.Fatalf("send shell call: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      float64(43),
		"method":  "tools/cancel",
		"params":  map[string]interface{}{"id": float64(42)},
	}); err != nil {
		t.Fatalf("send shell cancel: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	defer conn.SetReadDeadline(time.Time{})

	var sawCancelAck bool
	for {
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read cancel response: %v", err)
		}
		if method, _ := msg["method"].(string); method != "" {
			continue
		}
		switch id, _ := msg["id"].(float64); id {
		case 43:
			result, _ := msg["result"].(map[string]interface{})
			if result["canceled"] != true {
				t.Fatalf("expected cancel ack, got %#v", msg)
			}
			sawCancelAck = true
		case 42:
			errObj, _ := msg["error"].(map[string]interface{})
			if !strings.Contains(fmt.Sprint(errObj["message"]), "operation canceled") {
				t.Fatalf("expected operation canceled, got %#v", msg)
			}
			if !sawCancelAck {
				t.Fatal("tool result arrived before cancel ack")
			}
			return
		}
	}
}

func TestAgentWebSocketRejectsBlockedShellCommand(t *testing.T) {
	t.Setenv("DOOPS_WORKSPACE_ROOT", t.TempDir())
	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()

	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	result := callToolResult(t, conn, "doops_shell", map[string]interface{}{
		"session_id": "blocked-shell",
		"command":    "rm -rf /",
	})
	if result["error"] == nil {
		t.Fatalf("expected blocked command error, got %#v", result)
	}
}

func TestWorkspaceCommitBindingRejectsDirtyAndUntrackedContent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	auditRoot := filepath.Join(t.TempDir(), "audit")
	sessionID := "immutable-workspace"
	workspace := filepath.Join(root, sessionID)
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "tracked.txt"), []byte("committed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestCommand(t, workspace, "git", "init", "-q")
	runTestCommand(t, workspace, "git", "config", "user.email", "test@example.invalid")
	runTestCommand(t, workspace, "git", "config", "user.name", "DoOps Test")
	runTestCommand(t, workspace, "git", "add", "tracked.txt")
	runTestCommand(t, workspace, "git", "commit", "-q", "-m", "workspace")
	commit := strings.TrimSpace(runTestCommand(t, workspace, "git", "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(workspace, ".doops-ready"), []byte(commit+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	auditPath, err := appendSessionAuditLogAt(auditRoot, sessionID, "release audit\n")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(auditPath) == workspace {
		t.Fatal("audit log was written into the release workspace")
	}
	if err := validateWorkspaceCommitBinding(sessionID, commit); err != nil {
		t.Fatalf("clean workspace binding rejected: %v", err)
	}
	sessionState := filepath.Join(workspace, ".agent", "sessions", "doagent-session.json")
	if err := os.MkdirAll(filepath.Dir(sessionState), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessionState, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateWorkspaceCommitBinding(sessionID, commit); err != nil {
		t.Fatalf("agent runtime session state rejected: %v", err)
	}
	agentScratch := filepath.Join(workspace, ".agent", "scratch.json")
	if err := os.WriteFile(agentScratch, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateWorkspaceCommitBinding(sessionID, commit); err == nil ||
		!strings.Contains(err.Error(), "outside the authorized commit") {
		t.Fatalf("non-session agent state was not rejected: %v", err)
	}
	if err := os.Remove(agentScratch); err != nil {
		t.Fatal(err)
	}
	untracked := filepath.Join(workspace, "untracked.txt")
	if err := os.WriteFile(untracked, []byte("untrusted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateWorkspaceCommitBinding(sessionID, commit); err == nil ||
		!strings.Contains(err.Error(), "outside the authorized commit") {
		t.Fatalf("untracked workspace content was not rejected: %v", err)
	}
	if err := os.Remove(untracked); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "tracked.txt"), []byte("modified\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateWorkspaceCommitBinding(sessionID, commit); err == nil ||
		!strings.Contains(err.Error(), "outside the authorized commit") {
		t.Fatalf("modified workspace content was not rejected: %v", err)
	}
}

func TestBareWorkspaceCommitBindingRejectsDirtyAndUntrackedContent(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	bare := filepath.Join(root, "session.git")
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "tracked.txt"), []byte("committed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".gitignore"), []byte("ignored.yaml\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestCommand(t, source, "git", "init", "-q")
	runTestCommand(t, source, "git", "config", "user.email", "test@example.invalid")
	runTestCommand(t, source, "git", "config", "user.name", "DoOps Test")
	runTestCommand(t, source, "git", "add", "tracked.txt", ".gitignore")
	runTestCommand(t, source, "git", "commit", "-q", "-m", "workspace")
	commit := strings.TrimSpace(runTestCommand(t, source, "git", "rev-parse", "HEAD"))
	runTestCommand(t, root, "git", "clone", "--bare", source, bare)
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "tracked.txt"), []byte("committed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".gitignore"), []byte("ignored.yaml\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".doops-ready"), []byte(commit+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := validateBareWorkspaceCommitBinding(workspace, bare, commit); err != nil {
		t.Fatalf("clean bare workspace binding rejected: %v", err)
	}
	sessionState := filepath.Join(workspace, ".agent", "sessions", "doagent-session.json")
	if err := os.MkdirAll(filepath.Dir(sessionState), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessionState, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateBareWorkspaceCommitBinding(workspace, bare, commit); err != nil {
		t.Fatalf("agent runtime session state rejected: %v", err)
	}
	agentScratch := filepath.Join(workspace, ".agent", "scratch.json")
	if err := os.WriteFile(agentScratch, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateBareWorkspaceCommitBinding(workspace, bare, commit); err == nil ||
		!strings.Contains(err.Error(), "outside the authorized commit") {
		t.Fatalf("non-session agent state was not rejected: %v", err)
	}
	if err := os.Remove(agentScratch); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "tracked.txt"), []byte("modified\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateBareWorkspaceCommitBinding(workspace, bare, commit); err == nil ||
		!strings.Contains(err.Error(), "outside the authorized commit") {
		t.Fatalf("modified bare workspace content was not rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "tracked.txt"), []byte("committed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "untracked.txt"), []byte("untrusted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateBareWorkspaceCommitBinding(workspace, bare, commit); err == nil ||
		!strings.Contains(err.Error(), "outside the authorized commit") {
		t.Fatalf("untracked bare workspace content was not rejected: %v", err)
	}
	if err := os.Remove(filepath.Join(workspace, "untracked.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "ignored.yaml"), []byte("untrusted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateBareWorkspaceCommitBinding(workspace, bare, commit); err == nil ||
		!strings.Contains(err.Error(), "outside the authorized commit") {
		t.Fatalf("ignored bare workspace content was not rejected: %v", err)
	}
}

func TestAgentWebSocketRejectsDockerNewlineInjection(t *testing.T) {
	t.Setenv("DOOPS_WORKSPACE_ROOT", t.TempDir())
	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()

	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	result := callToolResult(t, conn, "doops_docker", map[string]interface{}{
		"session_id": "docker",
		"command":    "ps\nid",
	})
	if result["error"] == nil {
		t.Fatalf("expected docker newline injection error, got %#v", result)
	}
}

func TestAgentWebSocketReadLimitRejectsOversizedFrame(t *testing.T) {
	t.Setenv("DOOPS_MAX_WS_MESSAGE_BYTES", "128")
	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()

	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	large := strings.Repeat("x", 512)
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      99,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "doops_shell",
			"arguments": map[string]interface{}{
				"session_id": "large",
				"command":    large,
			},
		},
	}); err != nil {
		t.Fatalf("write oversized frame: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	var msg map[string]interface{}
	if err := conn.ReadJSON(&msg); err == nil {
		t.Fatalf("expected oversized frame to close connection, got %#v", msg)
	}
}

func TestBackgroundTaskLogPathStaysInsideWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	gw := NewGateway()
	if _, err := gw.submitBgTask("bg", "true", "/tmp/outside.log"); err == nil {
		t.Fatal("expected absolute background log path to be rejected")
	}
	task, err := gw.submitBgTask("bg", "printf ok", "logs/task.log")
	if err != nil {
		t.Fatalf("submit bg task: %v", err)
	}
	if !strings.HasPrefix(filepath.Clean(task.LogPath), filepath.Join(root, "bg")+string(os.PathSeparator)) {
		t.Fatalf("log path escaped workspace: %s", task.LogPath)
	}
}

func TestWSGitResponseWriterFlushesSmallWritesImmediately(t *testing.T) {
	var bodyFrames [][]byte
	writer := newWSGitResponseWriter(float64(42), func(value interface{}) {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal message: %v", err)
		}
		var message map[string]interface{}
		if err := json.Unmarshal(data, &message); err != nil {
			t.Fatalf("decode message: %v", err)
		}
		if method, _ := message["method"].(string); method != "git/body" {
			return
		}
		params, _ := message["params"].(map[string]interface{})
		dataB64, _ := params["data_b64"].(string)
		if dataB64 == "" {
			return
		}
		body, err := base64.StdEncoding.DecodeString(dataB64)
		if err != nil {
			t.Fatalf("decode body frame: %v", err)
		}
		bodyFrames = append(bodyFrames, body)
	})

	if _, err := writer.Write([]byte("progress")); err != nil {
		t.Fatalf("write small body: %v", err)
	}
	writer.Flush()
	if len(bodyFrames) != 1 {
		t.Fatalf("expected one immediate body frame, got %d", len(bodyFrames))
	}
	if got := string(bodyFrames[0]); got != "progress" {
		t.Fatalf("unexpected body frame: %q", got)
	}
}

func TestAgentWebSocketHandlesGitHTTPOverTunnel(t *testing.T) {
	gw := NewGateway()
	gw.gitHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery, "GET /git/release.git/info/refs?service=git-upload-pack"; got != want {
			t.Fatalf("unexpected git request: want %s, got %s", want, got)
		}
		w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("git-agent-ok"))
	})
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()

	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	id := float64(42)
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "git/http",
		"params": map[string]interface{}{
			"method":         "GET",
			"path":           "/git/release.git/info/refs",
			"raw_query":      "service=git-upload-pack",
			"content_length": int64(0),
		},
	}); err != nil {
		t.Fatalf("send git/http: %v", err)
	}
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "git/body",
		"params":  map[string]interface{}{"id": id, "eof": true},
	}); err != nil {
		t.Fatalf("send git/body eof: %v", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	defer conn.SetReadDeadline(time.Time{})

	var body bytes.Buffer
	var status int
	for {
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read git response: %v", err)
		}
		if errObj, ok := msg["error"]; ok {
			t.Fatalf("git/http returned error: %#v", errObj)
		}
		if method, _ := msg["method"].(string); method != "" {
			params, _ := msg["params"].(map[string]interface{})
			switch method {
			case "git/response":
				status = int(params["status"].(float64))
			case "git/body":
				if dataB64, _ := params["data_b64"].(string); dataB64 != "" {
					data, err := base64.StdEncoding.DecodeString(dataB64)
					if err != nil {
						t.Fatalf("decode git body: %v", err)
					}
					body.Write(data)
				}
			}
			continue
		}
		if gotID, _ := msg["id"].(float64); gotID == id {
			break
		}
	}
	if status != http.StatusCreated {
		t.Fatalf("unexpected git status: %d", status)
	}
	if body.String() != "git-agent-ok" {
		t.Fatalf("unexpected git body: %q", body.String())
	}
}

func TestAgentWebSocketCancelsGitHTTPOverTunnel(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	gw := NewGateway()
	gw.gitHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(canceled)
	})
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()

	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	id := float64(44)
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "git/http",
		"params": map[string]interface{}{
			"method":         "GET",
			"path":           "/git/release.git/info/refs",
			"raw_query":      "service=git-upload-pack",
			"content_length": int64(0),
		},
	}); err != nil {
		t.Fatalf("send git/http: %v", err)
	}
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "git/body",
		"params":  map[string]interface{}{"id": id, "eof": true},
	}); err != nil {
		t.Fatalf("send git/body eof: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("git handler did not start")
	}

	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "git/cancel",
		"params":  map[string]interface{}{"id": id},
	}); err != nil {
		t.Fatalf("send git/cancel: %v", err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("git handler context was not canceled")
	}

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	defer conn.SetReadDeadline(time.Time{})
	for {
		var message map[string]interface{}
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatalf("read git/cancel response: %v", err)
		}
		if gotID, _ := message["id"].(float64); gotID != id {
			continue
		}
		result, _ := message["result"].(map[string]interface{})
		if canceled, _ := result["canceled"].(bool); !canceled {
			t.Fatalf("unexpected git/cancel response: %#v", message)
		}
		return
	}
}

func TestAgentWebSocketGitHTTPStripsExternalAuthorization(t *testing.T) {
	gw := NewGateway()
	gw.gitHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("expected external authorization to be stripped, got %q", got)
		}
		if got := r.Header.Get("X-Doops-Key"); got != "" {
			t.Fatalf("expected external gateway auth header to be stripped, got %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()

	wsURL := "ws" + ts.URL[len("http"):]
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	id := float64(43)
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "git/http",
		"params": map[string]interface{}{
			"method":         "GET",
			"path":           "/git/release.git/info/refs",
			"raw_query":      "service=git-receive-pack",
			"content_length": int64(0),
			"header": map[string][]string{
				"Authorization": {"Bearer gateway-user-token"},
				"X-Doops-Key":   {"gateway-user-token"},
			},
		},
	}); err != nil {
		t.Fatalf("send git/http: %v", err)
	}
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "git/body",
		"params":  map[string]interface{}{"id": id, "eof": true},
	}); err != nil {
		t.Fatalf("send git/body eof: %v", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	defer conn.SetReadDeadline(time.Time{})

	for {
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read git response: %v", err)
		}
		if errObj, ok := msg["error"]; ok {
			t.Fatalf("git/http returned error: %#v", errObj)
		}
		if gotID, _ := msg["id"].(float64); gotID == id {
			break
		}
	}
}

func TestFileReadRejectsLargeFiles(t *testing.T) {
	t.Setenv("DOOPS_MAX_FILE_READ_BYTES", "8")
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	path := filepath.Join(root, "large", "large.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if err := os.WriteFile(path, []byte("0123456789"), 0644); err != nil {
		t.Fatalf("write large fixture: %v", err)
	}

	_, err := handleFileRead(mustJSON(t, map[string]interface{}{"session_id": "large", "path": "large.txt"}))
	if err == nil || !strings.Contains(err.Error(), "viewing small text files only") {
		t.Fatalf("expected small-text-only error, got %v", err)
	}
}

func TestFileReadRejectsBinaryFiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	path := filepath.Join(root, "binary", "archive.tgz")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if err := os.WriteFile(path, []byte{0x1f, 0x8b, 0x08, 0x00, 0x61, 0x00}, 0644); err != nil {
		t.Fatalf("write binary fixture: %v", err)
	}

	_, err := handleFileRead(mustJSON(t, map[string]interface{}{"session_id": "binary", "path": "archive.tgz"}))
	if err == nil || !strings.Contains(err.Error(), "looks like binary data") {
		t.Fatalf("expected binary rejection, got %v", err)
	}
}

func TestWorkspacePullBundleBeginAndChunk(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	session := "pull-smoke"
	ws := filepath.Join(root, session)
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "course.tgz"), []byte("course bundle content"), 0644); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}

	beginJSON, err := handleWorkspacePullBegin(mustJSON(t, map[string]interface{}{"session_id": session}))
	if err != nil {
		t.Fatalf("pull begin: %v", err)
	}
	var begin workspacePullBeginResult
	if err := json.Unmarshal([]byte(beginJSON), &begin); err != nil {
		t.Fatalf("decode pull begin: %v", err)
	}
	if begin.BundleID == "" || begin.Size <= 0 || begin.SHA256 == "" {
		t.Fatalf("incomplete bundle metadata: %+v", begin)
	}

	chunkJSON, err := handleWorkspacePullChunk(mustJSON(t, map[string]interface{}{
		"bundle_id": begin.BundleID,
		"offset":    0,
		"limit":     32,
	}))
	if err != nil {
		t.Fatalf("pull chunk: %v", err)
	}
	var chunk workspacePullChunkResult
	if err := json.Unmarshal([]byte(chunkJSON), &chunk); err != nil {
		t.Fatalf("decode chunk: %v", err)
	}
	if chunk.BundleID != begin.BundleID || chunk.NextOffset <= 0 || chunk.DataB64 == "" {
		t.Fatalf("bad chunk metadata: %+v", chunk)
	}
	if _, err := base64.StdEncoding.DecodeString(chunk.DataB64); err != nil {
		t.Fatalf("decode chunk data: %v", err)
	}
}

func TestAgentWebSocketWorkspaceChunkUpload(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)

	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()

	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	archive := tinyTarGz(t, "hello.txt", "hello chunked push\n")
	commit := sha256Hex(archive)
	begin := callTool(t, conn, "doops_workspace_begin", map[string]interface{}{
		"session_id": "smoke",
		"commit":     commit,
	})
	chunk := base64.StdEncoding.EncodeToString(archive)
	callTool(t, conn, "doops_workspace_chunk", map[string]interface{}{
		"upload_id": begin,
		"seq":       0,
		"data_b64":  chunk,
	})
	callTool(t, conn, "doops_workspace_commit", map[string]interface{}{
		"upload_id":  begin,
		"session_id": "smoke",
		"commit":     commit,
	})

	got, err := os.ReadFile(filepath.Join(root, "smoke", "hello.txt"))
	if err != nil {
		t.Fatalf("read extracted file: %v", err)
	}
	if string(got) != "hello chunked push\n" {
		t.Fatalf("extracted content mismatch: %q", got)
	}
	ready, err := os.ReadFile(filepath.Join(root, "smoke", ".doops-ready"))
	if err != nil {
		t.Fatalf("read ready file: %v", err)
	}
	if string(bytes.TrimSpace(ready)) != commit {
		t.Fatalf("ready commit mismatch: %q", ready)
	}
}

func TestWorkspaceUploadRejectsOutOfOrderChunks(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)

	archive := tinyTarGz(t, "hello.txt", "ordered\n")
	commit := sha256Hex(archive)
	uploadID, err := handleWorkspaceBegin(mustJSON(t, map[string]interface{}{
		"session_id": "ordered",
		"commit":     commit,
	}))
	if err != nil {
		t.Fatalf("begin upload: %v", err)
	}

	_, err = handleWorkspaceChunk(mustJSON(t, map[string]interface{}{
		"upload_id": uploadID,
		"seq":       1,
		"data_b64":  base64.StdEncoding.EncodeToString(archive),
	}))
	if err == nil || !strings.Contains(err.Error(), "unexpected chunk sequence") {
		t.Fatalf("expected out-of-order chunk error, got %v", err)
	}
}

func TestWorkspaceCommitRejectsSHA256MismatchAndCleansUpload(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)

	archive := tinyTarGz(t, "hello.txt", "checksum\n")
	wrongCommit := strings.Repeat("0", 64)
	uploadID, err := handleWorkspaceBegin(mustJSON(t, map[string]interface{}{
		"session_id": "checksum",
		"commit":     wrongCommit,
	}))
	if err != nil {
		t.Fatalf("begin upload: %v", err)
	}
	if _, err := handleWorkspaceChunk(mustJSON(t, map[string]interface{}{
		"upload_id": uploadID,
		"seq":       0,
		"data_b64":  base64.StdEncoding.EncodeToString(archive),
	})); err != nil {
		t.Fatalf("chunk upload: %v", err)
	}

	_, err = handleWorkspaceCommit(mustJSON(t, map[string]interface{}{
		"upload_id":  uploadID,
		"session_id": "checksum",
		"commit":     wrongCommit,
	}))
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("expected sha256 mismatch error, got %v", err)
	}
	if _, statErr := os.Stat(uploadArchivePath(uploadID)); !os.IsNotExist(statErr) {
		t.Fatalf("expected failed archive to be cleaned, stat err=%v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(root, "checksum")); !os.IsNotExist(statErr) {
		t.Fatalf("expected destination not to be created on checksum failure, stat err=%v", statErr)
	}
}

func TestWorkspaceUploadRejectsSizeLimit(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("DOOPS_MAX_WORKSPACE_UPLOAD_BYTES", "8")

	uploadID, err := handleWorkspaceBegin(mustJSON(t, map[string]interface{}{
		"session_id": "too-big",
		"commit":     strings.Repeat("1", 64),
	}))
	if err != nil {
		t.Fatalf("begin upload: %v", err)
	}
	_, err = handleWorkspaceChunk(mustJSON(t, map[string]interface{}{
		"upload_id": uploadID,
		"seq":       0,
		"data_b64":  base64.StdEncoding.EncodeToString([]byte("123456789")),
	}))
	if err == nil || !strings.Contains(err.Error(), "upload exceeds") {
		t.Fatalf("expected upload limit error, got %v", err)
	}
}

func TestWorkspaceCommitRejectsExpandedSizeLimit(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("DOOPS_WORKSPACE_ROOT", t.TempDir())
	t.Setenv("DOOPS_MAX_WORKSPACE_UPLOAD_BYTES", "512")

	archive := tinyTarGz(t, "large.txt", strings.Repeat("A", 4096))
	commit := sha256Hex(archive)
	uploadID, err := handleWorkspaceBegin(mustJSON(t, map[string]interface{}{
		"session_id": "expanded",
		"commit":     commit,
	}))
	if err != nil {
		t.Fatalf("begin upload: %v", err)
	}
	if _, err := handleWorkspaceChunk(mustJSON(t, map[string]interface{}{
		"upload_id": uploadID,
		"seq":       0,
		"data_b64":  base64.StdEncoding.EncodeToString(archive),
	})); err != nil {
		t.Fatalf("chunk upload: %v", err)
	}

	_, err = handleWorkspaceCommit(mustJSON(t, map[string]interface{}{
		"upload_id":  uploadID,
		"session_id": "expanded",
		"commit":     commit,
	}))
	if err == nil || !strings.Contains(err.Error(), "extracted workspace exceeds") {
		t.Fatalf("expected expanded size limit error, got %v", err)
	}
	if _, statErr := os.Stat(uploadArchivePath(uploadID)); !os.IsNotExist(statErr) {
		t.Fatalf("expected failed archive to be cleaned, stat err=%v", statErr)
	}
}

func TestCleanupExpiredWorkspaceUploads(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	uploadID := "upl_" + strings.Repeat("a", 32)
	if err := os.MkdirAll(uploadDir(), 0700); err != nil {
		t.Fatalf("mkdir upload dir: %v", err)
	}
	if err := os.WriteFile(uploadArchivePath(uploadID), []byte("stale"), 0600); err != nil {
		t.Fatalf("write stale archive: %v", err)
	}
	if err := saveUploadMeta(uploadID, workspaceUploadMeta{
		SessionID: "stale",
		Commit:    strings.Repeat("2", 64),
		NextSeq:   0,
		Size:      0,
		CreatedAt: time.Now().UTC().Add(-48 * time.Hour),
	}); err != nil {
		t.Fatalf("write stale meta: %v", err)
	}

	if err := cleanupExpiredUploads(24 * time.Hour); err != nil {
		t.Fatalf("cleanup expired uploads: %v", err)
	}
	if _, err := os.Stat(uploadArchivePath(uploadID)); !os.IsNotExist(err) {
		t.Fatalf("expected stale archive removed, stat err=%v", err)
	}
	if _, err := os.Stat(uploadMetaPath(uploadID)); !os.IsNotExist(err) {
		t.Fatalf("expected stale metadata removed, stat err=%v", err)
	}
}

func dialAgentTestWS(t *testing.T, serverURL string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + serverURL[len("http"):]
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	return conn
}

func initializeAgentTestWS(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
	}); err != nil {
		t.Fatalf("send initialize: %v", err)
	}
	var resp map[string]interface{}
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("read initialize: %v", err)
	}
	result, ok := resp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("initialize failed: %#v", resp)
	}
	if strings.TrimSpace(fmt.Sprint(result["runtimeId"])) == "" {
		t.Fatalf("initialize result must identify the agent process: %#v", resp)
	}
	capabilities, _ := result["capabilities"].(map[string]interface{})
	protocols, _ := capabilities["protocols"].(map[string]interface{})
	if !credentialPlanCapabilityCompatible(protocols["credentialPlan"]) {
		t.Fatalf("initialize result must advertise credential plan v2: %#v", resp)
	}
}

func callTool(t *testing.T, conn *websocket.Conn, tool string, args map[string]interface{}) string {
	t.Helper()
	msg := callToolResult(t, conn, tool, args)
	if errObj, ok := msg["error"]; ok {
		t.Fatalf("%s returned error: %#v", tool, errObj)
	}
	result, _ := msg["result"].(map[string]interface{})
	if isErr, ok := result["isError"]; ok && isErr == true {
		t.Fatalf("%s returned tool error: %#v", tool, result)
	}
	content, _ := result["content"].([]interface{})
	if len(content) == 0 {
		return ""
	}
	item, _ := content[0].(map[string]interface{})
	text, _ := item["text"].(string)
	return text
}

func callToolResult(t *testing.T, conn *websocket.Conn, tool string, args map[string]interface{}) map[string]interface{} {
	t.Helper()
	rawArgs, _ := json.Marshal(args)
	id := float64(len(tool) + len(rawArgs))
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      tool,
			"arguments": json.RawMessage(rawArgs),
		},
	}); err != nil {
		t.Fatalf("call %s: %v", tool, err)
	}
	for {
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read %s response: %v", tool, err)
		}
		if _, ok := msg["method"]; ok {
			continue
		}
		return msg
	}
}

func readNotification(t *testing.T, conn *websocket.Conn) map[string]interface{} {
	t.Helper()
	for {
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read notification: %v", err)
		}
		if method, _ := msg["method"].(string); method == "notifications/message" {
			return msg
		}
	}
}

func tinyTarGz(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	data := []byte(content)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(data))}); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatalf("write tar data: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func mustJSON(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return data
}

func TestSanitizeSecretTextRedactsCredentialFields(t *testing.T) {
	raw := "authorization=Bearer synthetic cookie=session-canary dbPassword=database-canary githubToken=token-canary " +
		"https://user:url-canary@example.test/repo.git?access_token=query-canary Basic YmFzaWMtY2FuYXJ5 " +
		"https://storage.example.test/object?X-Goog-Signature=signature-canary&X-Amz-Security-Token=security-token-canary&githubToken=github-query-canary " +
		"ghp_" + strings.Repeat("a", 40) + " github_pat_" + strings.Repeat("b", 82) + " AKIA" + strings.Repeat("A", 16) + " " +
		"-----BEGIN PRIVATE KEY-----\npem-canary\n-----END PRIVATE KEY-----"
	sanitized := sanitizeSecretText(raw)
	for _, secret := range []string{
		"synthetic", "session-canary", "database-canary", "token-canary",
		"url-canary", "query-canary", "signature-canary", "security-token-canary",
		"github-query-canary", "YmFzaWMtY2FuYXJ5", "pem-canary",
		"ghp_", "github_pat_", "AKIA",
	} {
		if strings.Contains(sanitized, secret) {
			t.Fatalf("sanitized text retained %q: %s", secret, sanitized)
		}
	}
}

func TestMarshalDoagentHistorySanitizesNestedMessages(t *testing.T) {
	history, err := marshalDoagentHistory("history-session", []interface{}{
		map[string]interface{}{
			"role":  "assistant",
			"token": "opaque-history-canary",
			"content": []interface{}{
				map[string]interface{}{"text": "https://storage.example.test/object?signature=history-canary"},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal history: %v", err)
	}
	if strings.Contains(history, "history-canary") ||
		strings.Contains(history, "opaque-history-canary") ||
		!strings.Contains(history, "REDACTED") {
		t.Fatalf("history was not sanitized: %s", history)
	}
}

func TestSubscribeDoagentSSESanitizesAgentMessagesBeforeCollectorAndProgress(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"https://storage.example.test/object?X-Amz-Signature="}}}}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"stream-canary"}}}}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"turn_finished","status":"completed","stopReason":"end_turn"}}}`)
		fmt.Fprintln(w)
	}))
	defer ts.Close()

	var progress strings.Builder
	var collected string
	err := subscribeDoagentSSEWithCollector(
		t.Context(),
		ts.URL,
		"sse-sanitize",
		func(evt notificationEvent) { progress.WriteString(evt.Data) },
		func(update map[string]interface{}) error {
			data, _ := json.Marshal(update)
			collected += string(data)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("subscribe sanitized SSE: %v", err)
	}
	if strings.Contains(progress.String(), "stream-canary") ||
		strings.Contains(collected, "stream-canary") ||
		!strings.Contains(progress.String(), "REDACTED") {
		t.Fatalf("raw agent message escaped sanitization: progress=%q collector=%q", progress.String(), collected)
	}
}

func TestSubscribeDoagentSSEDefersStructuredAgentMessage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message","content":{"type":"text","text":"{\"kind\":\"DeploymentRun\",\"subject\":\"https://storage.example.test/object?X-Amz-Signature=stream-canary\"}"}}}}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"turn_finished","status":"completed","stopReason":"end_turn"}}}`)
		fmt.Fprintln(w)
	}))
	defer ts.Close()

	var progress strings.Builder
	var collected []string
	err := subscribeDoagentSSEWithCollectorReady(
		t.Context(),
		ts.URL,
		"structured-no-forward",
		func(evt notificationEvent) { progress.WriteString(evt.Data) },
		func(update map[string]interface{}) error {
			collected = append(collected, fmt.Sprint(update["sessionUpdate"]))
			return nil
		},
		nil,
		false,
		false,
		nil,
	)
	if err != nil {
		t.Fatalf("subscribe structured SSE: %v", err)
	}
	if strings.Contains(progress.String(), "DeploymentRun") ||
		strings.Contains(progress.String(), "stream-canary") {
		t.Fatalf("unvalidated structured result was forwarded: %s", progress.String())
	}
	if !reflect.DeepEqual(collected, []string{"agent_message", "turn_finished"}) {
		t.Fatalf("structured updates were not collected: %#v", collected)
	}
}
