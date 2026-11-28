package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestSQLiteStartupAutoMigratesAllAssetTablesAndRoutes(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_STORE", "sqlite")
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("a", 32))
	dbPath := filepath.Join(t.TempDir(), "fresh-gateway.db")
	store, err := OpenGatewayStore(dbPath)
	if err != nil {
		t.Fatalf("open fresh sqlite store: %v", err)
	}
	defer store.Close()

	if !store.agentResourceSyncReady(context.Background()) {
		t.Fatal("fresh SQLite Gateway store must have agent resource sync tables ready on startup")
	}

	adminUser, err := store.CreateUser("auto-migrate-admin")
	if err != nil {
		t.Fatalf("create admin user: %v", err)
	}
	if err := store.SetUserAdmin(adminUser.ID, true); err != nil {
		t.Fatalf("set admin: %v", err)
	}
	adminTok, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: adminUser.ID})
	if err != nil {
		t.Fatalf("create admin token: %v", err)
	}

	hub := NewGatewayHub(store, GatewayHubOptions{AllowOpenAgentRegistration: true})
	defer hub.CloseTunnels()
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		var reader io.Reader
		if body != nil {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			reader = bytes.NewReader(raw)
		}
		req, err := http.NewRequest(method, srv.URL+path, reader)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+adminTok.Plaintext)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != want {
			t.Fatalf("%s %s: HTTP %d want %d: %s", method, path, res.StatusCode, want, out)
		}
		return out
	}

	// All asset, monitoring, inspection, catalog, and workspace GET routes must
	// respond 200 immediately on a freshly started SQLite Gateway with zero manual SQL.
	for _, path := range []string{
		"/v1/assets/machines",
		"/v1/assets/clusters",
		"/v1/assets/node-links",
		"/v1/assets/systems",
		"/v1/assets/dashboard",
		"/v1/assets/topology",
		"/v1/assets/coverage?baseline_id=live",
		"/v1/assets/monitor?view=alerts",
		"/v1/assets/inspections",
		"/v1/assets/records?kind=database",
		"/v1/assets/catalog?kind=database",
		"/v1/assets/cluster-profiles",
		"/v1/assets/workspace?view=directory",
	} {
		call(http.MethodGet, path, nil, http.StatusOK)
	}

	// Verify machine write paths (manual create with tags + CSV preview/import) work out of the box.
	call(http.MethodPost, "/v1/assets/machines", map[string]any{
		"action": "create",
		"machine": map[string]any{
			"asset_id": "SRV-AUTO-01",
			"name":     "auto-migrated-node",
			"kind":     "physical",
			"tags":     []string{"prod", "zheyin"},
		},
	}, http.StatusOK)

	csvBody := "asset_id,name,kind,address,cpu_count,memory_gib,disk_gib,host_asset_id\nSRV-AUTO-02,csv-node,vm,192.0.2.20,4,16,100,\n"
	previewRaw := call(http.MethodPost, "/v1/assets/machines", map[string]any{
		"action": "preview",
		"csv":    csvBody,
	}, http.StatusOK)
	var preview struct {
		PreviewToken string `json:"preview_token"`
	}
	if err := json.Unmarshal(previewRaw, &preview); err != nil || preview.PreviewToken == "" {
		t.Fatalf("unexpected preview response: %s", previewRaw)
	}
	call(http.MethodPost, "/v1/assets/machines", map[string]any{
		"action":        "import",
		"csv":           csvBody,
		"preview_token": preview.PreviewToken,
	}, http.StatusOK)

	listRaw := call(http.MethodGet, "/v1/assets/machines", nil, http.StatusOK)
	var list struct {
		Machines []struct {
			AssetID string   `json:"asset_id"`
			Tags    []string `json:"tags"`
		} `json:"machines"`
	}
	if err := json.Unmarshal(listRaw, &list); err != nil || len(list.Machines) != 2 {
		t.Fatalf("expected 2 machines after create+import, got %s (err=%v)", listRaw, err)
	}

	// Verify fail-closed behavior still returns 503 machine_inventory_unavailable
	// if the inventory table is missing or corrupted at runtime.
	if _, err := store.db.Exec(`DROP TABLE inventory_machines`); err != nil {
		t.Fatalf("drop inventory_machines: %v", err)
	}
	errBody := call(http.MethodGet, "/v1/assets/machines", nil, http.StatusServiceUnavailable)
	if !strings.Contains(string(errBody), `"machine_inventory_unavailable"`) {
		t.Fatalf("expected machine_inventory_unavailable after dropping table, got %s", errBody)
	}
}

func TestSQLiteStartupUpgradesLegacyInventoryDatabaseIdempotently(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_STORE", "sqlite")
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("b", 32))
	dbPath := filepath.Join(t.TempDir(), "legacy-gateway.db")

	// Pre-populate an older SQLite database containing only the original
	// inventory_machines table (missing the `active` column, tag index table,
	// login vault tables, coverage tables, and agent resource sync tables).
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw legacy sqlite db: %v", err)
	}
	if _, err := rawDB.Exec(`CREATE TABLE inventory_machines (
		asset_id TEXT PRIMARY KEY NOT NULL,
		name TEXT NOT NULL CHECK (length(name) > 0),
		kind TEXT NOT NULL CHECK (kind IN ('physical', 'vm')),
		address TEXT NOT NULL DEFAULT '',
		cpu_count INTEGER CHECK (cpu_count > 0),
		memory_gib REAL CHECK (memory_gib > 0),
		disk_gib REAL CHECK (disk_gib > 0),
		host_asset_id TEXT REFERENCES inventory_machines(asset_id),
		cluster TEXT NOT NULL DEFAULT '',
		instance TEXT NOT NULL DEFAULT '',
		revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
		CHECK (host_asset_id IS NULL OR (kind = 'vm' AND host_asset_id <> asset_id)),
		CHECK ((cluster = '') = (instance = ''))
	) STRICT;
	INSERT INTO inventory_machines(asset_id, name, kind, address) VALUES ('SRV-LEGACY-1', 'legacy-host', 'physical', '192.0.2.10');`); err != nil {
		rawDB.Close()
		t.Fatalf("seed legacy inventory_machines table: %v", err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("close raw legacy db: %v", err)
	}

	// Opening with OpenGatewayStore must upgrade the legacy table in place
	// (adding `active` with default 1) and create all missing asset tables
	// without losing the legacy row.
	store, err := OpenGatewayStore(dbPath)
	if err != nil {
		t.Fatalf("upgrade legacy sqlite store: %v", err)
	}
	var name, createdAt string
	var active int
	if err := store.db.QueryRow(`SELECT name, active, created_at FROM inventory_machines WHERE asset_id='SRV-LEGACY-1'`).Scan(&name, &active, &createdAt); err != nil {
		store.Close()
		t.Fatalf("query upgraded legacy machine row: %v", err)
	}
	if name != "legacy-host" || active != 1 || createdAt != "" {
		store.Close()
		t.Fatalf("upgraded legacy machine = (%q, active=%d, created_at=%q), want (legacy-host, active=1, created_at=\"\")", name, active, createdAt)
	}
	for _, table := range []string{
		"inventory_machine_tags",
		"inventory_machine_login_accounts",
		"asset_coverage_baselines",
		"asset_coverage_registers",
		"agent_resource_state",
		"agent_observations",
		"agent_resource_audit",
	} {
		if !store.tableExists(context.Background(), table) {
			store.Close()
			t.Fatalf("upgraded legacy store is missing table %s", table)
		}
	}
	adminUser, err := store.CreateUser("legacy-upgrade-admin")
	if err != nil {
		store.Close()
		t.Fatalf("create admin on upgraded store: %v", err)
	}
	if err := store.SetUserAdmin(adminUser.ID, true); err != nil {
		store.Close()
		t.Fatalf("grant admin on upgraded store: %v", err)
	}
	adminTok, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: adminUser.ID})
	if err != nil {
		store.Close()
		t.Fatalf("create token on upgraded store: %v", err)
	}
	hub := NewGatewayHub(store, GatewayHubOptions{AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/v1/assets/machines", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok.Plaintext)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	hub.CloseTunnels()
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "SRV-LEGACY-1") {
		store.Close()
		t.Fatalf("GET /v1/assets/machines on upgraded legacy store = %d %s, want 200 containing SRV-LEGACY-1", rec.Code, rec.Body.String())
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close upgraded store: %v", err)
	}

	// Reopening a second time must remain completely idempotent and keep data intact.
	reopened, err := OpenGatewayStore(dbPath)
	if err != nil {
		t.Fatalf("reopen upgraded sqlite store: %v", err)
	}
	defer reopened.Close()
	if err := reopened.db.QueryRow(`SELECT name, active FROM inventory_machines WHERE asset_id='SRV-LEGACY-1'`).Scan(&name, &active); err != nil {
		t.Fatalf("query reopened legacy machine row: %v", err)
	}
	if name != "legacy-host" || active != 1 {
		t.Fatalf("reopened legacy machine = (%q, active=%d), want (legacy-host, active=1)", name, active)
	}
}

func TestSQLiteAutoMigrationCoversAllDocsMigrationTables(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_STORE", "sqlite")
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "parity.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	migrations, err := filepath.Glob("../../../docs/migrations/*.sql")
	if err != nil || len(migrations) == 0 {
		t.Fatalf("glob migrations: %v (count=%d)", err, len(migrations))
	}
	re := regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?["']?([a-z0-9_]+)`)
	expected := map[string]bool{}
	for _, path := range migrations {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		clean := gatewayMySQLTestStripSQLComments(string(raw))
		for _, m := range re.FindAllStringSubmatch(clean, -1) {
			name := strings.ToLower(m[1])
			if strings.HasSuffix(name, "_new") {
				continue
			}
			expected[name] = true
		}
	}

	var missing []string
	ctx := context.Background()
	for table := range expected {
		if !store.tableExists(ctx, table) {
			missing = append(missing, table)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("SQLite OpenGatewayStore startup auto-migration is missing table(s) declared in backend/docs/migrations/*.sql: %v", missing)
	}
}

func TestSQLiteStartupUpgradesLegacyAssetMonitorSeriesSchema(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_STORE", "sqlite")
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("c", 32))
	dbPath := filepath.Join(t.TempDir(), "legacy-monitor.db")

	// Pre-populate a legacy SQLite database with the old pre-device/pre-disk-io/
	// pre-db-metrics asset_monitor_series table (missing device_id and newer
	// CHECK values) plus an existing sample, rule, and alert referencing it.
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw legacy monitor db: %v", err)
	}
	if _, err := rawDB.Exec(`
CREATE TABLE asset_monitor_series (
 id INTEGER PRIMARY KEY, cluster TEXT NOT NULL, instance TEXT NOT NULL, namespace TEXT NOT NULL,
 type TEXT NOT NULL CHECK(type IN ('machine','os','kernel','process','app','database')),
 asset_id TEXT NOT NULL, origin TEXT NOT NULL CHECK(origin IN ('k8s_top','exporter')),
 metric TEXT NOT NULL CHECK(metric IN ('cpu','memory','gpu','network','requests','storage')),
 UNIQUE(cluster,instance,type,asset_id,namespace,origin,metric)
);
CREATE TABLE asset_monitor_samples (
 series_id INTEGER NOT NULL REFERENCES asset_monitor_series(id), at_ms INTEGER NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('ok','missing')), value REAL, unit TEXT NOT NULL,
 PRIMARY KEY(series_id,at_ms)
);
CREATE TABLE asset_monitor_rules (
 series_id INTEGER PRIMARY KEY REFERENCES asset_monitor_series(id),
 threshold REAL NOT NULL CHECK(threshold>=0),
 duration_seconds INTEGER NOT NULL CHECK(duration_seconds BETWEEN 1 AND 604800),
 effective_ms INTEGER NOT NULL, first_above_ms INTEGER
);
CREATE TABLE asset_monitor_alerts (
 id INTEGER PRIMARY KEY, series_id INTEGER NOT NULL REFERENCES asset_monitor_series(id),
 state TEXT NOT NULL CHECK(state IN ('open','resolved')), threshold REAL NOT NULL,
 duration_seconds INTEGER NOT NULL, opened_ms INTEGER NOT NULL, resolved_ms INTEGER
);
INSERT INTO asset_monitor_series(id,cluster,instance,namespace,type,asset_id,origin,metric)
 VALUES(42,'zheyin','node-239','','machine','SRV-OLD','exporter','cpu');
INSERT INTO asset_monitor_samples(series_id,at_ms,status,value,unit)
 VALUES(42,1700000000000,'ok',55.5,'percent');
INSERT INTO asset_monitor_rules(series_id,threshold,duration_seconds,effective_ms)
 VALUES(42,80,300,1700000000000);
INSERT INTO asset_monitor_alerts(id,series_id,state,threshold,duration_seconds,opened_ms)
 VALUES(7,42,'open',80,300,1700000000000);
`); err != nil {
		rawDB.Close()
		t.Fatalf("seed legacy asset_monitor tables: %v", err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("close raw legacy monitor db: %v", err)
	}

	store, err := OpenGatewayStore(dbPath)
	if err != nil {
		t.Fatalf("open upgraded legacy monitor store: %v", err)
	}

	// Verify existing series, sample, rule, and alert rows survived with exact IDs.
	var deviceID string
	if err := store.db.QueryRow(`SELECT device_id FROM asset_monitor_series WHERE id=42 AND asset_id='SRV-OLD'`).Scan(&deviceID); err != nil || deviceID != "" {
		store.Close()
		t.Fatalf("legacy series 42 lost or wrong device_id=%q: %v", deviceID, err)
	}
	var sampleVal float64
	if err := store.db.QueryRow(`SELECT value FROM asset_monitor_samples WHERE series_id=42`).Scan(&sampleVal); err != nil || sampleVal != 55.5 {
		store.Close()
		t.Fatalf("legacy sample lost: val=%v err=%v", sampleVal, err)
	}
	var alertState string
	if err := store.db.QueryRow(`SELECT state FROM asset_monitor_alerts WHERE id=7 AND series_id=42`).Scan(&alertState); err != nil || alertState != "open" {
		store.Close()
		t.Fatalf("legacy alert lost: state=%q err=%v", alertState, err)
	}

	// Verify newer metrics and origins (db_tps, agent_host disk_read_iops, container memory_limit) now insert cleanly.
	for _, stmt := range []string{
		`INSERT INTO asset_monitor_series(cluster,instance,namespace,type,asset_id,origin,metric,device_id) VALUES('zheyin','node-239','db','database','pg-main','exporter','db_tps','')`,
		`INSERT INTO asset_monitor_series(cluster,instance,namespace,type,asset_id,origin,metric,device_id) VALUES('zheyin','node-239','','machine','SRV-OLD','agent_host','disk_read_iops','nvme0n1')`,
		`INSERT INTO asset_monitor_series(cluster,instance,namespace,type,asset_id,origin,metric,device_id) VALUES('zheyin','node-239','app','container','web','container_stats','memory_limit','')`,
	} {
		if _, err := store.db.Exec(stmt); err != nil {
			store.Close()
			t.Fatalf("upgraded asset_monitor_series rejected modern row (%s): %v", stmt, err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close upgraded monitor store: %v", err)
	}

	// Reopening a second time must be a fast no-op and preserve all 4 series.
	reopened, err := OpenGatewayStore(dbPath)
	if err != nil {
		t.Fatalf("reopen upgraded monitor store: %v", err)
	}
	defer reopened.Close()
	var count int
	if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM asset_monitor_series`).Scan(&count); err != nil || count != 4 {
		t.Fatalf("reopened monitor series count = %d, want 4 (err=%v)", count, err)
	}
}
