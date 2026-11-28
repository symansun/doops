package server

// INT-70: end-to-end proof that the audit row of a proxied request does not
// carry the upstream target's credentials.
//
// Real pieces (same harness as gateway_tunnel_e2e_test.go and
// proxy_tunnel_test.go): a real GatewayStore on a temporary SQLite file, a real
// GatewayHub on an httptest TCP listener, and a real Agent runtime -- the same
// (*Gateway).ServeGatewayWebSocketConnWithReady that cmd/agent drives -- attached
// through the real /v1/agent/connect WebSocket. Only the upstream is synthetic:
// a closed httptest listener is exactly the "connect: connection refused" branch
// of the Agent's http.Client.Do.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	auditScrubHost  = "audit-scrub.example.test"
	auditScrubToken = "token-canary-3f91"
	auditScrubSig   = "sig-canary-7b2c"
)

type proxyAuditScrubEnv struct {
	store   *GatewayStore
	hub     *GatewayHub
	server  *httptest.Server
	routeID string
}

func newProxyAuditScrubEnv(t *testing.T, upstreamURL string) *proxyAuditScrubEnv {
	t.Helper()
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	route, err := store.UpsertProxyRoute(ProxyRouteInput{
		Host: auditScrubHost, Cluster: "dev", Instance: "local",
		Upstream: upstreamURL, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create proxy route: %v", err)
	}
	hub := NewGatewayHub(store, GatewayHubOptions{
		AgentLease: time.Second, TargetReconnectGrace: time.Second,
		OperationTimeout: 2 * time.Second, TargetQueueTimeout: time.Second,
	})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	agentGateway := NewGateway()
	agentGateway.SetProxyAllowlist([]string{"127.0.0.1"})
	agentConn := connectProxyTestAgent(t, server.URL, agentGateway, "dev", "local")
	t.Cleanup(func() { agentConn.Close() })
	requireEventually(t, time.Second, func() bool { return hub.getAgent("dev", "local") != nil })
	return &proxyAuditScrubEnv{store: store, hub: hub, server: server, routeID: route.ID}
}

func (e *proxyAuditScrubEnv) proxyRequest(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, e.server.URL+path, nil)
	req.Host = auditScrubHost
	recorder := httptest.NewRecorder()
	e.hub.HandlePublicProxy(recorder, req)
	return recorder
}

func (e *proxyAuditScrubEnv) auditRow(t *testing.T) AuditEvent {
	t.Helper()
	events, err := e.store.ListAuditFiltered(AuditFilter{Action: ActionProxy, Limit: 20})
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	for _, event := range events {
		if strings.Contains(event.CommandSummary, e.routeID) {
			return event
		}
	}
	t.Fatalf("no audit row for proxy route %s: %#v", e.routeID, events)
	return AuditEvent{}
}

func TestProxyAuditErrorHidesUpstreamURLCredentials(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	upstreamHost := strings.TrimPrefix(closed.URL, "http://")
	closed.Close()

	env := newProxyAuditScrubEnv(t, closed.URL)
	recorder := env.proxyRequest(t, "/a/b?token="+auditScrubToken+"&sig="+auditScrubSig)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", recorder.Code, recorder.Body.String())
	}

	event := env.auditRow(t)
	if event.Status != "error" {
		t.Fatalf("audit status = %q, want error; error=%q", event.Status, event.Error)
	}
	// The row still says which upstream failed and why.
	if !strings.Contains(event.Error, "http://"+upstreamHost+"/a/b") {
		t.Fatalf("audit error lost the upstream host and path: %q", event.Error)
	}
	if !strings.Contains(event.Error, "connection refused") {
		t.Fatalf("audit error lost the failure reason: %q", event.Error)
	}
	// A real *url.Error stringifies the whole target; none of it may survive,
	// and that includes the client query, userinfo and fragment.
	for _, leak := range []string{
		auditScrubToken, auditScrubSig,
		"token=", "sig=", "?", "user:", "@", "#",
	} {
		if strings.Contains(event.Error, leak) {
			t.Fatalf("audit error still carries %q: %q", leak, event.Error)
		}
	}
}

func TestProxyAuditRowForUpstream500CarriesNoURL(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("upstream failed"))
	}))
	defer upstream.Close()

	env := newProxyAuditScrubEnv(t, upstream.URL)
	recorder := env.proxyRequest(t, "/a/b?token="+auditScrubToken+"&sig="+auditScrubSig)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	event := env.auditRow(t)
	// A relayed 500 is a completed tunnel call, not a tunnel failure: the
	// scrubber must not change the classification.
	if event.Status != "success" || event.Error != "" {
		t.Fatalf("audit status=%q error=%q, want success with no error", event.Status, event.Error)
	}
}
