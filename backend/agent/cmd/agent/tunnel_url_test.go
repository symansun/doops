package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/user/doops/agent/internal/server"
)

func TestAgentDefaultsToHostNetworkCompatibleListener(t *testing.T) {
	if got, want := defaultAgentListenAddress, "0.0.0.0"; got != want {
		t.Fatalf("default listener mismatch: got %q want %q", got, want)
	}
}

func TestAgentStartupRequiresGatewayIdentityAndRejectsOldTokenCommand(t *testing.T) {
	if err := validateStartupArgs("", "", "", nil); err == nil {
		t.Fatal("expected missing Gateway identity to fail")
	}
	if err := validateStartupArgs("https://gateway.example.com", "prod", "node", []string{"token"}); err == nil {
		t.Fatal("expected retired token command to fail")
	}
	if err := validateStartupArgs("https://gateway.example.com", "prod", "node", nil); err != nil {
		t.Fatalf("expected complete Gateway identity to pass: %v", err)
	}
}

func TestBuildGatewayAgentURLUsesVersionedEndpoint(t *testing.T) {
	got, err := buildGatewayAgentURL("https://gateway.example.com", "dev", "local")
	if err != nil {
		t.Fatalf("build url: %v", err)
	}
	want := "wss://gateway.example.com/v1/agent/connect?cluster=dev&instance=local"
	if got != want {
		t.Fatalf("gateway URL mismatch\nwant: %s\n got: %s", want, got)
	}
}

func TestBuildGatewayAgentURLAllowsExplicitVersionedEndpoint(t *testing.T) {
	got, err := buildGatewayAgentURL("https://gateway.example.com/v1/agent/connect", "dev", "local")
	if err != nil {
		t.Fatalf("build url: %v", err)
	}
	want := "wss://gateway.example.com/v1/agent/connect?cluster=dev&instance=local"
	if got != want {
		t.Fatalf("gateway URL mismatch\nwant: %s\n got: %s", want, got)
	}
}

func TestBuildGatewayAgentURLRejectsInsecureRemoteByDefault(t *testing.T) {
	// Ensure the default-deny path is exercised regardless of any ambient
	// DOOPS_ALLOW_INSECURE_GATEWAY value in the developer's shell.
	t.Setenv("DOOPS_ALLOW_INSECURE_GATEWAY", "")
	if _, err := buildGatewayAgentURL("http://203.0.113.10:42222", "dev", "local"); err == nil {
		t.Fatal("insecure (ws://) gateway URL to a non-loopback host must be rejected by default")
	}
}

func TestBuildGatewayAgentURLAllowsInsecureRemoteWithOptIn(t *testing.T) {
	t.Setenv("DOOPS_ALLOW_INSECURE_GATEWAY", "1")
	got, err := buildGatewayAgentURL("http://203.0.113.10:42222", "dev", "local")
	if err != nil {
		t.Fatalf("insecure gateway URL should be allowed with opt-in: %v", err)
	}
	want := "ws://203.0.113.10:42222/v1/agent/connect?cluster=dev&instance=local"
	if got != want {
		t.Fatalf("gateway URL mismatch\nwant: %s\n got: %s", want, got)
	}
}

func TestBuildGatewayAgentURLRejectsLegacyDirectAgentWSPath(t *testing.T) {
	t.Setenv("DOOPS_ALLOW_INSECURE_GATEWAY", "1")
	if _, err := buildGatewayAgentURL("http://203.0.113.10:42222/ws", "dev", "local"); err == nil {
		t.Fatal("unsupported /ws path must be rejected for gateway reverse tunnel")
	}
}

func TestBuildGatewayAgentURLDefaultsToSecureScheme(t *testing.T) {
	got, err := buildGatewayAgentURL("gateway.example.com", "dev", "local")
	if err != nil {
		t.Fatalf("bare host should default to wss: %v", err)
	}
	want := "wss://gateway.example.com/v1/agent/connect?cluster=dev&instance=local"
	if got != want {
		t.Fatalf("gateway URL mismatch\nwant: %s\n got: %s", want, got)
	}
}

func TestBuildGatewayEdgeURLRewritesAgentPath(t *testing.T) {
	got, err := buildGatewayEdgeURL("https://gateway.example.com", "demo", "gw-demo")
	if err != nil {
		t.Fatalf("build edge url: %v", err)
	}
	want := "wss://gateway.example.com/v1/edge/connect?cluster=demo&instance=gw-demo"
	if got != want {
		t.Fatalf("edge URL mismatch\nwant: %s\n got: %s", want, got)
	}
	got, err = buildGatewayEdgeURL("https://gateway.example.com/v1/agent/connect", "demo", "gw-demo")
	if err != nil {
		t.Fatalf("rewrite agent path: %v", err)
	}
	if got != want {
		t.Fatalf("rewritten edge URL mismatch\nwant: %s\n got: %s", want, got)
	}
}

func TestBuildGatewayAgentURLAllowsInsecureLocalhostForDevelopment(t *testing.T) {
	got, err := buildGatewayAgentURL("http://localhost:42222", "dev", "local")
	if err != nil {
		t.Fatalf("local insecure gateway should be allowed: %v", err)
	}
	want := "ws://localhost:42222/v1/agent/connect?cluster=dev&instance=local"
	if got != want {
		t.Fatalf("gateway URL mismatch\nwant: %s\n got: %s", want, got)
	}
}

func TestAgentHealthMuxRetiresInboundWS(t *testing.T) {
	mux := http.NewServeMux()
	registerAgentHTTP(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ws", nil))
	if rec.Code != http.StatusGone {
		t.Fatalf("GET /ws status=%d want 410", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"/v1/rpc", "/v1/agent/connect"} {
		if !strings.Contains(body, want) {
			t.Fatalf("retired /ws body missing %q: %s", want, body)
		}
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusGone {
		t.Fatalf("Upgrade /ws status=%d want 410 (must not switch protocols)", rec.Code)
	}
}

func TestHealthLiveAlways200(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	rec := httptest.NewRecorder()
	handleHealthLive(rec, req)
	if got, want := rec.Code, http.StatusOK; got != want {
		t.Fatalf("health live status mismatch: got %d want %d", got, want)
	}
}

func TestHealthReadyDependsOnReverseTunnelConnection(t *testing.T) {
	setReverseTunnelConnected(false)
	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rec := httptest.NewRecorder()
	handleHealthReady(rec, req)
	if got, want := rec.Code, http.StatusServiceUnavailable; got != want {
		t.Fatalf("health ready when disconnected: got %d want %d", got, want)
	}

	setReverseTunnelConnected(true)
	rec = httptest.NewRecorder()
	handleHealthReady(rec, req)
	if got, want := rec.Code, http.StatusOK; got != want {
		t.Fatalf("health ready when connected: got %d want %d", got, want)
	}

	setReverseTunnelConnected(false)
	rec = httptest.NewRecorder()
	handleHealthReady(rec, req)
	if got, want := rec.Code, http.StatusServiceUnavailable; got != want {
		t.Fatalf("health ready after disconnect: got %d want %d", got, want)
	}
}

func TestRunReverseTunnelUpdatesReadyStateOnConnectAndDisconnect(t *testing.T) {
	origDial := reverseTunnelDial
	origServeConn := reverseTunnelServeConn
	origContinue := reverseTunnelContinue
	t.Cleanup(func() {
		reverseTunnelDial = origDial
		reverseTunnelServeConn = origServeConn
		reverseTunnelContinue = origContinue
		setReverseTunnelConnected(false)
	})

	setReverseTunnelConnected(false)

	var loopCount int32
	var authorization string
	reverseTunnelContinue = func() bool {
		return atomic.LoadInt32(&loopCount) < 2
	}
	reverseTunnelDial = func(rawURL string, header http.Header) (*websocket.Conn, *http.Response, error) {
		count := atomic.AddInt32(&loopCount, 1)
		if count == 1 {
			if len(rawURL) == 0 {
				t.Fatalf("invalid dial URL: %s", rawURL)
			}
			authorization = header.Get("Authorization")
			return nil, &http.Response{StatusCode: http.StatusSwitchingProtocols}, nil
		}
		return nil, &http.Response{StatusCode: http.StatusBadRequest}, errors.New("stopped")
	}

	connected := make(chan struct{}, 1)
	reverseTunnelServeConn = func(gw *server.Gateway, conn *websocket.Conn, name string, onReady func()) {
		_ = name
		_ = gw
		_ = conn
		onReady()
		if isReverseTunnelConnected() {
			connected <- struct{}{}
		}
	}

	done := make(chan struct{})
	gw := server.NewGateway()
	go func() {
		runReverseTunnel(gw, "wss://gateway.example.com", "dev", "instance", 1*time.Millisecond)
		close(done)
	}()

	select {
	case <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("never observed ready state during tunnel serve")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runReverseTunnel did not finish in time")
	}
	if isReverseTunnelConnected() {
		t.Fatal("reverse tunnel should be not ready after serve returns")
	}
	if authorization != "" {
		t.Fatalf("reverse tunnel registration must not send authorization, got %q", authorization)
	}
}
