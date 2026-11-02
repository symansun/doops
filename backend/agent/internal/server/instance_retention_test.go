package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestAgentInstanceActivityWindow(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	if ActiveInstanceWindow != 7*24*time.Hour || OfflineInstanceRetention != 90*24*time.Hour {
		t.Fatalf("windows = %s / %s", ActiveInstanceWindow, OfflineInstanceRetention)
	}
	for _, tc := range []struct {
		name   string
		status string
		last   time.Time
		active bool
	}{
		{"online without heartbeat", "online", time.Time{}, true},
		{"offline one hour", "offline", now.Add(-time.Hour), true},
		{"offline exactly 7 days", "offline", now.Add(-ActiveInstanceWindow), true},
		{"offline just past 7 days", "offline", now.Add(-ActiveInstanceWindow - time.Millisecond), false},
		{"offline without heartbeat", "offline", time.Time{}, false},
		{"offline zero timestamp", "offline", time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), false},
	} {
		if got := agentInstanceActive(tc.status, tc.last, now); got != tc.active {
			t.Errorf("%s: active = %v, want %v", tc.name, got, tc.active)
		}
	}
	kept := AgentStatus{Status: "offline", LastSeen: now.Add(-OfflineInstanceRetention)}
	if offlineInstanceExpired(kept, now) {
		t.Fatal("exactly 90 days offline must be kept")
	}
	expired := AgentStatus{Status: "offline", LastSeen: now.Add(-OfflineInstanceRetention - time.Second)}
	if !offlineInstanceExpired(expired, now) {
		t.Fatal("older than 90 days offline must expire")
	}
	if offlineInstanceExpired(AgentStatus{Status: "online", LastSeen: now.Add(-200 * 24 * time.Hour)}, now) {
		t.Fatal("online instance must not expire")
	}
	if offlineInstanceExpired(AgentStatus{Status: "offline"}, now) {
		t.Fatal("offline instance with no timestamps must be kept")
	}
}

func putInstanceStatus(t *testing.T, store *GatewayStore, cluster, instance, status string, lastSeen time.Time) {
	t.Helper()
	if err := store.MarkAgentOnline(AgentStatus{
		Cluster: cluster, Instance: instance, Generation: 1,
		ConnectedAt: lastSeen, LastSeen: lastSeen,
	}); err != nil {
		t.Fatalf("mark %s/%s online: %v", cluster, instance, err)
	}
	if status == "offline" {
		if err := store.MarkAgentOffline(cluster, instance, 1, lastSeen); err != nil {
			t.Fatalf("mark %s/%s offline: %v", cluster, instance, err)
		}
	}
}

func TestPruneOfflineAgentStatusKeepsAuthorityAndRecentRows(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	putInstanceStatus(t, store, "dev", "fresh", "offline", now.Add(-24*time.Hour))
	putInstanceStatus(t, store, "dev", "boundary", "offline", now.Add(-OfflineInstanceRetention))
	putInstanceStatus(t, store, "dev", "expired", "offline", now.Add(-OfflineInstanceRetention-time.Second))
	putInstanceStatus(t, store, "dev", "online-old", "online", now.Add(-200*24*time.Hour))
	putInstanceStatus(t, store, "dev", "blank-old", "offline", now.Add(-time.Hour))
	putInstanceStatus(t, store, "dev", "blank-new", "offline", now.Add(-time.Hour))
	old := formatTime(now.Add(-OfflineInstanceRetention - time.Second))
	if _, err := store.db.Exec(`UPDATE agent_status SET last_seen = '', updated_at = ? WHERE cluster = 'dev' AND instance = 'blank-old'`, old); err != nil {
		t.Fatalf("age blank-old: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE agent_status SET last_seen = '' WHERE cluster = 'dev' AND instance = 'blank-new'`); err != nil {
		t.Fatalf("clear blank-new heartbeat: %v", err)
	}

	user, err := store.CreateUserWithPassword(CreateUserRequest{Name: "instance-owner", Password: "synthetic-canary"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := store.GrantUser(user.ID, ScopeGrant{Cluster: "dev", Instance: "expired"}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if _, err := store.UpsertProxyRoute(ProxyRouteInput{
		Host: "expired.example.test", Cluster: "dev", Instance: "expired",
		Upstream: "http://127.0.0.1:9", Enabled: true,
	}); err != nil {
		t.Fatalf("proxy route: %v", err)
	}

	deleted, err := store.PruneOfflineAgentStatus(now)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want 2 (expired and blank-old)", deleted)
	}
	again, err := store.PruneOfflineAgentStatus(now)
	if err != nil || again != 0 {
		t.Fatalf("second prune = %d, %v", again, err)
	}

	remaining := map[string]string{}
	agents, err := store.ListAgentStatus()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, agent := range agents {
		remaining[agent.Cluster+"/"+agent.Instance] = agent.Status
	}
	for _, key := range []string{"dev/fresh", "dev/boundary", "dev/online-old", "dev/blank-new"} {
		if _, ok := remaining[key]; !ok {
			t.Fatalf("kept instance missing: %s in %#v", key, remaining)
		}
	}
	if _, ok := remaining["dev/expired"]; ok {
		t.Fatal("expired instance was not removed")
	}
	if _, ok := remaining["dev/blank-old"]; ok {
		t.Fatal("offline instance with an old updated_at and no heartbeat was not removed")
	}
	if remaining["dev/online-old"] != "online" {
		t.Fatalf("online instance changed: %#v", remaining)
	}

	grants, err := store.ListGrants(user.ID)
	if err != nil || len(grants) != 1 || grants[0].Cluster != "dev" || grants[0].Instance != "expired" {
		t.Fatalf("grant did not survive cleanup: %#v, %v", grants, err)
	}
	routes, err := store.ListProxyRoutes()
	if err != nil || len(routes) != 1 || routes[0].Instance != "expired" {
		t.Fatalf("proxy route did not survive cleanup: %#v, %v", routes, err)
	}

	if err := store.MarkAgentOnline(AgentStatus{
		Cluster: "dev", Instance: "expired", Generation: 2, LastSeen: now,
	}); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	agents, err = store.ListAgentStatus()
	if err != nil {
		t.Fatalf("list after reconnect: %v", err)
	}
	var restored AgentStatus
	for _, agent := range agents {
		if agent.Cluster == "dev" && agent.Instance == "expired" {
			restored = agent
		}
	}
	if restored.Status != "online" {
		t.Fatalf("reconnect did not recreate the instance: %#v", agents)
	}
}

func TestAdminInstancesFilterHistoryUntilCleanup(t *testing.T) {
	f := newAdminAuditFixture(t)
	now := time.Now().UTC()
	putInstanceStatus(t, f.store, "dev", "live", "online", now)
	putInstanceStatus(t, f.store, "dev", "recent", "offline", now.Add(-2*24*time.Hour))
	putInstanceStatus(t, f.store, "dev", "stale", "offline", now.Add(-30*24*time.Hour))
	putInstanceStatus(t, f.store, "dev", "ancient", "offline", now.Add(-OfflineInstanceRetention-time.Hour))

	defaultList := credentialHTTPRequest(t, f.mux, f.adminToken, http.MethodGet, "/v1/admin/instances", nil)
	if defaultList.Code != http.StatusOK {
		t.Fatalf("default list: HTTP %d: %s", defaultList.Code, defaultList.Body.String())
	}
	if got := instanceNames(t, defaultList.Body.Bytes()); !sameNames(got, []string{"live", "recent"}) {
		t.Fatalf("default instances = %#v, want live and recent", got)
	}

	history := credentialHTTPRequest(t, f.mux, f.adminToken, http.MethodGet, "/v1/admin/instances?include=history", nil)
	if history.Code != http.StatusOK {
		t.Fatalf("history list: HTTP %d: %s", history.Code, history.Body.String())
	}
	if got := instanceNames(t, history.Body.Bytes()); !sameNames(got, []string{"live", "recent", "stale", "ancient"}) {
		t.Fatalf("history instances = %#v", got)
	}

	rejected := credentialHTTPRequest(t, f.mux, f.adminToken, http.MethodGet, "/v1/admin/instances?include=all", nil)
	if rejected.Code != http.StatusBadRequest {
		t.Fatalf("invalid include: HTTP %d: %s", rejected.Code, rejected.Body.String())
	}
	forbidden := credentialHTTPRequest(t, f.mux, f.userToken, http.MethodGet, "/v1/admin/instances?include=history", nil)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("ordinary user: HTTP %d: %s", forbidden.Code, forbidden.Body.String())
	}

	deleted, err := f.store.PruneOfflineAgentStatus(now)
	if err != nil || deleted != 1 {
		t.Fatalf("prune = %d, %v", deleted, err)
	}
	history = credentialHTTPRequest(t, f.mux, f.adminToken, http.MethodGet, "/v1/admin/instances?include=history", nil)
	if history.Code != http.StatusOK {
		t.Fatalf("history after prune: HTTP %d: %s", history.Code, history.Body.String())
	}
	if got := instanceNames(t, history.Body.Bytes()); !sameNames(got, []string{"live", "recent", "stale"}) {
		t.Fatalf("history after prune = %#v, want ancient removed and stale kept", got)
	}
}

func instanceNames(t *testing.T, body []byte) []string {
	t.Helper()
	var response struct {
		Instances []struct {
			Instance string `json:"instance"`
		} `json:"instances"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode instances: %v", err)
	}
	names := make([]string, 0, len(response.Instances))
	for _, instance := range response.Instances {
		names = append(names, instance.Instance)
	}
	return names
}

func sameNames(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]int{}
	for _, name := range got {
		seen[name]++
	}
	for _, name := range want {
		seen[name]--
		if seen[name] < 0 {
			return false
		}
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}
