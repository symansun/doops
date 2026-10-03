package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func gitCancellationTestConnection(t *testing.T, handler http.Handler) (*websocket.Conn, <-chan map[string]interface{}) {
	t.Helper()
	server := newGatewayAuthorizedAgentServer(t, &Gateway{gitHandler: handler})
	t.Cleanup(server.Close)
	conn := dialAgentTestWS(t, server.URL)
	messages := make(chan map[string]interface{}, 32)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer close(messages)
		for {
			var message map[string]interface{}
			if err := conn.ReadJSON(&message); err != nil {
				return
			}
			messages <- message
		}
	}()
	t.Cleanup(func() {
		_ = conn.Close()
		gitCancellationTestSignal(t, readerDone)
	})
	return conn, messages
}

func gitCancellationTestSend(t *testing.T, conn *websocket.Conn, id interface{}, method string, params interface{}) {
	t.Helper()
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	}); err != nil {
		t.Fatalf("send %s: %v", method, err)
	}
}

func gitCancellationTestRequest(t *testing.T, conn *websocket.Conn, id int) {
	t.Helper()
	gitCancellationTestSend(t, conn, id, "git/http", gitHTTPRequestParams{
		Method: http.MethodPost, Path: "/git/cancel.git/git-receive-pack", ContentLength: -1,
	})
}

func gitCancellationTestMessage(t *testing.T, messages <-chan map[string]interface{}) map[string]interface{} {
	t.Helper()
	select {
	case message, ok := <-messages:
		if !ok {
			t.Fatal("agent WebSocket closed before expected response")
		}
		return message
	case <-time.After(3 * time.Second):
		t.Fatal("agent did not send expected response")
		return nil
	}
}

func gitCancellationTestSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("Git handler or WebSocket reader did not stop")
	}
}

func gitCancellationTestAck(t *testing.T, message map[string]interface{}, id string, canceled bool) {
	t.Helper()
	result, _ := message["result"].(map[string]interface{})
	if message["id"] != id || result["canceled"] != canceled || message["error"] != nil {
		t.Fatalf("wrong independent cancel ACK: %#v", message)
	}
}

func gitCancellationTestTerminal(t *testing.T, message map[string]interface{}, id int, code int, text string) {
	t.Helper()
	rpcError, _ := message["error"].(map[string]interface{})
	if message["id"] != float64(id) || rpcError["code"] != float64(code) ||
		!strings.Contains(fmt.Sprint(rpcError["message"]), text) || message["result"] != nil || message["method"] != nil {
		t.Fatalf("wrong original Git terminal: %#v", message)
	}
}

func TestAgentGitCancellationTerminalWaitsForHandler(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("streaming=%t", streaming), func(t *testing.T) {
			started := make(chan struct{})
			canceled := make(chan struct{})
			release := make(chan struct{})
			finished := make(chan struct{})
			conn, messages := gitCancellationTestConnection(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(finished)
				if streaming {
					_, _ = w.Write([]byte("progress"))
				}
				close(started)
				<-r.Context().Done()
				close(canceled)
				<-release
			}))
			t.Cleanup(func() {
				select {
				case <-release:
				default:
					close(release)
				}
			})
			gitCancellationTestRequest(t, conn, 42)
			gitCancellationTestSignal(t, started)
			if streaming {
				header := gitCancellationTestMessage(t, messages)
				body := gitCancellationTestMessage(t, messages)
				params, _ := body["params"].(map[string]interface{})
				if header["method"] != "git/response" || body["method"] != "git/body" ||
					params["data_b64"] != base64.StdEncoding.EncodeToString([]byte("progress")) {
					t.Fatalf("Git progress was not streamed: %#v %#v", header, body)
				}
			}
			for _, id := range []string{"cancel-42", "cancel-42-again"} {
				gitCancellationTestSend(t, conn, id, "git/cancel", map[string]interface{}{"id": 42})
				gitCancellationTestAck(t, gitCancellationTestMessage(t, messages), id, true)
			}
			gitCancellationTestSignal(t, canceled)
			gitCancellationTestSend(t, conn, "cleanup-barrier", "initialize", nil)
			if message := gitCancellationTestMessage(t, messages); message["id"] != "cleanup-barrier" {
				t.Fatalf("original request terminated before handler cleanup: %#v", message)
			}
			close(release)
			gitCancellationTestTerminal(t, gitCancellationTestMessage(t, messages), 42, -32008, "execution_outcome_unknown")
			select {
			case <-finished:
			default:
				t.Fatal("terminal reply preceded handler return")
			}
			// Each new ACK also checks that the canceled request emits no second terminal.
			deadline := time.Now().Add(time.Second)
			for attempt := 0; ; attempt++ {
				id := fmt.Sprintf("retired-%d", attempt)
				gitCancellationTestSend(t, conn, id, "git/cancel", map[string]interface{}{"id": 42})
				message := gitCancellationTestMessage(t, messages)
				result, _ := message["result"].(map[string]interface{})
				if result["canceled"] == false {
					gitCancellationTestAck(t, message, id, false)
					break
				}
				gitCancellationTestAck(t, message, id, true)
				if time.Now().After(deadline) {
					t.Fatal("canceled Git request was not removed from active calls")
				}
			}
			gitCancellationTestSend(t, conn, "terminal-barrier", "initialize", nil)
			if message := gitCancellationTestMessage(t, messages); message["id"] != "terminal-barrier" {
				t.Fatalf("duplicate Git terminal or body EOF after cancellation: %#v", message)
			}
		})
	}
}

func TestAgentGitCancellationUnblocksBodyAndDisconnect(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		t.Run(fmt.Sprintf("disconnect=%t", disconnect), func(t *testing.T) {
			started := make(chan struct{})
			finished := make(chan struct{})
			readResult := make(chan error, 1)
			conn, messages := gitCancellationTestConnection(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(finished)
				close(started)
				_, err := io.Copy(io.Discard, r.Body)
				if r.Context().Err() == nil {
					err = fmt.Errorf("request body ended without context cancellation: %v", err)
				}
				readResult <- err
			}))
			gitCancellationTestRequest(t, conn, 42)
			gitCancellationTestSignal(t, started)
			gitCancellationTestSend(t, conn, nil, "git/body", map[string]interface{}{
				"id": 42, "data_b64": base64.StdEncoding.EncodeToString([]byte("partial push")),
			})
			if disconnect {
				_ = conn.Close()
			} else {
				gitCancellationTestSend(t, conn, "cancel-42", "git/cancel", map[string]interface{}{"id": 42})
				ackSeen, terminalSeen := false, false
				for !ackSeen || !terminalSeen {
					message := gitCancellationTestMessage(t, messages)
					if message["id"] == "cancel-42" {
						gitCancellationTestAck(t, message, "cancel-42", true)
						ackSeen = true
					} else {
						if terminalSeen {
							t.Fatalf("duplicate Git terminal: %#v", message)
						}
						gitCancellationTestTerminal(t, message, 42, -32008, "execution_outcome_unknown")
						terminalSeen = true
					}
				}
			}
			gitCancellationTestSignal(t, finished)
			if err := <-readResult; err != nil {
				t.Fatalf("request body did not close cleanly on cancellation: %v", err)
			}
		})
	}
}

func TestAgentGitCancellationTerminalWaitsForProcess(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	command := make(chan *exec.Cmd, 1)
	conn, messages := gitCancellationTestConnection(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		cmd := exec.CommandContext(r.Context(), "sleep", "60")
		if err := cmd.Start(); err != nil {
			t.Errorf("start Git handler process: %v", err)
			close(started)
			return
		}
		command <- cmd
		close(started)
		_ = cmd.Wait()
	}))
	gitCancellationTestRequest(t, conn, 42)
	gitCancellationTestSignal(t, started)
	gitCancellationTestSend(t, conn, "cancel-42", "git/cancel", map[string]interface{}{"id": 42})
	ackSeen, terminalSeen := false, false
	for !ackSeen || !terminalSeen {
		message := gitCancellationTestMessage(t, messages)
		if message["id"] == "cancel-42" {
			gitCancellationTestAck(t, message, "cancel-42", true)
			ackSeen = true
		} else {
			gitCancellationTestTerminal(t, message, 42, -32008, "execution_outcome_unknown")
			terminalSeen = true
			select {
			case <-finished:
			default:
				t.Fatal("Git terminal reply preceded process Wait")
			}
			cmd := <-command
			if cmd.ProcessState == nil || cmd.ProcessState.Success() {
				t.Fatalf("Git handler process was not killed and reaped: %v", cmd.ProcessState)
			}
		}
	}
}

func TestAgentGitCancellationPanicIsSingleErrorTerminal(t *testing.T) {
	started := make(chan struct{})
	conn, messages := gitCancellationTestConnection(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		panic("canceled handler cleanup")
	}))
	gitCancellationTestRequest(t, conn, 42)
	gitCancellationTestSignal(t, started)
	gitCancellationTestSend(t, conn, "cancel-42", "git/cancel", map[string]interface{}{"id": 42})
	ackSeen, terminalSeen := false, false
	for !ackSeen || !terminalSeen {
		message := gitCancellationTestMessage(t, messages)
		if message["id"] == "cancel-42" {
			gitCancellationTestAck(t, message, "cancel-42", true)
			ackSeen = true
		} else {
			if terminalSeen {
				t.Fatalf("duplicate Git panic terminal: %#v", message)
			}
			gitCancellationTestTerminal(t, message, 42, -32008, "execution_outcome_unknown")
			terminalSeen = true
		}
	}
	gitCancellationTestSend(t, conn, "panic-barrier", "initialize", nil)
	if message := gitCancellationTestMessage(t, messages); message["id"] != "panic-barrier" {
		t.Fatalf("duplicate terminal after handler panic: %#v", message)
	}
}

func TestAgentGitCancellationRetainsUnknownOnRealGateway(t *testing.T) {
	hub, server, tokens := newGatewayGitE2E(t, []gatewayGitE2ETarget{{Cluster: "dev", Instance: "local"}})
	t.Cleanup(server.Close)
	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	agentDone := make(chan struct{})
	agent := &Gateway{gitHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/git/cancel.git/info/refs" {
			close(started)
			<-r.Context().Done()
			close(canceled)
			<-release
			return
		}
		_, _ = io.WriteString(w, "next Git request completed")
	})}
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/agent/connect?cluster=dev&instance=local", nil)
	if err != nil {
		t.Fatalf("connect real Agent to Gateway: %v", err)
	}
	go func() {
		defer close(agentDone)
		agent.ServeGatewayWebSocketConnWithReady(conn, "git-cancellation-agent", nil)
	}()
	t.Cleanup(func() {
		_ = conn.Close()
		gitCancellationTestSignal(t, agentDone)
	})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	waitForGatewayAgent(t, hub, "dev", "local")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		gatewayGitTestURL(t, server.URL, "dev", "local", "cancel", tokens.Targets[0].UserToken)+"/info/refs?service=git-upload-pack", nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		response, _ := client.Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
	}()
	gitCancellationTestSignal(t, started)
	cancel()
	gitCancellationTestSignal(t, requestDone)
	gitCancellationTestSignal(t, canceled)
	if operations := hub.listActiveOperations(); len(operations) != 1 {
		t.Fatalf("Gateway released Git request on cancel ACK before handler returned: %#v", operations)
	}
	close(release)
	requireEventually(t, time.Second, func() bool { return len(hub.listActiveOperations()) == 0 })
	audits, err := hub.store.ListAudit(10)
	if err != nil || len(audits) != 1 || audits[0].Status != "outcome_unknown" {
		t.Fatalf("Git cancellation lost process-cleanup uncertainty: %#v, %v", audits, err)
	}
	response, err := client.Get(gatewayGitTestURL(t, server.URL, "dev", "local", "next", tokens.Targets[0].UserToken) + "/info/refs?service=git-upload-pack")
	if err != nil {
		t.Fatalf("next Git request after cancellation: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusConflict || !strings.Contains(string(body), "execution_outcome_unknown") {
		t.Fatalf("Gateway allowed new work without Git cleanup evidence: HTTP %d %q, %v", response.StatusCode, body, err)
	}
}
