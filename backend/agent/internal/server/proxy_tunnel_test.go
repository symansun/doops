package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestProxyResourceKeyClassifiesOnlySafeMethodsAsQueries(t *testing.T) {
	for _, test := range []struct {
		method string
		want   string
	}{
		{method: http.MethodGet, want: "query:proxy:route-1"},
		{method: http.MethodHead, want: "query:proxy:route-1"},
		{method: http.MethodOptions, want: "query:proxy:route-1"},
		{method: http.MethodTrace, want: "query:proxy:route-1"},
		{method: http.MethodPost, want: "proxy:route-1"},
		{method: http.MethodPut, want: "proxy:route-1"},
		{method: http.MethodPatch, want: "proxy:route-1"},
		{method: http.MethodDelete, want: "proxy:route-1"},
	} {
		t.Run(test.method, func(t *testing.T) {
			if got := proxyResourceKey("route-1", test.method); got != test.want {
				t.Fatalf("proxy resource = %q, want %q", got, test.want)
			}
		})
	}
}

func TestGatewayHTTPProxyUsesAgentReverseTunnelAndStreamsResponse(t *testing.T) {
	var upstreamHost, forwardedHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHost = r.Host
		forwardedHost = r.Header.Get("X-Forwarded-Host")
		w.Header().Set("X-Upstream", "agent-local")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("first"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(20 * time.Millisecond)
		_, _ = w.Write([]byte("-second"))
	}))
	defer upstream.Close()

	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertProxyRoute(ProxyRouteInput{
		Host: "app.example.test", Cluster: "dev", Instance: "local",
		Upstream: upstream.URL, Enabled: true,
	}); err != nil {
		t.Fatalf("create proxy route: %v", err)
	}
	hub := NewGatewayHub(store, GatewayHubOptions{
		AgentLease: time.Second, TargetReconnectGrace: time.Second,
		OperationTimeout: time.Second, TargetQueueTimeout: time.Second,
	})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	gatewayServer := httptest.NewServer(mux)
	defer gatewayServer.Close()

	agentGateway := NewGateway()
	agentGateway.SetProxyAllowlist([]string{"127.0.0.1"})
	agentConn := connectProxyTestAgent(t, gatewayServer.URL, agentGateway, "dev", "local")
	defer agentConn.Close()
	requireEventually(t, time.Second, func() bool { return hub.getAgent("dev", "local") != nil })

	req := httptest.NewRequest(http.MethodGet, gatewayServer.URL+"/stream?x=1", nil)
	req.Host = "app.example.test"
	req.Header.Set("Forwarded", "host=app.example.test")
	req.Header.Set("X-Forwarded-Host", "app.example.test")
	recorder := httptest.NewRecorder()
	hub.HandlePublicProxy(recorder, req)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("proxy status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("X-Upstream"); got != "agent-local" {
		t.Fatalf("proxy header = %q", got)
	}
	if got := recorder.Body.String(); got != "first-second" {
		t.Fatalf("proxy body = %q", got)
	}
	wantUpstreamHost := strings.TrimPrefix(upstream.URL, "http://")
	if upstreamHost != wantUpstreamHost {
		t.Fatalf("upstream Host = %q, want %q", upstreamHost, wantUpstreamHost)
	}
	if forwardedHost != wantUpstreamHost {
		t.Fatalf("upstream X-Forwarded-Host = %q, want %q", forwardedHost, wantUpstreamHost)
	}
}

func TestGatewayHTTPProxyRejectsMissingRouteAndOfflineTarget(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertProxyRoute(ProxyRouteInput{
		Host: "offline.example.test", Cluster: "dev", Instance: "missing",
		Upstream: "http://127.0.0.1:12345", Enabled: true,
	}); err != nil {
		t.Fatalf("create proxy route: %v", err)
	}
	hub := NewGatewayHub(store, GatewayHubOptions{
		TargetReconnectGrace: 10 * time.Millisecond, TargetQueueTimeout: time.Second,
	})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)

	for _, test := range []struct {
		name string
		host string
		want int
	}{
		{name: "missing route", host: "missing.example.test", want: http.StatusNotFound},
		{name: "offline target", host: "offline.example.test", want: http.StatusBadGateway},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://gateway.test/", nil)
			req.Host = test.host
			recorder := httptest.NewRecorder()
			hub.HandlePublicProxy(recorder, req)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestProxyRouteStoreNormalizesAndPersistsRoute(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	route, err := store.UpsertProxyRoute(ProxyRouteInput{
		Host: "APP.Example.Test.", Cluster: "dev", Instance: "local",
		Upstream: "http://127.0.0.1:8080/base/", Enabled: true,
	})
	if err != nil {
		t.Fatalf("upsert proxy route: %v", err)
	}
	if route.Host != "app.example.test" || route.Upstream != "http://127.0.0.1:8080/base" {
		t.Fatalf("route was not normalized: %#v", route)
	}
	reopened, err := store.ProxyRouteByHost("app.example.test:443")
	if err != nil {
		t.Fatalf("lookup normalized route: %v", err)
	}
	if reopened.ID != route.ID || !reopened.Enabled {
		t.Fatalf("persisted route mismatch: %#v", reopened)
	}
	for name, input := range map[string]ProxyRouteInput{
		"credentials": {Host: "bad.example.test", Cluster: "dev", Instance: "local", Upstream: "http://user:pass@127.0.0.1:8080"},
		"query":       {Host: "bad.example.test", Cluster: "dev", Instance: "local", Upstream: "http://127.0.0.1:8080/?token=secret"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := store.UpsertProxyRoute(input); err == nil {
				t.Fatal("invalid upstream was accepted")
			}
		})
	}
}

func TestAgentHTTPProxyRequiresExactUpstreamAllowlist(t *testing.T) {
	reached := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	agentGateway := NewGateway()
	agentGateway.SetProxyAllowlist([]string{"localhost"})
	server := newGatewayAuthorizedAgentServer(t, agentGateway)
	defer server.Close()
	conn := dialAgentTestWS(t, server.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	id := float64(91)
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0", "id": id, "method": "proxy/http",
		"params": map[string]interface{}{
			"method": "GET", "path": "/", "upstream": upstream.URL,
		},
	}); err != nil {
		t.Fatalf("send proxy request: %v", err)
	}
	if err := conn.WriteJSON(map[string]interface{}{
		"jsonrpc": "2.0", "method": "proxy/body",
		"params": map[string]interface{}{"id": id, "eof": true},
	}); err != nil {
		t.Fatalf("send proxy eof: %v", err)
	}
	var response map[string]interface{}
	for {
		if err := conn.ReadJSON(&response); err != nil {
			t.Fatalf("read proxy response: %v", err)
		}
		if gotID, ok := response["id"].(float64); ok && gotID == id && response["error"] != nil {
			break
		}
	}
	if !strings.Contains(response["error"].(map[string]interface{})["message"].(string), "not allowlisted") {
		t.Fatalf("unexpected allowlist error: %#v", response)
	}
	select {
	case <-reached:
		t.Fatal("disallowed upstream was contacted")
	default:
	}
}

func TestGatewayHTTPProxyTimeoutCancelsAgentRequest(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(canceled)
	}))
	defer upstream.Close()

	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertProxyRoute(ProxyRouteInput{
		Host: "slow.example.test", Cluster: "dev", Instance: "local",
		Upstream: upstream.URL, Enabled: true,
	}); err != nil {
		t.Fatalf("create proxy route: %v", err)
	}
	hub := NewGatewayHub(store, GatewayHubOptions{
		AgentLease: time.Second, TargetReconnectGrace: time.Second,
		OperationTimeout: 50 * time.Millisecond, TargetQueueTimeout: time.Second,
	})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	gatewayServer := httptest.NewServer(mux)
	defer gatewayServer.Close()
	agentGateway := NewGateway()
	agentGateway.SetProxyAllowlist([]string{"127.0.0.1"})
	agentConn := connectProxyTestAgent(t, gatewayServer.URL, agentGateway, "dev", "local")
	defer agentConn.Close()
	requireEventually(t, time.Second, func() bool { return hub.getAgent("dev", "local") != nil })

	req := httptest.NewRequest(http.MethodGet, gatewayServer.URL+"/", nil)
	req.Host = "slow.example.test"
	recorder := httptest.NewRecorder()
	hub.HandlePublicProxy(recorder, req)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("timeout status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("upstream request did not start")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("timeout did not cancel agent upstream request")
	}
}

func connectProxyTestAgent(t *testing.T, gatewayURL string, agentGateway *Gateway, cluster, instance string) *websocket.Conn {
	t.Helper()
	conn := dialFakeGatewayAgent(t, gatewayURL, cluster, instance)
	go agentGateway.ServeGatewayWebSocketConnWithReady(conn, "proxy-test-agent", nil)
	return conn
}
