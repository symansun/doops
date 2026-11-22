package server

import (
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestAgentTunnelReadTimeoutDefaultAndOverride(t *testing.T) {
	t.Setenv("DOOPS_TUNNEL_READ_TIMEOUT", "")
	if got := agentTunnelReadTimeout(); got != 90*time.Second {
		t.Fatalf("default read timeout = %s, want 90s", got)
	}

	t.Setenv("DOOPS_TUNNEL_READ_TIMEOUT", "37s")
	if got := agentTunnelReadTimeout(); got != 37*time.Second {
		t.Fatalf("override read timeout = %s, want 37s", got)
	}

	// Non-positive or unparseable values keep the default instead of failing open.
	for _, raw := range []string{"0s", "-5s", "not-a-duration"} {
		t.Setenv("DOOPS_TUNNEL_READ_TIMEOUT", raw)
		if got := agentTunnelReadTimeout(); got != defaultAgentTunnelReadTimeout {
			t.Fatalf("%q read timeout = %s, want the 90s default", raw, got)
		}
	}
}

// shortAgentTunnel makes every probe below use a short read timeout.
func shortAgentTunnel(t *testing.T, timeout time.Duration) {
	t.Helper()
	t.Setenv("DOOPS_TUNNEL_READ_TIMEOUT", timeout.String())
}

// serveRawAgentTunnel serves one Agent tunnel connection from a helper
// Gateway whose hub is not needed by these transport-level probes.
func serveRawAgentTunnel(t *testing.T) (*httptest.Server, *websocket.Conn) {
	t.Helper()
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	gw := &Gateway{}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	started := make(chan struct{})
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		close(started)
		gw.ServeGatewayWebSocketConnWithReady(conn, "probe", func() {})
		close(done)
	}))
	t.Cleanup(server.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial tunnel: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	<-started
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("agent tunnel did not stop")
		}
	})
	return server, conn
}

func TestAgentTunnelReadTimeoutKeepsAliveConnectionOpen(t *testing.T) {
	// A healthy Agent answers the Gateway's pings and sends frames. A read
	// timeout far below the fixed 5s ping interval only stays open if every
	// frame refreshes the deadline.
	shortAgentTunnel(t, 800*time.Millisecond)
	logs := captureLogs(t)
	_, conn := serveRawAgentTunnel(t)

	// The Agent keeps reading (and thereby answering pings) for longer than the
	// configured read timeout would allow a silent connection to live.
	readDone := make(chan error, 1)
	go func() {
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				readDone <- err
				return
			}
		}
	}()
	for i := 0; i < 4; i++ {
		time.Sleep(500 * time.Millisecond)
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"jsonrpc":"2.0","method":"heartbeat"}`)); err != nil {
			select {
			case readErr := <-readDone:
				t.Fatalf("connection stopped after %d heartbeats: %v", i, readErr)
			default:
				t.Fatalf("connection stopped after %d heartbeats: %v", i, err)
			}
		}
		if strings.Contains(logs.String(), "WS Client Disconnected") {
			t.Fatalf("tunnel closed despite heartbeats:\n%s", logs.String())
		}
	}
	// 2s of conversation outlived the 800ms read timeout several times over.
	if strings.Contains(logs.String(), "WS Client Disconnected") {
		t.Fatalf("tunnel closed despite heartbeats:\n%s", logs.String())
	}
}

func TestAgentTunnelReadTimeoutClosesSilentConnection(t *testing.T) {
	shortAgentTunnel(t, 400*time.Millisecond)
	_, conn := serveRawAgentTunnel(t)

	// Never answer anything: the connection must close at about the configured
	// value, far sooner than the 90s lease.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	start := time.Now()
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
	if elapsed := time.Since(start); elapsed < 300*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("silent tunnel closed after %s, want about 400ms", elapsed)
	}
}

// captureLogs redirects the standard logger for the duration of a test.
func captureLogs(t *testing.T) *strings.Builder {
	t.Helper()
	var buf strings.Builder
	previous := log.Writer()
	log.SetOutput(&safeWriter{w: &buf})
	t.Cleanup(func() { log.SetOutput(previous) })
	return &buf
}

type safeWriter struct {
	mu sync.Mutex
	w  *strings.Builder
}

func (s *safeWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

func (s *safeWriter) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.String()
}

func TestAgentTunnelInFlightToolsCountedOnDisconnect(t *testing.T) {
	shortAgentTunnel(t, 500*time.Millisecond)
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	logs := captureLogs(t)
	_, conn := serveRawAgentTunnel(t)

	// The command passes validation and fails fast inside the handler: the
	// handler's only write cannot complete because the client stops reading,
	// so the call is still in flight when the read timeout closes the tunnel.
	script := filepath.Join(root, "probe.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]interface{}{"name": "doops_shell", "arguments": map[string]interface{}{"session_id": "probe", "command": script, "workdir": root}},
	}); err != nil {
		t.Fatalf("write probe call: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}

	requireEventually(t, 3*time.Second, func() bool {
		return strings.Contains(logs.String(), "[agent] tunnel disconnected; ending")
	})
	got := logs.String()
	if !strings.Contains(got, "[agent] tunnel disconnected; ending 1 in-flight executions after the read timeout") {
		t.Fatalf("unexpected disconnect log:\n%s", got)
	}
	// Only the count is logged, never command text or tool arguments.
	if strings.Contains(got, "probe.sh") || strings.Contains(got, `"session_id"`) {
		t.Fatalf("disconnect log leaked call content:\n%s", got)
	}
}

func TestAgentTunnelDisconnectWithoutPendingWorkLogsNoCount(t *testing.T) {
	shortAgentTunnel(t, 300*time.Millisecond)
	logs := captureLogs(t)
	_, conn := serveRawAgentTunnel(t)

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
	requireEventually(t, 3*time.Second, func() bool {
		return strings.Contains(logs.String(), "WS Client Disconnected")
	})
	if strings.Contains(logs.String(), "in-flight executions") {
		t.Fatalf("idle disconnect logged in-flight work:\n%s", logs.String())
	}
}
