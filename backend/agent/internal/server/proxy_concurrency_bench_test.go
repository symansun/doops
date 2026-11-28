package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// This file carries the B-11 evidence: a real Gateway (HTTP listener, SQLite
// store, active-operation registry) talking to a real Agent tunnel over a real
// WebSocket, with an httptest upstream on the far side. Nothing on the request
// path is faked, so lease waits and audit writes show up exactly where they
// show up in production. TestProxyRouteConcurrencyBenchmark prints the measured
// table; it runs only with DOOPS_PROXY_BENCH=1 so the ordinary suite stays fast.

type proxyBenchUpstream struct {
	server  *httptest.Server
	service time.Duration
	// probeOpenAudits counts non-terminal proxy audit rows at the moment a
	// request reaches the upstream. Audit-before-mutation means the count can
	// never be zero while a mutation is arriving.
	probeOpenAudits bool

	mu       sync.Mutex
	arrivals map[string]time.Time
	served   map[string]time.Time
	order    []string
	hits     map[string]int

	inFlight    int
	maxInFlight int
	probeZero   int
	probeMin    int
	probeCalls  int

	store *GatewayStore
}

func (u *proxyBenchUpstream) String() string { return u.server.URL }

func (u *proxyBenchUpstream) handler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	id := strings.TrimSpace(string(body))
	if id == "" {
		id = strings.TrimSpace(r.URL.Query().Get("id"))
	}
	now := time.Now()

	u.mu.Lock()
	if u.arrivals == nil {
		u.arrivals = map[string]time.Time{}
		u.served = map[string]time.Time{}
		u.hits = map[string]int{}
	}
	u.hits[id]++
	u.arrivals[id] = now
	u.order = append(u.order, id)
	u.inFlight++
	if u.inFlight > u.maxInFlight {
		u.maxInFlight = u.inFlight
	}
	u.mu.Unlock()

	if u.probeOpenAudits && u.store != nil {
		var open int
		row := u.store.readDB().QueryRow(
			"SELECT COUNT(*) FROM audit_events WHERE action = 'proxy' AND status IN ('running','executing')")
		if err := row.Scan(&open); err == nil {
			u.mu.Lock()
			u.probeCalls++
			if open == 0 {
				u.probeZero++
			}
			u.probeMin = open
			u.mu.Unlock()
		}
	}

	if u.service > 0 {
		time.Sleep(u.service)
	}

	u.mu.Lock()
	u.inFlight--
	u.served[id] = time.Now()
	u.mu.Unlock()

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusCreated)
	_, _ = io.WriteString(w, id)
}

type proxyBenchSample struct {
	id      string
	method  string
	status  int
	body    string
	err     error
	start   time.Time
	done    time.Time
	arrival time.Time
	served  time.Time
}

func (s proxyBenchSample) admit() time.Duration   { return s.arrival.Sub(s.start) }
func (s proxyBenchSample) service() time.Duration { return s.served.Sub(s.arrival) }

func (s proxyBenchSample) tail() time.Duration  { return s.done.Sub(s.served) }
func (s proxyBenchSample) total() time.Duration { return s.done.Sub(s.start) }

type proxyBenchEnv struct {
	t        *testing.T
	store    *GatewayStore
	hub      *GatewayHub
	gateway  *httptest.Server
	agent    *Gateway
	upstream *proxyBenchUpstream
	client   *http.Client
	hosts    []string
}

func newProxyBenchEnv(t *testing.T, opts GatewayHubOptions, hosts []string, service time.Duration) *proxyBenchEnv {
	t.Helper()
	env := &proxyBenchEnv{t: t, hosts: hosts}
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	env.store = store

	env.upstream = &proxyBenchUpstream{service: service, store: store}
	env.upstream.server = httptest.NewServer(http.HandlerFunc(env.upstream.handler))
	for _, host := range hosts {
		if _, err := store.UpsertProxyRoute(ProxyRouteInput{
			Host: host, Cluster: "dev", Instance: "local",
			Upstream: env.upstream.server.URL, Enabled: true,
		}); err != nil {
			t.Fatalf("create proxy route %s: %v", host, err)
		}
	}

	env.hub = NewGatewayHub(store, opts)
	mux := http.NewServeMux()
	env.hub.RegisterRoutes(mux)
	env.gateway = httptest.NewServer(mux)

	env.agent = NewGateway()
	env.agent.SetProxyAllowlist([]string{"127.0.0.1"})
	conn := connectProxyTestAgent(t, env.gateway.URL, env.agent, "dev", "local")
	waitForGatewayAgent(t, env.hub, "dev", "local")

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 128
	env.client = &http.Client{Transport: transport}

	t.Cleanup(func() {
		env.client.CloseIdleConnections()
		_ = conn.Close()
		env.gateway.Close()
		env.upstream.server.Close()
		_ = store.Close()
	})
	return env
}

// run fires n concurrent requests of one method at one host and returns the
// per-request timing decomposition: admit = client send -> upstream arrival
// (route lookup + StartAudit + lease wait + beginExecution), service = upstream
// work, tail = upstream response -> client body end (finish audits + relay).
func (e *proxyBenchEnv) run(n int, method, host string) []proxyBenchSample {
	e.t.Helper()
	samples := make([]proxyBenchSample, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%s-%03d", strings.ToLower(method), i)
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			<-start
			var body io.Reader
			target := e.gateway.URL + "/work"
			if method != http.MethodGet {
				body = strings.NewReader(id)
			} else {
				target += "?id=" + id
			}
			req, err := http.NewRequest(method, target, body)
			if err != nil {
				e.t.Errorf("build request: %v", err)
				return
			}
			req.Host = host
			req.Header.Set("Forwarded", "host="+host)
			sample := proxyBenchSample{id: id, method: method, start: time.Now()}
			resp, err := e.client.Do(req)
			if err != nil {
				sample.err = err
				samples[i] = sample
				return
			}
			payload, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			sample.done = time.Now()
			sample.status = resp.StatusCode
			sample.body = strings.TrimSpace(string(payload))
			sample.err = readErr
			samples[i] = sample
		}(i, id)
	}
	close(start)
	wg.Wait()

	e.upstream.mu.Lock()
	for i := range samples {
		if arrival, ok := e.upstream.arrivals[samples[i].id]; ok {
			samples[i].arrival = arrival
		}
		if served, ok := e.upstream.served[samples[i].id]; ok {
			samples[i].served = served
		}
	}
	e.upstream.mu.Unlock()
	return samples
}

type proxyBenchRound struct {
	samples     []proxyBenchSample
	wall        time.Duration
	maxInFlight int
}

func (e *proxyBenchEnv) resetUpstreamPeak() {
	e.upstream.mu.Lock()
	e.upstream.maxInFlight = 0
	e.upstream.inFlight = 0
	e.upstream.mu.Unlock()
}

func (e *proxyBenchEnv) upstreamPeak() int {
	e.upstream.mu.Lock()
	defer e.upstream.mu.Unlock()
	return e.upstream.maxInFlight
}

// round resets the concurrency peak, runs one burst, and records both the bust
// wall clock and the peak number of requests the upstream saw at once.
func (e *proxyBenchEnv) round(n int, method, host string) proxyBenchRound {
	e.resetUpstreamPeak()
	begin := time.Now()
	samples := e.run(n, method, host)
	return proxyBenchRound{samples: samples, wall: time.Since(begin), maxInFlight: e.upstreamPeak()}
}

func proxyBenchPercentile(values []time.Duration, p float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(float64(len(sorted)-1) * p)
	if idx < 0 {
		idx = 0
	}
	return sorted[idx]
}

func proxyBenchColumn(samples []proxyBenchSample, pick func(proxyBenchSample) time.Duration) []time.Duration {
	out := make([]time.Duration, 0, len(samples))
	for _, sample := range samples {
		if sample.err != nil || sample.arrival.IsZero() || sample.served.IsZero() {
			continue
		}
		out = append(out, pick(sample))
	}
	return out
}

func proxyBenchWall(samples []proxyBenchSample) time.Duration {
	var first, last time.Time
	for _, sample := range samples {
		if sample.start.IsZero() || sample.done.IsZero() {
			continue
		}
		if first.IsZero() || sample.start.Before(first) {
			first = sample.start
		}
		if sample.done.After(last) {
			last = sample.done
		}
	}
	if first.IsZero() {
		return 0
	}
	return last.Sub(first)
}

func proxyBenchFailures(samples []proxyBenchSample, want int) int {
	failed := 0
	for _, sample := range samples {
		if sample.err != nil || sample.status != want {
			failed++
		}
	}
	return failed
}

func proxyBenchMS(d time.Duration) string {
	return fmt.Sprintf("%7.2f", float64(d)/float64(time.Millisecond))
}

// measureProxyAuditCost times the three audit writes a proxied POST performs on
// the request path, on the same store the benchmark serves from. concurrency
// replicates N requests writing at once, which is where the single write
// connection serializes them.
func measureProxyAuditCost(t *testing.T, store *GatewayStore, concurrency, rounds int) map[string][]time.Duration {
	t.Helper()
	phases := map[string][]time.Duration{"start": {}, "dispatch": {}, "finish": {}, "total": {}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				begin := time.Now()
				id, err := store.StartAudit(AuditEvent{
					UserID: "proxy", Cluster: "dev", Instance: "local", Action: ActionProxy,
					CommandSummary: "proxy bench", StartedAt: time.Now().UTC(),
				})
				if err != nil {
					t.Errorf("start audit: %v", err)
					return
				}
				dispatched := time.Now()
				if err := store.MarkExecutionDispatched(id); err != nil {
					t.Errorf("mark dispatched: %v", err)
					return
				}
				finished := time.Now()
				if err := store.FinishAudit(id, AuditFinish{Status: "success", BytesIn: 1, EndedAt: time.Now().UTC()}); err != nil {
					t.Errorf("finish audit: %v", err)
					return
				}
				end := time.Now()
				mu.Lock()
				phases["start"] = append(phases["start"], dispatched.Sub(begin))
				phases["dispatch"] = append(phases["dispatch"], finished.Sub(dispatched))
				phases["finish"] = append(phases["finish"], end.Sub(finished))
				phases["total"] = append(phases["total"], end.Sub(begin))
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return phases
}

// TestProxySameRouteWritesDoNotSerialize is the B-11 reverse experiment: it
// fails while one proxied write holds an exclusive per-route lease.
func TestProxySameRouteWritesDoNotSerialize(t *testing.T) {
	const service = 120 * time.Millisecond
	env := newProxyBenchEnv(t, GatewayHubOptions{
		AgentLease: time.Second, TargetReconnectGrace: time.Second,
		OperationTimeout: 10 * time.Second, TargetQueueTimeout: 5 * time.Second,
		MaxQueuedPerTarget: 16,
	}, []string{"write.example.test"}, service)

	samples := env.run(4, http.MethodPost, "write.example.test")
	for _, sample := range samples {
		if sample.err != nil {
			t.Fatalf("%s failed: %v", sample.id, sample.err)
		}
		if sample.status != http.StatusCreated {
			t.Fatalf("%s returned %d, want 201", sample.id, sample.status)
		}
		if sample.body != sample.id {
			t.Fatalf("%s received body %q, want %q", sample.id, sample.body, sample.id)
		}
	}
	if got := env.upstream.maxInFlight; got < 2 {
		t.Fatalf("same-route writes were serialized: the upstream never saw more than %d concurrent write, "+
			"but independent proxied HTTP requests must not share an exclusive lease", got)
	}
}

// TestConcurrentProxyWritesKeepAuditTruthful pins what concurrency must not
// break: one open audit row per in-flight request, one terminal success row per
// finished request, and exactly one upstream call per client request.
func TestConcurrentProxyWritesKeepAuditTruthful(t *testing.T) {
	const (
		requests = 12
		service  = 40 * time.Millisecond
	)
	env := newProxyBenchEnv(t, GatewayHubOptions{
		AgentLease: time.Second, TargetReconnectGrace: time.Second,
		OperationTimeout: 20 * time.Second, TargetQueueTimeout: 10 * time.Second,
		MaxQueuedPerTarget: 32,
	}, []string{"audit.example.test"}, service)
	env.upstream.probeOpenAudits = true

	samples := env.run(requests, http.MethodPost, "audit.example.test")
	for _, sample := range samples {
		if sample.err != nil {
			t.Fatalf("%s failed: %v", sample.id, sample.err)
		}
		if sample.status != http.StatusCreated || sample.body != sample.id {
			t.Fatalf("%s returned %d body %q, want 201 body %q", sample.id, sample.status, sample.body, sample.id)
		}
		if hit := env.upstream.hits[sample.id]; hit != 1 {
			t.Fatalf("%s reached the upstream %d times, want exactly 1", sample.id, hit)
		}
	}

	env.upstream.mu.Lock()
	zero, calls, minOpen := env.upstream.probeZero, env.upstream.probeCalls, env.upstream.probeMin
	env.upstream.mu.Unlock()
	if calls == 0 {
		t.Fatal("upstream audit probe never ran")
	}
	if zero != 0 {
		t.Fatalf("%d of %d upstream arrivals found no open proxy audit row (minimum open rows %d): "+
			"the audit row must be open before the mutation is dispatched", zero, calls, minOpen)
	}

	var events []AuditEvent
	requireEventually(t, 5*time.Second, func() bool {
		listed, err := env.store.ListAuditFiltered(AuditFilter{Action: ActionProxy, Limit: 500})
		if err != nil {
			t.Fatalf("list audit: %v", err)
		}
		events = listed
		terminal := 0
		for _, event := range listed {
			if event.Status != "running" && event.Status != "executing" {
				terminal++
			}
		}
		return terminal == requests
	})
	if len(events) != requests {
		t.Fatalf("stored %d proxy audit rows for %d requests", len(events), requests)
	}
	for _, event := range events {
		if event.Status != "success" {
			t.Fatalf("audit %d finished as %q, want success", event.ID, event.Status)
		}
		if event.BytesIn <= 0 {
			t.Fatalf("audit %d did not record the request body size", event.ID)
		}
		if event.EndedAt.IsZero() || event.EndedAt.Before(event.StartedAt) {
			t.Fatalf("audit %d has inconsistent timestamps: %s -> %s", event.ID, event.StartedAt, event.EndedAt)
		}
	}
}

// benchAgentReaders, benchAgentWriters and benchAgentUpgradeWaiters read the
// admission counters the Agent-upgrade fence is built from. A leaked reader does
// not fail a request: it stalls every future upgrade of that target, which is
// the most dangerous failure mode of admitting proxied traffic as readers.
func benchAgentReaders(agent *GatewayAgent) int {
	agent.opsMu.Lock()
	defer agent.opsMu.Unlock()
	return agent.readers
}

func benchAgentWriters(agent *GatewayAgent) int {
	agent.opsMu.Lock()
	defer agent.opsMu.Unlock()
	return agent.writers
}

func benchAgentUpgradeWaiters(agent *GatewayAgent) int {
	agent.opsMu.Lock()
	defer agent.opsMu.Unlock()
	return agent.upgradeWaiters
}

func (u *proxyBenchUpstream) inFlightNow() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.inFlight
}

// proxyAuditRows waits until want proxied requests have reached a terminal audit
// state and returns every stored proxy row.
func proxyAuditRows(t *testing.T, store *GatewayStore, want int) []AuditEvent {
	t.Helper()
	var events []AuditEvent
	requireEventually(t, 5*time.Second, func() bool {
		listed, err := store.ListAuditFiltered(AuditFilter{Action: ActionProxy, Limit: 500})
		if err != nil {
			t.Fatalf("list audit: %v", err)
		}
		events = listed
		terminal := 0
		for _, event := range listed {
			if event.Status != "running" && event.Status != "executing" {
				terminal++
			}
		}
		return terminal >= want
	})
	return events
}

// TestProxyWriteRejectedWhileUpgradePending pins the first half of the fence: an
// Agent upgrade that is already waiting rejects new proxied writes, and it does
// so through the audited path (502 plus a "blocked" audit row), not by silently
// queueing them behind the upgrade.
func TestProxyWriteRejectedWhileUpgradePending(t *testing.T) {
	const service = 300 * time.Millisecond
	env := newProxyBenchEnv(t, GatewayHubOptions{
		AgentLease: time.Second, TargetReconnectGrace: time.Second,
		OperationTimeout: 10 * time.Second, TargetQueueTimeout: 2 * time.Second,
		MaxQueuedPerTarget: 8,
	}, []string{"fence.example.test"}, service)

	agent := env.hub.getAgent("dev", "local")
	if agent == nil {
		t.Fatal("agent did not register")
	}
	burst := make(chan []proxyBenchSample, 1)
	go func() { burst <- env.run(2, http.MethodPost, "fence.example.test") }()
	requireEventually(t, 2*time.Second, func() bool { return env.upstream.inFlightNow() >= 1 })

	type upgradeResult struct {
		lease *operationLease
		err   error
	}
	upgrade := make(chan upgradeResult, 1)
	go func() {
		lease, err := agent.acquireActionLease(context.Background(), ActionAgentUpgrade, "target:dev/local", 1, 10*time.Second)
		upgrade <- upgradeResult{lease: lease, err: err}
	}()
	requireEventually(t, 2*time.Second, func() bool { return benchAgentUpgradeWaiters(agent) > 0 })

	blocked := env.run(1, http.MethodPost, "fence.example.test")
	if blocked[0].status != http.StatusBadGateway {
		t.Fatalf("a proxied write while an Agent upgrade was waiting returned %d, want 502", blocked[0].status)
	}

	for _, sample := range <-burst {
		if sample.status != http.StatusCreated {
			t.Fatalf("a proxied write that was already running returned %d, want 201", sample.status)
		}
	}
	var granted upgradeResult
	select {
	case granted = <-upgrade:
	case <-time.After(10 * time.Second):
		t.Fatal("the Agent upgrade never started after the proxied writes drained")
	}
	if granted.err != nil {
		t.Fatalf("the Agent upgrade could not start after the proxied writes drained: %v", granted.err)
	}
	granted.lease.release()

	allowed := env.run(1, http.MethodPost, "fence.example.test")
	if allowed[0].status != http.StatusCreated {
		t.Fatalf("a proxied write after the upgrade lease was released returned %d, want 201", allowed[0].status)
	}

	events := proxyAuditRows(t, env.store, 4)
	blockedRows := 0
	for _, event := range events {
		if event.Status == "blocked" {
			blockedRows++
		}
	}
	if blockedRows != 1 {
		t.Fatalf("stored %d blocked proxy audit rows for one refused write, want 1", blockedRows)
	}
}

// TestProxyTrafficDrainsBeforeUpgradeStarts pins the second half of the fence:
// while proxied requests are in flight the target lock cannot be granted, and
// once they drain it is granted without further waiting.
func TestProxyTrafficDrainsBeforeUpgradeStarts(t *testing.T) {
	const (
		requests = 4
		service  = 300 * time.Millisecond
	)
	env := newProxyBenchEnv(t, GatewayHubOptions{
		AgentLease: time.Second, TargetReconnectGrace: time.Second,
		OperationTimeout: 10 * time.Second, TargetQueueTimeout: 2 * time.Second,
		MaxQueuedPerTarget: 8,
	}, []string{"fence.example.test"}, service)

	agent := env.hub.getAgent("dev", "local")
	if agent == nil {
		t.Fatal("agent did not register")
	}
	burst := make(chan []proxyBenchSample, 1)
	go func() { burst <- env.run(requests, http.MethodPost, "fence.example.test") }()
	requireEventually(t, 2*time.Second, func() bool { return env.upstream.inFlightNow() >= 2 })

	type upgradeResult struct {
		lease *operationLease
		err   error
	}
	upgrade := make(chan upgradeResult, 1)
	begin := time.Now()
	go func() {
		lease, err := agent.acquireActionLease(context.Background(), ActionAgentUpgrade, "target:dev/local", 1, 10*time.Second)
		upgrade <- upgradeResult{lease: lease, err: err}
	}()
	select {
	case granted := <-upgrade:
		t.Fatalf("the Agent upgrade acquired the target lock while %d proxied writes were in flight (err=%v)",
			benchAgentReaders(agent), granted.err)
	case <-time.After(150 * time.Millisecond):
	}
	if got := benchAgentReaders(agent); got < 2 {
		t.Fatalf("the Agent counted %d readers while %d proxied writes were in flight", got, requests)
	}

	for _, sample := range <-burst {
		if sample.status != http.StatusCreated {
			t.Fatalf("a proxied write returned %d, want 201", sample.status)
		}
	}
	var granted upgradeResult
	select {
	case granted = <-upgrade:
	case <-time.After(10 * time.Second):
		t.Fatal("the Agent upgrade never started after the proxied writes drained")
	}
	if granted.err != nil {
		t.Fatalf("the Agent upgrade could not start after the proxied writes drained: %v", granted.err)
	}
	if waited := time.Since(begin); waited < service/2 {
		t.Fatalf("the Agent upgrade started after %s while the proxied writes needed %s", waited, service)
	}
	// The lease release runs in the handler's deferred finish, which can trail the
	// client-visible response by a scheduling step; the counter only has to be
	// back to zero, not to be zero at the instant the last response ends.
	requireEventually(t, 2*time.Second, func() bool { return benchAgentReaders(agent) == 0 })
	granted.lease.release()
}

// TestProxyLeasesDoNotLeakReaders is the guard against the expensive failure
// mode: admitted proxied traffic must leave the Agent counters exactly as it
// found them, or the next upgrade of that target waits forever.
func TestProxyLeasesDoNotLeakReaders(t *testing.T) {
	const requests = 8
	env := newProxyBenchEnv(t, GatewayHubOptions{
		AgentLease: time.Second, TargetReconnectGrace: time.Second,
		OperationTimeout: 10 * time.Second, TargetQueueTimeout: 2 * time.Second,
		MaxQueuedPerTarget: 8,
	}, []string{"leak.example.test"}, 30*time.Millisecond)

	agent := env.hub.getAgent("dev", "local")
	if agent == nil {
		t.Fatal("agent did not register")
	}
	for _, sample := range env.run(requests, http.MethodPost, "leak.example.test") {
		if sample.status != http.StatusCreated {
			t.Fatalf("%s returned %d, want 201", sample.id, sample.status)
		}
	}
	// The handler releases its lease in a deferred finish that may land just
	// after the client sees the last byte, so wait for the drain instead of
	// sampling once.
	requireEventually(t, 2*time.Second, func() bool {
		return benchAgentReaders(agent) == 0 && benchAgentWriters(agent) == 0
	})
	agent.opsMu.Lock()
	proxySlots := 0
	for key := range agent.resources {
		if strings.HasPrefix(key, "proxy:") {
			proxySlots++
		}
	}
	agent.opsMu.Unlock()
	if proxySlots != 0 {
		t.Fatalf("proxied writes left %d per-route resource slots allocated", proxySlots)
	}

	lease, err := agent.acquireActionLease(context.Background(), ActionAgentUpgrade, "target:dev/local", 1, time.Second)
	if err != nil {
		t.Fatalf("the Agent upgrade could not take the target lock after the proxied writes finished: %v", err)
	}
	lease.release()

	after := env.run(1, http.MethodPost, "leak.example.test")
	if after[0].status != http.StatusCreated {
		t.Fatalf("a proxied write after the upgrade lease was released returned %d, want 201", after[0].status)
	}
}

// TestSameRouteConcurrentWritesAllSucceed records the bound that replaced the
// per-route queue: eight concurrent writes on one route all succeed, reach the
// upstream in parallel, and each leaves exactly one audit row.
func TestSameRouteConcurrentWritesAllSucceed(t *testing.T) {
	const requests = 8
	env := newProxyBenchEnv(t, GatewayHubOptions{
		AgentLease: time.Second, TargetReconnectGrace: time.Second,
		OperationTimeout: 20 * time.Second, TargetQueueTimeout: 10 * time.Second,
		MaxQueuedPerTarget: 32,
	}, []string{"burst.example.test"}, 40*time.Millisecond)

	samples := env.run(requests, http.MethodPost, "burst.example.test")
	for _, sample := range samples {
		if sample.err != nil {
			t.Fatalf("%s failed: %v", sample.id, sample.err)
		}
		if sample.status != http.StatusCreated || sample.body != sample.id {
			t.Fatalf("%s returned %d body %q, want 201 body %q", sample.id, sample.status, sample.body, sample.id)
		}
		if hit := env.upstream.hits[sample.id]; hit != 1 {
			t.Fatalf("%s reached the upstream %d times, want exactly 1", sample.id, hit)
		}
	}
	if peak := env.upstreamPeak(); peak < 2 {
		t.Fatalf("the upstream saw at most %d concurrent writes for %d concurrent requests", peak, requests)
	} else {
		t.Logf("upstream peak concurrency: %d of %d concurrent requests", peak, requests)
	}

	events := proxyAuditRows(t, env.store, requests)
	if len(events) != requests {
		t.Fatalf("stored %d proxy audit rows for %d requests", len(events), requests)
	}
	for _, event := range events {
		if event.Status != "success" {
			t.Fatalf("audit %d finished as %q, want success", event.ID, event.Status)
		}
	}
}

// TestProxyRouteConcurrencyBenchmark prints the B-11 measurement table:
// per-scenario wall clock, throughput, p50/p95 latency, and the
// admit/service/tail split that separates lease waiting from audit writing.
func TestProxyRouteConcurrencyBenchmark(t *testing.T) {
	if os.Getenv("DOOPS_PROXY_BENCH") == "" {
		t.Skip("set DOOPS_PROXY_BENCH=1 to run the B-11 proxy concurrency benchmark")
	}
	const (
		service = 20 * time.Millisecond
		rounds  = 5
	)
	opts := GatewayHubOptions{
		AgentLease: time.Second, TargetReconnectGrace: time.Second,
		OperationTimeout: 30 * time.Second, TargetQueueTimeout: 10 * time.Second,
		MaxQueuedPerTarget: 32,
	}
	t.Logf("%-26s %3s %8s %7s %7s %7s %8s %8s %7s %7s %4s %5s", "scenario", "N", "wall_ms", "rps", "p50", "p95", "admit50", "admit95", "svc95", "tail95", "fail", "peak")
	report := func(label string, rounds []proxyBenchRound, want int) {
		var samples []proxyBenchSample
		var wall time.Duration
		peak := 0
		for _, round := range rounds {
			samples = append(samples, round.samples...)
			wall += round.wall
			if round.maxInFlight > peak {
				peak = round.maxInFlight
			}
		}
		wall /= time.Duration(len(rounds))
		total := proxyBenchColumn(samples, proxyBenchSample.total)
		admit := proxyBenchColumn(samples, proxyBenchSample.admit)
		svc := proxyBenchColumn(samples, proxyBenchSample.service)
		tail := proxyBenchColumn(samples, proxyBenchSample.tail)
		rps := 0.0
		if wall > 0 {
			rps = float64(len(samples)) / float64(len(rounds)) / wall.Seconds()
		}
		ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
		t.Logf("%-26s %3d %8.2f %7.1f %7.2f %7.2f %8.2f %8.2f %7.2f %7.2f %4d %5d",
			label, len(samples)/len(rounds), ms(wall), rps,
			ms(proxyBenchPercentile(total, 0.5)), ms(proxyBenchPercentile(total, 0.95)),
			ms(proxyBenchPercentile(admit, 0.5)), ms(proxyBenchPercentile(admit, 0.95)),
			ms(proxyBenchPercentile(svc, 0.95)), ms(proxyBenchPercentile(tail, 0.95)),
			proxyBenchFailures(samples, want), peak)
	}

	single := newProxyBenchEnv(t, opts, []string{"one.example.test"}, service)
	for _, n := range []int{1, 4, 16} {
		bursts := make([]proxyBenchRound, 0, rounds)
		for r := 0; r < rounds; r++ {
			bursts = append(bursts, single.round(n, http.MethodPost, "one.example.test"))
		}
		report(fmt.Sprintf("one route POST n=%d", n), bursts, http.StatusCreated)
	}
	for _, n := range []int{1, 16} {
		bursts := make([]proxyBenchRound, 0, rounds)
		for r := 0; r < rounds; r++ {
			bursts = append(bursts, single.round(n, http.MethodGet, "one.example.test"))
		}
		report(fmt.Sprintf("one route GET n=%d", n), bursts, http.StatusCreated)
	}

	two := newProxyBenchEnv(t, opts, []string{"a.example.test", "b.example.test"}, service)
	var twoRounds []proxyBenchRound
	for r := 0; r < rounds; r++ {
		two.resetUpstreamPeak()
		start := time.Now()
		var samples []proxyBenchSample
		var wg sync.WaitGroup
		var mu sync.Mutex
		for _, host := range two.hosts {
			wg.Add(1)
			go func(host string) {
				defer wg.Done()
				got := two.run(8, http.MethodPost, host)
				mu.Lock()
				samples = append(samples, got...)
				mu.Unlock()
			}(host)
		}
		wg.Wait()
		twoRounds = append(twoRounds, proxyBenchRound{samples: samples, wall: time.Since(start), maxInFlight: two.upstreamPeak()})
	}
	report("two routes POST 8+8", twoRounds, http.StatusCreated)

	var mixedRounds []proxyBenchRound
	for r := 0; r < rounds; r++ {
		single.resetUpstreamPeak()
		start := time.Now()
		var samples []proxyBenchSample
		var wg sync.WaitGroup
		var mu sync.Mutex
		for _, method := range []string{http.MethodPost, http.MethodGet} {
			wg.Add(1)
			go func(method string) {
				defer wg.Done()
				got := single.run(8, method, "one.example.test")
				mu.Lock()
				samples = append(samples, got...)
				mu.Unlock()
			}(method)
		}
		wg.Wait()
		mixedRounds = append(mixedRounds, proxyBenchRound{samples: samples, wall: time.Since(start), maxInFlight: single.upstreamPeak()})
	}
	report("one route POST 8 + GET 8", mixedRounds, http.StatusCreated)

	queueFail := newProxyBenchEnv(t, GatewayHubOptions{
		AgentLease: time.Second, TargetReconnectGrace: time.Second,
		OperationTimeout: 30 * time.Second, TargetQueueTimeout: 2 * time.Second,
		MaxQueuedPerTarget: 2,
	}, []string{"queue.example.test"}, service)
	failed, codes := 0, map[int]int{}
	for r := 0; r < rounds; r++ {
		for _, sample := range queueFail.round(16, http.MethodPost, "queue.example.test").samples {
			if sample.status != http.StatusCreated {
				failed++
			}
			codes[sample.status]++
		}
	}
	t.Logf("max-queued-per-target=2, N=16: %d of %d requests refused; status codes %v", failed, 16*rounds, codes)

	for _, concurrency := range []int{1, 16} {
		phases := measureProxyAuditCost(t, single.store, concurrency, 40)
		us := func(d time.Duration) float64 { return float64(d) / float64(time.Microsecond) }
		t.Logf("audit writes (one proxied POST = start+dispatch+finish), concurrency=%d: start p50/p95 %.0f/%.0f us, dispatch %.0f/%.0f us, finish %.0f/%.0f us, three-write total p50/p95 %.0f/%.0f us",
			concurrency,
			us(proxyBenchPercentile(phases["start"], 0.5)), us(proxyBenchPercentile(phases["start"], 0.95)),
			us(proxyBenchPercentile(phases["dispatch"], 0.5)), us(proxyBenchPercentile(phases["dispatch"], 0.95)),
			us(proxyBenchPercentile(phases["finish"], 0.5)), us(proxyBenchPercentile(phases["finish"], 0.95)),
			us(proxyBenchPercentile(phases["total"], 0.5)), us(proxyBenchPercentile(phases["total"], 0.95)))
	}
	for _, concurrency := range []int{1, 4, 16} {
		waits := measureProxyLeaseWait(t, single, concurrency, service)
		ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
		t.Logf("lease-only queue wait (key=proxy:one.example.test, hold=%s), N=%d: p50 %.2f ms, p95 %.2f ms, max %.2f ms",
			service, concurrency,
			ms(proxyBenchPercentile(waits, 0.5)), ms(proxyBenchPercentile(waits, 0.95)), ms(proxyBenchPercentile(waits, 1.0)))
	}
}

// measureProxyLeaseWait isolates the lease from every other request-path cost:
// N goroutines take the same resource key, hold it for hold, and report how long
// they waited. It is the queue-wait number the end-to-end admit column is
// checked against.
func measureProxyLeaseWait(t *testing.T, env *proxyBenchEnv, n int, hold time.Duration) []time.Duration {
	t.Helper()
	agent := env.hub.getAgent("dev", "local")
	if agent == nil {
		t.Fatal("agent did not register")
	}
	const key = "proxy:one.example.test"
	waits := make([]time.Duration, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			begin := time.Now()
			lease, err := agent.acquireActionLease(context.Background(), ActionProxy, key, 64, 10*time.Second)
			waits[i] = time.Since(begin)
			if err != nil {
				t.Errorf("lease wait %d: %v", i, err)
				return
			}
			time.Sleep(hold)
			lease.release()
		}(i)
	}
	close(start)
	wg.Wait()
	return waits
}
