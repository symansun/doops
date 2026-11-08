package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// The review's TCP backpressure fixture now requires the caller to finish before
// the peer is released. No peer close is used to make the write return.
func blockedWriteGateway(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	release := make(chan struct{})
	var connections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connections.Add(1)
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		var init map[string]interface{}
		if err := conn.ReadJSON(&init); err != nil {
			t.Error(err)
			return
		}
		if tcp, ok := conn.UnderlyingConn().(*net.TCPConn); ok {
			_ = tcp.SetReadBuffer(1024)
		}
		_ = conn.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "id": init["id"], "result": map[string]interface{}{}})
		<-release
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})
	return server.URL, &connections
}

func deadlineTestClient(t *testing.T, gateway string) *MCPClient {
	t.Helper()
	c := NewMCPClient(Server{Name: "local", Gateway: gateway, Cluster: "review", Instance: "node"},
		&SessionStore{Sessions: map[string]SessionInfo{}}, "review", false)
	t.Cleanup(c.Close)
	return c
}

func requireUnknownWrite(t *testing.T, err error) {
	t.Helper()
	var rpcErr *gatewayRPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32008 || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("expected unknown write outcome, got %v", err)
	}
}

func TestRPCWriteDeadline(t *testing.T) {
	for _, mode := range []string{"Call", "CallAndCapture", "CallStructured"} {
		t.Run(mode, func(t *testing.T) {
			gateway, connections := blockedWriteGateway(t)
			c := deadlineTestClient(t, gateway)
			c.CallTimeout = 40 * time.Millisecond
			if err := c.connect(); err != nil {
				t.Fatal(err)
			}
			args := map[string]interface{}{"path": "large.txt", "content": strings.Repeat("x", 8<<20)}
			done := make(chan error, 1)
			start := time.Now()
			go func() {
				switch mode {
				case "Call":
					done <- c.Call("doops_file_write", args)
				case "CallAndCapture":
					_, err := c.CallAndCapture("doops_file_write", args)
					done <- err
				case "CallStructured":
					var result map[string]interface{}
					done <- c.CallStructured("doops_file_write", args, &result)
				}
			}()
			select {
			case err := <-done:
				requireUnknownWrite(t, err)
				t.Logf("%s timeout=40ms returned=%s error=%v", mode, time.Since(start), err)
			case <-time.After(800 * time.Millisecond):
				t.Fatal("write ignored the call deadline")
			}
			c.mu.Lock()
			closed := c.conn == nil && !c.connected
			c.mu.Unlock()
			if !closed || connections.Load() != 1 {
				t.Fatalf("failed transport retained or request replayed: closed=%t connections=%d", closed, connections.Load())
			}
		})
	}
}

func TestRPCQueuedWriteDeadline(t *testing.T) {
	gateway, connections := blockedWriteGateway(t)
	c := deadlineTestClient(t, gateway)
	if err := c.connect(); err != nil {
		t.Fatal(err)
	}
	conn := c.conn
	first := make(chan error, 1)
	go func() {
		first <- c.writeRequest(conn, map[string]interface{}{"content": strings.Repeat("x", 8<<20)}, time.Now().Add(300*time.Millisecond))
	}()
	limit := time.After(time.Second)
	for len(c.writes) == 0 {
		select {
		case <-limit:
			t.Fatal("first writer did not start")
		case <-time.After(time.Millisecond):
		}
	}
	c.CallTimeout = 40 * time.Millisecond
	start := time.Now()
	err := c.Call("doops_file_write", map[string]interface{}{"path": "second", "content": "must not be sent"})
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "request not sent") || time.Since(start) > 200*time.Millisecond {
		t.Fatalf("queued request was not bounded/identified as unsent: elapsed=%s err=%v", time.Since(start), err)
	}
	select {
	case err := <-first:
		requireUnknownWrite(t, err)
	case <-time.After(time.Second):
		t.Fatal("first write did not finish")
	}
	if connections.Load() != 1 {
		t.Fatalf("request replayed on %d connections", connections.Load())
	}
}

func TestRPCWriteDeadlineCLI(t *testing.T) {
	binary := buildDoopsCLIBinary(t)
	root := t.TempDir()
	t.Setenv("DOOPS_CONFIG", filepath.Join(root, "config.json"))
	t.Setenv("DOOPS_CLI_CALL_TIMEOUT", "40ms")
	gateway, connections := blockedWriteGateway(t)
	if err := saveServers([]Server{{Name: "local-review", Gateway: gateway, Cluster: "review", Instance: "node"}}); err != nil {
		t.Fatal(err)
	}
	auth := &AuthStore{}
	auth.SetGateway(gateway, "synthetic-local-fixture")
	if err := auth.Save(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "large.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 8<<20)), 0644); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	code, output := transferCLI(t, binary, "write", "--storage", "gateway", "--path", "large.txt", "--file", path)
	if code != 1 || !strings.Contains(output, "execution_outcome_unknown") || connections.Load() != 1 || time.Since(start) > 2*time.Second {
		t.Fatalf("CLI write timeout failed: exit=%d connections=%d elapsed=%s\n%s", code, connections.Load(), time.Since(start), output)
	}
}

func TestRPCOldConnectionCannotCloseNewPending(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		for {
			var req map[string]interface{}
			if conn.ReadJSON(&req) != nil {
				return
			}
			_ = conn.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "id": req["id"], "result": map[string]interface{}{}})
		}
	}))
	defer server.Close()
	c := deadlineTestClient(t, server.URL)
	if err := c.connect(); err != nil {
		t.Fatal(err)
	}
	old := c.conn
	c.Close()
	if err := c.connect(); err != nil {
		t.Fatal(err)
	}
	id := c.nextReqID()
	conn, ch := c.registerCall(id)
	defer c.unregisterPending(id)
	// The old reader can observe its close only after the replacement is ready.
	c.closeConnection(old)
	if err := c.writeRequest(conn, map[string]interface{}{"jsonrpc": "2.0", "id": id, "method": "initialize"}, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-ch:
		if event.IsError || event.Parsed["id"] != float64(id) {
			t.Fatalf("new request was affected by old connection: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("new pending request was lost")
	}
}

func TestRPCInitializeDisconnectDoesNotDeadlockClose(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		var req map[string]interface{}
		_ = conn.ReadJSON(&req)
	}))
	defer server.Close()
	c := deadlineTestClient(t, server.URL)
	done := make(chan error, 1)
	go func() {
		err := c.connect()
		c.Close()
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("initialize disconnect was accepted")
		}
	case <-time.After(6 * time.Second):
		t.Fatal("initialize/reader/Close deadlocked")
	}
	c.pendingMu.RLock()
	defer c.pendingMu.RUnlock()
	if len(c.pending) != 0 {
		t.Fatal("initialize left pending requests after Close")
	}
}

func TestRPCQueuedRequestStaysOnOriginalConnection(t *testing.T) {
	for _, mode := range []string{"Call", "CallAndCapture", "CallStructured"} {
		t.Run(mode, func(t *testing.T) {
			received := make(chan string, 4)
			var connections atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upgrader := websocket.Upgrader{}
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				connections.Add(1)
				defer conn.Close()
				for {
					var req map[string]interface{}
					if conn.ReadJSON(&req) != nil {
						return
					}
					if req["method"] == "tools/call" {
						params := req["params"].(map[string]interface{})
						args := params["arguments"].(map[string]interface{})
						received <- args["path"].(string)
					}
					_ = conn.WriteJSON(map[string]interface{}{
						"jsonrpc": "2.0", "id": req["id"],
						"result": map[string]interface{}{
							"content":           []map[string]string{{"type": "text", "text": "done"}},
							"structuredContent": map[string]interface{}{"done": true},
						},
					})
				}
			}))
			defer server.Close()
			c := deadlineTestClient(t, server.URL)
			c.CallTimeout = 3 * time.Second
			if err := c.connect(); err != nil {
				t.Fatal(err)
			}
			c.writes <- struct{}{}
			done := make(chan error, 1)
			go func() {
				args := map[string]interface{}{"path": "queued", "content": "mutation"}
				switch mode {
				case "Call":
					done <- c.Call("doops_file_write", args)
				case "CallAndCapture":
					_, err := c.CallAndCapture("doops_file_write", args)
					done <- err
				case "CallStructured":
					var result map[string]interface{}
					done <- c.CallStructured("doops_file_write", args, &result)
				}
			}()
			until := time.Now().Add(time.Second)
			for {
				c.pendingMu.RLock()
				count := len(c.pending)
				c.pendingMu.RUnlock()
				if count > 0 {
					break
				}
				if time.Now().After(until) {
					t.Fatal("call did not register")
				}
				time.Sleep(time.Millisecond)
			}
			c.Close()
			if err := c.connect(); err != nil {
				t.Fatal(err)
			}
			<-c.writes
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "request not sent") {
					t.Fatalf("queued request was not rejected as unsent: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("queued request did not finish")
			}
			// A response on the replacement also orders all preceding writes on it.
			var result map[string]interface{}
			if err := c.CallStructured("doops_file_write", map[string]interface{}{"path": "fresh"}, &result); err != nil {
				t.Fatal(err)
			}
			if len(received) != 1 || <-received != "fresh" || connections.Load() != 2 {
				t.Fatalf("stale mutation crossed connections: queued=%d connections=%d", len(received), connections.Load())
			}
			t.Logf("%s stale_request_not_sent=true replacement_only_received=fresh", mode)
		})
	}
}
