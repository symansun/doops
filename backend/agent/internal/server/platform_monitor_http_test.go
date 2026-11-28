package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/user/doops/agent/pkg/release"
)

func TestPlatformMonitorVersionDriftPureCalculation(t *testing.T) {
	const (
		gatewayCommit = "b56892220123456789abcdef0123456789abcdef"
		oldCommitA    = "74fe24910123456789abcdef0123456789abcdef"
		oldCommitB    = "574d168a0123456789abcdef0123456789abcdef"
		oldCommitC    = "dc8899b20123456789abcdef0123456789abcdef"
	)

	t.Run("valid gateway commit flags mismatched and unreported online agents only", func(t *testing.T) {
		agents := []platformMonitorAgent{
			{Key: "prod/aligned", Activity: "online", ReleaseCommit: gatewayCommit},
			{Key: "prod/old-1", Activity: "online", ReleaseCommit: oldCommitA},
			{Key: "prod/old-2", Activity: "online", ReleaseCommit: oldCommitB},
			{Key: "prod/unreported", Activity: "online", ReleaseCommit: ""},
			{Key: "prod/offline", Activity: "recent_offline", ReleaseCommit: oldCommitC},
			{Key: "prod/history", Activity: "history", ReleaseCommit: ""},
			{Key: "prod/archived", Activity: "archived", ReleaseCommit: oldCommitC},
		}

		baseline, drifted := applyPlatformAgentVersionDrift(agents, gatewayCommit)
		if baseline != gatewayCommit {
			t.Fatalf("baseline = %q, want %q", baseline, gatewayCommit)
		}
		if drifted != 3 {
			t.Fatalf("drifted = %d, want 3", drifted)
		}
		wantFlags := map[string]bool{
			"prod/aligned":    false,
			"prod/old-1":      true,
			"prod/old-2":      true,
			"prod/unreported": true,
			"prod/offline":    false,
			"prod/history":    false,
			"prod/archived":   false,
		}
		for _, a := range agents {
			if a.VersionDrifted != wantFlags[a.Key] {
				t.Errorf("%s version_drifted = %v, want %v", a.Key, a.VersionDrifted, wantFlags[a.Key])
			}
		}
	})

	t.Run("valid gateway commit flags all 10 online agents when none match gateway", func(t *testing.T) {
		agents := []platformMonitorAgent{
			{Key: "c/1", Activity: "online", ReleaseCommit: oldCommitA},
			{Key: "c/2", Activity: "online", ReleaseCommit: oldCommitA},
			{Key: "c/3", Activity: "online", ReleaseCommit: oldCommitA},
			{Key: "c/4", Activity: "online", ReleaseCommit: oldCommitA},
			{Key: "c/5", Activity: "online", ReleaseCommit: oldCommitA},
			{Key: "c/6", Activity: "online", ReleaseCommit: oldCommitA},
			{Key: "c/7", Activity: "online", ReleaseCommit: oldCommitB},
			{Key: "c/8", Activity: "online", ReleaseCommit: oldCommitB},
			{Key: "c/9", Activity: "online", ReleaseCommit: oldCommitC},
			{Key: "c/10", Activity: "online", ReleaseCommit: ""},
		}

		baseline, drifted := applyPlatformAgentVersionDrift(agents, gatewayCommit)
		if baseline != gatewayCommit || drifted != 10 {
			t.Fatalf("baseline=%q drifted=%d, want %q and 10", baseline, drifted, gatewayCommit)
		}
		for _, a := range agents {
			if !a.VersionDrifted {
				t.Errorf("%s expected version_drifted=true", a.Key)
			}
		}
	})

	t.Run("dev gateway build without valid commit flags non-primary and unreported online agents", func(t *testing.T) {
		agents := []platformMonitorAgent{
			{Key: "c/a1", Activity: "online", ReleaseCommit: oldCommitA},
			{Key: "c/a2", Activity: "online", ReleaseCommit: oldCommitA},
			{Key: "c/b1", Activity: "online", ReleaseCommit: oldCommitB},
			{Key: "c/u1", Activity: "online", ReleaseCommit: ""},
			{Key: "c/off", Activity: "recent_offline", ReleaseCommit: oldCommitC},
		}

		baseline, drifted := applyPlatformAgentVersionDrift(agents, "unknown")
		if baseline != "" {
			t.Fatalf("dev baseline = %q, want empty", baseline)
		}
		if drifted != 2 {
			t.Fatalf("dev drifted = %d, want 2", drifted)
		}
		if agents[0].VersionDrifted || agents[1].VersionDrifted {
			t.Fatalf("primary version agents must not be marked drifted: %+v", agents[:2])
		}
		if !agents[2].VersionDrifted || !agents[3].VersionDrifted {
			t.Fatalf("non-primary and unreported online agents must be marked drifted: %+v", agents[2:4])
		}
		if agents[4].VersionDrifted {
			t.Fatalf("offline agent must not be marked drifted: %+v", agents[4])
		}
	})

	t.Run("dev gateway build treats single reported version as consistent", func(t *testing.T) {
		agents := []platformMonitorAgent{
			{Key: "c/1", Activity: "online", ReleaseCommit: oldCommitA},
			{Key: "c/2", Activity: "online", ReleaseCommit: oldCommitA},
		}
		baseline, drifted := applyPlatformAgentVersionDrift(agents, "")
		if baseline != "" || drifted != 0 {
			t.Fatalf("baseline=%q drifted=%d, want empty and 0", baseline, drifted)
		}
		for _, a := range agents {
			if a.VersionDrifted {
				t.Errorf("%s should not be drifted", a.Key)
			}
		}
	})

	t.Run("dev gateway build flags all online agents when every online agent is unreported", func(t *testing.T) {
		agents := []platformMonitorAgent{
			{Key: "c/1", Activity: "online", ReleaseCommit: ""},
			{Key: "c/2", Activity: "online", ReleaseCommit: ""},
		}
		baseline, drifted := applyPlatformAgentVersionDrift(agents, "dev-local")
		if baseline != "" || drifted != 2 {
			t.Fatalf("baseline=%q drifted=%d, want empty and 2", baseline, drifted)
		}
		for _, a := range agents {
			if !a.VersionDrifted {
				t.Errorf("%s should be marked drifted", a.Key)
			}
		}
	})
}

func TestPlatformMonitorHTTPReportsVersionDriftAndOfflineTotal(t *testing.T) {
	const (
		gatewayCommit = "b56892220123456789abcdef0123456789abcdef"
		oldCommit     = "74fe24910123456789abcdef0123456789abcdef"
	)

	origCommit := release.Commit
	release.Commit = gatewayCommit
	t.Cleanup(func() { release.Commit = origCommit })

	f := newExecutionLifecycleFixture(t, GatewayHubOptions{AllowOpenAgentRegistration: true})
	adminAuth, err := f.store.VerifyUserToken(f.admin.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	ops := f.user(t, "ops-drift", ScopeGrant{Cluster: "prod", Instance: "aligned"})
	opsAuth, err := f.store.VerifyUserToken(ops.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	for _, grant := range []ScopeGrant{
		{Cluster: "prod", Instance: "stale"},
		{Cluster: "prod", Instance: "offline-recent"},
	} {
		if err := f.store.GrantUser(opsAuth.UserID, grant); err != nil {
			t.Fatal(err)
		}
	}

	connectLive := func(cluster, instance, commit string) {
		conn := dialFakeGatewayAgent(t, f.server.URL, cluster, instance)
		t.Cleanup(func() { _ = conn.Close() })
		go serveFakeAgent(t, conn)
		waitForGatewayAgent(t, f.hub, cluster, instance)
		live := f.hub.getAgent(cluster, instance)
		live.stateMu.Lock()
		live.ReleaseCommit = commit
		live.stateMu.Unlock()
	}

	connectLive("prod", "aligned", gatewayCommit)
	connectLive("prod", "stale", oldCommit)
	connectLive("prod", "unreported", "")

	now := time.Now().UTC()
	if err := f.store.MarkAgentOnline(AgentStatus{
		Cluster: "prod", Instance: "offline-recent", ReleaseCommit: oldCommit, Generation: 1,
		ConnectedAt: now.Add(-2 * time.Hour), LastSeen: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkAgentOffline("prod", "offline-recent", 1, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	if err := f.store.MarkAgentOnline(AgentStatus{
		Cluster: "prod", Instance: "offline-history", ReleaseCommit: oldCommit, Generation: 1,
		ConnectedAt: now.Add(-20 * 24 * time.Hour), LastSeen: now.Add(-15 * 24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkAgentOffline("prod", "offline-history", 1, now.Add(-15*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	if err := f.store.MarkAgentOnline(AgentStatus{
		Cluster: "prod", Instance: "retired", ReleaseCommit: oldCommit, Generation: 1,
		ConnectedAt: now.Add(-5 * time.Hour), LastSeen: now.Add(-4 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkAgentOffline("prod", "retired", 1, now.Add(-4*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ArchiveAgentStatus("prod", "retired", adminAuth.UserID, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	rawAdmin := f.http(t, f.admin, http.MethodGet, "/v1/platform-monitor?range=24h", "", http.StatusOK)
	var adminView platformMonitorResponse
	if err := json.Unmarshal(rawAdmin, &adminView); err != nil {
		t.Fatal(err)
	}
	if adminView.AgentSummary.GatewayReleaseCommit != gatewayCommit {
		t.Fatalf("admin gateway_release_commit = %q, want %q", adminView.AgentSummary.GatewayReleaseCommit, gatewayCommit)
	}
	if adminView.AgentSummary.DriftedOnline != 2 {
		t.Fatalf("admin drifted_online = %d, want 2 (stale + unreported)", adminView.AgentSummary.DriftedOnline)
	}
	if adminView.AgentSummary.OfflineTotal != 2 {
		t.Fatalf("admin offline_total = %d, want 2 (recent_offline + history, excluding archived); summary=%+v",
			adminView.AgentSummary.OfflineTotal, adminView.AgentSummary)
	}

	byKey := make(map[string]platformMonitorAgent, len(adminView.Agents))
	for _, a := range adminView.Agents {
		byKey[a.Key] = a
	}
	if byKey["prod/aligned"].VersionDrifted {
		t.Fatalf("prod/aligned should not be drifted")
	}
	if !byKey["prod/stale"].VersionDrifted {
		t.Fatalf("prod/stale should be drifted")
	}
	if !byKey["prod/unreported"].VersionDrifted {
		t.Fatalf("prod/unreported should be drifted")
	}
	if byKey["prod/offline-recent"].VersionDrifted || byKey["prod/offline-history"].VersionDrifted || byKey["prod/retired"].VersionDrifted {
		t.Fatalf("offline/archived agents must not be marked drifted")
	}

	// Non-admin user sees drift and offline_total scoped strictly to granted targets.
	rawOps := f.http(t, ops, http.MethodGet, "/v1/platform-monitor?range=24h", "", http.StatusOK)
	var opsView platformMonitorResponse
	if err := json.Unmarshal(rawOps, &opsView); err != nil {
		t.Fatal(err)
	}
	if opsView.AgentSummary.GatewayReleaseCommit != gatewayCommit ||
		opsView.AgentSummary.DriftedOnline != 1 ||
		opsView.AgentSummary.OfflineTotal != 1 {
		t.Fatalf("scoped ops summary mismatch: %+v", opsView.AgentSummary)
	}

	// Dev build (non-hex commit) omits gateway_release_commit and falls back to multi-version drift.
	release.Commit = "unknown"
	rawDev := f.http(t, f.admin, http.MethodGet, "/v1/platform-monitor?range=24h", "", http.StatusOK)
	var devView platformMonitorResponse
	if err := json.Unmarshal(rawDev, &devView); err != nil {
		t.Fatal(err)
	}
	if devView.AgentSummary.GatewayReleaseCommit != "" {
		t.Fatalf("dev build should omit gateway_release_commit, got %q", devView.AgentSummary.GatewayReleaseCommit)
	}
	if devView.AgentSummary.DriftedOnline != 2 {
		t.Fatalf("dev build drifted_online = %d, want 2 (non-primary + unreported)", devView.AgentSummary.DriftedOnline)
	}
}
