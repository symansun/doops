package server

// SF-11: real Gateway<->Agent tunnel end-to-end acceptance
// (docs/reviews/2026-11-24-skill-first-compliance.md section 5 red line 5, section 8).
//
// What is real here:
//   - a real GatewayStore (temporary SQLite file),
//   - a real GatewayHub HTTP/WS server on a real TCP port (httptest),
//   - a real Agent tunnel client: the same (*Gateway).ServeGatewayWebSocketConnWithReady
//     that cmd/agent drives, connected over a real gorilla/websocket socket to
//     /v1/agent/connect with a target-bound enrollment token,
//   - a real Skill package (skills_builtin/inspect-vm-host) synced through
//     doops_skill_sync and executed through doops_shell,
//   - real artifact files on disk and real metric rows read back through
//     GET /v1/monitor/runs/<id> and GET /v1/monitor/metrics.
//
// Nothing in these tests calls an internal dispatch/runner function directly:
// every step goes through the public HTTP/WS surface. Gateway and Agent live in
// one process only because the Agent entrypoint additionally owns a health port
// and a reconnect loop; the tunnel codec is the production one either way.
//
// The tests need only sh and python3 (the package degrades honestly without
// /proc), no cluster, no canary credential and no external service.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const (
	tunnelE2ECluster  = "dev"
	tunnelE2EInstance = "local"
	tunnelE2ESkill    = "inspect-vm-host"
)

// tunnelE2EStartAgent runs the production Agent tunnel runtime against the
// fixture's real Gateway server, enrolled for exactly this target, and waits
// until the Gateway registry publishes it.
func tunnelE2EStartAgent(t *testing.T, f *executionLifecycleFixture, cluster, instance string) func() {
	t.Helper()
	agent := NewGateway()
	conn := dialEnrolledTestAgent(t, f, cluster, instance)
	done := make(chan struct{})
	go func() {
		defer close(done)
		agent.ServeGatewayWebSocketConnWithReady(conn, "tunnel-e2e-"+cluster+"-"+instance, nil)
	}()
	waitForGatewayAgent(t, f.hub, cluster, instance)

	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = conn.Close()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Errorf("Agent runtime did not stop after the tunnel socket closed")
			}
			requireEventually(t, 5*time.Second, func() bool { return f.hub.getAgent(cluster, instance) == nil })
		})
	}
	t.Cleanup(stop)
	return stop
}

// tunnelE2EEnv boots the real store + WS Gateway with enrollment required, an
// operator granted on dev/local, and an enrolled Agent for that target.
func tunnelE2EEnv(t *testing.T) (*executionLifecycleFixture, CreatedToken, func()) {
	t.Helper()
	t.Setenv("DOOPS_AGENT_SKILLS_ROOT", t.TempDir())
	// A local runner has no AI endpoint. If a package silently fell back to the
	// AI-driven dispatch mode, the run must fail here instead of hanging.
	t.Setenv("DO_AGENT_URL", "http://127.0.0.1:1")
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{RequireAgentEnrollment: true})
	f.httpTimeout = 120 * time.Second
	operator := f.user(t, "tunnel-e2e-op", ScopeGrant{Cluster: tunnelE2ECluster, Instance: tunnelE2EInstance})
	stop := tunnelE2EStartAgent(t, f, tunnelE2ECluster, tunnelE2EInstance)
	return f, operator, stop
}

func tunnelE2ERun(t *testing.T, f *executionLifecycleFixture, operator CreatedToken) (monitorRunRecord, monitorRunItemRecord) {
	t.Helper()
	body := fmt.Sprintf(`{"targets":[{"cluster":%q,"instance":%q}],"skills":[%q]}`, tunnelE2ECluster, tunnelE2EInstance, tunnelE2ESkill)
	raw := f.http(t, operator, "POST", "/v1/monitor/runs", body, http.StatusOK)
	var resp struct {
		Run   monitorRunRecord       `json:"run"`
		Items []monitorRunItemRecord `json:"items"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode run response: %v; %s", err, raw)
	}
	if strings.TrimSpace(resp.Run.ID) == "" {
		t.Fatalf("run response carries no run id: %s", raw)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("run %s produced %d items, want exactly 1 for one Skill on one target: %s", resp.Run.ID, len(resp.Items), raw)
	}
	return resp.Run, resp.Items[0]
}

func tunnelE2ERunDetail(t *testing.T, f *executionLifecycleFixture, operator CreatedToken, runID string) (monitorRunRecord, []monitorRunItemRecord) {
	t.Helper()
	raw := f.http(t, operator, "GET", "/v1/monitor/runs/"+url.PathEscape(runID), "", http.StatusOK)
	var resp struct {
		Run   monitorRunRecord       `json:"run"`
		Items []monitorRunItemRecord `json:"items"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode run detail: %v; %s", err, raw)
	}
	if len(resp.Items) == 0 {
		t.Fatalf("run detail %s carries no items: %s", runID, raw)
	}
	return resp.Run, resp.Items
}

// tunnelE2EArtifactDir resolves the on-disk artifact directory the index wrote,
// the same way the reader resolves it.
func tunnelE2EArtifactDir(t *testing.T, f *executionLifecycleFixture, runID, slug string) string {
	t.Helper()
	var dir string
	if err := f.store.db.QueryRow("SELECT artifact_dir FROM monitor_run_items WHERE run_id=? AND asset_id=?", runID, slug).Scan(&dir); err != nil {
		t.Fatalf("read artifact_dir for %s/%s: %v", runID, slug, err)
	}
	if strings.TrimSpace(dir) == "" {
		t.Fatalf("%s/%s persisted no artifact_dir", runID, slug)
	}
	root := f.store.monitorArtifactRoot()
	if root == "" {
		t.Fatalf("artifact storage is disabled for this store")
	}
	return filepath.Join(root, filepath.FromSlash(dir))
}

// TestGatewayTunnelE2ESkillRunRoundTrip is the positive acceptance: a user
// request reaches the Agent through the tunnel, the Skill runs there, and the
// result, checks, artifact and metrics come back through the Gateway API.
func TestGatewayTunnelE2ESkillRunRoundTrip(t *testing.T) {
	f, operator, _ := tunnelE2EEnv(t)
	run, item := tunnelE2ERun(t, f, operator)

	if run.OfflineTargets != 0 {
		t.Fatalf("run %s reports %d offline targets although the Agent tunnel is up", run.ID, run.OfflineTargets)
	}
	switch run.Status {
	case "success", "warning", "critical":
	default:
		t.Fatalf("run status = %q after a live tunnel execution, want success/warning/critical (raw checks=%+v)", run.Status, item.Checks)
	}
	if item.Status == "" || item.Status == "offline" || item.Status == "error" {
		t.Fatalf("run item status = %q (reason=%q); the Skill did not execute over the tunnel: %+v", item.Status, item.Reason, item)
	}
	if item.Reason != "" {
		t.Fatalf("run item completed but carries reason %q", item.Reason)
	}
	if item.AssetID != tunnelE2ESkill || item.AssetKind != "skill" || item.Strategy != "skill:"+tunnelE2ESkill {
		t.Fatalf("run item is not bound to the dispatched Skill: asset_id=%q kind=%q strategy=%q", item.AssetID, item.AssetKind, item.Strategy)
	}
	if item.CollectorMethod != "skill_script" {
		t.Fatalf("collector method = %q, want skill_script (evidence=%+v)", item.CollectorMethod, item.Evidence)
	}
	if item.Evidence["synced"] != true {
		t.Fatalf("the Skill package was not materialized on the Agent: %+v", item.Evidence)
	}
	if entrypoint, _ := item.Evidence["entrypoint"].(string); strings.TrimSpace(entrypoint) == "" {
		t.Fatalf("the dispatched Skill carries no entrypoint evidence: %+v", item.Evidence)
	}

	if len(item.Checks) == 0 {
		t.Fatalf("Skill %s returned no checks over the tunnel: %+v", tunnelE2ESkill, item)
	}
	for i, check := range item.Checks {
		if strings.TrimSpace(check.ID) == "" {
			t.Errorf("check %d has no id: %+v", i, check)
		}
		switch check.Status {
		case "pass", "warning", "critical":
		default:
			t.Errorf("check %d (%s) status = %q, want pass/warning/critical", i, check.ID, check.Status)
		}
	}

	// The artifact payload must be the Skill's own output, not a synthesized
	// placeholder: decode it and compare the verdict with the stored item.
	payloadStatus, metrics := builtinSkillE2EMetrics(t, item.ArtifactJSON)
	if payloadStatus != item.Status {
		t.Fatalf("item status = %q but the payload status is %q", item.Status, payloadStatus)
	}
	if len(metrics) == 0 {
		t.Fatalf("Skill %s produced no numeric metrics over the tunnel", tunnelE2ESkill)
	}
	builtinSkillE2EVerifyArtifactFiles(t, f, run.ID, tunnelE2ESkill, item)
	builtinSkillE2EVerifyMetricSeries(t, f, operator, tunnelE2ESkill, run.ID, metrics)

	detail, detailItems := tunnelE2ERunDetail(t, f, operator, run.ID)
	if detail.ArtifactState != monitorArtifactStatePresent {
		t.Fatalf("run artifact_state = %q after a successful run, want %q", detail.ArtifactState, monitorArtifactStatePresent)
	}
	if detailItems[0].ArtifactState != monitorArtifactStatePresent {
		t.Fatalf("item artifact_state = %q after a successful run, want %q", detailItems[0].ArtifactState, monitorArtifactStatePresent)
	}
	if len(detailItems[0].Checks) != len(item.Checks) {
		t.Fatalf("run detail kept %d checks, the run response had %d", len(detailItems[0].Checks), len(item.Checks))
	}
	// The per-item metrics_json column is vestigial for Skill runs (SF-09 retired
	// the native collectors that filled it); the normalized metrics live in the
	// artifact payload and in inspection_metric_points. Read them back through the
	// detail payload and compare with the series bridge asserted above.
	detailStatus, detailMetrics := builtinSkillE2EMetrics(t, detailItems[0].ArtifactJSON)
	if detailStatus != item.Status {
		t.Fatalf("run detail payload status = %q, run item status = %q", detailStatus, item.Status)
	}
	if len(detailMetrics) != len(metrics) {
		t.Fatalf("run detail payload exposes %d metrics, the run returned %d", len(detailMetrics), len(metrics))
	}
	for key, want := range metrics {
		if got, ok := detailMetrics[key]; !ok || got != want {
			t.Fatalf("run detail payload metric %s = %v (present=%v), want %v", key, got, ok, want)
		}
	}

	t.Logf("run=%s status=%s item=%s checks=%d metrics=%d", run.ID, run.Status, item.Status, len(item.Checks), len(metrics))
}

// TestGatewayTunnelE2EOfflineTunnelFailsFast fails the dispatch instead of
// hanging: once the Agent socket is gone the Gateway must answer with a
// terminal offline run, not block on a dead tunnel.
func TestGatewayTunnelE2EOfflineTunnelFailsFast(t *testing.T) {
	f, operator, stopAgent := tunnelE2EEnv(t)
	stopAgent()
	f.httpTimeout = 15 * time.Second

	started := time.Now()
	run, item := tunnelE2ERun(t, f, operator)
	elapsed := time.Since(started)

	if elapsed > 10*time.Second {
		t.Fatalf("dispatch to an offline target took %s; it must fail fast instead of waiting on the tunnel", elapsed)
	}
	if run.Status != "failed" {
		t.Fatalf("run status = %q when the only target is offline, want failed (summary=%q)", run.Status, run.Summary)
	}
	if run.OfflineTargets != 1 {
		t.Fatalf("run offline_targets = %d, want 1", run.OfflineTargets)
	}
	if item.Status != "offline" || item.Reason != "target_offline" {
		t.Fatalf("item status/reason = %q/%q, want offline/target_offline", item.Status, item.Reason)
	}
	var online bool
	for _, check := range item.Checks {
		if check.ID == "agent.online" {
			online = true
			if check.Status != "critical" {
				t.Fatalf("agent.online check status = %q, want critical", check.Status)
			}
		}
	}
	if !online {
		t.Fatalf("offline item carries no agent.online check: %+v", item.Checks)
	}
	if run.FinishedAtMs == 0 {
		t.Fatalf("failed run has no finished_at: %+v", run)
	}
	t.Logf("offline dispatch returned in %s with status=%s reason=%s", elapsed, run.Status, item.Reason)
}

// TestGatewayTunnelE2EEnrollmentAndGrantRejection covers both authorization
// boundaries: a user without a grant never reaches the runner, and an Agent
// enrollment token bound to one target cannot open a tunnel for another.
func TestGatewayTunnelE2EEnrollmentAndGrantRejection(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{RequireAgentEnrollment: true})
	f.httpTimeout = 10 * time.Second
	t.Setenv("DOOPS_AGENT_SKILLS_ROOT", t.TempDir())
	t.Setenv("DO_AGENT_URL", "http://127.0.0.1:1")
	operator := f.user(t, "tunnel-e2e-granted", ScopeGrant{Cluster: tunnelE2ECluster, Instance: tunnelE2EInstance})

	// (1) A granted user may not inspect an ungranted target: 403 before any
	// dispatch, and no run row is created.
	body := fmt.Sprintf(`{"targets":[{"cluster":"dev","instance":"other"}],"skills":[%q]}`, tunnelE2ESkill)
	raw := f.http(t, operator, "POST", "/v1/monitor/runs", body, http.StatusForbidden)
	if !strings.Contains(string(raw), "monitor_target_forbidden") {
		t.Fatalf("403 body does not carry monitor_target_forbidden: %s", raw)
	}
	var list struct {
		Runs []monitorRunRecord `json:"runs"`
	}
	if err := json.Unmarshal(f.http(t, operator, "GET", "/v1/monitor/runs", "", http.StatusOK), &list); err != nil {
		t.Fatalf("decode run list: %v", err)
	}
	if len(list.Runs) != 0 {
		t.Fatalf("a forbidden target still created %d runs: %+v", len(list.Runs), list.Runs)
	}

	// (2) An enrollment token issued for dev/local cannot register dev/other,
	// even though that target has no enrollment of its own.
	enrollment, err := f.store.CreateAgentEnrollment(tunnelE2ECluster, tunnelE2EInstance)
	if err != nil {
		t.Fatalf("create enrollment: %v", err)
	}
	query := url.Values{"cluster": {"dev"}, "instance": {"other"}}
	endpoint := "ws" + strings.TrimPrefix(f.server.URL, "http") + "/v1/agent/connect?" + query.Encode()
	conn, resp, err := websocket.DefaultDialer.Dial(endpoint, http.Header{"Authorization": {"Bearer " + enrollment.Plaintext}})
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil {
		t.Fatalf("an enrollment token bound to %s/%s opened a tunnel for dev/other", tunnelE2ECluster, tunnelE2EInstance)
	}
	if resp == nil {
		t.Fatalf("rejected Agent handshake returned no HTTP response: %v", err)
	}
	rejectionBody, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		t.Fatalf("read rejection body: %v", readErr)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("misbound enrollment handshake = HTTP %d, want 401: %s", resp.StatusCode, rejectionBody)
	}
	if !strings.Contains(string(rejectionBody), "agent_enrollment_auth_failed") {
		t.Fatalf("401 body does not carry agent_enrollment_auth_failed: %s", rejectionBody)
	}
	if agent := f.hub.getAgent("dev", "other"); agent != nil {
		t.Fatalf("a rejected Agent was nevertheless registered for dev/other: %+v", agent)
	}
}

// TestGatewayTunnelE2EArtifactMissingAfterDeletion fails the artifact state,
// not the reader: removing the artifact directory must surface as
// artifact_state=missing while checks and metrics stay readable.
func TestGatewayTunnelE2EArtifactMissingAfterDeletion(t *testing.T) {
	f, operator, _ := tunnelE2EEnv(t)
	run, item := tunnelE2ERun(t, f, operator)
	builtinSkillE2EVerifyArtifactFiles(t, f, run.ID, tunnelE2ESkill, item)

	dir := tunnelE2EArtifactDir(t, f, run.ID, tunnelE2ESkill)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove artifact dir %s: %v", dir, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("artifact dir still exists after deletion: %s (%v)", dir, err)
	}

	detail, detailItems := tunnelE2ERunDetail(t, f, operator, run.ID)
	if detail.ArtifactState != monitorArtifactStateMissing {
		t.Fatalf("run artifact_state = %q after deleting %s, want %q", detail.ArtifactState, dir, monitorArtifactStateMissing)
	}
	if detailItems[0].ArtifactState != monitorArtifactStateMissing {
		t.Fatalf("item artifact_state = %q after deleting %s, want %q", detailItems[0].ArtifactState, dir, monitorArtifactStateMissing)
	}
	if len(detailItems[0].Checks) == 0 {
		t.Fatalf("losing the artifact files also dropped the stored checks: %+v", detailItems[0])
	}
	// The metric series is projected at persistence time, so it survives file
	// loss and stays readable for the run.
	var series struct {
		Series []struct {
			Key    string `json:"key"`
			Points []struct {
				RunID string `json:"run_id"`
			} `json:"points"`
		} `json:"series"`
	}
	query := url.Values{}
	query.Set("skill", tunnelE2ESkill)
	query.Set("target", tunnelE2ECluster+"/"+tunnelE2EInstance)
	query.Set("range", "1h")
	if err := json.Unmarshal(f.http(t, operator, "GET", "/v1/monitor/metrics?"+query.Encode(), "", http.StatusOK), &series); err != nil {
		t.Fatalf("decode metric series: %v", err)
	}
	matched := 0
	for _, s := range series.Series {
		for _, point := range s.Points {
			if point.RunID == run.ID {
				matched++
			}
		}
	}
	if matched == 0 {
		t.Fatalf("metric series for run %s disappeared with the artifact files", run.ID)
	}
}
