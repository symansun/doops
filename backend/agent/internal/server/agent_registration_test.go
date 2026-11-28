package server

import (
	"fmt"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGatewayAgentRegistrationStaysOpenUntilEnrollmentIsConfirmed(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	// Open registration is opt-in: this test exercises the temporary
	// -allow-open-agent-registration migration mode.
	gw := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, AllowOpenAgentRegistration: true})
	var delivered atomic.Int32
	gw.registration.onRegistered = func(*GatewayAgent) { delivered.Add(1) }
	mux := http.NewServeMux()
	gw.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	agentWS := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/agent/connect?cluster=dev&instance=local"
	openConn, _, err := websocket.DefaultDialer.Dial(agentWS, nil)
	if err != nil {
		t.Fatalf("existing agent must register before an enrollment is confirmed: %v", err)
	}
	defer openConn.Close()
	go serveFakeAgent(t, openConn)
	var openAgent *GatewayAgent
	requireEventually(t, time.Second, func() bool {
		openAgent = gw.getAgent("dev", "local")
		return openAgent != nil && delivered.Load() == 1
	})

	badWS := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/agent/connect?cluster=dev"
	_, resp, err := websocket.DefaultDialer.Dial(badWS, nil)
	if err == nil {
		t.Fatal("agent registration without instance should fail")
	}
	if resp == nil || resp.StatusCode != http.StatusBadRequest {
		if resp == nil {
			t.Fatalf("expected HTTP 400 for missing instance, got nil response: %v", err)
		}
		t.Fatalf("expected HTTP 400 for missing instance, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	created, err := store.CreateAgentEnrollment("dev", "local")
	if err != nil {
		t.Fatalf("create enrollment: %v", err)
	}
	if current := gw.getAgent("dev", "local"); current != openAgent || delivered.Load() != 1 {
		t.Fatal("issuing an enrollment disconnected the live agent")
	}
	pending, _, err := websocket.DefaultDialer.Dial(agentWS, nil)
	if err != nil {
		t.Fatalf("target must stay open until the issued token has been used: %v", err)
	}
	_ = pending.Close()

	enrolled, _, err := websocket.DefaultDialer.Dial(agentWS, http.Header{"Authorization": []string{"Bearer " + created.Plaintext}})
	if err != nil {
		t.Fatalf("dial enrolled agent: %v", err)
	}
	defer enrolled.Close()
	go serveFakeAgent(t, enrolled)
	var ready *GatewayAgent
	requireEventually(t, time.Second, func() bool {
		ready = gw.getAgent("dev", "local")
		return ready != nil && ready != openAgent && delivered.Load() == 2
	})

	if _, resp, err = websocket.DefaultDialer.Dial(agentWS, nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("open registration remained after the enrollment was confirmed")
	}
	resp.Body.Close()
	if current := gw.getAgent("dev", "local"); current != ready || delivered.Load() != 2 {
		t.Fatal("unauthenticated candidate replaced the enrolled session")
	}
	// A token issued for dev/local is not a token for dev/other: it is refused
	// and it never falls back to open registration.
	other := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/agent/connect?cluster=dev&instance=other"
	untouched, resp, err := websocket.DefaultDialer.Dial(other, http.Header{"Authorization": []string{"Bearer " + created.Plaintext}})
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("token for another target must be refused with 401, got err=%v resp=%v", err, resp)
	}
	if untouched != nil {
		_ = untouched.Close()
	}
	resp.Body.Close()
	if current := gw.getAgent("dev", "other"); current != nil {
		t.Fatal("a token for another target registered dev/other")
	}
}

func TestGatewayAgentCandidateDoesNotReplaceReadySessionBeforeInitialize(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	gw := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	gw.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	readyConn := dialFakeGatewayAgent(t, ts.URL, "dev", "local")
	defer readyConn.Close()
	go serveFakeAgent(t, readyConn)

	var readyAgent *GatewayAgent
	requireEventually(t, time.Second, func() bool {
		readyAgent = gw.getAgent("dev", "local")
		return readyAgent != nil
	})

	candidateConn := dialFakeGatewayAgent(t, ts.URL, "dev", "local")
	defer candidateConn.Close()

	time.Sleep(100 * time.Millisecond)
	if current := gw.getAgent("dev", "local"); current != readyAgent {
		t.Fatalf("uninitialized candidate replaced ready session: current=%p ready=%p", current, readyAgent)
	}
}

func TestGatewayAgentCandidateAckFailureKeepsReadySession(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	gw := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	gw.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	readyConn := dialFakeGatewayAgent(t, ts.URL, "dev", "local")
	defer readyConn.Close()
	go serveFakeAgent(t, readyConn)

	var readyAgent *GatewayAgent
	requireEventually(t, time.Second, func() bool {
		readyAgent = gw.getAgent("dev", "local")
		return readyAgent != nil
	})

	gw.registration.acknowledge = func(*GatewayAgent) error {
		return fmt.Errorf("forced acknowledgement failure")
	}
	candidateConn := dialFakeGatewayAgent(t, ts.URL, "dev", "local")
	defer candidateConn.Close()
	go serveFakeAgent(t, candidateConn)

	time.Sleep(100 * time.Millisecond)
	if current := gw.getAgent("dev", "local"); current != readyAgent {
		t.Fatalf("failed candidate replaced ready session: current=%p ready=%p", current, readyAgent)
	}
}

func TestGatewayAgentCandidateHeartbeatFailureKeepsReadySession(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	gw := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, AllowOpenAgentRegistration: true})
	gw.registration.heartbeatTimeout = 50 * time.Millisecond
	mux := http.NewServeMux()
	gw.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	readyConn := dialFakeGatewayAgent(t, ts.URL, "dev", "local")
	defer readyConn.Close()
	go serveFakeAgent(t, readyConn)

	var readyAgent *GatewayAgent
	requireEventually(t, time.Second, func() bool {
		readyAgent = gw.getAgent("dev", "local")
		return readyAgent != nil
	})

	candidateConn := dialFakeGatewayAgent(t, ts.URL, "dev", "local")
	defer candidateConn.Close()
	candidateConn.SetPingHandler(func(string) error { return nil })
	if err := candidateConn.WriteControl(
		websocket.PongMessage,
		[]byte("unsolicited-heartbeat"),
		time.Now().Add(time.Second),
	); err != nil {
		t.Fatalf("write unsolicited pong: %v", err)
	}
	go serveFakeAgent(t, candidateConn)

	time.Sleep(150 * time.Millisecond)
	if current := gw.getAgent("dev", "local"); current != readyAgent {
		t.Fatalf("candidate without heartbeat replaced ready session: current=%p ready=%p", current, readyAgent)
	}
}

func TestGatewayRejectsSupersededRuntimeReconnect(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	registry, err := NewAgentRegistry(store)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}

	old := newGatewayAgentSession("dev", "local", "token", "old", nil, nil)
	old.RuntimeID = "runtime-old"
	if err := registry.Register(old); err != nil {
		t.Fatalf("register old runtime: %v", err)
	}
	replacement := newGatewayAgentSession("dev", "local", "token", "new", nil, nil)
	replacement.RuntimeID = "runtime-new"
	if err := registry.Register(replacement); err != nil {
		t.Fatalf("register replacement runtime: %v", err)
	}

	reconnect := newGatewayAgentSession("dev", "local", "token", "old-reconnect", nil, nil)
	reconnect.RuntimeID = "runtime-old"
	if err := registry.Register(reconnect); err == nil {
		t.Fatal("superseded runtime reconnected and could replace the healthy candidate")
	}
	if current := registry.Get("dev", "local"); current != replacement {
		t.Fatalf("superseded runtime replaced current session: current=%p replacement=%p", current, replacement)
	}
}

func TestGatewayAllowsSupersededRuntimeToRecoverAfterCandidateDisconnects(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	registry, err := NewAgentRegistry(store)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}

	old := newGatewayAgentSession("dev", "local", "token", "old", nil, nil)
	old.RuntimeID = "runtime-old"
	if err := registry.Register(old); err != nil {
		t.Fatalf("register old runtime: %v", err)
	}
	replacement := newGatewayAgentSession("dev", "local", "token", "new", nil, nil)
	replacement.RuntimeID = "runtime-new"
	if err := registry.Register(replacement); err != nil {
		t.Fatalf("register replacement runtime: %v", err)
	}
	if err := registry.Unregister(replacement); err != nil {
		t.Fatalf("unregister failed replacement: %v", err)
	}

	recovered := newGatewayAgentSession("dev", "local", "token", "old-recovered", nil, nil)
	recovered.RuntimeID = "runtime-old"
	if err := registry.Register(recovered); err != nil {
		t.Fatalf("superseded runtime could not recover after candidate disconnect: %v", err)
	}
	if current := registry.Get("dev", "local"); current != recovered {
		t.Fatalf("recovered runtime is not current: current=%p recovered=%p", current, recovered)
	}
}

func TestGatewayKeepsCurrentRuntimeEligibleWhenCandidatePersistenceFails(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	registry, err := NewAgentRegistry(store)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	current := newGatewayAgentSession("dev", "local", "token", "current", nil, nil)
	current.RuntimeID = "runtime-current"
	if err := registry.Register(current); err != nil {
		t.Fatalf("register current runtime: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	candidate := newGatewayAgentSession("dev", "local", "token", "candidate", nil, nil)
	candidate.RuntimeID = "runtime-candidate"
	if err := registry.Register(candidate); err == nil {
		t.Fatal("candidate registration unexpectedly succeeded with a closed store")
	}
	if runtimeIDRetired(registry.retired[current.Key], current.RuntimeID) {
		t.Fatal("current runtime was retired before candidate persistence succeeded")
	}
	if got := registry.Get("dev", "local"); got != current {
		t.Fatalf("candidate persistence failure replaced current runtime: got=%p current=%p", got, current)
	}
}

func TestGatewayStartupReconcilesPersistedOnlineStatus(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := store.MarkAgentOnline(AgentStatus{
		Cluster:    "dev",
		Instance:   "local",
		Generation: 7,
	}); err != nil {
		t.Fatalf("mark stale agent online: %v", err)
	}

	hub := NewGatewayHub(store, GatewayHubOptions{AllowOpenAgentRegistration: true})

	statuses, err := store.ListAgentStatus()
	if err != nil {
		t.Fatalf("list agent status: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Status != "offline" {
		t.Fatalf("gateway startup must reconcile stale online status: %#v", statuses)
	}
	next := &GatewayAgent{Cluster: "dev", Instance: "local", Key: "dev/local", ConnectedAt: time.Now().UTC(), LastSeen: time.Now().UTC()}
	if err := hub.registry.Register(next); err != nil {
		t.Fatalf("register next session: %v", err)
	}
	if next.Generation != 8 {
		t.Fatalf("generation must remain monotonic across gateway restart: got %d want 8", next.Generation)
	}
}

func TestGatewayStaleAgentDisconnectDoesNotMarkReplacementOffline(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	gw := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, AllowOpenAgentRegistration: true})
	oldAgent := &GatewayAgent{
		Cluster:  "dev",
		Instance: "local",
		Key:      tunnelKey("dev", "local"),
		TokenID:  "old",
		Remote:   "old",
	}
	newAgent := &GatewayAgent{
		Cluster:  "dev",
		Instance: "local",
		Key:      tunnelKey("dev", "local"),
		TokenID:  "new",
		Remote:   "new",
	}

	gw.registerAgent(oldAgent)
	gw.registerAgent(newAgent)
	gw.unregisterAgent(oldAgent)

	targets := gw.ListTargets()
	if len(targets) != 1 || targets[0].TokenID != "new" {
		t.Fatalf("expected replacement agent to remain online in memory, got %#v", targets)
	}
	statuses, err := store.ListAgentStatus()
	if err != nil {
		t.Fatalf("list agent status: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Status != "online" || statuses[0].TokenID != "new" {
		t.Fatalf("expected replacement agent to remain online in store, got %#v", statuses)
	}
}
