package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestGatewayHubRetiresInboundWSWithGone(t *testing.T) {
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	hub := NewGatewayHub(store, GatewayHubOptions{})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)

	assertRetiredInboundWS(t, mux)
}

func TestGatewayHubDoesNotUpgradeRetiredInboundWS(t *testing.T) {
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	hub := NewGatewayHub(store, GatewayHubOptions{})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if conn != nil {
		t.Fatal("retired /ws must not upgrade to a WebSocket")
	}
	if err == nil {
		t.Fatal("expected websocket dial to fail")
	}
	if resp == nil {
		t.Fatalf("expected HTTP 410 response, got err=%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("websocket upgrade status=%d want 410", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	assertRetiredInboundWSBody(t, string(body))
}

func assertRetiredInboundWS(t *testing.T, handler http.Handler) {
	t.Helper()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ws", nil))
	if rec.Code != http.StatusGone {
		t.Fatalf("GET /ws status=%d want 410", rec.Code)
	}
	assertRetiredInboundWSBody(t, rec.Body.String())

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusGone {
		t.Fatalf("Upgrade /ws status=%d want 410 (must not switch protocols)", rec.Code)
	}
	assertRetiredInboundWSBody(t, rec.Body.String())
}

func assertRetiredInboundWSBody(t *testing.T, body string) {
	t.Helper()
	for _, want := range []string{"/v1/rpc", "/v1/agent/connect"} {
		if !strings.Contains(body, want) {
			t.Fatalf("retired /ws body missing %q: %s", want, body)
		}
	}
}
