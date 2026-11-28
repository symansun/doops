package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPlatformMonitorFleetUsageAndIsolation(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{AllowOpenAgentRegistration: true})
	alice := f.user(t, "alice-ops", ScopeGrant{Cluster: "dev", Instance: "local"})
	bob := f.user(t, "bob-ops", ScopeGrant{Cluster: "prod", Instance: "api"})
	adminAuth, err := f.store.VerifyUserToken(f.admin.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	aliceAuth, err := f.store.VerifyUserToken(alice.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	bobAuth, err := f.store.VerifyUserToken(bob.Plaintext)
	if err != nil {
		t.Fatal(err)
	}

	const (
		enrollCanary = "enroll-token-canary-9812"
		outputCanary = "detached-secret-output-canary-4411"
		commitA      = "0123456789abcdef0123456789abcdef01234567"
		commitB      = "fedcba9876543210fedcba9876543210fedcba98"
	)

	// Unauthenticated and malformed parameters fail closed.
	req, err := http.NewRequest(http.MethodGet, f.server.URL+"/v1/platform-monitor", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", res.StatusCode)
	}
	f.http(t, f.admin, http.MethodPost, "/v1/platform-monitor", "{}", http.StatusMethodNotAllowed)
	f.http(t, f.admin, http.MethodGet, "/v1/platform-monitor?range=90d", "", http.StatusBadRequest)
	f.http(t, f.admin, http.MethodGet, "/v1/platform-monitor?cluster=../bad", "", http.StatusBadRequest)

	// Connect a live Agent on dev/local and populate runtime load + commit.
	conn := dialFakeGatewayAgent(t, f.server.URL, "dev", "local")
	t.Cleanup(func() { _ = conn.Close() })
	go serveFakeAgent(t, conn)
	waitForGatewayAgent(t, f.hub, "dev", "local")

	live := f.hub.getAgent("dev", "local")
	live.stateMu.Lock()
	live.TokenID = enrollCanary
	live.ReleaseCommit = commitA
	live.stateMu.Unlock()
	live.opsMu.Lock()
	live.writers = 1
	live.sessionWriters["target:dev/local"] = 1
	live.resources["helm:apps/web"] = &agentResourceSlot{queued: 2}
	live.opsMu.Unlock()
	live.pendingMu.Lock()
	live.activeSessions["console-live-1"] = 1
	live.pendingMu.Unlock()
	f.hub.registry.recordLoadSampleMinInterval(0)

	now := time.Now().UTC()
	// Offline Agent prod/api retains its persisted release_commit and records online/offline transitions.
	if err := f.store.MarkAgentOnline(AgentStatus{
		Cluster: "prod", Instance: "api", TokenID: enrollCanary, Remote: "10.0.2.9:41000",
		ReleaseCommit: commitB, Generation: 1,
		ConnectedAt: now.Add(-3 * time.Hour), LastSeen: now.Add(-2 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkAgentOffline("prod", "api", 1, now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkAgentOnline(AgentStatus{
		Cluster: "prod", Instance: "legacy", Generation: 1,
		ConnectedAt: now.Add(-20 * 24 * time.Hour), LastSeen: now.Add(-15 * 24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkAgentOffline("prod", "legacy", 1, now.Add(-15*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkAgentOnline(AgentStatus{
		Cluster: "dev", Instance: "retired", Generation: 1,
		ConnectedAt: now.Add(-4 * time.Hour), LastSeen: now.Add(-3 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkAgentOffline("dev", "retired", 1, now.Add(-3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ArchiveAgentStatus("dev", "retired", adminAuth.UserID, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	// Seed audit history with LLM model + token usage.
	idAliceOk, err := f.store.StartAudit(AuditEvent{
		UserID: aliceAuth.UserID, TokenID: aliceAuth.TokenID, Cluster: "dev", Instance: "local",
		Action: ActionAsk, Session: "console-live-1", CommandSummary: "inspect pods",
		Model: "openai/gpt-5.2", StartedAt: now.Add(-20 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.FinishAudit(idAliceOk, AuditFinish{
		Status: "success", BytesIn: 120, BytesOut: 880,
		Model: "openai/gpt-5.2", InputTokens: 1200, OutputTokens: 340, TotalTokens: 1540,
		EndedAt: now.Add(-20*time.Minute + 2*time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := f.store.StartAudit(AuditEvent{
		UserID: aliceAuth.UserID, TokenID: aliceAuth.TokenID, Cluster: "dev", Instance: "local",
		Action: ActionExec, Session: "console-live-1", CommandSummary: "kubectl rollout status deploy/web",
		StartedAt: now.Add(-30 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	idBobUnknown, err := f.store.StartAudit(AuditEvent{
		UserID: bobAuth.UserID, TokenID: bobAuth.TokenID, Cluster: "prod", Instance: "api",
		Action: ActionWrite, Session: "deploy-bob", CommandSummary: "apply site config",
		CorrelationID: "op-bob-unknown-1", StartedAt: now.Add(-10 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.FinishAudit(idBobUnknown, AuditFinish{
		Status: "outcome_unknown", Error: "agent disconnected before terminal receipt",
		BytesIn: 300, BytesOut: 50, EndedAt: now.Add(-10*time.Minute + 5*time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := f.store.StartAudit(AuditEvent{
		UserID: adminAuth.UserID, TokenID: adminAuth.TokenID, Cluster: "dev", Instance: "local",
		Action: GatewayAction("agent:upgrade"), Session: "upg-1", Status: "observing",
		StartedAt: now.Add(-5 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	f.hub.client.detached.register(&detachedTask{
		AuditID: 9001, OperationID: "op-bg-alice", UserID: aliceAuth.UserID,
		Cluster: "dev", Instance: "local", Session: "bg-1", CommandSummary: "sleep 60",
		Status: "running", StartedAt: now.Add(-2 * time.Minute), TaskID: "task-a1", output: outputCanary,
	})
	f.hub.client.detached.register(&detachedTask{
		AuditID: 9002, OperationID: "op-bg-bob", UserID: bobAuth.UserID,
		Cluster: "prod", Instance: "api", Session: "bg-2", CommandSummary: "pg_dump",
		Status: "running", StartedAt: now.Add(-time.Minute), TaskID: "task-b2", output: outputCanary,
	})

	// Admin receives global fleet + usage + persisted offline release_commit + tokens + load_trend.
	rawAdmin := f.http(t, f.admin, http.MethodGet, "/v1/platform-monitor?range=24h", "", http.StatusOK)
	if strings.Contains(string(rawAdmin), enrollCanary) || strings.Contains(string(rawAdmin), outputCanary) {
		t.Fatalf("admin response leaked secret canary: %s", rawAdmin)
	}
	var adminView platformMonitorResponse
	if err := json.Unmarshal(rawAdmin, &adminView); err != nil {
		t.Fatal(err)
	}
	if adminView.Scope != "global" || adminView.Range != "24h" || len(adminView.Trend) != 24 || len(adminView.LoadTrend) != 24 {
		t.Fatalf("unexpected admin envelope: scope=%s range=%s buckets=%d load=%d",
			adminView.Scope, adminView.Range, len(adminView.Trend), len(adminView.LoadTrend))
	}
	var foundOfflineCommit bool
	for _, a := range adminView.Agents {
		if a.Key == "prod/api" && a.ReleaseCommit == commitB && a.WindowConnects == 1 && a.WindowDisconnects == 1 {
			foundOfflineCommit = true
		}
	}
	if !foundOfflineCommit || adminView.AgentSummary.FlappingAgents < 1 || len(adminView.ConnectionEvents) < 2 {
		t.Fatalf("offline commit or connection events missing: agents=%+v summary=%+v events=%+v",
			adminView.Agents, adminView.AgentSummary, adminView.ConnectionEvents)
	}
	if adminView.UsageSummary.TotalTokens != 1540 || adminView.UsageSummary.InputTokens != 1200 ||
		adminView.UsageSummary.OutputTokens != 340 || len(adminView.ByModel) != 1 || adminView.ByModel[0].Model != "openai/gpt-5.2" {
		t.Fatalf("token usage mismatch: summary=%+v models=%+v", adminView.UsageSummary, adminView.ByModel)
	}

	// Verify idx_audit_started_ms is used for time-window scans.
	var planID, planParent, planUnused int
	var planDetail string
	if err := f.store.db.QueryRow(`EXPLAIN QUERY PLAN SELECT id FROM audit_events WHERE started_at_ms >= ? AND started_at_ms <= ?`,
		now.Add(-24*time.Hour).UnixMilli(), now.UnixMilli()).Scan(&planID, &planParent, &planUnused, &planDetail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(planDetail, "idx_audit_started_ms") {
		t.Fatalf("time window query did not use idx_audit_started_ms: %s", planDetail)
	}

	// Ordinary user sees only granted non-archived targets, own usage/tasks, and NO global load_trend.
	rawAlice := f.http(t, alice, http.MethodGet, "/v1/platform-monitor?range=24h", "", http.StatusOK)
	if strings.Contains(string(rawAlice), "bob-ops") || strings.Contains(string(rawAlice), "prod/api") ||
		strings.Contains(string(rawAlice), "retired") || strings.Contains(string(rawAlice), enrollCanary) {
		t.Fatalf("non-admin response leaked unauthorized targets or users: %s", rawAlice)
	}
	var aliceView platformMonitorResponse
	if err := json.Unmarshal(rawAlice, &aliceView); err != nil {
		t.Fatal(err)
	}
	if aliceView.Scope != "self" || len(aliceView.Agents) != 1 || len(aliceView.LoadTrend) != 0 ||
		aliceView.UsageSummary.TotalTokens != 1540 || !aliceView.DetachedTasks[0].IsOwn {
		t.Fatalf("unexpected alice isolation: %+v", aliceView)
	}
}

func TestDetachedTaskPersistenceAndRestartRecovery(t *testing.T) {
	store := openCredentialTestStore(t)
	now := time.Now().UTC()
	reg1 := newDetachedTaskRegistry(store)
	reg1.register(&detachedTask{
		AuditID: 501, OperationID: "op-done", UserID: "usr-1", Cluster: "dev", Instance: "local",
		Session: "s1", CommandSummary: "echo ok", Status: "running", StartedAt: now.Add(-5 * time.Minute),
	})
	reg1.appendOutput(501, "TaskID: t-501\nall done\n")
	reg1.finish(501, "success")

	reg1.register(&detachedTask{
		AuditID: 502, OperationID: "op-inflight", UserID: "usr-1", Cluster: "dev", Instance: "local",
		Session: "s2", CommandSummary: "sleep 300", Status: "running", StartedAt: now.Add(-time.Minute),
	})

	// Simulate Gateway restart against the same SQLite store.
	reg2 := newDetachedTaskRegistry(store)
	done := reg2.get(501, "usr-1")
	if done == nil || done.Status != "success" || done.TaskID != "t-501" || !strings.Contains(done.output, "all done") {
		t.Fatalf("completed detached task lost on restart: %+v", done)
	}
	interrupted := reg2.get(502, "usr-1")
	if interrupted == nil || interrupted.Status != "outcome_unknown" || interrupted.FinishedAt.IsZero() {
		t.Fatalf("in-flight detached task did not recover to outcome_unknown: %+v", interrupted)
	}
}
