package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackCLIHistoryToDirAndLoadVerify(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "migrations")
	historyJSON := []byte(`{
		"engine": "mysql",
		"records": [
			{"mode": "query", "kind": "read", "statement": "SELECT 1", "stmt_hash": "h0", "status": "succeeded"},
			{"mode": "dry_run", "kind": "dml", "statement": "UPDATE orders SET v2 = 9 WHERE id = 1", "stmt_hash": "hdry", "status": "succeeded"},
			{"mode": "backfill", "kind": "backfill", "statement": "BACKFILL orders SET v2 = 1 WHERE id > 0", "stmt_hash": "hbf", "status": "succeeded"},
			{"mode": "migrate", "kind": "ddl", "statement": "ALTER TABLE orders ADD COLUMN old_mig INT", "stmt_hash": "hmig", "status": "succeeded"},
			{"mode": "apply", "kind": "ddl", "statement": "ALTER TABLE orders ADD COLUMN v2 INT", "stmt_hash": "h1", "status": "succeeded"},
			{"mode": "apply", "kind": "ddl", "statement": "ALTER TABLE orders ADD COLUMN v2 INT", "stmt_hash": "h1", "status": "succeeded"},
			{"mode": "apply", "kind": "dml", "statement": "UPDATE orders SET v2 = 1 WHERE id = 1", "stmt_hash": "h2", "status": "succeeded"}
		]
	}`)

	path, count, err := packCLIHistoryToDir(dir, "add_v2", "", historyJSON)
	if err != nil || count != 2 || filepath.Base(path) != "V001__add_v2.sql" {
		t.Fatalf("pack failed: path=%s count=%d err=%v", path, count, err)
	}
	packedRaw, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(packedRaw), "BACKFILL") || strings.Contains(string(packedRaw), "old_mig") || strings.Contains(string(packedRaw), "= 9") {
		t.Fatalf("CLI pack leaked non-apply statements:\n%s", string(packedRaw))
	}
	manifest, err := loadCLIMigrationDirJSON(dir, true)
	if err != nil || !strings.Contains(manifest, "V001__add_v2.sql") {
		t.Fatalf("loadCLIMigrationDirJSON failed: %s err=%v", manifest, err)
	}
	// Tampering fails dosql.sum check.
	if err := os.WriteFile(path, []byte("DROP TABLE orders;\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCLIMigrationDirJSON(dir, false); err == nil || !strings.Contains(err.Error(), "dosql_migrate_sum_mismatch") {
		t.Fatalf("expected sum mismatch, got %v", err)
	}
}

func TestInitDoSQLProjectWorkspaceAndAutoDiscovery(t *testing.T) {
	proj := t.TempDir()
	created, skipped, err := initDoSQLProjectWorkspace(proj)
	if err != nil {
		t.Fatalf("initDoSQLProjectWorkspace failed: %v", err)
	}
	if len(created) != 5 || len(skipped) != 0 {
		t.Fatalf("first init expected 5 created / 0 skipped, got %d / %d", len(created), len(skipped))
	}

	policyPath := filepath.Join(proj, ".dosql", "policy.yaml")
	customPolicy := "schema: dosql.policy.v1\ndml:\n  forbid_truncate: error\n"
	if err := os.WriteFile(policyPath, []byte(customPolicy), 0644); err != nil {
		t.Fatal(err)
	}

	// Re-running init must be idempotent and never overwrite customized policy.yaml.
	created2, skipped2, err := initDoSQLProjectWorkspace(proj)
	if err != nil {
		t.Fatalf("second init failed: %v", err)
	}
	if len(created2) != 0 || len(skipped2) != 5 {
		t.Fatalf("second init expected 0 created / 5 skipped, got %d / %d", len(created2), len(skipped2))
	}
	gotPolicy, err := os.ReadFile(policyPath)
	if err != nil || string(gotPolicy) != customPolicy {
		t.Fatalf("second init overwrote policy.yaml: %q err=%v", string(gotPolicy), err)
	}

	// Auto-discovery resolves .dosql/policy.yaml and .dosql/migrations when in project dir.
	t.Chdir(proj)
	if got := resolveDefaultPolicyFile(""); got != filepath.FromSlash(".dosql/policy.yaml") {
		t.Fatalf("resolveDefaultPolicyFile(\"\") = %q", got)
	}
	if got := resolveDefaultPolicyFile("/custom/policy.yaml"); got != "/custom/policy.yaml" {
		t.Fatalf("explicit policy should win, got %q", got)
	}
	if got := resolveDefaultMigrationsDir(""); got != filepath.FromSlash(".dosql/migrations") {
		t.Fatalf("resolveDefaultMigrationsDir(\"\") = %q", got)
	}
	args := map[string]interface{}{}
	if err := attachSQLGovernanceArgs(args, "", "", "", "", nil, ""); err != nil {
		t.Fatalf("attachSQLGovernanceArgs auto-discovery failed: %v", err)
	}
	if args["sql_policy"] != customPolicy {
		t.Fatalf("expected auto-discovered policy in args, got %#v", args["sql_policy"])
	}

	// Offline CLI runSQLCommand --mode init works without --target.
	proj2 := t.TempDir()
	if err := runSQLCommand([]string{"--mode", "init", "--project-dir", proj2}, "", nil, nil, nil, false); err != nil {
		t.Fatalf("runSQLCommand --mode init failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(proj2, ".dosql", "policy.yaml")); err != nil {
		t.Fatalf("runSQLCommand --mode init did not create policy.yaml: %v", err)
	}
}
