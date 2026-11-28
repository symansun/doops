package gatewaycmd

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/user/doops/agent/internal/server"
)

// TestGatewayServeTimeoutTableReachesStartupLogs proves the environment values
// flow through the timeout table into the real serve process: the monitor
// runner and the offline reaper log the configured ticks.
func TestGatewayServeTimeoutTableReachesStartupLogs(t *testing.T) {
	t.Setenv("DOOPS_MONITOR_TICK", "1s")
	t.Setenv("DOOPS_OFFLINE_REAPER_INTERVAL", "2s")
	t.Setenv("DOOPS_OFFLINE_INSTANCE_RETENTION", "90s")

	port := freeGatewayLockTestPort(t)
	child := startGatewayLockHelper(t, gatewayServeTestArgs(filepath.Join(t.TempDir(), "gateway.db"), port)...)
	waitGatewayLockTestHealth(t, child, port)

	deadline := time.Now().Add(3 * time.Second)
	for {
		logs := child.output.String()
		if strings.Contains(logs, "[gateway] periodic monitor & inspection runner started (tick=1s)") &&
			strings.Contains(logs, "retention=1m30s, interval=2s") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("startup logs did not reflect the timeout table:\n%s", logs)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestGatewayServeShutdownDrainTimeoutBoundsShutdown proves -shutdown-drain-timeout
// bounds the real drain: one in-flight operation that never completes releases
// the process after roughly one second instead of the two-minute default.
func TestGatewayServeShutdownDrainTimeoutBoundsShutdown(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	store, err := server.OpenGatewayStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.CreateUser("drain-flag")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.GrantUser(user.ID, server.ScopeGrant{Cluster: "dev", Instance: "local"}); err != nil {
		t.Fatal(err)
	}
	token, err := store.CreateToken(server.CreateTokenRequest{Kind: server.TokenKindUser, UserID: user.ID, Name: "drain-flag"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	port := freeGatewayLockTestPort(t)
	child := startGatewayLockHelper(t,
		append(gatewayServeTestArgs(dbPath, port), "-shutdown-drain-timeout", "1s", "-allow-open-agent-registration")...)
	waitGatewayLockTestHealth(t, child, port)

	received := make(chan struct{}, 1)
	go dialSilentFlagTestAgent(t, fmt.Sprintf("ws://127.0.0.1:%d/v1/agent/connect?cluster=dev&instance=local", port), received)

	conn, _, err := websocket.DefaultDialer.Dial(
		fmt.Sprintf("ws://127.0.0.1:%d/v1/rpc?cluster=dev&instance=local", port),
		http.Header{"Authorization": []string{"Bearer " + token.Plaintext}})
	if err != nil {
		t.Fatalf("dial client: %v", err)
	}
	defer conn.Close()
	_ = conn.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
	var init map[string]interface{}
	if err := conn.ReadJSON(&init); err != nil {
		t.Fatalf("read initialize: %v", err)
	}
	_ = conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]interface{}{"name": "doops_web", "arguments": map[string]interface{}{"session_id": "drain", "operation": "site-list"}},
	})
	select {
	case <-received:
	case <-time.After(5 * time.Second):
		t.Fatalf("the Agent never received the in-flight tools/call:\n%s", child.output.String())
	}

	started := time.Now()
	if err := child.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal: %v", err)
	}
	select {
	case <-child.done:
	case <-time.After(5 * time.Second):
		t.Fatalf("gateway did not exit within 5s of a 1s drain budget:\n%s", child.output.String())
	}
	elapsed := time.Since(started)
	logs := child.output.String()
	if !strings.Contains(logs, "[gateway] draining: refusing new operations, waiting up to 1s for 1 active") {
		t.Fatalf("drain did not announce the 1s budget and 1 active operation:\n%s", logs)
	}
	if !strings.Contains(logs, "[gateway] drain timed out with 1 operations still running") {
		t.Fatalf("drain did not hit its bound:\n%s", logs)
	}
	if elapsed > 4*time.Second {
		t.Fatalf("shutdown took %s; the 1s drain budget was not applied", elapsed)
	}
}

// dialSilentFlagTestAgent registers an Agent that completes the handshake but
// never answers tools/call, keeping one Gateway operation in flight.
func dialSilentFlagTestAgent(t *testing.T, url string, received chan<- struct{}) {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Errorf("dial agent: %v", err)
		return
	}
	stop := make(chan struct{})
	t.Cleanup(func() {
		close(stop)
		_ = conn.Close()
	})
	go func() {
		select {
		case <-t.Context().Done():
			_ = conn.Close()
		case <-stop:
		}
	}()
	for {
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		switch msg["method"] {
		case "initialize":
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0", "id": msg["id"],
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"runtimeId":       "silent-agent",
					"serverInfo":      map[string]interface{}{"name": "silent", "version": "test"},
					"capabilities":    map[string]interface{}{"protocols": []interface{}{"2024-11-05"}},
				},
			})
		case "tools/call":
			select {
			case received <- struct{}{}:
			default:
			}
		}
	}
}

// TestGatewayServeDurationsComeFromTheTimeoutTable is the source guard: every
// runtime duration this command hands to the server must be read from the
// timeout table instead of a literal.
func TestGatewayServeDurationsComeFromTheTimeoutTable(t *testing.T) {
	source, err := os.ReadFile("gatewaycmd.go")
	if err != nil {
		t.Fatalf("read gatewaycmd.go: %v", err)
	}
	expressions := map[string]string{
		"operation-timeout":      "server.OperationTimeout()",
		"detached-task-timeout":  "server.DetachedTaskTimeout()",
		"target-reconnect-grace": "server.TargetReconnectGrace()",
		"shutdown-drain-timeout": "server.ShutdownDrainTimeout()",
		"monitor-tick":           "server.MonitorTick()",
	}
	for flag, want := range expressions {
		needle := `fs.Duration("` + flag + `",`
		start := strings.Index(string(source), needle)
		if start < 0 {
			t.Fatalf("flag %q is no longer declared with fs.Duration", flag)
		}
		rest := string(source)[start+len(needle):]
		comma := strings.Index(rest, ",")
		if comma < 0 {
			t.Fatalf("flag %q declaration has no default terminator", flag)
		}
		if got := strings.TrimSpace(rest[:comma]); got != want {
			t.Fatalf("flag %q defaults to %q, want %q (the timeout table)", flag, got, want)
		}
	}
	for _, want := range []string{
		"hub.StartOfflineInstanceReaper(server.OfflineReaperInterval())",
		"server.HTTPReadHeaderTimeout()",
		"server.HTTPShutdownTimeout()",
		"server.HTTPIdleTimeout()",
	} {
		if !strings.Contains(string(source), want) {
			t.Fatalf("gatewaycmd.go no longer wires %q from the timeout table", want)
		}
	}
}
