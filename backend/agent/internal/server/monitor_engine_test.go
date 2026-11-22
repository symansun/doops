package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/user/doops/agent/api"
)

func monitorEngineFixture(t *testing.T) *executionLifecycleFixture {
	t.Helper()
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	mux := http.NewServeMux()
	f.hub.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	f.server = srv
	return f
}

func TestMonitorEngineFullDispatchAndBridge(t *testing.T) {
	f := monitorEngineFixture(t)

	// Apply optional asset_monitor.sql + asset_catalog.sql so bridge & catalog integration are tested too.
	for _, mig := range []string{"asset_monitor.sql", "asset_catalog.sql", "machine_inventory.sql", "cluster_inventory.sql"} {
		raw, err := os.ReadFile("../../../docs/migrations/" + mig)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.db.Exec(string(raw)); err != nil {
			t.Fatalf("apply %s: %v", mig, err)
		}
	}

	// Spin up local HTTP app & TCP DB listener for real app_service & database_service probes.
	appSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer appSrv.Close()
	appAddr := strings.TrimPrefix(appSrv.URL, "http://")
	appHost, _, _ := net.SplitHostPort(appAddr)

	dbLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer dbLn.Close()
	go func() {
		for {
			c, e := dbLn.Accept()
			if e != nil {
				return
			}
			_ = c.Close()
		}
	}()
	dbHost, dbPort, _ := net.SplitHostPort(dbLn.Addr().String())

	// Stub kubectl, docker, and ssh binaries on PATH for deterministic multi-strategy testing.
	binDir := t.TempDir()
	kubectlScript := `#!/bin/sh
case "$*" in
  "get nodes -o json")
    echo '{"items":[{"metadata":{"name":"node-a"},"status":{"conditions":[{"type":"Ready","status":"True"}],"capacity":{"cpu":"4","memory":"8Gi"},"nodeInfo":{"kubeletVersion":"v1.30.1"}}}]}'
    ;;
  "get pods -A -o json")
    echo '{"items":[{"metadata":{"name":"web-1","namespace":"apps"},"status":{"phase":"Running","containerStatuses":[{"name":"web","ready":true,"restartCount":0}]}},{"metadata":{"name":"worker-bad","namespace":"apps"},"status":{"phase":"Running","containerStatuses":[{"name":"worker","ready":false,"restartCount":7,"state":{"waiting":{"reason":"CrashLoopBackOff"}}}]}}]}'
    ;;
  "get deployments,statefulsets -A -o json")
    echo '{"items":[{"kind":"Deployment","metadata":{"name":"web","namespace":"apps"},"spec":{"replicas":2},"status":{"readyReplicas":2}}]}'
    ;;
  "get pvc -A -o json")
    echo '{"items":[{"metadata":{"name":"data-0","namespace":"apps"},"status":{"phase":"Bound"}}]}'
    ;;
  "top nodes --no-headers")
    echo 'node-a   500m   12%   2048Mi   25%'
    ;;
  "top pods -A --no-headers")
    echo 'apps   web-1   120m   256Mi'
    ;;
  "get events -A --field-selector type=Warning -o json")
    echo '{"items":[]}'
    ;;
  *)
    exit 1
    ;;
esac
`
	dockerScript := `#!/bin/sh
[ "$1" = "stats" ] || exit 1
printf '{"Name":"/api-gw","CPUPerc":"25.00%%","MemUsage":"128MiB / 512MiB"}\n'
`
	sshScript := `#!/bin/sh
cat <<'EOF'
===OS===
Ubuntu 24.04 LTS
===KERNEL===
Linux 6.8.0
===NPROC===
4
===UPTIME===
7200.50 14400.00
===LOAD===
0.42 0.35 0.28 1/200 1234
===CPU0===
cpu  1000 0 500 8500 0 0 0 0 0 0
===NET0===
eth0 10000 20000
===CPU1===
cpu  1100 0 550 8850 0 0 0 0 0 0
===NET1===
eth0 14096 24096
===MEM===
MemTotal:       8388608 kB
MemAvailable:   4194304 kB
Slab:            262144 kB
===DF===
/dev/sda1 104857600 41943040 62914560 40% /
===PS===
120 0
===SYSCTL===
kernel/randomize_va_space 2
fs/protected_hardlinks 1
fs/protected_symlinks 1
kernel/kptr_restrict 1
EOF
`
	for name, body := range map[string]string{"kubectl": kubectlScript, "docker": dockerScript, "ssh": sshScript} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Register one online Agent (dev/local) and one offline Agent (dev/offline-node).
	inventoryTestAgent(t, f, "local")
	if _, err := f.store.db.Exec(`INSERT INTO agent_status(cluster,instance,status,updated_at,last_seen) VALUES('dev','offline-node','offline',?,?)`,
		formatTime(time.Now().UTC()), formatTime(time.Now().UTC())); err != nil {
		t.Fatal(err)
	}

	// Register local machine + remote SSH machine + DB catalog item under dev/local.
	if _, err := f.store.db.Exec(`INSERT INTO inventory_machines(asset_id,name,kind,address,cluster,instance) VALUES
		('srv-local','Local VM','vm','10.0.0.10','dev','local'),
		('srv-remote-ssh','Remote SSH Host','physical','10.0.0.99','dev','local')`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`INSERT INTO inventory_machine_tags(asset_id,tag) VALUES('srv-local','env=prod'),('srv-remote-ssh','collector=ssh')`); err != nil {
		t.Fatal(err)
	}
	dbDetails, _ := json.Marshal(map[string]string{"engine": "PostgreSQL", "host": dbHost, "port": dbPort, "database": "appdb"})
	if _, err := f.store.db.Exec(`INSERT INTO inventory_resource_catalog(id,kind,cluster,instance,created_by,name,environment,details,identity_key,revision)
		VALUES('db-cat-1','database','dev','local','admin','orders-pg','prod',?,'["PostgreSQL","127.0.0.1","5432","appdb"]',1)`,
		string(dbDetails)); err != nil {
		t.Fatal(err)
	}
	// Note: appHost may be 127.0.0.1 without port in catalog; test app_service via explicit URL check too.
	_ = appHost

	// Non-admin user only granted dev/local.
	operator := f.user(t, "mon-op", ScopeGrant{Cluster: "dev", Instance: "local"})

	// 1. Non-admin cannot create schedules (403); admin creates a schedule.
	schedBody := `{"action":"create","name":"全校定时巡检","description":"每5分钟巡检所有策略","scope_mode":"targets","targets":[{"cluster":"dev","instance":"local"},{"cluster":"dev","instance":"offline-node"}],"strategies":["vm_host","ssh_host","k8s_cluster","container_runtime","app_service","database_service"],"interval_seconds":300}`
	f.http(t, operator, "POST", "/v1/monitor/schedules", schedBody, 403)
	schedRaw := f.http(t, f.admin, "POST", "/v1/monitor/schedules", schedBody, 200)
	var schedResp struct {
		Schedule monitorScheduleRecord `json:"schedule"`
	}
	if err := json.Unmarshal(schedRaw, &schedResp); err != nil || schedResp.Schedule.ID == "" {
		t.Fatalf("create schedule: %s", schedRaw)
	}
	schedID := schedResp.Schedule.ID

	// 2. Make schedule due and run periodic dispatcher directly.
	pastMs := time.Now().Add(-time.Minute).UnixMilli()
	if _, err := f.store.db.Exec(`UPDATE monitor_schedules SET next_run_at_ms=? WHERE id=?`, pastMs, schedID); err != nil {
		t.Fatal(err)
	}
	runs := f.hub.RunDueMonitorSchedules(context.Background(), time.Now().UTC())
	if len(runs) != 1 {
		t.Fatalf("expected 1 due scheduled run, got %d", len(runs))
	}
	run := runs[0]
	if run.TriggerKind != "scheduled" || run.OfflineTargets != 1 || run.Status != "partial" {
		t.Fatalf("unexpected scheduled run summary: %+v", run)
	}
	if run.TotalAssets < 5 || run.MetricsWritten == 0 {
		t.Fatalf("expected >=5 asset items and written metrics, got %+v", run)
	}

	// Running due schedules again before next_run_at_ms produces 0 runs (idempotent advance).
	if again := f.hub.RunDueMonitorSchedules(context.Background(), time.Now().UTC()); len(again) != 0 {
		t.Fatalf("expected 0 duplicate scheduled runs, got %d", len(again))
	}

	// 3. Inspect run details via HTTP — admin sees both targets, operator sees ONLY dev/local!
	adminDetailRaw := f.http(t, f.admin, "GET", "/v1/monitor/runs/"+run.ID, "", 200)
	var adminDetail struct {
		Run   monitorRunRecord       `json:"run"`
		Items []monitorRunItemRecord `json:"items"`
	}
	if err := json.Unmarshal(adminDetailRaw, &adminDetail); err != nil {
		t.Fatal(err)
	}
	seenStrategies := map[string]bool{}
	sawOffline := false
	for _, it := range adminDetail.Items {
		seenStrategies[it.Strategy] = true
		if it.Instance == "offline-node" && it.Status == "offline" && it.Reason == "target_offline" {
			sawOffline = true
		}
		if it.Strategy == monitorStrategySSHHost && it.CollectorMethod != "ssh" {
			t.Fatalf("expected ssh collector method, got %+v", it)
		}
		if it.Strategy == monitorStrategyK8sCluster && it.Status != "critical" {
			t.Fatalf("expected critical k8s status due to CrashLoopBackOff pod, got %+v", it)
		}
		if it.Strategy == monitorStrategyDatabaseService && it.Status != "pass" {
			t.Fatalf("expected database TCP probe pass, got %+v", it)
		}
	}
	if !sawOffline {
		t.Fatalf("missing offline-node gap item in %+v", adminDetail.Items)
	}
	for _, wantStrat := range []string{monitorStrategyVMHost, monitorStrategySSHHost, monitorStrategyK8sCluster, monitorStrategyContainerRuntime, monitorStrategyDatabaseService} {
		if !seenStrategies[wantStrat] {
			t.Fatalf("missing strategy %s in %v", wantStrat, seenStrategies)
		}
	}

	opDetailRaw := f.http(t, operator, "GET", "/v1/monitor/runs/"+run.ID, "", 200)
	var opDetail struct {
		Items []monitorRunItemRecord `json:"items"`
	}
	_ = json.Unmarshal(opDetailRaw, &opDetail)
	for _, it := range opDetail.Items {
		if it.Instance != "local" {
			t.Fatalf("operator saw unauthorized target item: %+v", it)
		}
	}

	// 4. Verify fleet resource overview & asset history endpoints.
	ovRaw := f.http(t, f.admin, "GET", "/v1/monitor/overview?range=1h", "", 200)
	var ov monitorOverviewResponse
	if err := json.Unmarshal(ovRaw, &ov); err != nil {
		t.Fatal(err)
	}
	if ov.Summary.TotalAssets < 4 || ov.Summary.OnlineAgents != 1 || len(ov.Resources) < 4 || len(ov.Trends) != 12 {
		t.Fatalf("unexpected overview: summary=%+v resources=%d trends=%d", ov.Summary, len(ov.Resources), len(ov.Trends))
	}

	histRaw := f.http(t, operator, "GET", "/v1/monitor/resources/history?cluster=dev&instance=local&asset_kind=vm&asset_id=srv-local&range=1h", "", 200)
	var hist monitorHistoryResponse
	if err := json.Unmarshal(histRaw, &hist); err != nil || len(hist.Points) == 0 || hist.Latest == nil {
		t.Fatalf("unexpected history: %s", histRaw)
	}
	// Operator cannot read unauthorized target history.
	f.http(t, operator, "GET", "/v1/monitor/resources/history?cluster=dev&instance=offline-node&asset_kind=vm&asset_id=offline-node&range=1h", "", 403)

	// 5. Verify metrics were bridged into existing /v1/assets/monitor automatically!
	bridgeRaw := f.http(t, operator, "GET", "/v1/assets/monitor?cluster=dev&instance=local", "", 200)
	if !strings.Contains(string(bridgeRaw), `"srv-local"`) || !strings.Contains(string(bridgeRaw), `"api-gw"`) || !strings.Contains(string(bridgeRaw), `"web-1"`) {
		t.Fatalf("expected bridged VM/container/k8s series in /v1/assets/monitor, got: %s", bridgeRaw)
	}

	// 5b. Verify /v1/assets/machines probe coverage picks up last_sample_ms from monitor_resource_snapshots
	// even if asset_monitor_samples is cleared for an asset (e.g. SSH host or fresh DB without asset_monitor.sql).
	if _, err := f.store.db.Exec(`DELETE FROM asset_monitor_samples WHERE series_id IN (SELECT id FROM asset_monitor_series WHERE asset_id='srv-remote-ssh')`); err != nil {
		t.Fatal(err)
	}
	machinesRaw := f.http(t, f.admin, "GET", "/v1/assets/machines", "", 200)
	var machinesResp struct {
		Machines []struct {
			AssetID      string `json:"asset_id"`
			LastSampleMs *int64 `json:"last_sample_ms"`
		} `json:"machines"`
	}
	if err := json.Unmarshal(machinesRaw, &machinesResp); err != nil {
		t.Fatal(err)
	}
	foundSSHSample := false
	for _, m := range machinesResp.Machines {
		if m.AssetID == "srv-remote-ssh" && m.LastSampleMs != nil && *m.LastSampleMs > 0 {
			foundSSHSample = true
		}
	}
	if !foundSSHSample {
		t.Fatalf("expected srv-remote-ssh last_sample_ms populated from monitor_resource_snapshots, got %s", machinesRaw)
	}

	// 5c. Verify open threshold alerts surface in /v1/monitor/overview scoped by target grants.
	nowMs := time.Now().UTC().UnixMilli()
	var localSeriesID int64
	if err := f.store.db.QueryRow(`SELECT id FROM asset_monitor_series WHERE cluster='dev' AND instance='local' AND asset_id='srv-local' LIMIT 1`).Scan(&localSeriesID); err != nil {
		t.Fatal(err)
	}
	resOfflineSeries, err := f.store.db.Exec(`INSERT INTO asset_monitor_series(cluster,instance,namespace,type,asset_id,origin,metric,device_id)
		VALUES('dev','offline-node','','machine','srv-offline','agent_host','cpu','')`)
	if err != nil {
		t.Fatal(err)
	}
	offlineSeriesID, _ := resOfflineSeries.LastInsertId()
	if _, err := f.store.db.Exec(`INSERT INTO asset_monitor_alerts(series_id,opened_ms,threshold,duration_seconds,state) VALUES(?,?,1.0,60,'open'),(?,?,2.0,120,'open')`,
		localSeriesID, nowMs-5000, offlineSeriesID, nowMs-3000); err != nil {
		t.Fatal(err)
	}
	adminAlertOvRaw := f.http(t, f.admin, "GET", "/v1/monitor/overview?range=1h", "", 200)
	var adminAlertOv monitorOverviewResponse
	if err := json.Unmarshal(adminAlertOvRaw, &adminAlertOv); err != nil {
		t.Fatal(err)
	}
	if adminAlertOv.Summary.OpenThresholdAlerts != 2 || len(adminAlertOv.ThresholdAlerts) != 2 {
		t.Fatalf("expected admin to see 2 open threshold alerts, got summary=%d list=%+v", adminAlertOv.Summary.OpenThresholdAlerts, adminAlertOv.ThresholdAlerts)
	}
	opAlertOvRaw := f.http(t, operator, "GET", "/v1/monitor/overview?range=1h", "", 200)
	var opAlertOv monitorOverviewResponse
	if err := json.Unmarshal(opAlertOvRaw, &opAlertOv); err != nil {
		t.Fatal(err)
	}
	if opAlertOv.Summary.OpenThresholdAlerts != 1 || len(opAlertOv.ThresholdAlerts) != 1 || opAlertOv.ThresholdAlerts[0].Instance != "local" {
		t.Fatalf("expected operator to see only authorized dev/local alert, got summary=%d list=%+v", opAlertOv.Summary.OpenThresholdAlerts, opAlertOv.ThresholdAlerts)
	}

	// 6. Verify retest of failed/offline items & manual target authorization check.
	f.http(t, operator, "POST", "/v1/monitor/runs", `{"targets":[{"cluster":"dev","instance":"offline-node"}]}`, 403)
	retestRaw := f.http(t, f.admin, "POST", "/v1/monitor/runs", fmt.Sprintf(`{"retest_run_id":"%s"}`, run.ID), 200)
	var retestResp struct {
		Run monitorRunRecord `json:"run"`
	}
	if err := json.Unmarshal(retestRaw, &retestResp); err != nil || retestResp.Run.TriggerKind != "retest" {
		t.Fatalf("unexpected retest response: %s", retestRaw)
	}

	// 7. Toggle & delete schedule.
	f.http(t, f.admin, "POST", "/v1/monitor/schedules", fmt.Sprintf(`{"action":"toggle","id":"%s","enabled":false}`, schedID), 200)
	f.http(t, f.admin, "POST", "/v1/monitor/schedules", fmt.Sprintf(`{"action":"delete","id":"%s"}`, schedID), 200)
}

func TestMonitorEngineAppProbeAndInternalToolGuard(t *testing.T) {
	f := monitorEngineFixture(t)
	inventoryTestAgent(t, f, "local")

	// Start an HTTPS test server with HSTS + nosniff (Wait: httptest uses self-signed cert; test plain HTTP probe directly).
	appSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("healthy"))
	}))
	defer appSrv.Close()

	// Test direct app strategy evaluation.
	res := executeAppServiceStrategy(context.Background(), api.MonitorInspectAsset{
		AssetID:   "portal-web",
		AssetName: "教务门户",
		AssetKind: "app",
		Strategy:  monitorStrategyAppService,
		URL:       appSrv.URL,
	}, defaultMonitorThresholds())
	if res.Metrics.ResponseTimeMs == nil || res.Metrics.ErrorRate == nil || *res.Metrics.ErrorRate != 0 {
		t.Fatalf("expected healthy HTTP probe metrics, got %+v", res)
	}
	if len(res.Checks) < 3 || res.Checks[0].Status != "pass" {
		t.Fatalf("expected availability+latency+web_security checks, got %+v", res.Checks)
	}

	// Verify ad-hoc manual run works without any optional migrations applied (fresh Gateway DB).
	runRaw := f.http(t, f.admin, "POST", "/v1/monitor/runs", `{"targets":[{"cluster":"dev","instance":"local"}],"strategies":["vm_host"]}`, 200)
	var runResp struct {
		Run   monitorRunRecord       `json:"run"`
		Items []monitorRunItemRecord `json:"items"`
	}
	if err := json.Unmarshal(runRaw, &runResp); err != nil || runResp.Run.ID == "" || len(runResp.Items) != 1 {
		t.Fatalf("unexpected fresh-db run response: %s", runRaw)
	}

	// Verify internal tool cannot be invoked directly over client RPC.
	spec := gatewayToolIndex[monitorInspectTool]
	if spec.visibility != gatewayInternal || spec.publicDenial != "monitor_inspect_internal_only" {
		t.Fatalf("expected internal visibility with denial for %s, got %+v", monitorInspectTool, spec)
	}
}
