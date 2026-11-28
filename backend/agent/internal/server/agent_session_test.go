package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/gorilla/websocket"
	"github.com/user/doops/agent/api"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGatewayKeepsIdleAgentAliveWithPing(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	lease := 200 * time.Millisecond
	gw := NewGatewayHub(store, GatewayHubOptions{AgentLease: lease, AllowOpenAgentRegistration: true})
	gw.registration.heartbeatTimeout = time.Second
	mux := http.NewServeMux()
	gw.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	agentConn := dialFakeGatewayAgent(t, ts.URL, "dev", "local")
	defer agentConn.Close()
	pings := make(chan string, 8)
	time.Sleep(lease * 2)
	go serveFakeAgentObservingPings(t, agentConn, pings)

	regularPings := 0
	timeout := time.NewTimer(20 * time.Second)
	defer timeout.Stop()
	for regularPings < 5 {
		select {
		case payload := <-pings:
			if payload == "ping" {
				regularPings++
			}
		case <-timeout.C:
			t.Fatalf("expected gateway heartbeat pings, got %d", regularPings)
		}
	}
	targets := gw.ListTargets()
	if len(targets) != 1 {
		t.Fatalf("expected idle agent to stay online, got %d targets", len(targets))
	}
	if targets[0].Key != "dev/local" {
		t.Fatalf("unexpected target: %#v", targets[0])
	}
}

func TestGatewayAgentPingDoesNotWaitForDataWriteLock(t *testing.T) {
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

	pingSeen := make(chan struct{}, 1)
	clientConn.SetPingHandler(func(data string) error {
		select {
		case pingSeen <- struct{}{}:
		default:
		}
		return clientConn.WriteControl(
			websocket.PongMessage,
			[]byte(data),
			time.Now().Add(time.Second),
		)
	})
	go func() {
		for {
			if _, _, err := clientConn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	agent := &GatewayAgent{
		conn:   <-serverConn,
		closed: make(chan struct{}),
	}
	defer agent.conn.Close()
	agent.writeMu.Lock()
	defer agent.writeMu.Unlock()
	defer close(agent.closed)

	go agent.pingLoop(nil, 30*time.Millisecond)
	select {
	case <-pingSeen:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("gateway ping waited for the data write lock")
	}
}

func TestGatewayAgentReadLoopKeepsPongResponsiveWhenStoreIsBusy(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	hub := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Second, AllowOpenAgentRegistration: true})

	heldConn, err := store.db.Conn(context.Background())
	if err != nil {
		t.Fatalf("hold store connection: %v", err)
	}
	defer heldConn.Close()

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

	pong := make(chan struct{}, 1)
	clientConn.SetPongHandler(func(string) error {
		select {
		case pong <- struct{}{}:
		default:
		}
		return nil
	})
	go func() {
		for {
			if _, _, err := clientConn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	agent := &GatewayAgent{
		Cluster:        "dev",
		Instance:       "local",
		Key:            "dev/local",
		conn:           <-serverConn,
		pending:        make(map[int64]*gatewayMessageQueue),
		activeSessions: make(map[string]int),
		closed:         make(chan struct{}),
	}
	go agent.readLoop(hub.opts.AgentLease, hub.opts.AgentLease)

	if err := clientConn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/message",
	}); err != nil {
		t.Fatalf("write data message: %v", err)
	}
	if err := clientConn.WriteControl(
		websocket.PingMessage,
		[]byte("store-busy"),
		time.Now().Add(time.Second),
	); err != nil {
		t.Fatalf("write ping: %v", err)
	}

	select {
	case <-pong:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("gateway read loop stopped processing ping while the store was busy")
	}
}

func TestGatewayAgentReadLoopAcknowledgesStreamingProgress(t *testing.T) {
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

	progressAck := make(chan struct{}, 1)
	clientConn.SetPongHandler(func(payload string) error {
		if payload == "read-progress" {
			select {
			case progressAck <- struct{}{}:
			default:
			}
		}
		return nil
	})
	go func() {
		for {
			if _, _, err := clientConn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	agent := &GatewayAgent{
		Key:            "dev/local",
		conn:           <-serverConn,
		pending:        make(map[int64]*gatewayMessageQueue),
		activeSessions: make(map[string]int),
		closed:         make(chan struct{}),
	}
	go agent.readLoop(time.Second, time.Second)

	writer, err := clientConn.NextWriter(websocket.TextMessage)
	if err != nil {
		t.Fatalf("open message writer: %v", err)
	}
	if _, err := writer.Write(bytes.Repeat([]byte("x"), 64<<10)); err != nil {
		t.Fatalf("write partial message: %v", err)
	}
	select {
	case <-progressAck:
	case <-time.After(time.Second):
		t.Fatal("gateway did not acknowledge bytes read before the message completed")
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close message writer: %v", err)
	}
}

func TestGatewayRelayTimeoutDoesNotCloseAgentConnection(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	gw := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, OperationTimeout: 25 * time.Millisecond, AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	gw.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	agentConn := dialFakeGatewayAgent(t, ts.URL, "dev", "local")
	defer agentConn.Close()
	cancelSeen := make(chan struct{}, 1)
	go serveTimeoutFakeAgent(t, agentConn, cancelSeen)
	waitForGatewayAgent(t, gw, "dev", "local")
	agent := gw.getAgent("dev", "local")
	err = agent.relayToolCall(context.Background(), api.ToolCallParams{
		Name:      "doops_shell",
		Arguments: json.RawMessage(`{"session_id":"timeout","command":"sleep"}`),
	}, 25*time.Millisecond, func(gatewayWSMessage) error { return nil })
	if !errors.Is(err, errExecutionOutcomeUnknown) {
		t.Fatalf("timed-out Shell must retain external outcome uncertainty, got %v", err)
	}
	select {
	case <-cancelSeen:
	case <-time.After(time.Second):
		t.Fatal("agent did not receive tools/cancel after timeout")
	}
	time.Sleep(50 * time.Millisecond)
	if gw.getAgent("dev", "local") == nil {
		t.Fatal("timed out operation must not unregister or close the agent connection")
	}
}

func TestGatewayAgentReadLoopKeepsPingResponsiveWhenPendingConsumerStalls(t *testing.T) {
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

	pong := make(chan struct{}, 1)
	clientConn.SetPongHandler(func(string) error {
		select {
		case pong <- struct{}{}:
		default:
		}
		return nil
	})
	cancelSeen := make(chan struct{}, 1)
	go func() {
		for {
			messageType, data, err := clientConn.ReadMessage()
			if err != nil {
				return
			}
			if messageType != websocket.TextMessage {
				continue
			}
			var message map[string]interface{}
			if json.Unmarshal(data, &message) == nil && message["method"] == "tools/cancel" {
				select {
				case cancelSeen <- struct{}{}:
				default:
				}
			}
		}
	}()

	pending := newGatewayMessageQueue(newGatewayMessageBudget(64<<10), 16<<10, 1, "tools/cancel")
	agent := &GatewayAgent{
		conn:           <-serverConn,
		pending:        map[int64]*gatewayMessageQueue{1: pending},
		activeSessions: make(map[string]int),
		closed:         make(chan struct{}),
	}
	go agent.readLoop(time.Second, time.Second)
	defer func() {
		pending.close()
		_ = clientConn.Close()
	}()

	for i := 0; i < 32; i++ {
		if err := clientConn.WriteJSON(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  map[string]interface{}{"sequence": i},
		}); err != nil {
			t.Fatalf("write pending response %d: %v", i, err)
		}
	}
	if err := clientConn.WriteControl(
		websocket.PingMessage,
		[]byte("still-alive"),
		time.Now().Add(time.Second),
	); err != nil {
		t.Fatalf("write ping: %v", err)
	}

	select {
	case <-pong:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("gateway read loop stopped processing ping while a pending consumer was stalled")
	}
	select {
	case <-cancelSeen:
	case <-time.After(time.Second):
		t.Fatal("gateway did not cancel the stalled request after its queue exceeded the limit")
	}
	if _, ok, err := pending.dequeue(); !errors.Is(err, errGatewayMessageQueueFull) || ok {
		t.Fatalf("expected only the stalled request to fail, got ok=%v err=%v", ok, err)
	}
}
