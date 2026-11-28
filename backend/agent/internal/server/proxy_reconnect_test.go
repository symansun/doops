package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestProxyWaitsForAgentReconnectWithinGraceAndSucceeds(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("echo:" + string(body)))
	}))
	defer upstream.Close()

	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertProxyRoute(ProxyRouteInput{
		Host: "grace.example.test", Cluster: "dev", Instance: "local", Upstream: upstream.URL, Enabled: true,
	}); err != nil {
		t.Fatalf("upsert route: %v", err)
	}

	hub := NewGatewayHub(store, GatewayHubOptions{
		AgentLease:           time.Second,
		TargetReconnectGrace: 2 * time.Second,
		OperationTimeout:     3 * time.Second,
		TargetQueueTimeout:   time.Second,
	})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	agentGW := NewGateway()
	agentGW.SetProxyAllowlist([]string{"127.0.0.1"})

	// Connect Agent 120ms after the proxy request arrives, simulating reconnect
	// within -target-reconnect-grace.
	var agentConnClose func()
	connected := make(chan struct{})
	go func() {
		time.Sleep(120 * time.Millisecond)
		conn := connectProxyTestAgent(t, ts.URL, agentGW, "dev", "local")
		agentConnClose = func() { _ = conn.Close() }
		close(connected)
	}()
	defer func() {
		<-connected
		if agentConnClose != nil {
			agentConnClose()
		}
	}()

	req := httptest.NewRequest(http.MethodPost, ts.URL+"/items", strings.NewReader("reconnect-payload"))
	req.Host = "grace.example.test"
	rec := httptest.NewRecorder()
	hub.HandlePublicProxy(rec, req)

	if rec.Code != http.StatusCreated || rec.Body.String() != "echo:reconnect-payload" {
		t.Fatalf("proxy after reconnect grace = %d %q, want 201 echo:reconnect-payload", rec.Code, rec.Body.String())
	}
	events, err := store.ListAuditFiltered(AuditFilter{Action: ActionProxy, Limit: 10})
	if err != nil || len(events) != 1 || events[0].Status != "success" {
		t.Fatalf("audit after reconnect grace = (%#v, %v), want 1 success row", events, err)
	}
}

func TestProxyReturns502WhenAgentReconnectGraceExpires(t *testing.T) {
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertProxyRoute(ProxyRouteInput{
		Host: "offline.example.test", Cluster: "dev", Instance: "local", Upstream: "http://127.0.0.1:18080", Enabled: true,
	}); err != nil {
		t.Fatalf("upsert route: %v", err)
	}

	hub := NewGatewayHub(store, GatewayHubOptions{
		AgentLease:           time.Second,
		TargetReconnectGrace: 120 * time.Millisecond,
		OperationTimeout:     time.Second,
		TargetQueueTimeout:   time.Second,
	})

	start := time.Now()
	req := httptest.NewRequest(http.MethodPost, "http://offline.example.test/items", strings.NewReader("payload"))
	req.Host = "offline.example.test"
	rec := httptest.NewRecorder()
	hub.HandlePublicProxy(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("offline proxy status = %d, want 502", rec.Code)
	}
	if elapsed < 100*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("grace timeout elapsed = %v, want ~120ms", elapsed)
	}
	if ops := hub.client.listActiveOperations(); len(ops) != 0 {
		t.Fatalf("leaked active operations after offline 502: %#v", ops)
	}
}

func TestProxyInFlightDisconnectRecordsTruthfulOutcomeAndReconnectRecoversWithout409(t *testing.T) {
	releaseBlocked := make(chan struct{})
	var blockedReleaseOnce sync.Once
	unblockUpstream := func() { blockedReleaseOnce.Do(func() { close(releaseBlocked) }) }
	defer unblockUpstream()

	postEntered := make(chan struct{}, 1)
	getEntered := make(chan struct{}, 1)
	var recoveredHits atomic.Int32

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/block-post":
			select {
			case postEntered <- struct{}{}:
			default:
			}
			select {
			case <-releaseBlocked:
			case <-r.Context().Done():
			}
			w.WriteHeader(http.StatusCreated)
		case "/block-get":
			select {
			case getEntered <- struct{}{}:
			default:
			}
			select {
			case <-releaseBlocked:
			case <-r.Context().Done():
			}
			w.WriteHeader(http.StatusOK)
		default:
			recoveredHits.Add(1)
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusCreated)
			} else {
				w.WriteHeader(http.StatusOK)
			}
			_, _ = w.Write([]byte("ok:" + string(body)))
		}
	}))
	defer upstream.Close()

	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertProxyRoute(ProxyRouteInput{
		Host: "reconnect.example.test", Cluster: "dev", Instance: "local", Upstream: upstream.URL, Enabled: true,
	}); err != nil {
		t.Fatalf("upsert route: %v", err)
	}

	hub := NewGatewayHub(store, GatewayHubOptions{
		AgentLease:           time.Second,
		TargetReconnectGrace: time.Second,
		OperationTimeout:     3 * time.Second,
		TargetQueueTimeout:   time.Second,
	})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	agentGW1 := NewGateway()
	agentGW1.SetProxyAllowlist([]string{"127.0.0.1"})
	conn1 := connectProxyTestAgent(t, ts.URL, agentGW1, "dev", "local")
	requireEventually(t, time.Second, func() bool { return hub.getAgent("dev", "local") != nil })
	oldAgent := hub.getAgent("dev", "local")

	var wg sync.WaitGroup
	var postRec, getRec *httptest.ResponseRecorder
	wg.Add(2)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodPost, ts.URL+"/block-post", strings.NewReader("in-flight-post"))
		req.Host = "reconnect.example.test"
		postRec = httptest.NewRecorder()
		hub.HandlePublicProxy(postRec, req)
	}()
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodGet, ts.URL+"/block-get", nil)
		req.Host = "reconnect.example.test"
		getRec = httptest.NewRecorder()
		hub.HandlePublicProxy(getRec, req)
	}()

	for _, ch := range []chan struct{}{postEntered, getEntered} {
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatal("in-flight proxy request never reached upstream")
		}
	}

	// Abruptly sever the Agent WebSocket mid-flight, then reconnect a fresh Agent.
	_ = conn1.Close()
	wg.Wait()
	unblockUpstream()

	if postRec.Code != http.StatusBadGateway {
		t.Fatalf("disconnected POST status = %d, want 502", postRec.Code)
	}
	if getRec.Code != http.StatusBadGateway {
		t.Fatalf("disconnected GET status = %d, want 502", getRec.Code)
	}
	if leakedReaders := benchAgentReaders(oldAgent); leakedReaders != 0 {
		t.Fatalf("old Agent leaked readers = %d after disconnect", leakedReaders)
	}

	events, err := store.ListAuditFiltered(AuditFilter{Action: ActionProxy, Limit: 20})
	if err != nil {
		t.Fatalf("list audits after disconnect: %v", err)
	}
	statusCounts := map[string]int{}
	for _, ev := range events {
		statusCounts[ev.Status]++
	}
	if statusCounts["outcome_unknown"] != 1 || statusCounts["error"] != 1 {
		t.Fatalf("disconnect audit statuses = %v, want outcome_unknown=1 and error=1", statusCounts)
	}

	// Reconnect a brand new Agent generation and verify concurrent POST + GET
	// immediately recover without 409 conflicts.
	agentGW2 := NewGateway()
	agentGW2.SetProxyAllowlist([]string{"127.0.0.1"})
	conn2 := connectProxyTestAgent(t, ts.URL, agentGW2, "dev", "local")
	defer conn2.Close()
	requireEventually(t, time.Second, func() bool {
		cur := hub.getAgent("dev", "local")
		return cur != nil && cur != oldAgent
	})

	const recoveredN = 6
	var recWG sync.WaitGroup
	errs := make(chan string, recoveredN)
	for i := 0; i < recoveredN; i++ {
		recWG.Add(1)
		go func(idx int) {
			defer recWG.Done()
			method := http.MethodPost
			want := http.StatusCreated
			if idx%3 == 0 {
				method = http.MethodGet
				want = http.StatusOK
			}
			req := httptest.NewRequest(method, ts.URL+"/recovered", strings.NewReader("after-reconnect"))
			req.Host = "reconnect.example.test"
			rec := httptest.NewRecorder()
			hub.HandlePublicProxy(rec, req)
			if rec.Code != want {
				errs <- rec.Body.String()
			}
		}(i)
	}
	recWG.Wait()
	close(errs)
	for msg := range errs {
		t.Fatalf("post-reconnect proxy request failed (unexpected 409/502): %s", msg)
	}
	if got := recoveredHits.Load(); got != recoveredN {
		t.Fatalf("recovered upstream hits = %d, want %d", got, recoveredN)
	}
}

func TestProxyCrossGenerationActiveOpDoesNotConflictWhileUpgradeFenceHolds(t *testing.T) {
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	hub := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute})
	agentGen1 := &GatewayAgent{Cluster: "dev", Instance: "local"}
	agentGen2 := &GatewayAgent{Cluster: "dev", Instance: "local"}

	opID, holder := hub.client.registerActiveOperationForAgent(GatewayActiveOperation{
		Resource: "proxy:route-1",
		UserID:   "proxy",
		Cluster:  "dev",
		Instance: "local",
		Action:   ActionProxy,
		Kind:     "proxy",
	}, func() {}, agentGen1)
	if holder != "" {
		t.Fatalf("first proxy op unexpectedly conflicted with %q", holder)
	}
	defer hub.client.finishActiveOperation(opID)

	// A second POST on the same proxy route arriving on a freshly reconnected
	// Agent generation must NOT receive a spurious 409 conflict.
	op2ID, holder2 := hub.client.registerActiveOperationForAgent(GatewayActiveOperation{
		Resource: "proxy:route-1",
		UserID:   "proxy",
		Cluster:  "dev",
		Instance: "local",
		Action:   ActionProxy,
		Kind:     "proxy",
	}, func() {}, agentGen2)
	defer hub.client.finishActiveOperation(op2ID)
	if holder2 != "" {
		t.Fatalf("same-route proxy write across Agent reconnect generations falsely conflicted with %q", holder2)
	}

	// Meanwhile, an Agent upgrade on the same target MUST still observe a
	// conflict against the in-flight proxy operation (upgrade fence intact).
	upgradeHolder := hub.client.activeOperationConflict(GatewayActiveOperation{
		Resource: "target:dev/local",
		UserID:   "admin",
		Cluster:  "dev",
		Instance: "local",
		Action:   ActionAgentUpgrade,
		Kind:     "upgrade",
	}, agentGen2)
	if upgradeHolder == "" {
		t.Fatal("Agent upgrade failed to conflict with in-flight cross-generation proxy operation")
	}
}

func TestProxyTerminalAuditSingleAtomicWriteAndFileFirst(t *testing.T) {
	t.Setenv(secretKeyEnv, "test-proxy-atomic-audit-key")
	t.Setenv(auditStorageEnv, auditStorageModeFile)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("created"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	dir := t.TempDir()
	store, err := OpenGatewayStore(filepath.Join(dir, "gateway.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ep, _, err := store.CreateWebhookEndpoint(context.Background(), WebhookCreateInput{
		Name: "proxy-audit-hook", URL: "https://93.184.216.34/proxy-events",
		EventTypes: []string{"execution.finished"},
	})
	if err != nil {
		t.Fatalf("create webhook endpoint: %v", err)
	}
	if _, err := store.UpsertProxyRoute(ProxyRouteInput{
		Host: "atomic.example.test", Cluster: "dev", Instance: "local", Upstream: upstream.URL, Enabled: true,
	}); err != nil {
		t.Fatalf("upsert route: %v", err)
	}

	hub := NewGatewayHub(store, GatewayHubOptions{
		AgentLease:           time.Second,
		TargetReconnectGrace: time.Second,
		OperationTimeout:     3 * time.Second,
		TargetQueueTimeout:   time.Second,
	})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	agentGW := NewGateway()
	agentGW.SetProxyAllowlist([]string{"127.0.0.1"})
	conn := connectProxyTestAgent(t, ts.URL, agentGW, "dev", "local")
	defer conn.Close()
	requireEventually(t, time.Second, func() bool { return hub.getAgent("dev", "local") != nil })

	// 1 POST + 1 GET through HandlePublicProxy.
	postReq := httptest.NewRequest(http.MethodPost, ts.URL+"/write", strings.NewReader("payload-body"))
	postReq.Host = "atomic.example.test"
	postRec := httptest.NewRecorder()
	hub.HandlePublicProxy(postRec, postReq)
	if postRec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201", postRec.Code)
	}

	getReq := httptest.NewRequest(http.MethodGet, ts.URL+"/read", nil)
	getReq.Host = "atomic.example.test"
	getRec := httptest.NewRecorder()
	hub.HandlePublicProxy(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", getRec.Code)
	}

	events, err := store.ListAuditFiltered(AuditFilter{Action: ActionProxy, Limit: 10})
	if err != nil || len(events) != 2 {
		t.Fatalf("audit_events = (%#v, %v), want 2 rows", events, err)
	}
	for _, ev := range events {
		if ev.Status != "success" || ev.EndedAt.IsZero() {
			t.Fatalf("audit row not finalized cleanly: %#v", ev)
		}
	}

	// File-First JSONL must contain EXACTLY 2 lines (1 per request, zero duplicates).
	recordsDir := filepath.Join(store.AuditDocDir(), "records")
	entries, err := os.ReadDir(recordsDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("read File-First audit records dir %s: (%v, %v)", recordsDir, entries, err)
	}
	totalLines := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(recordsDir, entry.Name()))
		if err != nil {
			t.Fatalf("read jsonl %s: %v", entry.Name(), err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if strings.TrimSpace(line) != "" {
				totalLines++
			}
		}
	}
	if totalLines != 2 {
		t.Fatalf("File-First JSONL record count = %d, want exact 2 (no missing or duplicate appends)", totalLines)
	}

	// Atomic webhook delivery: exact 2 execution.finished deliveries.
	deliveries, err := store.ListWebhookDeliveries(context.Background(), ep.ID, 10)
	if err != nil || len(deliveries) != 2 {
		t.Fatalf("webhook deliveries = (%#v, %v), want exact 2", deliveries, err)
	}

	// Zero leaked in-flight File-First audit entries.
	store.auditDocMu.Lock()
	remainingInFlight := len(store.auditInFlight)
	store.auditDocMu.Unlock()
	if remainingInFlight != 0 {
		t.Fatalf("in-flight audit map leaked %d entries", remainingInFlight)
	}
}
