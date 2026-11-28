package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

const monitorPath = "/v1/assets/monitor"

func monitorFixture(t *testing.T) *executionLifecycleFixture {
	t.Helper()
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	srv := httptest.NewServer(http.HandlerFunc(f.hub.HandleAssetMonitor))
	t.Cleanup(srv.Close)
	f.server = srv
	ddl, err := os.ReadFile("../../../docs/migrations/asset_monitor.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = f.store.db.Exec(string(ddl)); err != nil {
			t.Fatal(err)
		}
	}
	return f
}
func monitorExporter(t *testing.T, f *executionLifecycleFixture, defaults ...string) func(CreatedToken, ...string) error {
	t.Helper()
	tls := httptest.NewTLSServer(http.HandlerFunc(f.hub.HandleAssetMonitor))
	t.Cleanup(tls.Close)
	caFile := filepath.Join(t.TempDir(), "test-ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tls.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	return func(token CreatedToken, args ...string) error {
		cmd := exec.Command("python3", append([]string{"../../../scripts/asset_monitor_collector.py", "--cluster", "dev", "--instance", "local", "--gateway", tls.URL}, append(defaults, args...)...)...)
		cmd.Env = append(os.Environ(), "SSL_CERT_FILE="+caFile)
		cmd.Stdin = strings.NewReader(token.Plaintext + "\n")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("collector: %s", out)
		}
		return nil
	}
}
func TestAssetMonitorGatewayAndExporter(t *testing.T) {
	f := monitorFixture(t)
	_, err := f.store.db.Exec(`INSERT INTO agent_status(cluster,instance,status,updated_at) VALUES('dev','local','offline','now'),('dev','private','offline','now')`)
	if err != nil {
		t.Fatal(err)
	}
	operator := f.user(t, "monitor-reader", ScopeGrant{Cluster: "dev", Instance: "local"})
	f.http(t, operator, "GET", monitorPath+"?cluster=dev&instance=private", "", 403)
	collect := monitorExporter(t, f, "--asset-id", "test-volume", "--type", "app", "--mount", "/")
	if err := collect(operator); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("untrusted writer: %v", err)
	}
	if err := collect(f.admin); err != nil {
		t.Fatal(err)
	}
	rule := `{"action":"rule","cluster":"dev","instance":"local","type":"app","asset_id":"test-volume","origin":"exporter","metric":"storage","device_id":"/","threshold":0,"duration_seconds":1}`
	f.http(t, operator, "POST", monitorPath, rule, 403)
	f.http(t, f.admin, "POST", monitorPath, rule, 200)
	if err = collect(f.admin, "--interval-seconds", "1", "--count", "3"); err != nil {
		t.Fatal(err)
	}
	query := monitorPath + "?cluster=dev&instance=local&type=app&asset_id=test-volume&origin=exporter&range=1h&metric=storage&device_id=%2F"
	read := func() monitorSeries {
		t.Helper()
		var result struct {
			Series []monitorSeries `json:"series"`
		}
		if err := json.Unmarshal(f.http(t, operator, "GET", query, "", 200), &result); err != nil || len(result.Series) != 1 {
			t.Fatalf("detail: %v", err)
		}
		return result.Series[0]
	}
	s := read()
	if len(s.Points) != 4 || len(s.Alerts) != 1 || s.Alerts[0].State != "open" || s.Unit != "bytes" || s.Latest.Value == nil || *s.Latest.Value <= 0 {
		t.Fatalf("expected real filesystem observations + alert: %+v", s)
	}
	write := func(at int64, status, value string, want int) {
		t.Helper()
		body := fmt.Sprintf(`{"action":"ingest","cluster":"dev","instance":"local","samples":[{"type":"app","asset_id":"test-volume","metric":"storage","device_id":"/","unit":"bytes","at_ms":%d,"status":"%s","value":%s}]}`, at, status, value)
		f.http(t, f.admin, "POST", monitorPath, body, want)
	}
	at := time.Now().UnixMilli()
	write(at, "missing", "null", 200)
	write(at, "missing", "null", 200)
	write(at, "ok", "0", 409)
	s = read()
	if s.Alerts[0].State != "interrupted" || s.Latest.Value != nil || len(s.Points) != 5 {
		t.Fatalf("gap or duplicate mishandled: %+v", s)
	}
	f.http(t, f.admin, "POST", monitorPath, strings.Replace(rule, `"duration_seconds":1`, `"duration_seconds":0`, 1), 400)
	write(at+60000, "ok", "1", 400)
	f.http(t, f.admin, "POST", monitorPath, `{"action":"collect","cluster":"dev","instance":"local","namespace":"orders","samples":[]}`, 400)
	f.http(t, operator, "POST", monitorPath, `{"action":"collect","cluster":"dev","instance":"local","namespace":"orders"}`, 503)
	f.http(t, f.admin, "POST", monitorPath, strings.Replace(rule, "local", "never-registered", 1), 404)
	f.http(t, operator, "GET", monitorPath+"?cluster=dev&instance=local&range=invalid", "", 400)
	if _, err = f.store.db.Exec(`DELETE FROM grants WHERE user_id=(SELECT user_id FROM tokens WHERE id=?)`, operator.ID); err != nil {
		t.Fatal(err)
	}
	f.http(t, operator, "GET", query, "", 403)
}
func TestAssetMonitorNativeTarget(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	ddl, err := os.ReadFile("../../../docs/migrations/asset_monitor.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.db.Exec(string(ddl)); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err = os.WriteFile(filepath.Join(dir, "kubectl"), []byte(`#!/bin/sh
[ "$*" = 'top pods --namespace orders' ] || exit 11
printf 'NAME CPU(cores) MEMORY(bytes)\norder-api 125m 37Mi\n'
`), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	inventoryTestAgent(t, f, "local")
	srv := httptest.NewServer(http.HandlerFunc(f.hub.HandleAssetMonitor))
	defer srv.Close()
	client := *f
	client.server = srv
	op := f.user(t, "sample-pods", ScopeGrant{Cluster: "dev", Instance: "local"})
	body := `{"action":"collect","cluster":"dev","instance":"local","namespace":"orders"}`
	client.http(t, op, "POST", monitorPath, body, 200)
	data := client.http(t, op, "GET", monitorPath+"?cluster=dev&instance=local&type=app&asset_id=order-api&namespace=orders&origin=k8s_top", "", 200)
	var d struct{ Series []monitorSeries }
	if err = json.Unmarshal(data, &d); err != nil || len(d.Series) != 2 || *d.Series[0].Latest.Value != 0.125 || *d.Series[1].Latest.Value != 37*1048576 {
		t.Fatalf("unexpected native series: %s", data)
	}
	client.http(t, op, "POST", monitorPath, strings.Replace(body, "local", "private", 1), 403)

	// Native container runtime collection (docker stats) + memory_limit threshold rule.
	if err = os.WriteFile(filepath.Join(dir, "docker"), []byte(`#!/bin/sh
[ "$1" = 'stats' ] || exit 12
printf '{"Name":"/web-nginx","CPUPerc":"45.00%%","MemUsage":"128MiB / 512MiB"}\n{"Name":"redis-cache","CPUPerc":"5.00%%","MemUsage":"64MiB / 256MiB"}\n'
`), 0755); err != nil {
		t.Fatal(err)
	}
	cRes := client.http(t, op, "POST", monitorPath, `{"action":"collect","mode":"containers","cluster":"dev","instance":"local"}`, 200)
	if !strings.Contains(string(cRes), `"runtime":"docker"`) || !strings.Contains(string(cRes), `"accepted":6`) {
		t.Fatalf("unexpected container collect response: %s", cRes)
	}
	cData := client.http(t, op, "GET", monitorPath+"?cluster=dev&instance=local&type=container&asset_id=web-nginx&origin=container_stats", "", 200)
	var cDetail struct{ Series []monitorSeries }
	if err = json.Unmarshal(cData, &cDetail); err != nil || len(cDetail.Series) != 3 {
		t.Fatalf("unexpected container series: %s", cData)
	}
	client.http(t, f.admin, "POST", monitorPath, `{"action":"rule","cluster":"dev","instance":"local","type":"container","asset_id":"web-nginx","origin":"container_stats","metric":"memory_limit","threshold":1048576,"duration_seconds":60}`, 200)

	// Native VM / host tunnel collection.
	hRes := client.http(t, op, "POST", monitorPath, `{"action":"collect","mode":"host","cluster":"dev","instance":"local"}`, 200)
	var hAccepted struct{ Accepted int }
	if json.Unmarshal(hRes, &hAccepted) != nil || hAccepted.Accepted < 3 {
		t.Fatalf("unexpected host collect response: %s", hRes)
	}
	hData := client.http(t, op, "GET", monitorPath+"?cluster=dev&instance=local&type=machine&asset_id=local&origin=agent_host", "", 200)
	var hDetail struct{ Series []monitorSeries }
	if json.Unmarshal(hData, &hDetail) != nil || len(hDetail.Series) < 3 {
		t.Fatalf("unexpected host series: %s", hData)
	}
}

func TestAssetMonitorScopeHistoryAndRecovery(t *testing.T) {
	f := monitorFixture(t)
	applyInventoryMigration(t, f)
	if _, err := f.store.db.Exec(`INSERT INTO agent_status(cluster,instance,updated_at) VALUES('dev','local','now'); INSERT INTO cluster_inventory_sources(cluster,instance,asset_id,name,verified_at,verified_by,scope) VALUES('dev','local','C-1','test','now','test','list_pods')`); err != nil {
		t.Fatal(err)
	}
	reader := f.user(t, "series-reader", ScopeGrant{Cluster: "dev", Instance: "local"})
	route := monitorRequest{Action: "ingest", Cluster: "dev", Instance: "local", ClusterID: "C-1"}
	start := time.Now().Add(-2 * time.Hour).UnixMilli()
	value := 2.0
	write := func(want int, points ...monitorInput) {
		t.Helper()
		body := map[string]any{"action": "ingest", "cluster": route.Cluster, "instance": route.Instance, "cluster_id": route.ClusterID, "samples": points}
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		f.http(t, f.admin, "POST", monitorPath, string(data), want)
	}
	for kind := range monitorKinds {
		for metric, unit := range monitorUnits {
			if strings.HasPrefix(metric, "disk_") || metric == "memory_limit" {
				continue // Machine disk I/O and container memory_limit have dedicated HTTP coverage.
			}
			if strings.HasPrefix(metric, "db_") && kind != "database" {
				continue // db_* metrics only exist on database assets.
			}
			p := monitorInput{monitorAsset: monitorAsset{Type: kind, AssetID: "asset-1"}, Metric: metric, Unit: unit, monitorPoint: monitorPoint{At: start, Status: "ok", Value: &value}}
			if metric == "storage_total" || metric == "storage_available" {
				p.DeviceID = "/"
			}
			if metric == "gpu" {
				p.DeviceID = "GPU-11111111-1111-1111-1111-111111111111"
			}
			write(200, p)
		}
	}
	base := monitorPath + "?cluster=dev&instance=local&cluster_id=C-1"
	list := f.http(t, reader, "GET", base, "", 200)
	var catalog struct{ Assets []monitorAsset }
	if json.Unmarshal(list, &catalog) != nil || len(catalog.Assets) != len(monitorKinds) {
		t.Fatalf("all monitor kinds: %s", list)
	}
	f.http(t, reader, "GET", strings.Replace(base, "C-1", "C-2", 1), "", 403)
	path := base + "&type=machine&asset_id=asset-1&origin=exporter"
	read := func(q string) []monitorSeries {
		t.Helper()
		var d struct{ Series []monitorSeries }
		if err := json.Unmarshal(f.http(t, reader, "GET", path+q, "", 200), &d); err != nil {
			t.Fatal(err)
		}
		return d.Series
	}
	for _, s := range read("&range=6h") {
		if len(s.Points) != 1 || s.Unit != monitorUnits[s.Metric] || !s.Stale {
			t.Fatalf("series %+v", s)
		}
	}
	if got := read("&range=1h&metric=cpu"); len(got) != 1 || len(got[0].Points) != 0 {
		t.Fatal("time filter")
	}
	p := monitorInput{monitorAsset: monitorAsset{Type: "machine", AssetID: "asset-1"}, Metric: "cpu", Unit: "cores", monitorPoint: monitorPoint{At: start + 1000, Value: &value, Status: "ok"}}
	bad := p
	bad.At = start
	write(200, bad)
	other := 9.
	bad.Value = &other
	write(409, p, bad)
	if len(read("&range=6h&metric=cpu")[0].Points) != 1 {
		t.Fatal("partial batch committed")
	}
	rule := `{"cluster":"dev","instance":"local","type":"machine","asset_id":"asset-1","origin":"exporter","metric":"cpu","action":"rule","threshold":1,"duration_seconds":1}`
	f.http(t, f.admin, "POST", monitorPath, rule, 200)
	// Move the fixture's rule effective time to exercise a six-minute gap without a wall-clock wait.
	if _, err := f.store.db.Exec(`UPDATE asset_monitor_rules SET effective_ms=?`, start); err != nil {
		t.Fatal(err)
	}
	write(200, p)
	p.At += 1000
	write(200, p)
	p.At += 1000
	low := 0.
	p.Value = &low
	write(200, p)
	p.At += 1000
	p.Value = &value
	write(200, p)
	p.At += 360000
	write(200, p)
	p.At += 1000
	write(200, p)
	alerts := read("&range=6h&metric=cpu")[0].Alerts
	if len(alerts) != 2 || alerts[1].State != "recovered" || alerts[0].State != "interrupted" || alerts[0].Opened != p.At {
		t.Fatalf("hold/reset/recovery: %+v", alerts)
	}
}

const gpuA = "GPU-11111111-1111-1111-1111-111111111111"
const gpuB = "GPU-22222222-2222-2222-2222-222222222222"

func monitorGPUFixture(t *testing.T) (*executionLifecycleFixture, func(CreatedToken, ...string) error) {
	t.Helper()
	f := monitorFixture(t)
	if _, err := f.store.db.Exec(`INSERT INTO agent_status(cluster,instance,updated_at) VALUES('dev','local','now')`); err != nil {
		t.Fatal(err)
	}
	return f, monitorExporter(t, f, "--type", "app", "--asset-id", "dual-host")
}
func monitorGPUSeries(t *testing.T, f *executionLifecycleFixture, device string) []monitorSeries {
	t.Helper()
	query := monitorPath + "?cluster=dev&instance=local&type=app&asset_id=dual-host&origin=exporter&metric=gpu"
	if device != "" {
		query += "&device_id=" + device
	}
	var result struct{ Series []monitorSeries }
	if err := json.Unmarshal(f.http(t, f.admin, "GET", query, "", 200), &result); err != nil {
		t.Fatal(err)
	}
	return result.Series
}
func TestAssetMonitorDualGPUCollectorHTTP(t *testing.T) {
	f, collect := monitorGPUFixture(t)
	dir := t.TempDir()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FIRST_GPU", gpuA)
	t.Setenv("SECOND_GPU", gpuB)
	// The fixture replaces only the NVIDIA driver output. The production Python
	// collector, HTTPS bearer authentication, SQLite store and rule engine run normally.
	driver := `#!/bin/sh
[ "$1" = -i ] && [ "$4" = --format=csv,noheader,nounits ] || exit 2
case "$2:$3" in
  0:--query-gpu=uuid) echo "$FIRST_GPU";;
  1:--query-gpu=uuid) echo "$SECOND_GPU";;
  ` + gpuA + `:--query-gpu=uuid,utilization.gpu) echo '` + gpuA + `, 81';;
  ` + gpuB + `:--query-gpu=uuid,utilization.gpu) [ -z "$DROP_B" ] || exit 3; echo '` + gpuB + `, 12';;
  *) exit 2;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "nvidia-smi"), []byte(driver), 0755); err != nil {
		t.Fatal(err)
	}
	if err := collect(f.admin, "--gpu", "0", "--gpu", "1"); err != nil {
		t.Fatal(err)
	}
	both := monitorGPUSeries(t, f, "")
	if len(both) != 2 || both[0].DeviceID != gpuA || both[1].DeviceID != gpuB || *both[0].Latest.Value != 81 || *both[1].Latest.Value != 12 {
		t.Fatalf("initial device split: %+v", both)
	}
	rule := `{"cluster":"dev","instance":"local","type":"app","asset_id":"dual-host","origin":"exporter","metric":"gpu","action":"rule","threshold":50,"duration_seconds":1`
	f.http(t, f.admin, "POST", monitorPath, rule+"}", 400)
	for _, id := range []string{gpuA, gpuB} {
		f.http(t, f.admin, "POST", monitorPath, rule+`,"device_id":"`+id+`"}`, 200)
	}
	t.Setenv("FIRST_GPU", gpuB)
	t.Setenv("SECOND_GPU", gpuA)
	if err := collect(f.admin, "--gpu", "0", "--gpu", "1", "--interval-seconds", "1", "--count", "3"); err != nil {
		t.Fatal(err)
	}
	av, bv := monitorGPUSeries(t, f, gpuA), monitorGPUSeries(t, f, gpuB)
	if len(av) != 1 || len(bv) != 1 || len(av[0].Alerts) != 1 || av[0].Alerts[0].State != "open" || len(bv[0].Alerts) != 0 || av[0].Rule == nil || bv[0].Rule == nil {
		t.Fatalf("device-specific alerts: %+v / %+v", av, bv)
	}
	for _, s := range []monitorSeries{av[0], bv[0]} {
		if len(s.Points) != 4 {
			t.Fatalf("points: %+v", s)
		}
		for _, p := range s.Points {
			if *p.Value != *s.Latest.Value {
				t.Fatal("index swap crossed device histories")
			}
		}
	}
	t.Setenv("DROP_B", "1")
	if err := collect(f.admin, "--gpu", gpuA, "--gpu", gpuB); err != nil {
		t.Fatal(err)
	}
	if monitorGPUSeries(t, f, gpuB)[0].Latest.Status != "missing" || monitorGPUSeries(t, f, gpuA)[0].Latest.Status != "ok" {
		t.Fatal("missing sample crossed device histories")
	}
	p := monitorInput{monitorAsset: monitorAsset{Type: "app", AssetID: "dual-host"}, Metric: "gpu", Unit: "%", monitorPoint: monitorPoint{At: time.Now().UnixMilli(), Status: "ok"}}
	v := 11.
	p.Value = &v
	post := func(points []monitorInput, want int) {
		body, _ := json.Marshal(map[string]any{"cluster": "dev", "instance": "local", "action": "ingest", "samples": points})
		f.http(t, f.admin, "POST", monitorPath, string(body), want)
	}
	post([]monitorInput{p}, 400)
	p.DeviceID = "0"
	post([]monitorInput{p}, 400)
	a, b := p, p
	a.DeviceID = gpuA
	b.DeviceID = gpuB
	post([]monitorInput{a, b}, 200)
	post([]monitorInput{a, b}, 200)
	if monitorGPUSeries(t, f, gpuA)[0].Latest.At != p.At || monitorGPUSeries(t, f, gpuB)[0].Latest.At != p.At {
		t.Fatal("same host and timestamp must accept both cards")
	}
}

func TestAssetMonitorDualGPUHardware(t *testing.T) {
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		t.Skip("no local NVIDIA driver")
	}
	out, err := exec.Command("nvidia-smi", "--query-gpu=uuid", "--format=csv,noheader,nounits").Output()
	if err != nil {
		t.Fatal(err)
	}
	ids := strings.Fields(string(out))
	if len(ids) < 2 {
		t.Skip("requires two local NVIDIA GPUs")
	}
	f, collect := monitorGPUFixture(t)
	if err := collect(f.admin, "--gpu", ids[0], "--gpu", ids[1]); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids[:2] {
		series := monitorGPUSeries(t, f, id)
		if len(series) != 1 || series[0].DeviceID != id || series[0].Latest.Status != "ok" || series[0].Latest.Value == nil {
			t.Fatalf("hardware sample for %s: %+v", id, series)
		}
	}
}

func TestAssetMonitorDeleteRuleHTTP(t *testing.T) {
	f := monitorFixture(t)
	if _, err := f.store.db.Exec(`INSERT INTO agent_status(cluster,instance,updated_at) VALUES('dev','local','now')`); err != nil {
		t.Fatal(err)
	}
	viewer := f.user(t, "rule-viewer", ScopeGrant{Cluster: "dev", Instance: "local"})
	t0 := time.Now().Add(-10 * time.Second).UnixMilli()
	ingest := func(at int64, val float64) {
		body := fmt.Sprintf(`{"action":"ingest","cluster":"dev","instance":"local","samples":[{"type":"machine","asset_id":"vm-del","metric":"cpu","unit":"cores","at_ms":%d,"status":"ok","value":%v}]}`, at, val)
		f.http(t, f.admin, "POST", monitorPath, body, 200)
	}
	readSeries := func() monitorSeries {
		t.Helper()
		var detail struct{ Series []monitorSeries }
		raw := f.http(t, f.admin, "GET", monitorPath+"?cluster=dev&instance=local&type=machine&asset_id=vm-del&origin=exporter&metric=cpu", "", 200)
		if err := json.Unmarshal(raw, &detail); err != nil {
			t.Fatal(err)
		}
		if len(detail.Series) != 1 {
			t.Fatalf("want 1 series, got %+v", detail.Series)
		}
		return detail.Series[0]
	}

	ingest(t0, 2.0)
	f.http(t, f.admin, "POST", monitorPath, `{"action":"rule","cluster":"dev","instance":"local","type":"machine","asset_id":"vm-del","origin":"exporter","metric":"cpu","threshold":1.0,"duration_seconds":1}`, 200)
	time.Sleep(20 * time.Millisecond)
	t1 := time.Now().UnixMilli()
	ingest(t1, 3.0)
	time.Sleep(1100 * time.Millisecond)
	t2 := time.Now().UnixMilli()
	ingest(t2, 3.5)

	before := readSeries()
	if before.Rule == nil || len(before.Alerts) != 1 || before.Alerts[0].State != "open" {
		t.Fatalf("expected active rule and open alert before delete, got %+v", before)
	}

	delPayload := `{"action":"delete_rule","cluster":"dev","instance":"local","type":"machine","asset_id":"vm-del","origin":"exporter","metric":"cpu"}`
	// Non-admin rejected.
	f.http(t, viewer, "POST", monitorPath, delPayload, 403)
	// Rejects extra threshold/duration fields on delete_rule.
	f.http(t, f.admin, "POST", monitorPath, `{"action":"delete_rule","cluster":"dev","instance":"local","type":"machine","asset_id":"vm-del","origin":"exporter","metric":"cpu","threshold":1.0}`, 400)
	// Missing series returns 404.
	f.http(t, f.admin, "POST", monitorPath, `{"action":"delete_rule","cluster":"dev","instance":"local","type":"machine","asset_id":"no-such-vm","origin":"exporter","metric":"cpu"}`, 404)
	// Admin deletes rule and closes open alert as reconfigured.
	f.http(t, f.admin, "POST", monitorPath, delPayload, 200)
	// Idempotent second delete succeeds.
	f.http(t, f.admin, "POST", monitorPath, delPayload, 200)

	after := readSeries()
	if after.Rule != nil {
		t.Fatalf("expected rule removed, got %+v", after.Rule)
	}
	if len(after.Alerts) != 1 || after.Alerts[0].State != "reconfigured" || after.Alerts[0].Ended == nil {
		t.Fatalf("expected open alert closed as reconfigured, got %+v", after.Alerts)
	}

	// Subsequent high sample does not reopen an alert after rule deletion.
	ingest(time.Now().UnixMilli(), 9.0)
	final := readSeries()
	if len(final.Alerts) != 1 || final.Alerts[0].State != "reconfigured" {
		t.Fatalf("alert must stay closed after rule deletion, got %+v", final.Alerts)
	}
}

func TestAssetMonitorNativeCollectBridgesToMonitorSnapshots(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	ddl, err := os.ReadFile("../../../docs/migrations/asset_monitor.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.db.Exec(string(ddl)); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err = os.WriteFile(filepath.Join(dir, "kubectl"), []byte(`#!/bin/sh
[ "$*" = 'top pods --namespace orders' ] || exit 11
printf 'NAME CPU(cores) MEMORY(bytes)\norder-api 250m 64Mi\n'
`), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "docker"), []byte(`#!/bin/sh
[ "$1" = 'stats' ] || exit 12
printf '{"Name":"/web-nginx","CPUPerc":"50.00%%","MemUsage":"128MiB / 512MiB"}\n'
`), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	inventoryTestAgent(t, f, "local")
	srv := httptest.NewServer(http.HandlerFunc(f.hub.HandleAssetMonitor))
	defer srv.Close()
	client := *f
	client.server = srv
	op := f.user(t, "bridge-op", ScopeGrant{Cluster: "dev", Instance: "local"})

	// Collect containers and K8s pods via legacy native endpoint; both must bridge into monitor_resource_snapshots.
	client.http(t, op, "POST", monitorPath, `{"action":"collect","mode":"containers","cluster":"dev","instance":"local"}`, 200)
	client.http(t, op, "POST", monitorPath, `{"action":"collect","mode":"k8s_pods","cluster":"dev","instance":"local","namespace":"orders"}`, 200)

	// Register a CMDB machine + tag and write an older Skill-First inspect-vm-host
	// snapshot to verify: (1) host bridge inherits CMDB capacity/tags and derives
	// warning status on high utilization, (2) BuildMonitorOverview deduplicates
	// Skill-First vs native host rows for the same target without double-counting,
	// and (3) native k8s_pods namespace rollup lands in asset_kind="app" (not "k8s").
	nowMs := time.Now().UnixMilli()
	vCPU, vMem, vDiskTot, vDiskUsed, vNet, vLat, vErr := 7.2, float64(8<<30), float64(100<<30), float64(90<<30), 2048.0, 12.5, 0.0
	tx, err := f.store.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO inventory_machines(asset_id, name, kind, address, cpu_count, memory_gib, disk_gib, cluster, instance, active, created_at) VALUES(?,?,?,?,?,?,?,?,?,1,?)`,
		"vm-01", "业务节点 01", "vm", "10.0.0.10", 8, 16, 200, "dev", "local", "2026-11-28T00:00:00Z"); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO inventory_machine_tags(asset_id, tag) VALUES(?,?)`, "vm-01", "env=prod"); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	olderCores, olderUsed, olderMemTot := 8.0, 2.0, float64(16<<30)
	if err := insertMonitorResourceSnapshot(t.Context(), tx, "run-older", monitorResourceSnapshot{
		Cluster:          "dev",
		Instance:         "local",
		AssetID:          "inspect-vm-host",
		AssetName:        "local (inspect-vm-host)",
		AssetKind:        "skill",
		Strategy:         "skill:inspect-vm-host",
		CollectorMethod:  "skill_script",
		CollectedAtMs:    nowMs - 30_000,
		Status:           "ok",
		CPUCores:         &olderUsed,
		CPUTotalCores:    &olderCores,
		MemoryTotalBytes: &olderMemTot,
		Tags:             []string{},
		Details:          map[string]any{"source": "inspection_metrics"},
	}); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := bridgeNativeMonitorCollectToSnapshots(t.Context(), tx, monitorRequest{Cluster: "dev", Instance: "local"}, "host", []monitorInput{
		{monitorAsset: monitorAsset{Type: "machine", AssetID: "vm-01"}, Metric: "cpu", Unit: "cores", monitorPoint: monitorPoint{At: nowMs, Status: "ok", Value: &vCPU}},
		{monitorAsset: monitorAsset{Type: "machine", AssetID: "vm-01"}, Metric: "memory", Unit: "bytes", monitorPoint: monitorPoint{At: nowMs, Status: "ok", Value: &vMem}},
		{DeviceID: "/", monitorAsset: monitorAsset{Type: "machine", AssetID: "vm-01"}, Metric: "storage_total", Unit: "bytes", monitorPoint: monitorPoint{At: nowMs, Status: "ok", Value: &vDiskTot}},
		{DeviceID: "/", monitorAsset: monitorAsset{Type: "machine", AssetID: "vm-01"}, Metric: "storage", Unit: "bytes", monitorPoint: monitorPoint{At: nowMs, Status: "ok", Value: &vDiskUsed}},
		{DeviceID: "eth0", monitorAsset: monitorAsset{Type: "machine", AssetID: "vm-01"}, Metric: "network", Unit: "bytes/s", monitorPoint: monitorPoint{At: nowMs, Status: "ok", Value: &vNet}},
	}, ""); err != nil {
		tx.Rollback()
		t.Fatalf("bridge host snapshot: %v", err)
	}
	if err := bridgeNativeMonitorCollectToSnapshots(t.Context(), tx, monitorRequest{Cluster: "dev", Instance: "local"}, "apps", []monitorInput{
		{monitorAsset: monitorAsset{Type: "app", AssetID: "portal.local"}, Metric: "response_time_ms", Unit: "ms", monitorPoint: monitorPoint{At: nowMs, Status: "ok", Value: &vLat}},
		{monitorAsset: monitorAsset{Type: "app", AssetID: "portal.local"}, Metric: "error_rate", Unit: "%", monitorPoint: monitorPoint{At: nowMs, Status: "ok", Value: &vErr}},
	}, ""); err != nil {
		tx.Rollback()
		t.Fatalf("bridge app snapshot: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var adminUserID string
	if err := f.store.db.QueryRow(`SELECT user_id FROM tokens WHERE id=?`, f.admin.ID).Scan(&adminUserID); err != nil {
		t.Fatalf("resolve admin user_id: %v", err)
	}
	overview, err := f.hub.BuildMonitorOverview(t.Context(), TokenAuth{UserID: adminUserID, TokenID: f.admin.ID}, "1h", "", "", "")
	if err != nil {
		t.Fatalf("BuildMonitorOverview failed: %v", err)
	}
	var foundContainer, foundK8sPodsApp, foundHost, foundApp, foundSkillHost bool
	for _, r := range overview.Resources {
		if r.AssetKind == "container" && r.AssetID == "local:containers" && r.CollectorMethod == "container_stats" {
			foundContainer = true
			if r.MemoryPercent == nil || *r.MemoryPercent < 24 || *r.MemoryPercent > 26 {
				t.Fatalf("expected bridged container memory percent ~25%%, got %+v", r.MemoryPercent)
			}
		}
		if r.AssetKind == "app" && r.AssetID == "k8s-ns:orders" && r.CollectorMethod == "k8s_api" {
			foundK8sPodsApp = true
			if r.CPUCores == nil || *r.CPUCores != 0.25 {
				t.Fatalf("expected bridged k8s pod cpu 0.25, got %+v", r.CPUCores)
			}
		}
		if r.AssetKind == "vm" && r.AssetID == "vm-01" && r.CollectorMethod == "agent_host" {
			foundHost = true
			if r.DiskPercent == nil || *r.DiskPercent < 89.9 || *r.DiskPercent > 90.1 {
				t.Fatalf("expected bridged host disk percent 90%%, got %+v", r.DiskPercent)
			}
			if r.CPUPercent == nil || *r.CPUPercent < 89.9 || *r.CPUPercent > 90.1 {
				t.Fatalf("expected bridged host cpu percent 90%%, got %+v", r.CPUPercent)
			}
			if r.Status != "warning" {
				t.Fatalf("expected bridged host warning status at 90%% usage, got %q", r.Status)
			}
			if len(r.Tags) != 1 || r.Tags[0] != "env=prod" {
				t.Fatalf("expected bridged host tags [env=prod], got %+v", r.Tags)
			}
		}
		if r.AssetKind == "app" && r.AssetID == "portal.local" && r.CollectorMethod == "app_probe" {
			foundApp = true
			if r.ResponseTimeMs == nil || *r.ResponseTimeMs != 12.5 {
				t.Fatalf("expected bridged app latency 12.5ms, got %+v", r.ResponseTimeMs)
			}
		}
		if r.AssetKind == "skill" && r.AssetID == "inspect-vm-host" {
			foundSkillHost = true
		}
	}
	if !foundContainer || !foundK8sPodsApp || !foundHost || !foundApp {
		t.Fatalf("expected native collect to bridge into monitor_resource_snapshots (container=%v k8sPodsApp=%v host=%v app=%v); resources=%+v",
			foundContainer, foundK8sPodsApp, foundHost, foundApp, overview.Resources)
	}
	if foundSkillHost {
		t.Fatalf("expected older skill:inspect-vm-host snapshot to be deduplicated against fresher native host snapshot; resources=%+v", overview.Resources)
	}
	if overview.Summary.TotalCPUCores != 8 {
		t.Fatalf("expected deduplicated TotalCPUCores=8 (no double-counting), got %v", overview.Summary.TotalCPUCores)
	}
	if overview.ByKind["k8s"] != 0 {
		t.Fatalf("expected native k8s_pods namespace snapshot not to inflate k8s cluster count, got by_kind[k8s]=%d", overview.ByKind["k8s"])
	}
}

func TestAssetMonitorInternalErrorLoggedAndReturns503(t *testing.T) {
	f := monitorFixture(t)
	if _, err := f.store.db.Exec(`INSERT INTO agent_status(cluster,instance,status,updated_at) VALUES('dev','local','online','now')`); err != nil {
		t.Fatal(err)
	}
	// Drop asset_monitor_series so readAssetMonitor triggers an unexpected internal DB error.
	if _, err := f.store.db.Exec(`DROP TABLE asset_monitor_alerts; DROP TABLE asset_monitor_rules; DROP TABLE asset_monitor_samples; DROP TABLE asset_monitor_series`); err != nil {
		t.Fatal(err)
	}
	var logBuf bytes.Buffer
	prevOut := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(prevOut) })

	body := f.http(t, f.admin, "GET", monitorPath+"?cluster=dev&instance=local&type=app&asset_id=order-api&namespace=orders&origin=k8s_top", "", 503)
	if !strings.Contains(string(body), `"asset_monitor_unavailable"`) {
		t.Fatalf("expected asset_monitor_unavailable response, got %s", body)
	}
	if !strings.Contains(logBuf.String(), "asset monitor GET internal error:") {
		t.Fatalf("expected Gateway log to capture internal asset monitor error, got %q", logBuf.String())
	}
}

// TestAssetMonitorReadDetailMySQL reproduces BUG-1 against a real MySQL server:
// reading /v1/assets/monitor detail for a multi-series K8s Pod asset must drain
// and close the outer asset_monitor_series cursor before querying samples, rules,
// and alerts on the same transaction, otherwise go-sql-driver/mysql fails with
// busy-buffer / bad-connection and HandleAssetMonitor returns 503.
func TestAssetMonitorReadDetailMySQL(t *testing.T) {
	port := strings.TrimSpace(os.Getenv("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT"))
	if port == "" {
		t.Skip("set DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT to run the live MySQL asset monitor detail regression")
	}
	addr := net.JoinHostPort("127.0.0.1", port)
	adminCfg := mysql.Config{
		User:                 "root",
		Passwd:               os.Getenv("DOOPS_TEST_GATEWAY_MYSQL_PASSWORD"),
		Net:                  "tcp",
		Addr:                 addr,
		AllowNativePasswords: true,
		Timeout:              8 * time.Second,
	}
	adminDB, err := sql.Open("mysql", adminCfg.FormatDSN())
	if err != nil {
		t.Fatalf("open MySQL admin: %v", err)
	}
	t.Cleanup(func() { adminDB.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := adminDB.PingContext(ctx); err != nil {
		t.Fatalf("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT=%s is unreachable: %v", port, err)
	}
	dbName := "gw_mon_" + strings.ToLower(randomHex(6))
	if _, err := adminDB.ExecContext(ctx, "CREATE DATABASE `"+dbName+"` DEFAULT CHARACTER SET utf8mb4 DEFAULT COLLATE utf8mb4_0900_ai_ci"); err != nil {
		t.Fatalf("create db %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer ccancel()
		_, _ = adminDB.ExecContext(cctx, "DROP DATABASE IF EXISTS `"+dbName+"`")
	})
	dbCfg := adminCfg
	dbCfg.DBName = dbName
	t.Setenv("DOOPS_GATEWAY_STORE", "mysql")
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", dbCfg.FormatDSN())
	t.Setenv("DOOPS_GATEWAY_DATA_DIR", t.TempDir())

	store, err := OpenGatewayStore("")
	if err != nil {
		t.Fatalf("OpenGatewayStore(mysql): %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	adminUser, err := store.CreateUser("mon-admin")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, "UPDATE users SET is_admin=1 WHERE id=?", adminUser.ID); err != nil {
		t.Fatalf("promote mon-admin: %v", err)
	}
	avail := true
	if err := store.MarkAgentOnline(AgentStatus{
		Cluster: "prod", Instance: "k8s-a", Generation: 1, ConnectedAt: time.Now().UTC(), Mode: "kubernetes", KubernetesAvailable: &avail,
	}); err != nil {
		t.Fatalf("MarkAgentOnline: %v", err)
	}

	nowMs := time.Now().UnixMilli()
	cpuVal, memVal, thresh := 0.25, float64(64*1024*1024), 0.1
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin seed tx: %v", err)
	}
	req := monitorRequest{Cluster: "prod", Instance: "k8s-a"}
	samples := []monitorInput{
		{monitorAsset: monitorAsset{Type: "app", AssetID: "order-api-0", Namespace: "orders", Origin: "k8s_top"}, Metric: "cpu", Unit: "cores", monitorPoint: monitorPoint{At: nowMs - 60_000, Status: "ok", Value: &cpuVal}},
		{monitorAsset: monitorAsset{Type: "app", AssetID: "order-api-0", Namespace: "orders", Origin: "k8s_top"}, Metric: "cpu", Unit: "cores", monitorPoint: monitorPoint{At: nowMs, Status: "ok", Value: &cpuVal}},
		{monitorAsset: monitorAsset{Type: "app", AssetID: "order-api-0", Namespace: "orders", Origin: "k8s_top"}, Metric: "memory", Unit: "bytes", monitorPoint: monitorPoint{At: nowMs - 60_000, Status: "ok", Value: &memVal}},
		{monitorAsset: monitorAsset{Type: "app", AssetID: "order-api-0", Namespace: "orders", Origin: "k8s_top"}, Metric: "memory", Unit: "bytes", monitorPoint: monitorPoint{At: nowMs, Status: "ok", Value: &memVal}},
	}
	for _, sample := range samples {
		if _, err := storeMonitorPoint(ctx, tx, req, sample, "k8s_top", adminUser.ID); err != nil {
			_ = tx.Rollback()
			t.Fatalf("storeMonitorPoint %s: %v", sample.Metric, err)
		}
	}
	ruleReq := monitorRequest{
		Cluster:      "prod",
		Instance:     "k8s-a",
		monitorAsset: monitorAsset{Type: "app", AssetID: "order-api-0", Namespace: "orders", Origin: "k8s_top"},
		Metric:       "cpu",
		monitorRule:  monitorRule{Threshold: &thresh, Duration: 1},
	}
	if _, err := saveMonitorRule(tx, ruleReq, nowMs, adminUser.ID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("saveMonitorRule: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit seed tx: %v", err)
	}

	tok, err := store.CreateToken(CreateTokenRequest{
		Kind:      TokenKindUser,
		UserID:    adminUser.ID,
		Name:      "mon-admin-tok",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	auth := TokenAuth{UserID: adminUser.ID, TokenID: tok.ID, Kind: TokenKindUser}
	// 1) List endpoint must return 200 with the seeded k8s_top Pod asset.
	listAny, err := store.readAssetMonitor(ctx, auth, url.Values{"cluster": {"prod"}, "instance": {"k8s-a"}})
	if err != nil {
		t.Fatalf("readAssetMonitor list on MySQL failed: %v", err)
	}
	listMap, _ := listAny.(map[string]any)
	assets, _ := listMap["assets"].([]monitorAsset)
	if len(assets) != 1 || assets[0].AssetID != "order-api-0" || assets[0].Origin != "k8s_top" {
		t.Fatalf("unexpected MySQL list assets: %+v", listAny)
	}

	// 2) Detail endpoint for the same Pod (2 series: cpu + memory) must return both
	// series, samples, and rule without MySQL busy-buffer / bad-connection errors.
	detailAny, err := store.readAssetMonitor(ctx, auth, url.Values{
		"cluster":   {"prod"},
		"instance":  {"k8s-a"},
		"type":      {"app"},
		"asset_id":  {"order-api-0"},
		"namespace": {"orders"},
		"origin":    {"k8s_top"},
		"range":     {"1h"},
	})
	if err != nil {
		t.Fatalf("readAssetMonitor detail on MySQL failed: %v", err)
	}
	detailMap, _ := detailAny.(map[string]any)
	seriesList, _ := detailMap["series"].([]monitorSeries)
	if len(seriesList) != 2 {
		t.Fatalf("expected 2 series (cpu, memory) on MySQL, got %d: %+v", len(seriesList), seriesList)
	}
	if seriesList[0].Metric != "cpu" || len(seriesList[0].Points) != 2 || seriesList[0].Rule == nil || *seriesList[0].Rule.Threshold != 0.1 {
		t.Fatalf("unexpected cpu series on MySQL: %+v", seriesList[0])
	}
	if seriesList[1].Metric != "memory" || len(seriesList[1].Points) != 2 {
		t.Fatalf("unexpected memory series on MySQL: %+v", seriesList[1])
	}
}
