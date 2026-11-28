package server

// INT-46: D17 proved the MySQL backend and the offline copier separately, but
// never moved a populated live Gateway database across. This test is that
// rehearsal: a real store created by OpenGatewayStore is seeded through the
// store's own API, copied by the production copier into the reviewed MySQL
// DDL, and then compared table by table and value by value. It does not prove
// a production cutover (that still needs a capture of the real database and a
// maintenance window), only that the copier and schema agree on a real store.
//
// Set DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT to the published port of a disposable
// local MySQL 8.4 with an empty root account. The database is created by this
// test with a unique name and dropped again. When the variable is set but the
// server is unreachable the test fails instead of skipping: a cutover rehearsal
// that silently did not run is worse than no rehearsal.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

// gatewayMySQLCutoverCanary is the only secret value this file stores. It is a
// deliberately fake canary, never a real credential, and every assertion below
// fails if it appears in the copied database or in the copy report.
const gatewayMySQLCutoverCanary = "canary-cutover-6b2d94f0"

type gatewayMySQLCutoverSeed struct {
	UserID            string
	CredentialID      string
	CredentialVersion string
	ScheduleID        string
	RunID             string
	SkillSlug         string
	SkillSHA256       string
	SkillStorageDir   string
	ArtifactDir       string
	ArtifactSHA256    string
	ArtifactBytes     int64
	MetricKeys        []string
}

func TestGatewayMySQLCutoverRehearsal(t *testing.T) {
	port := strings.TrimSpace(os.Getenv("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT"))
	if port == "" {
		t.Skip("unset DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT to skip this test")
	}
	ctx := t.Context()
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("k", 32))

	source, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("open the live SQLite store: %v", err)
	}
	t.Cleanup(func() { _ = source.Close() })

	// A production SQLite database also carries the operator-migration tables
	// and five legacy columns from the frozen export; a fresh store only creates
	// the runtime half. Add exactly that shape so the cutover source is the
	// database a Gateway actually opens in production.
	gatewayMySQLCutoverExtendLiveStore(t, source.db)
	seed := gatewayMySQLCutoverSeedSource(t, ctx, source)

	dst := gatewayMySQLCutoverDestination(t, port)
	if collation := gatewayMySQLCutoverDatabaseCollation(t, dst); collation != "utf8mb4_0900_ai_ci" {
		t.Fatalf("rehearsal database collation = %s, want utf8mb4_0900_ai_ci", collation)
	}
	if _, err := installGatewayMySQLSchema(ctx, dst); err != nil {
		t.Fatalf("install the reviewed MySQL schema: %v", err)
	}
	if _, err := source.db.Exec("INSERT INTO inventory_machines (asset_id, name, kind, address, host_asset_id, cluster, instance, revision, active) VALUES " +
		"('cutover-host', 'cutover host', 'physical', '10.60.0.1', NULL, '', '', 1, 1), " +
		"('cutover-vm', 'cutover vm', 'vm', '10.60.0.2', 'cutover-host', '', '', 1, 1)"); err != nil {
		t.Fatalf("seed the self-referencing inventory rows: %v", err)
	}

	// The reviewed DDL keeps the inventory_machines self-reference. The copier now
	// resolves it data-aware (NULL first, backfill in the same transaction), so the
	// rehearsal leaves the constraint in place instead of dropping it by hand.
	sourceData := filepath.Dir(source.dbPath)
	targetData := t.TempDir()
	report, err := gatewayCopyRun(ctx, source.db, dst, false, &gatewayCopyDataDirs{Source: sourceData, Target: targetData})
	if err != nil {
		t.Fatalf("cut the live SQLite store over to MySQL: %v", err)
	}
	if len(report.TableRows) != len(gatewayMySQLTestTables) {
		var missing []string
		for _, name := range gatewayMySQLTestTables {
			if _, ok := report.TableRows[name]; !ok {
				missing = append(missing, name)
			}
		}
		t.Fatalf("copied %d tables, want %d; missing %v", len(report.TableRows), len(gatewayMySQLTestTables), missing)
	}
	if strings.Contains(fmt.Sprint(report.TableRows), gatewayMySQLCutoverCanary) {
		t.Fatalf("the copy report leaked the credential canary")
	}

	wantRows := map[string]int64{
		"users":                    1,
		"grants":                   1,
		"audit_events":             2,
		"monitor_schedules":        1,
		"monitor_runs":             1,
		"monitor_run_items":        1,
		"inspection_metric_points": 2,
		"credentials":              1,
		"credential_versions":      1,
	}
	for table, want := range wantRows {
		if got := report.TableRows[table]; got != want {
			t.Fatalf("%s: copier moved %d rows, want %d", table, got, want)
		}
	}
	// A fresh store also seeds the builtin Skill catalog, so this table carries
	// more than the rehearsal row. Compare it with the source instead of a
	// constant, and keep the seeded row itself under assertion below.
	var sourceSkills int64
	if err := source.db.QueryRow("SELECT COUNT(*) FROM platform_skills").Scan(&sourceSkills); err != nil {
		t.Fatalf("count the source platform skills: %v", err)
	}
	if sourceSkills < 2 {
		t.Fatalf("source platform_skills = %d, want the builtin catalog plus the rehearsal row", sourceSkills)
	}
	if got := report.TableRows["platform_skills"]; got != sourceSkills {
		t.Fatalf("platform_skills: copier moved %d rows, want %d", got, sourceSkills)
	}

	// Row counts and every non-secret column, compared value by value.
	for _, table := range []string{
		"users", "grants", "audit_events", "monitor_schedules", "monitor_runs",
		"monitor_run_items", "inspection_metric_points", "platform_skills",
		"credentials", "credential_versions",
	} {
		gatewayMySQLCutoverAssertTable(t, source.db, dst, table)
	}

	// The schema ledger is carried, not regenerated: exactly the one baseline
	// row the source store was migrated to.
	var ledgerRows int
	if err := dst.QueryRow("SELECT COUNT(*) FROM gateway_schema_migrations").Scan(&ledgerRows); err != nil {
		t.Fatalf("read the copied schema ledger: %v", err)
	}
	if ledgerRows != 1 {
		t.Fatalf("gateway_schema_migrations rows = %d, want 1", ledgerRows)
	}
	var version string
	if err := dst.QueryRow("SELECT version FROM gateway_schema_migrations").Scan(&version); err != nil {
		t.Fatalf("read the copied schema version: %v", err)
	}
	if version != gatewaySchemaBaselineVersion {
		t.Fatalf("gateway_schema_migrations version = %q, want %q", version, gatewaySchemaBaselineVersion)
	}

	// The self-reference survives the cutover and the child points at its parent.
	var constraints int
	if err := dst.QueryRow("SELECT COUNT(*) FROM information_schema.KEY_COLUMN_USAGE WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='inventory_machines' AND COLUMN_NAME='host_asset_id' AND REFERENCED_TABLE_NAME='inventory_machines'").Scan(&constraints); err != nil {
		t.Fatalf("read the destination self-reference: %v", err)
	}
	if constraints != 1 {
		t.Fatalf("destination self-reference count = %d, want 1", constraints)
	}
	var childHost, childKind string
	if err := dst.QueryRow("SELECT host_asset_id, kind FROM inventory_machines WHERE asset_id='cutover-vm'").Scan(&childHost, &childKind); err != nil {
		t.Fatalf("read the copied inventory child: %v", err)
	}
	if childHost != "cutover-host" || childKind != "vm" {
		t.Fatalf("copied inventory child = (%s, %s), want (cutover-host, vm)", childHost, childKind)
	}
	if report.Files == nil || report.Files.ArtifactRows == 0 {
		t.Fatalf("the cutover report carries no filesystem evidence: %+v", report.Files)
	}

	// Key shapes the generic column comparison cannot express.
	var runStatus, runScheduleID string
	if err := dst.QueryRow("SELECT status, schedule_id FROM monitor_runs WHERE id=?", seed.RunID).Scan(&runStatus, &runScheduleID); err != nil {
		t.Fatalf("read the copied monitor run: %v", err)
	}
	if runStatus != "warning" || runScheduleID != seed.ScheduleID {
		t.Fatalf("monitor_runs row = (status %q, schedule_id %q), want (warning, %q)", runStatus, runScheduleID, seed.ScheduleID)
	}
	if seed.ArtifactDir == "" || len(seed.ArtifactSHA256) != 64 || seed.ArtifactBytes == 0 {
		t.Fatalf("seed artifact index is incomplete: dir=%q sha=%q bytes=%d", seed.ArtifactDir, seed.ArtifactSHA256, seed.ArtifactBytes)
	}
	var artifactDir, artifactSHA string
	var artifactBytes int64
	if err := dst.QueryRow("SELECT artifact_dir, artifact_sha256, artifact_bytes FROM monitor_run_items WHERE run_id=?", seed.RunID).Scan(&artifactDir, &artifactSHA, &artifactBytes); err != nil {
		t.Fatalf("read the copied artifact index: %v", err)
	}
	if artifactDir != seed.ArtifactDir || artifactSHA != seed.ArtifactSHA256 || artifactBytes != seed.ArtifactBytes {
		t.Fatalf("artifact index changed in the cutover: got (%q, %q, %d), want (%q, %q, %d)", artifactDir, artifactSHA, artifactBytes, seed.ArtifactDir, seed.ArtifactSHA256, seed.ArtifactBytes)
	}

	// The MySQL Gateway resolves artifacts file-first from the data dir. A
	// cutover is only complete when the copied row points at copied bytes and
	// the same reader the HTTP handler uses reports the artifact as present.
	cutoverStore := &GatewayStore{db: dst, dialect: gatewayDialectMySQL, backend: "mysql", dbDir: targetData}
	detail, detailItems, err := cutoverStore.GetMonitorRunDetail(ctx, TokenAuth{UserID: seed.UserID}, seed.RunID)
	if err != nil {
		t.Fatalf("read the copied run back through the store: %v", err)
	}
	if detail.ArtifactState != monitorArtifactStatePresent {
		t.Fatalf("copied run artifact_state = %q, want %q", detail.ArtifactState, monitorArtifactStatePresent)
	}
	if len(detailItems) == 0 || detailItems[0].ArtifactState != monitorArtifactStatePresent || len(detailItems[0].ArtifactJSON) == 0 {
		t.Fatalf("copied run item did not resolve its artifact from the target data dir: %+v", detailItems)
	}

	// A migrated Skill has to stay executable, not just present: its package
	// path comes from the copied platform_skills row and a broken script would
	// only surface when a real inspection runs.
	skillScript := filepath.Join(targetData, filepath.FromSlash(seed.SkillStorageDir), "scripts", "check.sh")
	if _, err := os.Stat(skillScript); err != nil {
		t.Fatalf("copied skill script %s is missing: %v", skillScript, err)
	}
	if out, err := exec.Command("sh", "-n", skillScript).CombinedOutput(); err != nil {
		t.Fatalf("copied skill script is not valid shell: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	metricKeys, err := gatewayMySQLCutoverMetricKeys(dst)
	if err != nil {
		t.Fatalf("read the copied metric keys: %v", err)
	}
	if strings.Join(metricKeys, ",") != strings.Join(seed.MetricKeys, ",") {
		t.Fatalf("inspection_metric_points keys = %v, want %v", metricKeys, seed.MetricKeys)
	}
	var skillSHA, skillDir string
	if err := dst.QueryRow("SELECT sha256, storage_dir FROM platform_skills WHERE slug=?", seed.SkillSlug).Scan(&skillSHA, &skillDir); err != nil {
		t.Fatalf("read the copied platform skill: %v", err)
	}
	if skillSHA != seed.SkillSHA256 || skillDir != seed.SkillStorageDir {
		t.Fatalf("platform_skills metadata changed: got (%q, %q), want (%q, %q)", skillSHA, skillDir, seed.SkillSHA256, seed.SkillStorageDir)
	}
	var credentialName, credentialState string
	if err := dst.QueryRow("SELECT name, state FROM credentials WHERE id=?", seed.CredentialID).Scan(&credentialName, &credentialState); err != nil {
		t.Fatalf("read the copied credential metadata: %v", err)
	}
	if credentialName != "cutover-registry" || credentialState != "active" {
		t.Fatalf("credentials row = (%q, %q), want (cutover-registry, active)", credentialName, credentialState)
	}
	var versionState string
	if err := dst.QueryRow("SELECT state FROM credential_versions WHERE id=?", seed.CredentialVersion).Scan(&versionState); err != nil {
		t.Fatalf("read the copied credential version: %v", err)
	}
	if versionState != "active" {
		t.Fatalf("credential_versions state = %q, want active", versionState)
	}

	// The payload is encrypted at rest in the source and must stay so after the
	// copy: the canary may not appear in any text column of the MySQL database.
	var ciphertext string
	if err := dst.QueryRow("SELECT payload_ciphertext FROM credential_versions WHERE credential_id=?", seed.CredentialID).Scan(&ciphertext); err != nil {
		t.Fatalf("read the copied credential ciphertext: %v", err)
	}
	if ciphertext == "" || strings.Contains(ciphertext, gatewayMySQLCutoverCanary) {
		t.Fatalf("credential ciphertext is empty or contains the canary after the cutover")
	}
	gatewayMySQLCutoverAssertCanaryAbsent(t, dst)
}

// gatewayMySQLCutoverExtendLiveStore reproduces the operator half of a
// production SQLite database on top of a freshly migrated store: the table
// definitions from the frozen export (not the indexes or triggers, which the
// live store owns) and the five legacy columns earlier releases wrote.
func gatewayMySQLCutoverExtendLiveStore(t *testing.T, db *sql.DB) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "gateway-sqlite-schema.sql"))
	if err != nil {
		t.Fatalf("read the frozen export: %v", err)
	}
	for _, stmt := range strings.Split(string(data), ";") {
		stmt = strings.TrimSpace(stmt)
		if !strings.HasPrefix(strings.ToUpper(stmt), "CREATE TABLE") {
			continue
		}
		if _, err := db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("extend the live store with an operator table: %v", err)
		}
	}
	for _, statement := range []string{
		"ALTER TABLE audit_events ADD COLUMN project TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE audit_events ADD COLUMN environment TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE audit_events ADD COLUMN template TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE audit_events ADD COLUMN namespace TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE users ADD COLUMN account_type TEXT NOT NULL DEFAULT 'person'",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("add a legacy column to the live store: %v", err)
		}
	}
	if got, want := len(gatewaySchemaLiveTables(t, db)), len(gatewayMySQLTestTables); got != want {
		t.Fatalf("cutover source tables = %d, want %d (the live store plus the operator tables)", got, want)
	}
}

func gatewayMySQLCutoverSeedSource(t *testing.T, ctx context.Context, store *GatewayStore) gatewayMySQLCutoverSeed {
	t.Helper()
	base := time.Now().UTC().Truncate(time.Millisecond)
	baseMs := base.UnixMilli()

	user, err := store.CreateUserWithPassword(CreateUserRequest{
		Name:     "cutover-alice",
		Password: "cutover-test-" + gatewayMySQLCutoverCanary,
	})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := store.GrantUser(user.ID, ScopeGrant{Cluster: "cutover-cluster", Instance: "cutover-instance"}); err != nil {
		t.Fatalf("seed target grant: %v", err)
	}
	for i, action := range []GatewayAction{GatewayAction("monitor.inspect"), GatewayAction("credential.use")} {
		auditID, err := store.StartAudit(AuditEvent{
			UserID:         user.ID,
			Cluster:        "cutover-cluster",
			Instance:       "cutover-instance",
			Action:         action,
			Session:        "cutover-rehearsal",
			CommandSummary: fmt.Sprintf("cutover rehearsal %d", i+1),
			Status:         "running",
			StartedAt:      base.Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Fatalf("seed audit event %d: %v", i+1, err)
		}
		finish := AuditFinish{Status: "success", BytesIn: 11, BytesOut: 22, Model: "cutover-model", InputTokens: 3, OutputTokens: 4, TotalTokens: 7}
		if i == 1 {
			finish.Status, finish.Error, finish.Tail = "error", "cutover rehearsal error", "tail"
		}
		if err := store.FinishAudit(auditID, finish); err != nil {
			t.Fatalf("finish audit event %d: %v", i+1, err)
		}
	}

	skill, err := store.UpsertPlatformSkill(ctx, platformSkillRecord{
		Slug:        "cutover-inspection",
		Name:        "Cutover inspection",
		Description: "rehearsal skill",
		Scope:       "custom",
		Category:    "inspection",
		OwnerID:     user.ID,
		OwnerName:   user.Name,
		SkillMD:     "---\nname: cutover-inspection\ndescription: rehearsal skill\n---\n# Cutover\n",
		Files:       []platformSkillFile{{Path: "scripts/check.sh", Content: "#!/bin/sh\necho cutover\n"}},
		CreatedAtMs: baseMs,
	})
	if err != nil {
		t.Fatalf("seed platform skill: %v", err)
	}

	credential, err := store.CreateCredential(CredentialCreateRequest{
		Name:      "cutover-registry",
		Scope:     CredentialScopePersonal,
		Type:      CredentialTypeOpaque,
		OwnerID:   user.ID,
		CreatedBy: user.ID,
	})
	if err != nil {
		t.Fatalf("seed credential metadata: %v", err)
	}
	payload, err := json.Marshal(map[string]any{"data": map[string]string{"token": gatewayMySQLCutoverCanary}})
	if err != nil {
		t.Fatalf("encode the credential payload: %v", err)
	}
	credentialVersion, err := store.PutCredentialVersion(CredentialVersionPutRequest{
		CredentialID: credential.ID,
		Payload:      payload,
		CreatedBy:    user.ID,
		Activate:     true,
	})
	if err != nil {
		t.Fatalf("seed credential version: %v", err)
	}

	schedule, err := store.SaveMonitorSchedule(ctx, monitorScheduleRecord{
		ID:              "cutover-schedule",
		Name:            "Cutover schedule",
		Description:     "rehearsal schedule",
		Enabled:         true,
		ScopeMode:       "targets",
		Targets:         []monitorTargetRef{{Cluster: "cutover-cluster", Instance: "cutover-instance"}},
		Tags:            []string{"cutover"},
		Skills:          []string{skill.Slug},
		Prompt:          "inspect the cutover target",
		Command:         "doops inspect",
		IntervalSeconds: 300,
		NextRunAtMs:     baseMs + 300000,
		CreatedBy:       user.ID,
		CreatedAt:       formatTime(base),
		UpdatedAt:       formatTime(base),
	}, false)
	if err != nil {
		t.Fatalf("seed monitor schedule: %v", err)
	}

	run := monitorRunRecord{
		ID:             "cutover-run",
		ScheduleID:     schedule.ID,
		ScheduleName:   schedule.Name,
		TriggerKind:    "manual",
		TriggeredBy:    user.ID,
		Status:         "warning",
		Skills:         []string{skill.Slug},
		Prompt:         "inspect the cutover target",
		Command:        "doops inspect",
		TotalTargets:   1,
		TotalAssets:    1,
		WarningChecks:  1,
		StartedAtMs:    baseMs,
		FinishedAtMs:   baseMs + 1500,
		DurationMs:     1500,
		Summary:        "cutover rehearsal warning",
		MetricsWritten: 2,
	}
	if err := store.CreateMonitorRun(ctx, run); err != nil {
		t.Fatalf("seed monitor run: %v", err)
	}
	artifact, err := json.Marshal(map[string]any{
		"status":  "warning",
		"summary": "cutover rehearsal",
		"metrics": []map[string]any{
			{"key": "cpu_percent", "label": "CPU", "value": 42.5, "unit": "%", "status": "pass"},
			{"key": "memory_percent", "label": "Memory", "value": 61.25, "unit": "%", "status": "warn"},
		},
	})
	if err != nil {
		t.Fatalf("encode the artifact: %v", err)
	}
	item := monitorRunItemRecord{
		RunID:           run.ID,
		Cluster:         "cutover-cluster",
		Instance:        "cutover-instance",
		AssetID:         "cutover-asset-1",
		AssetName:       "Cutover asset",
		AssetKind:       "machine",
		Strategy:        "skill:" + skill.Slug,
		CollectorMethod: "skill",
		DispatchMode:    "skill",
		Status:          "warning",
		Reason:          "rehearsal",
		StartedAtMs:     baseMs,
		FinishedAtMs:    baseMs + 1200,
		DurationMs:      1200,
		Evidence:        map[string]any{"skill_slug": skill.Slug, "exec_mode": "skill"},
		ArtifactFormat:  "json",
		ArtifactJSON:    artifact,
		RawOutput:       "cutover raw output",
	}
	if _, err := store.PersistMonitorRunResults(ctx, run, []monitorRunItemRecord{item}, nil, nil); err != nil {
		t.Fatalf("seed monitor run items: %v", err)
	}

	seed := gatewayMySQLCutoverSeed{
		UserID:            user.ID,
		CredentialID:      credential.ID,
		CredentialVersion: credentialVersion.ID,
		ScheduleID:        schedule.ID,
		RunID:             run.ID,
		SkillSlug:         skill.Slug,
		SkillSHA256:       skill.SHA256,
		MetricKeys:        []string{"cpu_percent", "memory_percent"},
	}
	if err := store.db.QueryRow("SELECT storage_dir FROM platform_skills WHERE slug=?", skill.Slug).Scan(&seed.SkillStorageDir); err != nil {
		t.Fatalf("read the seeded skill storage dir: %v", err)
	}
	if err := store.db.QueryRow("SELECT artifact_dir, artifact_sha256, artifact_bytes FROM monitor_run_items WHERE run_id=?", run.ID).Scan(&seed.ArtifactDir, &seed.ArtifactSHA256, &seed.ArtifactBytes); err != nil {
		t.Fatalf("read the seeded artifact index: %v", err)
	}
	return seed
}

func gatewayMySQLCutoverDestination(t *testing.T, port string) *sql.DB {
	t.Helper()
	admin, err := sql.Open("mysql", "root@tcp(127.0.0.1:"+port+")/?parseTime=true")
	if err != nil {
		t.Fatalf("open MySQL admin connection on port %s: %v", port, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		_ = admin.Close()
		t.Fatalf("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT=%s is set but MySQL is unreachable (fail closed, not skip): %v", port, err)
	}
	name := fmt.Sprintf("doops_cutover_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE \x60"+name+"\x60 CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
		_ = admin.Close()
		t.Fatalf("create disposable MySQL database %s: %v", name, err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS \x60"+name+"\x60")
		_ = admin.Close()
	})
	cfg := mysql.NewConfig()
	cfg.User, cfg.Net, cfg.Addr = "root", "tcp", net.JoinHostPort("127.0.0.1", port)
	cfg.DBName = name
	cfg.ParseTime = true
	cfg.Collation = "utf8mb4_0900_ai_ci"
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		t.Fatalf("invalid local MySQL configuration: %v", err)
	}
	db := sql.OpenDB(connector)
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("connect to rehearsal database %s: %v", name, err)
	}
	return db
}

func gatewayMySQLCutoverDatabaseCollation(t *testing.T, db *sql.DB) string {
	t.Helper()
	var collation string
	if err := db.QueryRow("SELECT DEFAULT_COLLATION_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME=DATABASE()").Scan(&collation); err != nil {
		t.Fatalf("read the rehearsal database collation: %v", err)
	}
	return collation
}

// gatewayMySQLCutoverAssertTable compares every row and every non-secret
// column of one table between the live source and the copied destination.
// Values are compared, not only counts: a copier that writes a default or a
// neighbouring column must fail here with the table, row key and column name.
func gatewayMySQLCutoverAssertTable(t *testing.T, src, dst *sql.DB, table string) {
	t.Helper()
	cols, keys := gatewayMySQLCutoverShape(t, src, table)
	destination := gatewayMySQLCutoverMySQLColumns(t, dst, table)
	var compare []string
	for _, col := range cols {
		if !containsString(destination, col) {
			continue
		}
		if strings.HasSuffix(col, "_hash") || col == "payload_ciphertext" {
			continue
		}
		compare = append(compare, col)
	}
	var keyCols []string
	for _, key := range keys {
		if !containsString(compare, key) {
			t.Fatalf("%s: key column %s is not comparable", table, key)
		}
		keyCols = append(keyCols, key)
	}
	sourceRows, err := gatewayMySQLCutoverRows(src, "sqlite", table, compare, keyCols)
	if err != nil {
		t.Fatalf("%s: read the source rows: %v", table, err)
	}
	copiedRows, err := gatewayMySQLCutoverRows(dst, "mysql", table, compare, keyCols)
	if err != nil {
		t.Fatalf("%s: read the copied rows: %v", table, err)
	}
	if len(sourceRows) != len(copiedRows) {
		t.Fatalf("%s rows = %d, want %d", table, len(copiedRows), len(sourceRows))
	}
	if len(sourceRows) == 0 {
		t.Fatalf("%s: the rehearsal seeded no rows, so the cutover proves nothing", table)
	}
	for key, want := range sourceRows {
		got, ok := copiedRows[key]
		if !ok {
			t.Fatalf("%s: row %q is missing after the cutover", table, key)
		}
		for i, col := range compare {
			if got[i] != want[i] {
				t.Fatalf("%s: row %q column %s = %q, want %q", table, key, col, got[i], want[i])
			}
		}
	}
}

func gatewayMySQLCutoverMySQLColumns(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query("SELECT COLUMN_NAME FROM information_schema.COLUMNS "+
		"WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? ORDER BY ORDINAL_POSITION", table)
	if err != nil {
		t.Fatalf("%s: read the copied shape: %v", table, err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("%s: scan the copied shape: %v", table, err)
		}
		cols = append(cols, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%s: read the copied shape: %v", table, err)
	}
	return cols
}

func gatewayMySQLCutoverShape(t *testing.T, db *sql.DB, table string) ([]string, []string) {
	t.Helper()
	rows, err := db.Query("SELECT name, pk FROM pragma_table_info(?) ORDER BY cid", table)
	if err != nil {
		t.Fatalf("%s: read the source shape: %v", table, err)
	}
	defer rows.Close()
	var cols []string
	keyParts := map[int64]string{}
	for rows.Next() {
		var name string
		var pk int64
		if err := rows.Scan(&name, &pk); err != nil {
			t.Fatalf("%s: scan the source shape: %v", table, err)
		}
		cols = append(cols, name)
		if pk > 0 {
			keyParts[pk] = name
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%s: read the source shape: %v", table, err)
	}
	var keys []string
	for i := int64(1); i <= int64(len(keyParts)); i++ {
		key, ok := keyParts[i]
		if !ok {
			t.Fatalf("%s: primary key is not contiguous", table)
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		t.Fatalf("%s: no primary key to order and identify rows", table)
	}
	return cols, keys
}

func gatewayMySQLCutoverRows(db *sql.DB, engine, table string, cols, keys []string) (map[string][]string, error) {
	quote := func(name string) string {
		if engine == "mysql" {
			return "\x60" + name + "\x60"
		}
		return "\"" + name + "\""
	}
	projection := make([]string, len(cols))
	for i, col := range cols {
		projection[i] = quote(col)
	}
	order := make([]string, len(keys))
	for i, key := range keys {
		order[i] = quote(key)
	}
	query := "SELECT " + strings.Join(projection, ",") + " FROM " + quote(table) + " ORDER BY " + strings.Join(order, ",")
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keyIndex := make([]int, len(keys))
	for i, key := range keys {
		keyIndex[i] = indexOf(cols, key)
	}
	out := map[string][]string{}
	for rows.Next() {
		values := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		rendered := make([]string, len(cols))
		for i, value := range values {
			if value.Valid {
				rendered[i] = value.String
			} else {
				rendered[i] = "NULL"
			}
		}
		var parts []string
		for _, i := range keyIndex {
			parts = append(parts, rendered[i])
		}
		key := strings.Join(parts, "\x00")
		if _, exists := out[key]; exists {
			return nil, fmt.Errorf("duplicate primary key %q", key)
		}
		out[key] = rendered
	}
	return out, rows.Err()
}

func gatewayMySQLCutoverMetricKeys(db *sql.DB) ([]string, error) {
	rows, err := db.Query("SELECT metric_key FROM inspection_metric_points ORDER BY metric_key")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func gatewayMySQLCutoverAssertCanaryAbsent(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query("SELECT TABLE_NAME, COLUMN_NAME FROM information_schema.COLUMNS " +
		"WHERE TABLE_SCHEMA=DATABASE() " +
		"AND DATA_TYPE IN ('char','varchar','tinytext','text','mediumtext','longtext') " +
		"ORDER BY TABLE_NAME, ORDINAL_POSITION")
	if err != nil {
		t.Fatalf("list the copied text columns: %v", err)
	}
	type textColumn struct{ table, column string }
	var columns []textColumn
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatalf("scan the copied text column: %v", err)
		}
		columns = append(columns, textColumn{table, column})
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("list the copied text columns: %v", err)
	}
	for _, column := range columns {
		var matches int
		query := "SELECT COUNT(*) FROM \x60" + column.table + "\x60 WHERE \x60" + column.column + "\x60 LIKE ?"
		if err := db.QueryRow(query, "%"+gatewayMySQLCutoverCanary+"%").Scan(&matches); err != nil {
			t.Fatalf("search %s.%s for the canary: %v", column.table, column.column, err)
		}
		if matches != 0 {
			t.Fatalf("the credential canary appears in %s.%s after the cutover", column.table, column.column)
		}
	}
}

func indexOf(values []string, want string) int {
	for i, value := range values {
		if value == want {
			return i
		}
	}
	return -1
}
