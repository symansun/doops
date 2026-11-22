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

func TestGatewayRPCForwardsAllowedActionAndRejectsDeniedAction(t *testing.T) {
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
	go serveFakeAgent(t, agentConn)

	clientWS := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/rpc?cluster=dev&instance=local"
	clientConn, _, err := websocket.DefaultDialer.Dial(clientWS, http.Header{"Authorization": []string{"Bearer " + userToken.Plaintext}})
	if err != nil {
		t.Fatalf("dial client websocket: %v", err)
	}
	defer clientConn.Close()
	writeGatewayJSON(t, clientConn, map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
	readUntilID(t, clientConn, 1)

	args, _ := json.Marshal(map[string]interface{}{"command": "hostname", "session_id": "smoke"})
	writeGatewayJSON(t, clientConn, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "doops_shell",
			"arguments": json.RawMessage(args),
		},
	})
	final := readUntilID(t, clientConn, 2)
	if _, ok := final["result"]; !ok {
		t.Fatalf("expected forwarded result, got %#v", final)
	}

	writeArgs, _ := json.Marshal(map[string]interface{}{"path": "/tmp/nope", "content": "x", "session_id": "smoke"})
	writeGatewayJSON(t, clientConn, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "doops_file_write",
			"arguments": json.RawMessage(writeArgs),
		},
	})
	written := readUntilID(t, clientConn, 3)
	if _, ok := written["result"]; !ok {
		t.Fatalf("a target grant must cover file writes, got %#v", written)
	}
	events, err := store.ListAudit(10)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	for _, event := range events {
		if event.Status == "forbidden" {
			t.Fatalf("granted target produced a forbidden audit: %#v", event)
		}
	}

	otherWS := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/rpc?cluster=prod&instance=local"
	otherConn, response, err := websocket.DefaultDialer.Dial(otherWS, http.Header{"Authorization": []string{"Bearer " + userToken.Plaintext}})
	if otherConn != nil {
		otherConn.Close()
	}
	if response != nil {
		defer response.Body.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("ungranted target must be refused at connect: response=%v err=%v", response, err)
	}
}

func TestGatewayRPCForwardsBackgroundToolsAsExec(t *testing.T) {
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
	go serveFakeAgent(t, agentConn)

	clientConn := dialGatewayRPCClient(t, ts.URL, userToken.Plaintext, "dev", "local")
	defer clientConn.Close()

	for id, call := range []struct {
		name string
		args map[string]interface{}
	}{
		{name: "doops_bg", args: map[string]interface{}{"command": "sleep 1", "session_id": "release"}},
		{name: "doops_task_status", args: map[string]interface{}{"task_id": "task-1", "session_id": "release"}},
	} {
		args, _ := json.Marshal(call.args)
		writeGatewayJSON(t, clientConn, map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id + 2,
			"method":  "tools/call",
			"params": map[string]interface{}{
				"name":      call.name,
				"arguments": json.RawMessage(args),
			},
		})
		final := readUntilID(t, clientConn, float64(id+2))
		if _, ok := final["result"]; !ok {
			t.Fatalf("expected %s to be forwarded, got %#v", call.name, final)
		}
	}

	var events []AuditEvent
	deadline := time.Now().Add(2 * time.Second)
	for {
		events, err = store.ListAuditFiltered(AuditFilter{Session: "release", Limit: 10})
		if err != nil {
			t.Fatalf("list audit: %v", err)
		}
		if len(events) == 2 && events[0].Status == "success" && events[1].Status == "success" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected two completed background-task audit events, got %#v", events)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, event := range events {
		if event.Action != ActionExec || event.Status != "success" {
			t.Fatalf("background tool must be authorized and recorded as exec, got %#v", event)
		}
	}
	if events[1].CommandSummary != "sleep 1" {
		t.Fatalf("background task audit must retain the submitted command, got %#v", events[1])
	}
}

func TestGatewayRPCDoesNotAllowClientActionOverrideForShell(t *testing.T) {
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

	gw := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, MaxQueuedPerTarget: -1, AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	gw.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	agentConn := dialFakeGatewayAgent(t, ts.URL, "dev", "local")
	defer agentConn.Close()
	go serveFakeAgent(t, agentConn)

	clientConn := dialGatewayRPCClient(t, ts.URL, userToken.Plaintext, "dev", "local")
	defer clientConn.Close()

	args, _ := json.Marshal(map[string]interface{}{
		"command":       "hostname",
		"session_id":    "smoke",
		"_doops_action": "info",
	})
	writeGatewayJSON(t, clientConn, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params":  map[string]interface{}{"name": "doops_shell", "arguments": json.RawMessage(args)},
	})
	final := readUntilID(t, clientConn, 2)
	if _, ok := final["result"]; !ok {
		t.Fatalf("granted shell call failed: %#v", final)
	}
	events, err := store.ListAudit(10)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Session == "smoke" {
			found = true
			if event.Action != ActionExec {
				t.Fatalf("client override relabeled shell audit as %q", event.Action)
			}
		}
	}
	if !found {
		t.Fatalf("shell call left no audit: %#v", events)
	}
}

func TestGatewayRejectsStructuredSSHForAgentWithoutCapability(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	user, _ := store.CreateUser("alice")
	userToken, _ := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "alice"})
	if err := store.GrantUser(user.ID, ScopeGrant{
		Cluster: "dev", Instance: "local",
	}); err != nil {
		t.Fatalf("grant exec: %v", err)
	}

	gw := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	gw.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	agentConn := dialFakeGatewayAgent(t, ts.URL, "dev", "local")
	defer agentConn.Close()
	go serveFakeAgent(t, agentConn)
	requireEventually(t, time.Second, func() bool { return gw.getAgent("dev", "local") != nil })

	client := dialGatewayRPCClient(t, ts.URL, userToken.Plaintext, "dev", "local")
	defer client.Close()
	args, _ := json.Marshal(map[string]interface{}{
		"session_id": "structured-ssh-old-agent",
		"host":       "ops@example.com",
		"command":    []string{"true"},
	})
	writeGatewayJSON(t, client, map[string]interface{}{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]interface{}{"name": "doops_ssh", "arguments": json.RawMessage(args)},
	})
	resp := readUntilID(t, client, 2)
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok || !strings.Contains(fmt.Sprint(errObj["message"]), "does not advertise protocol") {
		t.Fatalf("expected capability gate, got %#v", resp)
	}
	if !strings.Contains(fmt.Sprint(errObj["message"]), "upgrade the Agent") {
		t.Fatalf("capability gate must direct an Agent upgrade, got %#v", resp)
	}
}

func TestGatewayDispatchesTaggedAndPinnedAgentUpgrade(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	user, _ := store.CreateUser("alice")
	userToken, _ := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "alice"})
	if err := store.GrantUser(user.ID, ScopeGrant{
		Cluster: "dev", Instance: "local",
	}); err != nil {
		t.Fatalf("grant agent upgrade: %v", err)
	}

	gw := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, OperationTimeout: 30 * time.Minute, AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	gw.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	agentConn := dialFakeGatewayAgent(t, ts.URL, "dev", "local")
	defer agentConn.Close()
	go serveFakeAgentWithRuntimeID(t, agentConn, "runtime-old")
	requireEventually(t, time.Second, func() bool { return gw.getAgent("dev", "local") != nil })

	client := dialGatewayRPCClient(t, ts.URL, userToken.Plaintext, "dev", "local")
	defer client.Close()
	args, _ := json.Marshal(map[string]interface{}{
		"image":     "registry.example/doops:latest",
		"mode":      "k8s",
		"namespace": "doops-system",
		"workload":  "deployment/doops-agent",
		"container": "doops-agent",
	})
	writeGatewayJSON(t, client, map[string]interface{}{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]interface{}{"name": "doops_agent_upgrade", "arguments": json.RawMessage(args)},
	})
	resp := readUntilID(t, client, 2)
	if resp["error"] != nil {
		t.Fatalf("tagged self-upgrade should reach the agent: %#v", resp)
	}
	if gw.getAgent("dev", "local") == nil {
		t.Fatal("tagged self-upgrade disconnected the agent")
	}
	events, err := store.ListAuditFiltered(AuditFilter{Action: ActionAgentUpgrade, Limit: 10})
	if err != nil || len(events) != 1 || events[0].CorrelationID == "" || events[0].Tail == "" {
		t.Fatalf("upgrade did not persist correlated result evidence: events=%#v err=%v", events, err)
	}

	dryRunArgs, _ := json.Marshal(map[string]interface{}{
		"image":   "registry.example/doops@sha256:" + strings.Repeat("a", 64),
		"mode":    "k8s",
		"dry_run": true,
	})
	writeGatewayJSON(t, client, map[string]interface{}{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]interface{}{"name": "doops_agent_upgrade", "arguments": json.RawMessage(dryRunArgs)},
	})
	if resp := readUntilID(t, client, 3); resp["error"] != nil {
		t.Fatalf("dry-run upgrade should remain available: %#v", resp)
	}

	pinned, _ := json.Marshal(map[string]interface{}{
		"image": "registry.example/doops@sha256:" + strings.Repeat("a", 64),
		"mode":  "k8s",
	})
	writeGatewayJSON(t, client, map[string]interface{}{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]interface{}{"name": "doops_agent_upgrade", "arguments": json.RawMessage(pinned)},
	})
	dispatched := readUntilID(t, client, 4)
	if dispatched["error"] != nil {
		t.Fatalf("pinned self-upgrade should reach the agent: %#v", dispatched)
	}
	if gw.getAgent("dev", "local") == nil {
		t.Fatal("dispatched self-upgrade disconnected the agent")
	}
}

func TestGatewayForwardsShellCommandsWithoutInspectingContent(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	user, _ := store.CreateUser("alice")
	userToken, _ := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "alice"})
	if err := store.GrantUser(user.ID, ScopeGrant{
		Cluster: "dev", Instance: "local",
	}); err != nil {
		t.Fatalf("grant exec: %v", err)
	}

	gw := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	gw.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	agentConn := dialFakeGatewayAgent(t, ts.URL, "dev", "local")
	defer agentConn.Close()
	go serveFakeAgent(t, agentConn)
	requireEventually(t, time.Second, func() bool { return gw.getAgent("dev", "local") != nil })

	client := dialGatewayRPCClient(t, ts.URL, userToken.Plaintext, "dev", "local")
	defer client.Close()
	commands := []string{
		"kubectl -n doops-system set image deployment/doops-agent doops-agent=image:v2",
		"printf '%s' 'encoded-job' | base64 -d | kubectl apply -f -",
	}
	for index, command := range commands {
		id := index + 2
		args, _ := json.Marshal(map[string]interface{}{
			"command":    command,
			"session_id": fmt.Sprintf("gateway-forwards-%d", id),
		})
		writeGatewayJSON(t, client, map[string]interface{}{
			"jsonrpc": "2.0", "id": id, "method": "tools/call",
			"params": map[string]interface{}{"name": "doops_shell", "arguments": json.RawMessage(args)},
		})
		resp := readUntilID(t, client, float64(id))
		if resp["error"] != nil || resp["result"] == nil {
			t.Fatalf("gateway must forward %q to the Agent unchanged, got %#v", command, resp)
		}
	}
}

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

func TestGatewayRPCDoesNotFallbackSessionNotificationsWithoutSessionID(t *testing.T) {
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
	go serveSessionOrphanNotificationFakeAgent(t, agentConn, started, release)

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
	for name, conn := range map[string]*websocket.Conn{"session-a": clientA, "session-b": clientB} {
		msg := readGatewayMessage(t, conn)
		if method, _ := msg["method"].(string); method == "notifications/message" {
			t.Fatalf("%s received orphan notification without session routing: %#v", name, msg)
		}
		if id, _ := msg["id"].(float64); id != 2 {
			t.Fatalf("%s expected final response id 2, got %#v", name, msg)
		}
	}
}

func TestGatewayNotificationRoutingRequiresRequestID(t *testing.T) {
	for _, notification := range []map[string]interface{}{
		{"jsonrpc": "2.0", "method": "notifications/message", "params": map[string]interface{}{"data": "missing"}},
		{"jsonrpc": "2.0", "method": "notifications/message", "params": map[string]interface{}{"sessionID": "legacy", "data": "missing"}},
		{"jsonrpc": "2.0", "method": "notifications/message", "params": map[string]interface{}{"request_id": "not-a-number", "data": "invalid"}},
	} {
		if requestID, ok := requestIDFromNotification(notification); ok || requestID != 0 {
			t.Fatalf("invalid notification was routed: request_id=%d ok=%t notification=%#v", requestID, ok, notification)
		}
	}
	valid := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/message",
		"params":  map[string]interface{}{"request_id": float64(42), "data": "valid"},
	}
	if requestID, ok := requestIDFromNotification(valid); !ok || requestID != 42 {
		t.Fatalf("valid request-correlated notification was rejected: request_id=%d ok=%t", requestID, ok)
	}
}

func TestGatewayRPCRejectsInternalCredentialToolsFromUserClients(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	user, err := store.CreateUser("credential-caller")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := store.SetUserAdmin(user.ID, true); err != nil {
		t.Fatalf("grant user: %v", err)
	}
	token, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "credential-caller"})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	hub := NewGatewayHub(store, GatewayHubOptions{AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	client := dialGatewayRPCClient(t, server.URL, token.Plaintext, "dev", "local")
	defer client.Close()
	for index, tool := range []string{"doops_credential_import", "doops_credential_materialize", "doops_credential_cleanup"} {
		id := float64(index + 2)
		writeGatewayJSON(t, client, map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"method":  "tools/call",
			"params": map[string]interface{}{
				"name":      tool,
				"arguments": map[string]interface{}{},
			},
		})
		response := readUntilID(t, client, id)
		rpcError, _ := response["error"].(map[string]interface{})
		if rpcError["code"] != float64(-32003) {
			t.Fatalf("%s was not rejected as internal-only: %#v", tool, response)
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

func TestGatewayRPCCancelsOperationWhenClientDisconnects(t *testing.T) {
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
	args, _ := json.Marshal(map[string]interface{}{"command": "sleep", "session_id": "disconnect-drain"})
	writeGatewayJSON(t, clientA, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params":  map[string]interface{}{"name": "doops_shell", "arguments": json.RawMessage(args)},
	})
	time.Sleep(200 * time.Millisecond)
	_ = clientA.Close()

	requireEventually(t, time.Second, func() bool {
		return len(gw.listActiveOperations()) == 0
	})
	close(blockRelease)
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
