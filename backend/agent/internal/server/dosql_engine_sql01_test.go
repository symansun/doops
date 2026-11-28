package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/doops/agent/api"
	"github.com/user/doops/agent/internal/dosql/inspect"
	"github.com/user/doops/agent/internal/dosql/migrate"
	"github.com/user/doops/agent/internal/dosql/recorder"
)

func TestSQL01MySQLDSNPreservesTLSAndParams(t *testing.T) {
	target, err := doSQLMySQLTarget("app", "mysql://u:synthetic@rds.test:3306/app?tls=true&charset=utf8mb4&parseTime=true", "", "", "")
	if err != nil {
		t.Fatalf("mysql uri failed: %v", err)
	}
	if !strings.Contains(target.DSN, "tls=true") || !strings.Contains(target.DSN, "charset=utf8mb4") || !strings.Contains(target.DSN, "parseTime=true") {
		t.Fatalf("mysql DSN dropped query params: %s", target.DSN)
	}

	jdbc, err := doSQLMySQLTarget("app", "jdbc:mysql://rds.test:3306/app?useSSL=true&serverTimezone=UTC&characterEncoding=utf8mb4", "jdbc:mysql://", "u", "synthetic")
	if err != nil {
		t.Fatalf("jdbc mysql failed: %v", err)
	}
	if !strings.Contains(jdbc.DSN, "tls=true") || !strings.Contains(jdbc.DSN, "charset=utf8mb4") {
		t.Fatalf("jdbc mysql DSN dropped TLS/charset: %s", jdbc.DSN)
	}

	if _, err := doSQLMySQLTarget("app", "mysql://u:synthetic@rds.test:3306/app?bogusParam=1", "", "", ""); err == nil || !strings.Contains(err.Error(), "dosql_connection_uri_param_unsupported") {
		t.Fatalf("expected unknown mysql param rejection, got %v", err)
	}
}

func TestSQL01DirectPromptPolicyLintAndOverride(t *testing.T) {
	binding := api.AgentCredentialBinding{Name: "orders-db", Use: string(CredentialUseDatabase)}
	target := doSQLTarget{Engine: "mysql", Driver: "mysql", Database: "orders", Address: "db.test:3306"}

	// Lint action reports unbounded UPDATE error and missing PK warning.
	out, err := handleDirectDoSQLAction(t.Context(), "s1", api.AgentPromptParams{
		SQLAction: "lint",
		SQL:       "CREATE TABLE t (price FLOAT); UPDATE t SET price = 1;",
	}, binding, target)
	if err != nil {
		t.Fatalf("lint action failed: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("lint json invalid: %v", err)
	}
	pol, _ := parsed["policy"].(map[string]interface{})
	if passed, _ := pol["passed"].(bool); passed {
		t.Fatalf("expected lint failure on unbounded UPDATE, got %s", out)
	}
}

func TestSQL01DoagentAllowedToolsIncludesExecOnApply(t *testing.T) {
	askTools := doagentAllowedTools(false, "dosql", true)
	if !containsDoSQLString(askTools, "mcp_dosql_read") || containsDoSQLString(askTools, "mcp_dosql_exec") {
		t.Fatalf("ask tools mismatch: %v", askTools)
	}
	applyTools := doagentAllowedTools(true, "dosql", true)
	if !containsDoSQLString(applyTools, "mcp_dosql_read") || !containsDoSQLString(applyTools, "mcp_dosql_exec") {
		t.Fatalf("apply tools must include both mcp_dosql_read and mcp_dosql_exec: %v", applyTools)
	}
}

func TestSQL04MCPParamsStrictDecodeAndSchemaParity(t *testing.T) {
	// Verify every new operation's JSON payload decodes cleanly under DisallowUnknownFields
	// and matches properties exposed in doSQLMCPToolDefinition.
	readDef := doSQLMCPToolDefinition(doSQLReadMCPToolName, doSQLReadOperations, "read")
	execDef := doSQLMCPToolDefinition(doSQLExecMCPToolName, doSQLExecOperations, "exec")
	readProps := readDef["inputSchema"].(map[string]interface{})["properties"].(map[string]interface{})
	execProps := execDef["inputSchema"].(map[string]interface{})["properties"].(map[string]interface{})

	payloads := []struct {
		name  string
		props map[string]interface{}
		body  string
	}{
		{"diff", readProps, `{"operation":"diff","credential_ref":"db","engine":"mysql","database_name":"orders","diff_from":"v1","diff_to":"live","tables":["orders"]}`},
		{"datadiff", readProps, `{"operation":"datadiff","credential_ref":"db","engine":"mysql","database_name":"orders","source_database":"staging","table":"sys_dict","key_column":"id","columns":["id","code"],"chunk_size":5000,"generate_fix":true}`},
		{"status", readProps, `{"operation":"status","credential_ref":"db","engine":"mysql","database_name":"orders","migrations":"[]"}`},
		{"migrate", execProps, `{"operation":"migrate","credential_ref":"db","engine":"mysql","database_name":"orders","migrations":"[]","baseline":1,"resume":true,"release_tag":"v2.0"}`},
		{"backfill", execProps, `{"operation":"backfill","credential_ref":"db","engine":"mysql","database_name":"orders","table":"orders","key_column":"id","set":"status_v2=status","where":"status_v2 IS NULL","batch_size":500,"throttle":"50ms","start_after":100,"dry_run":true}`},
	}
	for _, tc := range payloads {
		t.Run(tc.name, func(t *testing.T) {
			var raw map[string]interface{}
			if err := json.Unmarshal([]byte(tc.body), &raw); err != nil {
				t.Fatal(err)
			}
			for k := range raw {
				if _, ok := tc.props[k]; !ok {
					t.Fatalf("property %q missing from MCP inputSchema for %s", k, tc.name)
				}
			}
			var params doSQLParams
			dec := json.NewDecoder(strings.NewReader(tc.body))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&params); err != nil {
				t.Fatalf("doSQLParams rejected valid MCP payload for %s: %v", tc.name, err)
			}
		})
	}
}

func TestSQL05UnifiedStateDirAndBackwardCompatibility(t *testing.T) {
	wsParent := t.TempDir()
	wsRoot := filepath.Join(wsParent, "ws")
	t.Setenv("DOOPS_WORKSPACE_ROOT", wsRoot)
	t.Setenv("DOOPS_DOSQL_DRIVER_DIR", "")
	target := doSQLTarget{Engine: "mysql", Driver: "mysql", Database: "orders", Address: "db.test:3306"}

	// 1. Default (unset DOOPS_DOSQL_STATE_DIR): preserves legacy paths beside workspaceRoot().
	t.Setenv("DOOPS_DOSQL_STATE_DIR", "")
	journalPath, err := doSQLVersionJournalPath(target)
	if err != nil {
		t.Fatalf("legacy journal path failed: %v", err)
	}
	wantLegacyJournalDir := filepath.Join(wsParent, ".doops-dosql-versions", "v2")
	if filepath.Dir(journalPath) != wantLegacyJournalDir {
		t.Fatalf("legacy journal dir = %q, want %q", filepath.Dir(journalPath), wantLegacyJournalDir)
	}
	driverDir, err := doSQLExternalDriverDir()
	if err != nil {
		t.Fatalf("legacy driver dir failed: %v", err)
	}
	if want := filepath.Join(wsParent, ".doops-dosql-drivers"); driverDir != want {
		t.Fatalf("legacy driver dir = %q, want %q", driverDir, want)
	}

	// 2. Unified absolute DOOPS_DOSQL_STATE_DIR: consolidates versions, history, snapshots, and drivers.
	stateDir := filepath.Join(t.TempDir(), "dosql-state")
	t.Setenv("DOOPS_DOSQL_STATE_DIR", stateDir)
	unifiedJournal, err := doSQLVersionJournalPath(target)
	if err != nil {
		t.Fatalf("unified journal path failed: %v", err)
	}
	wantUnifiedJournalDir := filepath.Join(stateDir, "versions", "v2")
	if filepath.Dir(unifiedJournal) != wantUnifiedJournalDir {
		t.Fatalf("unified journal dir = %q, want %q", filepath.Dir(unifiedJournal), wantUnifiedJournalDir)
	}
	recStore, err := doSQLRecorderStore(target)
	if err != nil {
		t.Fatalf("unified recorder store failed: %v", err)
	}
	if !strings.HasPrefix(recStore.Path, filepath.Join(stateDir, "history")+string(filepath.Separator)) {
		t.Fatalf("unified recorder path = %q, want under %q", recStore.Path, filepath.Join(stateDir, "history"))
	}
	snapStore, err := doSQLSnapshotStore(target)
	if err != nil {
		t.Fatalf("unified snapshot store failed: %v", err)
	}
	if !strings.HasPrefix(snapStore.Dir, filepath.Join(stateDir, "snapshots")+string(filepath.Separator)) {
		t.Fatalf("unified snapshot dir = %q, want under %q", snapStore.Dir, filepath.Join(stateDir, "snapshots"))
	}
	unifiedDrivers, err := doSQLExternalDriverDir()
	if err != nil {
		t.Fatalf("unified driver dir failed: %v", err)
	}
	if want := filepath.Join(stateDir, "drivers"); unifiedDrivers != want {
		t.Fatalf("unified driver dir = %q, want %q", unifiedDrivers, want)
	}
	// Explicit DOOPS_DOSQL_DRIVER_DIR still overrides the unified drivers subdir.
	customDrivers := filepath.Join(t.TempDir(), "custom-drivers")
	t.Setenv("DOOPS_DOSQL_DRIVER_DIR", customDrivers)
	if got, err := doSQLExternalDriverDir(); err != nil || got != customDrivers {
		t.Fatalf("driver dir override = %q err=%v, want %q", got, err, customDrivers)
	}
	t.Setenv("DOOPS_DOSQL_DRIVER_DIR", "")

	// 3. Relative DOOPS_DOSQL_STATE_DIR fails closed everywhere.
	t.Setenv("DOOPS_DOSQL_STATE_DIR", "relative/dosql-state")
	for name, call := range map[string]func() error{
		"journal": func() error { _, err := doSQLVersionJournalPath(target); return err },
		"history": func() error { _, err := doSQLRecorderStore(target); return err },
		"snap":    func() error { _, err := doSQLSnapshotStore(target); return err },
		"drivers": func() error { _, err := doSQLExternalDriverDir(); return err },
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "dosql_state_dir_invalid") {
			t.Fatalf("%s: expected dosql_state_dir_invalid, got %v", name, err)
		}
	}
}

func TestSQL06HistoryPackIsolationMigratePolicyJournalAndCanonicalIdentity(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "dosql-state")
	wsRoot := filepath.Join(t.TempDir(), "ws")
	t.Setenv("DOOPS_DOSQL_STATE_DIR", stateDir)
	t.Setenv("DOOPS_WORKSPACE_ROOT", wsRoot)
	if err := os.MkdirAll(filepath.Join(wsRoot, "s1"), 0700); err != nil {
		t.Fatal(err)
	}

	// 1. Canonical identity matches across versions, history, and snapshots regardless of case/host order.
	tA := doSQLTarget{Engine: "MYSQL", Driver: "mysql", Database: "orders", Address: "DB-B.TEST:3306,db-a.test:3306"}
	tB := doSQLTarget{Engine: "mysql", Driver: "mysql", Database: "orders", Address: "db-a.test:3306,db-b.test:3306"}
	jA, err := doSQLVersionJournalPath(tA)
	if err != nil {
		t.Fatal(err)
	}
	jB, err := doSQLVersionJournalPath(tB)
	if err != nil {
		t.Fatal(err)
	}
	rA, _ := doSQLRecorderStore(tA)
	rB, _ := doSQLRecorderStore(tB)
	sA, _ := doSQLSnapshotStore(tA)
	sB, _ := doSQLSnapshotStore(tB)
	jHash := strings.TrimSuffix(filepath.Base(jA), ".jsonl")
	rHash := strings.TrimSuffix(filepath.Base(rA.Path), ".jsonl")
	sHash := filepath.Base(sA.Dir)
	if jA != jB || rA.Path != rB.Path || sA.Dir != sB.Dir || jHash != rHash || jHash != sHash {
		t.Fatalf("canonical identity hashes diverged: journal=%s history=%s snap=%s", jHash, rHash, sHash)
	}

	// 2. Real SQLite DB with explicit host:port exercises version journal, policy gate,
	// history sanitization, non-polluting dry_run/backfill, and non-colliding snapshots.
	target := newDoSQLDirectTestTarget(t)
	target.Address = "127.0.0.1:13306"
	binding := api.AgentCredentialBinding{Name: "orders-db", Use: string(CredentialUseDatabase)}

	// Simulate an apply (version 1) + snapshot v000001.json.
	jPath, err := doSQLVersionJournalPath(target)
	if err != nil {
		t.Fatal(err)
	}
	v1, err := beginDoSQLVersion(jPath, "dbop_apply_1", binding.Name, "fp1", []string{"UPDATE orders SET status='paid' WHERE id=1"})
	if err != nil || v1 != 1 {
		t.Fatalf("begin v1 failed: v=%d err=%v", v1, err)
	}
	if err := endDoSQLVersion(jPath, v1, "dbop_apply_1", binding.Name, "fp1", doSQLDirectResult{Status: "verified", Committed: true}, nil); err != nil {
		t.Fatal(err)
	}
	snapStore, _ := doSQLSnapshotStore(target)
	if _, err := snapStore.Save(v1, &inspect.SchemaIR{Engine: "sqlite", Database: "orders"}); err != nil {
		t.Fatal(err)
	}

	// Run backfill (both dry-run and real) via handleDirectDoSQLAction; verify evidence file + Kind=backfill.
	bfOut, err := handleDirectDoSQLAction(t.Context(), "s1", api.AgentPromptParams{
		SQLAction: "backfill", SQLBackfillTable: "orders", SQLBackfillKey: "id",
		SQLBackfillSet: "status = 'bf'", SQLBackfillWhere: "id = 1", SQLDryRun: true,
	}, binding, target)
	if err != nil || !strings.Contains(bfOut, "backfill_dry_run") || !strings.Contains(bfOut, "evidence_path") {
		t.Fatalf("backfill dry-run action failed: %s err=%v", bfOut, err)
	}

	// Record a real apply + a dry_run into recStore (with a secret canary in a comment) and verify pack filtering.
	recStore, _ := doSQLRecorderStore(target)
	_ = recStore.Append(
		recorder.StatementRecord{SessionID: "s1", OperationID: "op_dry", Engine: "sqlite", Database: "orders", Mode: "dry_run", Kind: "dml", Statement: "UPDATE orders SET status='dry' WHERE id=1", Status: "succeeded"},
		recorder.StatementRecord{SessionID: "s2", OperationID: "op_real", Engine: "sqlite", Database: "orders", Mode: "apply", Kind: "dml", Statement: sanitizeSecretText("UPDATE orders SET status='real' WHERE id=1 -- mysql://u:synthetic-secret-canary@db.test:3306/orders"), Status: "succeeded"},
	)

	// History with SQLAllSessions=true returns records across s1 and s2, with canary redacted.
	histJSON, err := handleDirectDoSQLAction(t.Context(), "s1", api.AgentPromptParams{SQLAction: "history", SQLAllSessions: true}, binding, target)
	if err != nil || strings.Contains(histJSON, "synthetic-secret-canary") {
		t.Fatalf("history leaked secret or failed: %v", err)
	}
	var histResp struct {
		Records []recorder.StatementRecord `json:"records"`
	}
	if err := json.Unmarshal([]byte(histJSON), &histResp); err != nil || len(histResp.Records) < 3 {
		t.Fatalf("expected >=3 cross-session records, got %s", histJSON)
	}
	packed, err := migrate.PackFromHistory(t.TempDir(), "clean pack", "sqlite", histResp.Records)
	if err != nil {
		t.Fatalf("PackFromHistory failed: %v", err)
	}
	if !strings.Contains(packed.Content, "status='real'") || strings.Contains(packed.Content, "dry") || strings.Contains(packed.Content, "BACKFILL") {
		t.Fatalf("packed migration polluted by dry_run or backfill:\n%s", packed.Content)
	}

	// 3. Migrate enforces Policy-as-Code (rejects unbounded UPDATE without override).
	unsafeManifest, _ := json.Marshal([]migrate.MigrationFile{{
		Version: 1, VersionTag: "V001", Description: "unsafe", FileName: "V001__unsafe.sql",
		Checksum: "sha256:unsafe", Content: "UPDATE orders SET status = 'oops';",
	}})
	if _, err := handleDirectDoSQLAction(t.Context(), "s1", api.AgentPromptParams{
		SQLAction: "migrate", SQLMigrations: string(unsafeManifest),
	}, binding, target); err == nil || !strings.Contains(err.Error(), "dosql_policy_rejected") {
		t.Fatalf("expected migrate to enforce policy and reject unbounded UPDATE, got %v", err)
	}

	// Safe migration gets journal version 2 (never collides with apply version 1!) and writes evidence.
	safeManifest, _ := json.Marshal([]migrate.MigrationFile{{
		Version: 1, VersionTag: "V001", Description: "add_col", FileName: "V001__add_col.sql",
		Checksum: "sha256:safe", Content: "ALTER TABLE orders ADD COLUMN v2 TEXT;",
	}})
	migOut, err := handleDirectDoSQLAction(t.Context(), "s1", api.AgentPromptParams{
		SQLAction: "migrate", SQLMigrations: string(safeManifest), SQLReleaseTag: "v2.0.0",
	}, binding, target)
	if err != nil {
		t.Fatalf("safe migrate failed: %v", err)
	}
	var migResp struct {
		Version      int64  `json:"version"`
		EvidencePath string `json:"evidence_path"`
	}
	if err := json.Unmarshal([]byte(migOut), &migResp); err != nil || migResp.Version != 2 || migResp.EvidencePath == "" {
		t.Fatalf("expected migrate journal version=2 + evidence_path, got %s", migOut)
	}
	// Version journal now lists both v1 (apply) and v2 (migrate).
	vers, err := listDoSQLVersions(jPath)
	if err != nil || len(vers) != 2 || vers[1].Version != 2 || vers[1].Status != "verified" {
		t.Fatalf("expected 2 journal versions (apply + migrate), got %+v err=%v", vers, err)
	}
}
