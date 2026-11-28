package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestGatewayMySQLProxyConcurrencyAndReconnect validates concurrent same-route
// proxy writes, reads, atomic audit+webhook persistence, and mid-flight Agent
// disconnect/reconnect recovery against a live MySQL backend (24-conn pool).
// Gated on DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT (same fail-closed contract as the
// other GatewayMySQL tests in CI).
func TestGatewayMySQLProxyConcurrencyAndReconnect(t *testing.T) {
	store := openGatewayMySQLCredentialE2EStore(t)
	t.Setenv(secretKeyEnv, strings.Repeat("p", 32))

	ctx := context.Background()
	ep, _, err := store.CreateWebhookEndpoint(ctx, WebhookCreateInput{
		Name:       "mysql-proxy-hook",
		URL:        "https://93.184.216.34/mysql-proxy-hook",
		EventTypes: []string{"execution.finished"},
	})
	if err != nil {
		t.Fatalf("create webhook endpoint on MySQL: %v", err)
	}

	var (
		inFlight      atomic.Int32
		maxInFlight   atomic.Int32
		blockNextPost atomic.Bool
		postBlocked   = make(chan struct{}, 1)
		releaseBlock  = make(chan struct{})
		releaseOnce   sync.Once
	)
	unblock := func() { releaseOnce.Do(func() { close(releaseBlock) }) }
	defer unblock()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/disconnect-probe" && blockNextPost.Load() {
			select {
			case postBlocked <- struct{}{}:
			default:
			}
			select {
			case <-releaseBlock:
			case <-r.Context().Done():
			}
			w.WriteHeader(http.StatusCreated)
			return
		}
		cur := inFlight.Add(1)
		for {
			prev := maxInFlight.Load()
			if cur <= prev || maxInFlight.CompareAndSwap(prev, cur) {
				break
			}
		}
		time.Sleep(15 * time.Millisecond)
		inFlight.Add(-1)
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		_, _ = w.Write([]byte("ok:" + string(body)))
	}))
	defer upstream.Close()

	if _, err := store.UpsertProxyRoute(ProxyRouteInput{
		Host: "mysql-proxy.example.test", Cluster: "dev", Instance: "local",
		Upstream: upstream.URL, Enabled: true,
	}); err != nil {
		t.Fatalf("upsert proxy route on MySQL: %v", err)
	}

	hub := NewGatewayHub(store, GatewayHubOptions{
		AgentLease:           time.Second,
		TargetReconnectGrace: 2 * time.Second,
		OperationTimeout:     5 * time.Second,
		TargetQueueTimeout:   2 * time.Second,
	})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	agentGW1 := NewGateway()
	agentGW1.SetProxyAllowlist([]string{"127.0.0.1"})
	conn1 := connectProxyTestAgent(t, ts.URL, agentGW1, "dev", "local")
	requireEventually(t, 2*time.Second, func() bool { return hub.getAgent("dev", "local") != nil })
	oldAgent := hub.getAgent("dev", "local")

	// Phase 1: 24 concurrent POSTs + 8 concurrent GETs hitting MySQL's 24-conn pool.
	const (
		postN = 24
		getN  = 8
		total = postN + getN
	)
	var wg sync.WaitGroup
	errCh := make(chan string, total)
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			method := http.MethodPost
			want := http.StatusCreated
			payload := fmt.Sprintf("post-%02d", idx)
			if idx >= postN {
				method = http.MethodGet
				want = http.StatusOK
				payload = ""
			}
			req := httptest.NewRequest(method, ts.URL+"/concurrent", strings.NewReader(payload))
			req.Host = "mysql-proxy.example.test"
			rec := httptest.NewRecorder()
			hub.HandlePublicProxy(rec, req)
			if rec.Code != want {
				errCh <- fmt.Sprintf("req %d (%s): status=%d body=%s", idx, method, rec.Code, rec.Body.String())
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Fatalf("MySQL concurrent proxy request failed: %s", msg)
	}
	if peak := maxInFlight.Load(); peak < 4 {
		t.Fatalf("upstream peak concurrency on MySQL = %d, want >= 4", peak)
	}

	events, err := store.ListAuditFiltered(AuditFilter{Action: ActionProxy, Limit: 100})
	if err != nil {
		t.Fatalf("list MySQL proxy audits: %v", err)
	}
	if len(events) != total {
		t.Fatalf("MySQL audit rows = %d, want %d", len(events), total)
	}
	for _, ev := range events {
		if ev.Status != "success" || ev.EndedAt.IsZero() {
			t.Fatalf("MySQL audit row incomplete: %#v", ev)
		}
	}
	deliveries, err := store.ListWebhookDeliveries(ctx, ep.ID, 100)
	if err != nil {
		t.Fatalf("list MySQL webhook deliveries: %v", err)
	}
	if len(deliveries) != total {
		t.Fatalf("MySQL webhook deliveries = %d, want %d", len(deliveries), total)
	}

	// Phase 2: Mid-flight Agent disconnect -> outcome_unknown in MySQL, then
	// immediate reconnect -> 8 concurrent POSTs succeed with zero 409 conflicts.
	blockNextPost.Store(true)
	var discRec *httptest.ResponseRecorder
	discDone := make(chan struct{})
	go func() {
		defer close(discDone)
		req := httptest.NewRequest(http.MethodPost, ts.URL+"/disconnect-probe", strings.NewReader("mid-flight"))
		req.Host = "mysql-proxy.example.test"
		discRec = httptest.NewRecorder()
		hub.HandlePublicProxy(discRec, req)
	}()
	select {
	case <-postBlocked:
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect probe never reached upstream")
	}
	_ = conn1.Close()
	<-discDone
	unblock()
	if discRec.Code != http.StatusBadGateway {
		t.Fatalf("mid-flight disconnect POST status = %d, want 502", discRec.Code)
	}

	agentGW2 := NewGateway()
	agentGW2.SetProxyAllowlist([]string{"127.0.0.1"})
	conn2 := connectProxyTestAgent(t, ts.URL, agentGW2, "dev", "local")
	defer conn2.Close()
	requireEventually(t, 2*time.Second, func() bool {
		cur := hub.getAgent("dev", "local")
		return cur != nil && cur != oldAgent
	})

	const afterN = 8
	var afterWG sync.WaitGroup
	afterErrs := make(chan string, afterN)
	for i := 0; i < afterN; i++ {
		afterWG.Add(1)
		go func(idx int) {
			defer afterWG.Done()
			req := httptest.NewRequest(http.MethodPost, ts.URL+"/after-reconnect", strings.NewReader(fmt.Sprintf("rec-%d", idx)))
			req.Host = "mysql-proxy.example.test"
			rec := httptest.NewRecorder()
			hub.HandlePublicProxy(rec, req)
			if rec.Code != http.StatusCreated {
				afterErrs <- fmt.Sprintf("post-reconnect %d: status=%d body=%s", idx, rec.Code, rec.Body.String())
			}
		}(i)
	}
	afterWG.Wait()
	close(afterErrs)
	for msg := range afterErrs {
		t.Fatalf("MySQL post-reconnect proxy request failed: %s", msg)
	}

	finalEvents, err := store.ListAuditFiltered(AuditFilter{Action: ActionProxy, Limit: 100})
	if err != nil {
		t.Fatalf("list final MySQL proxy audits: %v", err)
	}
	counts := map[string]int{}
	for _, ev := range finalEvents {
		counts[ev.Status]++
	}
	if counts["success"] != total+afterN || counts["outcome_unknown"] != 1 || len(finalEvents) != total+afterN+1 {
		t.Fatalf("final MySQL audit counts = %v (len=%d), want success=%d outcome_unknown=1", counts, len(finalEvents), total+afterN)
	}
}
