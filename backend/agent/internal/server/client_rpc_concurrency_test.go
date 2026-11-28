package server

import (
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGatewayRPCRunsSameSessionShellConcurrentlyAndFencesWorkspace(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	user, _ := store.CreateUser("alice")
	userToken, _ := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "alice"})
	if err := store.GrantUser(user.ID, ScopeGrant{Cluster: "dev", Instance: "local"}); err != nil {
		t.Fatalf("grant exec: %v", err)
	}

	gw := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, MaxQueuedPerTarget: -1, AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	gw.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	agentConn := dialFakeGatewayAgent(t, ts.URL, "dev", "local")
	defer agentConn.Close()
	release := make(chan struct{})
	started := make(chan string, 4)
	go serveSessionAwareBlockingFakeAgent(t, agentConn, started, release)

	clientA := dialGatewayRPCClient(t, ts.URL, userToken.Plaintext, "dev", "local")
	defer clientA.Close()
	clientB := dialGatewayRPCClient(t, ts.URL, userToken.Plaintext, "dev", "local")
	defer clientB.Close()
	clientC := dialGatewayRPCClient(t, ts.URL, userToken.Plaintext, "dev", "local")
	defer clientC.Close()

	call := func(conn *websocket.Conn, tool string, args map[string]interface{}) {
		raw, _ := json.Marshal(args)
		writeGatewayJSON(t, conn, map[string]interface{}{
			"jsonrpc": "2.0", "id": 2, "method": "tools/call",
			"params": map[string]interface{}{"name": tool, "arguments": json.RawMessage(raw)},
		})
	}
	call(clientA, "doops_shell", map[string]interface{}{"command": "sleep", "session_id": "busy"})
	call(clientB, "doops_shell", map[string]interface{}{"command": "sleep", "session_id": "busy"})
	// Barrier: both same-session shell calls reach the Agent before either is released.
	for i := 0; i < 2; i++ {
		select {
		case session := <-started:
			if session != "busy" {
				t.Fatalf("unexpected dispatched session %q", session)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("same-session shell call %d was serialized instead of dispatched", i+1)
		}
	}
	call(clientC, "doops_file_write", map[string]interface{}{"path": "config.txt", "content": "x", "session_id": "busy"})
	fenced := readUntilID(t, clientC, 2)
	if errObj, ok := fenced["error"].(map[string]interface{}); !ok || !strings.Contains(fmt.Sprint(errObj["message"]), "target busy") {
		t.Fatalf("same-session workspace write must wait for running shell calls, got %#v", fenced)
	}
	select {
	case session := <-started:
		t.Fatalf("fenced workspace write reached the Agent: %q", session)
	default:
	}
	close(release)
	for _, conn := range []*websocket.Conn{clientA, clientB} {
		if done := readUntilID(t, conn, 2); done["error"] != nil {
			t.Fatalf("concurrent shell call failed: %#v", done)
		}
	}
}

func TestGatewayRPCAllowsConcurrentStructuredWorkspacesForSameTarget(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	user, _ := store.CreateUser("alice")
	userToken, _ := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "alice"})
	if err := store.GrantUser(user.ID, ScopeGrant{Cluster: "dev", Instance: "local"}); err != nil {
		t.Fatalf("grant write: %v", err)
	}

	gw := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	gw.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	agentConn := dialFakeGatewayAgent(t, ts.URL, "dev", "local")
	defer agentConn.Close()
	release := make(chan struct{})
	started := make(chan string, 2)
	go serveSessionAwareBlockingFakeAgent(t, agentConn, started, release)

	clientA := dialGatewayRPCClient(t, ts.URL, userToken.Plaintext, "dev", "local")
	defer clientA.Close()
	clientB := dialGatewayRPCClient(t, ts.URL, userToken.Plaintext, "dev", "local")
	defer clientB.Close()

	argsA, _ := json.Marshal(map[string]interface{}{"path": "config.txt", "content": "a", "session_id": "session-a"})
	argsB, _ := json.Marshal(map[string]interface{}{"path": "config.txt", "content": "b", "session_id": "session-b"})
	writeGatewayJSON(t, clientA, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params":  map[string]interface{}{"name": "doops_file_write", "arguments": json.RawMessage(argsA)},
	})
	writeGatewayJSON(t, clientB, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params":  map[string]interface{}{"name": "doops_file_write", "arguments": json.RawMessage(argsB)},
	})

	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case got := <-started:
			seen[got] = true
		case <-time.After(2 * time.Second):
			t.Fatal("expected both structured workspaces to reach the agent concurrently")
		}
	}
	if !seen["session-a"] || !seen["session-b"] {
		t.Fatalf("started sessions = %#v, want both session-a and session-b", seen)
	}
	close(release)

	for name, client := range map[string]*websocket.Conn{"client A": clientA, "client B": clientB} {
		msg := readGatewayNotification(t, client)
		if got := notificationData(msg); !strings.Contains(got, "session-") {
			t.Fatalf("%s got wrong notification: %#v", name, msg)
		}
		if resp := readUntilID(t, client, 2); resp["error"] != nil {
			t.Fatalf("%s final response failed: %#v", name, resp)
		}
	}
}

func TestGatewayRPCAllowsConcurrentCallsOnOneClientConnection(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	user, _ := store.CreateUser("alice")
	userToken, _ := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "alice"})
	if err := store.GrantUser(user.ID, ScopeGrant{Cluster: "dev", Instance: "local"}); err != nil {
		t.Fatalf("grant info: %v", err)
	}

	gw := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	gw.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	agentConn := dialFakeGatewayAgent(t, ts.URL, "dev", "local")
	defer agentConn.Close()
	release := make(chan struct{})
	started := make(chan int, 2)
	go serveConcurrentBlockingFakeAgent(t, agentConn, started, release)
	// doops_node_info refuses to wait for a reconnect grace, so the Agent must
	// already be registered before the calls are written; otherwise both fail
	// as target offline and the assertion below would be about the race, not
	// about concurrency.
	waitForGatewayAgent(t, gw, "dev", "local")

	client := dialGatewayRPCClient(t, ts.URL, userToken.Plaintext, "dev", "local")
	defer client.Close()
	args, _ := json.Marshal(map[string]interface{}{"session_id": "same-session"})
	for _, id := range []int{2, 3} {
		writeGatewayJSON(t, client, map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"method":  "tools/call",
			"params":  map[string]interface{}{"name": "doops_node_info", "arguments": json.RawMessage(args)},
		})
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("same client connection serialized concurrent read-only calls")
		}
	}
	state := gw.registry.Get("dev", "local").snapshot()
	if state.ActiveOps != 2 || len(state.Sessions) != 1 || state.Sessions[0] != "same-session" {
		t.Fatalf("same-session concurrent state was not counted accurately: %#v", state)
	}
	close(release)
	seen := map[float64]bool{}
	for i := 0; i < 2; i++ {
		response := readGatewayMessage(t, client)
		id, ok := response["id"].(float64)
		if !ok || (id != 2 && id != 3) {
			t.Fatalf("unexpected response: %#v", response)
		}
		if response["error"] != nil {
			t.Fatalf("request %v failed: %#v", id, response)
		}
		seen[id] = true
	}
	if !seen[2] || !seen[3] {
		t.Fatalf("missing concurrent responses: %#v", seen)
	}
}

func TestGatewayRPCAllowsShellMutationsAcrossSessions(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	user, _ := store.CreateUser("alice")
	userToken, _ := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "alice"})
	if err := store.GrantUser(user.ID, ScopeGrant{Cluster: "dev", Instance: "local"}); err != nil {
		t.Fatalf("grant exec: %v", err)
	}

	gw := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	gw.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	agentConn := dialFakeGatewayAgent(t, ts.URL, "dev", "local")
	defer agentConn.Close()
	release := make(chan struct{})
	started := make(chan string, 2)
	go serveSessionAwareBlockingFakeAgent(t, agentConn, started, release)

	clientA := dialGatewayRPCClient(t, ts.URL, userToken.Plaintext, "dev", "local")
	defer clientA.Close()
	clientB := dialGatewayRPCClient(t, ts.URL, userToken.Plaintext, "dev", "local")
	defer clientB.Close()

	argsA, _ := json.Marshal(map[string]interface{}{"command": "kubectl apply -f app.yaml", "session_id": "session-a"})
	argsB, _ := json.Marshal(map[string]interface{}{"command": "kubectl apply -f app.yaml", "session_id": "session-b"})
	writeGatewayJSON(t, clientA, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params":  map[string]interface{}{"name": "doops_shell", "arguments": json.RawMessage(argsA)},
	})
	writeGatewayJSON(t, clientB, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params":  map[string]interface{}{"name": "doops_shell", "arguments": json.RawMessage(argsB)},
	})

	startedSessions := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case session := <-started:
			startedSessions[session] = true
		case <-time.After(2 * time.Second):
			t.Fatal("expected both sessions to reach the agent concurrently")
		}
	}
	if !startedSessions["session-a"] || !startedSessions["session-b"] {
		t.Fatalf("unexpected started sessions: %#v", startedSessions)
	}
	close(release)
	for name, client := range map[string]*websocket.Conn{"session-a": clientA, "session-b": clientB} {
		notification := readGatewayNotification(t, client)
		if got := notificationData(notification); !strings.Contains(got, name) {
			t.Fatalf("%s client got wrong notification: %#v", name, notification)
		}
		if resp := readUntilID(t, client, 2); resp["error"] != nil {
			t.Fatalf("%s client final response failed: %#v", name, resp)
		}
	}
}

func TestGatewayRPCAllowsConcurrentReadOnlyOperationsForSameTarget(t *testing.T) {
	for _, request := range []struct {
		action GatewayAction
		tool   string
	}{
		{ActionInfo, "doops_node_info"},
		{ActionRead, "doops_file_read"},
		{ActionRead, "doops_workspace_read"},
	} {
		t.Run(request.tool, func(t *testing.T) {
			testGatewayConcurrentReadOnlyRPC(t, request.action, request.tool)
		})
	}
}

func testGatewayConcurrentReadOnlyRPC(t *testing.T, action GatewayAction, tool string) {
	t.Helper()
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	user, _ := store.CreateUser("alice")
	userToken, _ := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "alice"})
	if err := store.GrantUser(user.ID, ScopeGrant{Cluster: "dev", Instance: "local"}); err != nil {
		t.Fatalf("grant %s: %v", action, err)
	}

	gw := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	gw.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	agentConn := dialFakeGatewayAgent(t, ts.URL, "dev", "local")
	defer agentConn.Close()
	release := make(chan struct{})
	started := make(chan int, 2)
	go serveConcurrentBlockingFakeAgent(t, agentConn, started, release)
	// doops_node_info, doops_task_status and the credential control tools do not
	// wait for a reconnect grace, so these tests must observe a registered Agent
	// before dispatching.
	waitForGatewayAgent(t, gw, "dev", "local")

	clientA := dialGatewayRPCClient(t, ts.URL, userToken.Plaintext, "dev", "local")
	defer clientA.Close()
	clientB := dialGatewayRPCClient(t, ts.URL, userToken.Plaintext, "dev", "local")
	defer clientB.Close()

	args, _ := json.Marshal(map[string]interface{}{"session_id": "info", "operation": "list", "path": "report.md"})
	writeGatewayJSON(t, clientA, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params":  map[string]interface{}{"name": tool, "arguments": json.RawMessage(args)},
	})
	writeGatewayJSON(t, clientB, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params":  map[string]interface{}{"name": tool, "arguments": json.RawMessage(args)},
	})

	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("expected both read-only operations to reach agent concurrently")
		}
	}
	close(release)
	respA := readUntilID(t, clientA, 2)
	respB := readUntilID(t, clientB, 2)
	if _, ok := respA["error"]; ok {
		t.Fatalf("first info operation failed: %#v", respA)
	}
	if _, ok := respB["error"]; ok {
		t.Fatalf("second info operation failed: %#v", respB)
	}
}

func TestGatewayRPCQueuesSecondOperationForSameTarget(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	user, _ := store.CreateUser("alice")
	userToken, _ := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "alice"})
	if err := store.GrantUser(user.ID, ScopeGrant{Cluster: "dev", Instance: "local"}); err != nil {
		t.Fatalf("grant exec: %v", err)
	}

	gw := NewGatewayHub(store, GatewayHubOptions{
		AgentLease:                 time.Minute,
		TargetQueueTimeout:         time.Second,
		MaxQueuedPerTarget:         1,
		AllowOpenAgentRegistration: true,
	})
	mux := http.NewServeMux()
	gw.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	agentConn := dialFakeGatewayAgent(t, ts.URL, "dev", "local")
	defer agentConn.Close()
	blockRelease := make(chan struct{})
	go serveBlockingFakeAgent(t, agentConn, blockRelease)

	clientA := dialGatewayRPCClient(t, ts.URL, userToken.Plaintext, "dev", "local")
	defer clientA.Close()
	clientB := dialGatewayRPCClient(t, ts.URL, userToken.Plaintext, "dev", "local")
	defer clientB.Close()

	args, _ := json.Marshal(map[string]interface{}{"command": "sleep", "session_id": "queue"})
	writeGatewayJSON(t, clientA, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params":  map[string]interface{}{"name": "doops_shell", "arguments": json.RawMessage(args)},
	})
	time.Sleep(200 * time.Millisecond)
	writeGatewayJSON(t, clientB, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params":  map[string]interface{}{"name": "doops_shell", "arguments": json.RawMessage(args)},
	})

	close(blockRelease)
	readUntilID(t, clientA, 2)
	respB := readUntilID(t, clientB, 2)
	if _, ok := respB["error"]; ok {
		t.Fatalf("queued operation should succeed after first operation releases, got %#v", respB)
	}
}

func TestGatewayRPCDefaultDoesNotQueueBehindBusyTarget(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	user, _ := store.CreateUser("alice")
	userToken, _ := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "alice"})
	if err := store.GrantUser(user.ID, ScopeGrant{Cluster: "dev", Instance: "local"}); err != nil {
		t.Fatalf("grant exec: %v", err)
	}

	gw := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	gw.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	agentConn := dialFakeGatewayAgent(t, ts.URL, "dev", "local")
	defer agentConn.Close()
	blockRelease := make(chan struct{})
	go serveBlockingFakeAgent(t, agentConn, blockRelease)

	clientA := dialGatewayRPCClient(t, ts.URL, userToken.Plaintext, "dev", "local")
	defer clientA.Close()
	clientB := dialGatewayRPCClient(t, ts.URL, userToken.Plaintext, "dev", "local")
	defer clientB.Close()

	args, _ := json.Marshal(map[string]interface{}{"command": "sleep", "session_id": "busy-default"})
	writeGatewayJSON(t, clientA, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params":  map[string]interface{}{"name": "doops_shell", "arguments": json.RawMessage(args)},
	})
	time.Sleep(200 * time.Millisecond)

	// A same-session workspace write conflicts with the running shell call;
	// with default options it must fail fast instead of waiting in a hidden queue.
	writeArgs, _ := json.Marshal(map[string]interface{}{"path": "config.txt", "content": "x", "session_id": "busy-default"})
	writeGatewayJSON(t, clientB, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params":  map[string]interface{}{"name": "doops_file_write", "arguments": json.RawMessage(writeArgs)},
	})
	busy := readUntilID(t, clientB, 2)
	if errObj, ok := busy["error"].(map[string]interface{}); !ok || !strings.Contains(fmt.Sprint(errObj["message"]), "target busy") {
		t.Fatalf("expected default busy instead of hidden queue, got %#v", busy)
	}
	close(blockRelease)
	readUntilID(t, clientA, 2)
}

func TestGatewayRPCAllowsOneUserToRunOnSeveralTargetsAtOnce(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	user, _ := store.CreateUser("alice")
	userToken, _ := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "alice"})
	if err := store.GrantUser(user.ID, ScopeGrant{Cluster: "*", Instance: "local"}); err != nil {
		t.Fatalf("grant exec: %v", err)
	}

	gw := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	gw.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	devAgent := dialFakeGatewayAgent(t, ts.URL, "dev", "local")
	defer devAgent.Close()
	prodAgent := dialFakeGatewayAgent(t, ts.URL, "prod", "local")
	defer prodAgent.Close()
	release := make(chan struct{})
	go serveBlockingFakeAgent(t, devAgent, release)
	go serveFakeAgent(t, prodAgent)

	devClient := dialGatewayRPCClient(t, ts.URL, userToken.Plaintext, "dev", "local")
	defer devClient.Close()
	prodClient := dialGatewayRPCClient(t, ts.URL, userToken.Plaintext, "prod", "local")
	defer prodClient.Close()

	args, _ := json.Marshal(map[string]interface{}{"command": "sleep", "session_id": "limit"})
	writeGatewayJSON(t, devClient, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params":  map[string]interface{}{"name": "doops_shell", "arguments": json.RawMessage(args)},
	})
	time.Sleep(200 * time.Millisecond)

	writeGatewayJSON(t, prodClient, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params":  map[string]interface{}{"name": "doops_shell", "arguments": json.RawMessage(args)},
	})
	// The dev call is still blocked in the agent; the prod call must not wait for it.
	concurrent := readUntilID(t, prodClient, 2)
	if _, ok := concurrent["result"]; !ok {
		t.Fatalf("second target was limited by the first: %#v", concurrent)
	}
	close(release)
	if first := readUntilID(t, devClient, 2); first["result"] == nil {
		t.Fatalf("blocked call did not complete: %#v", first)
	}
}
