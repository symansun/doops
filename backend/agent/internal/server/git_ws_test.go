package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"github.com/gorilla/websocket"
	"net/http"
	"testing"
	"time"
)

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

	// The cancellation acknowledgement has its own RPC ID, independent of the
	// terminal response from the original Git request.
	cancelID := float64(45)
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      cancelID,
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
		if gotID, _ := message["id"].(float64); gotID != cancelID {
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
