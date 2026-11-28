package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func stubHandshakeSleep(t *testing.T) *[]time.Duration {
	t.Helper()
	var recorded []time.Duration
	prev := websocketHandshakeSleep
	websocketHandshakeSleep = func(d time.Duration) {
		recorded = append(recorded, d)
	}
	t.Cleanup(func() {
		websocketHandshakeSleep = prev
	})
	return &recorded
}

func TestConnectNamesGatewayRPCURLOnHandshakeFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}))
	defer ts.Close()

	client := newGatewayTestClient(t, ts.URL, "dial-error")
	defer client.Close()
	err := client.connect()
	if err == nil {
		t.Fatal("expected gateway handshake failure")
	}
	msg := err.Error()
	if strings.Contains(msg, "agent WS") {
		t.Fatalf("error still pretends to dial agent WS: %v", err)
	}
	if !strings.Contains(msg, "gateway") || !strings.Contains(msg, "/v1/rpc") {
		t.Fatalf("error must name gateway /v1/rpc, got %v", err)
	}
	if !strings.Contains(msg, ts.URL) && !strings.Contains(msg, strings.TrimPrefix(ts.URL, "http://")) {
		t.Fatalf("error must include the dialed URL, got %v", err)
	}
}

func TestConnectSurfacesRetiredWSGoneBody(t *testing.T) {
	const retired = "inbound /ws has been removed; CLI must use /v1/rpc?cluster=&instance=; agents must use /v1/agent/connect"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte(retired))
	}))
	defer ts.Close()

	client := newGatewayTestClient(t, ts.URL, "retired-ws")
	defer client.Close()
	err := client.connect()
	if err == nil {
		t.Fatal("expected 410 handshake failure")
	}
	msg := err.Error()
	if strings.Contains(msg, "agent WS") {
		t.Fatalf("error still pretends to dial agent WS: %v", err)
	}
	if !strings.Contains(msg, "410") {
		t.Fatalf("error must include HTTP 410, got %v", err)
	}
	if !strings.Contains(msg, retired) {
		t.Fatalf("error must include retired /ws body, got %v", err)
	}
}

func TestConnectRetriesTransientHTTPHandshakeErrorsThenSucceeds(t *testing.T) {
	for _, status := range []int{
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	} {
		t.Run(fmt.Sprintf("HTTP_%d", status), func(t *testing.T) {
			sleeps := stubHandshakeSleep(t)
			upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
			var attempts atomic.Int32
			var toolCalls atomic.Int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if attempts.Add(1) <= 2 {
					http.Error(w, fmt.Sprintf("transient %d", status), status)
					return
				}
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Errorf("upgrade on attempt 3: %v", err)
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
							"result":  map[string]interface{}{"protocolVersion": "2024-11-05"},
						})
					case "tools/call":
						toolCalls.Add(1)
						_ = conn.WriteJSON(map[string]interface{}{
							"jsonrpc": "2.0",
							"id":      req["id"],
							"result": map[string]interface{}{
								"content": []interface{}{
									map[string]interface{}{"type": "text", "text": "ok"},
								},
							},
						})
					}
				}
			}))
			defer ts.Close()

			client := newGatewayTestClient(t, ts.URL, "transient-retry")
			defer client.Close()
			if err := client.Call("doops_shell", map[string]interface{}{"command": "hostname"}); err != nil {
				t.Fatalf("expected recovery after transient HTTP %d handshake errors, got %v", status, err)
			}
			if got := attempts.Load(); got != 3 {
				t.Fatalf("expected 3 handshake attempts, got %d", got)
			}
			if got := toolCalls.Load(); got != 1 {
				t.Fatalf("expected exactly 1 tool call after handshake recovery, got %d", got)
			}
			wantSleeps := []time.Duration{
				websocketHandshakeInitialBackoff,
				websocketHandshakeInitialBackoff * 2,
			}
			if len(*sleeps) != len(wantSleeps) || (*sleeps)[0] != wantSleeps[0] || (*sleeps)[1] != wantSleeps[1] {
				t.Fatalf("unexpected exponential backoff sequence: got %v want %v", *sleeps, wantSleeps)
			}
		})
	}
}

func TestConnectExhaustsTransientHTTPHandshakeRetriesWithRealStatus(t *testing.T) {
	for _, status := range []int{
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	} {
		t.Run(fmt.Sprintf("HTTP_%d", status), func(t *testing.T) {
			sleeps := stubHandshakeSleep(t)
			var attempts atomic.Int32
			bodyText := fmt.Sprintf("proxy error %d", status)
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				http.Error(w, bodyText, status)
			}))
			defer ts.Close()

			client := newGatewayTestClient(t, ts.URL, "transient-exhausted")
			defer client.Close()
			err := client.connect()
			if err == nil {
				t.Fatalf("expected persistent HTTP %d to fail", status)
			}
			if got := attempts.Load(); got != websocketHandshakeAttempts {
				t.Fatalf("expected %d handshake attempts, got %d", websocketHandshakeAttempts, got)
			}
			if len(*sleeps) != websocketHandshakeAttempts-1 {
				t.Fatalf("expected %d backoff sleeps, got %d (%v)", websocketHandshakeAttempts-1, len(*sleeps), *sleeps)
			}
			var totalBackoff time.Duration
			for i, d := range *sleeps {
				want := websocketHandshakeInitialBackoff << i
				if d != want {
					t.Fatalf("sleep[%d] = %v, want %v", i, d, want)
				}
				totalBackoff += d
			}
			if totalBackoff < 3*time.Second || totalBackoff > 6*time.Second {
				t.Fatalf("total handshake retry backoff window = %v, want within [3s, 6s]", totalBackoff)
			}
			msg := err.Error()
			if !strings.Contains(msg, fmt.Sprintf("%d", status)) || !strings.Contains(msg, bodyText) {
				t.Fatalf("exhausted error must preserve HTTP %d and body %q, got: %v", status, bodyText, err)
			}
			if !strings.Contains(msg, "gateway /v1/rpc") {
				t.Fatalf("exhausted error must name gateway /v1/rpc, got: %v", err)
			}
		})
	}
}

func TestConnectDoesNotRetryNonTransientHTTPHandshakeErrors(t *testing.T) {
	for _, status := range []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusConflict,
		http.StatusInternalServerError,
	} {
		t.Run(fmt.Sprintf("HTTP_%d", status), func(t *testing.T) {
			sleeps := stubHandshakeSleep(t)
			var attempts atomic.Int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				http.Error(w, fmt.Sprintf("non-retryable %d", status), status)
			}))
			defer ts.Close()

			client := newGatewayTestClient(t, ts.URL, "non-transient")
			defer client.Close()
			err := client.connect()
			if err == nil {
				t.Fatalf("expected HTTP %d handshake failure", status)
			}
			if got := attempts.Load(); got != 1 {
				t.Fatalf("HTTP %d must not be retried, got %d attempts", status, got)
			}
			if len(*sleeps) != 0 {
				t.Fatalf("HTTP %d must not sleep for retry, got %v", status, *sleeps)
			}
			if !strings.Contains(err.Error(), fmt.Sprintf("%d", status)) {
				t.Fatalf("error must include HTTP %d, got: %v", status, err)
			}
		})
	}
}

func TestTransientWebSocketHandshakeErrorClassifier(t *testing.T) {
	transient := []error{
		io.EOF,
		io.ErrUnexpectedEOF,
		net.ErrClosed,
		errors.New("read tcp 127.0.0.1:12345->127.0.0.1:42222: read: connection reset by peer"),
		errors.New("wsarecv: An existing connection was forcibly closed by the remote host."),
	}
	for _, err := range transient {
		if !isTransientWebSocketHandshakeError(err) {
			t.Fatalf("expected transient classification for %v", err)
		}
	}
	nonTransient := []error{
		nil,
		errors.New("x509: certificate signed by unknown authority"),
		errors.New("no such host"),
		errors.New("permission denied"),
	}
	for _, err := range nonTransient {
		if isTransientWebSocketHandshakeError(err) {
			t.Fatalf("expected non-transient classification for %v", err)
		}
	}
}
