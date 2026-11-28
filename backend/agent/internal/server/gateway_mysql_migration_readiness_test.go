package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestGatewayMySQLMigrationColumnWidthsDDL pins the INT-108 DDL widths without
// needing a live MySQL server: session/digest columns must hold 104-char
// session IDs and 71-char sha256: digests, and inventory_import_attempts.receipt
// must be MEDIUMTEXT rather than 64 KiB TEXT.
func TestGatewayMySQLMigrationColumnWidthsDDL(t *testing.T) {
	ddl := string(gatewayMySQLSchemaSQL)
	for _, target := range gatewayMySQLMigrationWideColumns {
		body := gatewayMySQLDDLBody(t, ddl, target.table)
		re := regexp.MustCompile(`(?mi)^\s*` + regexp.QuoteMeta(target.column) + `\s+([A-Za-z]+)(?:\s*\(\s*([0-9]+)\s*\))?`)
		m := re.FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("%s.%s not found in gateway_mysql_schema.sql", target.table, target.column)
		}
		typ := strings.ToUpper(m[1])
		if target.minChars == gatewayMySQLMediumTextChars {
			if typ != "MEDIUMTEXT" && typ != "LONGTEXT" {
				t.Fatalf("%s.%s declared %s, want MEDIUMTEXT or wider", target.table, target.column, typ)
			}
			continue
		}
		if typ != "VARCHAR" || m[2] == "" {
			t.Fatalf("%s.%s declared %s, want VARCHAR(%d)", target.table, target.column, typ, target.minChars)
		}
		width, err := strconv.ParseInt(m[2], 10, 64)
		if err != nil {
			t.Fatalf("%s.%s invalid width %q: %v", target.table, target.column, m[2], err)
		}
		if width < target.minChars {
			t.Fatalf("%s.%s declared VARCHAR(%d), want >= %d", target.table, target.column, width, target.minChars)
		}
	}
}

// TestSQLiteMigrateNormalizesLegacyAgentTokenUserID proves SQLite startup
// normalizes legacy empty-string user_id on agent tokens to NULL while leaving
// user tokens untouched, so pragma_foreign_key_check and MySQL FK both pass.
func TestSQLiteMigrateNormalizesLegacyAgentTokenUserID(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	store, err := OpenGatewayStore(dbPath)
	if err != nil {
		t.Fatalf("open initial SQLite store: %v", err)
	}
	now := formatTime(time.Now().UTC())
	if _, err := store.db.Exec(`INSERT INTO tokens (id, kind, user_id, name, prefix, hash, cluster, instance, expires_at, revoked_at, created_at, issued_via)
		VALUES ('tok_legacy_agent', 'agent', '', 'legacy agent', 'dgw_agent_0000', 'sha256:`+strings.Repeat("a", 64)+`', 'c1', 'i1', '', '', ?, '')`, now); err != nil {
		t.Fatalf("seed legacy agent token: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close initial SQLite store: %v", err)
	}
	reopened, err := OpenGatewayStore(dbPath)
	if err != nil {
		t.Fatalf("reopen SQLite store: %v", err)
	}
	defer reopened.Close()
	var userID sql.NullString
	if err := reopened.db.QueryRow(`SELECT user_id FROM tokens WHERE id = 'tok_legacy_agent'`).Scan(&userID); err != nil {
		t.Fatalf("read normalized token: %v", err)
	}
	if userID.Valid {
		t.Fatalf("legacy agent token user_id = %q, want NULL", userID.String)
	}
	if err := gatewayCopyCheckSourceForeignKeys(t.Context(), reopened.db); err != nil {
		t.Fatalf("normalized SQLite store still failed FK check: %v", err)
	}
}

// TestGatewayCopyCheckSourceForeignKeysSemantics verifies the exact FK boundary:
// platform credentials (scope='platform', owner_id=”) are exempted because
// MySQL dropped that FK, while broken created_by, personal owner_id, or empty
// tokens.user_id still fail closed with the offending table name.
func TestGatewayCopyCheckSourceForeignKeysSemantics(t *testing.T) {
	store, seed := gatewayMySQLCopyCommandSeedSource(t)
	defer store.Close()
	now := formatTime(time.Now().UTC())

	// Legitimate platform credential with owner_id='' must pass.
	if _, err := store.db.Exec(`INSERT INTO credentials (id, name, scope, owner_id, type, state, created_by, created_at, updated_at, revoked_at)
		VALUES ('cred_plat_1', 'plat-cred', 'platform', '', 'opaque', 'active', ?, ?, ?, '')`, seed.UserID, now, now); err != nil {
		t.Fatalf("seed platform credential: %v", err)
	}
	if err := gatewayCopyCheckSourceForeignKeys(t.Context(), store.db); err != nil {
		t.Fatalf("legitimate platform credential rejected: %v", err)
	}

	// Corrupt personal credential owner_id must still fail closed on credentials.
	if _, err := store.db.Exec(`INSERT INTO credentials (id, name, scope, owner_id, type, state, created_by, created_at, updated_at, revoked_at)
		VALUES ('cred_bad_owner', 'bad-owner', 'personal', 'missing-user', 'opaque', 'active', ?, ?, ?, '')`, seed.UserID, now, now); err != nil {
		t.Fatalf("seed bad personal credential: %v", err)
	}
	err := gatewayCopyCheckSourceForeignKeys(t.Context(), store.db)
	if err == nil || err.Error() != errGatewayCopySourceFK.Error() || gatewayCopyTableName(err) != "credentials" {
		t.Fatalf("bad personal owner_id got err=%v table=%q, want %v on credentials", err, gatewayCopyTableName(err), errGatewayCopySourceFK)
	}
	if _, err := store.db.Exec(`DELETE FROM credentials WHERE id = 'cred_bad_owner'`); err != nil {
		t.Fatal(err)
	}

	// Corrupt created_by on a platform credential must still fail closed.
	if _, err := store.db.Exec(`INSERT INTO credentials (id, name, scope, owner_id, type, state, created_by, created_at, updated_at, revoked_at)
		VALUES ('cred_bad_creator', 'bad-creator', 'platform', '', 'opaque', 'active', 'missing-creator', ?, ?, '')`, now, now); err != nil {
		t.Fatalf("seed bad created_by credential: %v", err)
	}
	err = gatewayCopyCheckSourceForeignKeys(t.Context(), store.db)
	if err == nil || err.Error() != errGatewayCopySourceFK.Error() || gatewayCopyTableName(err) != "credentials" {
		t.Fatalf("bad created_by got err=%v table=%q, want %v on credentials", err, gatewayCopyTableName(err), errGatewayCopySourceFK)
	}
	if _, err := store.db.Exec(`DELETE FROM credentials WHERE id = 'cred_bad_creator'`); err != nil {
		t.Fatal(err)
	}

	// Un-normalized legacy token with user_id='' must fail closed naming tokens.
	if _, err := store.db.Exec(`INSERT INTO tokens (id, kind, user_id, name, prefix, hash, cluster, instance, expires_at, revoked_at, created_at, issued_via)
		VALUES ('tok_bad_empty', 'agent', '', 'bad', 'dgw_agent_1111', 'sha256:`+strings.Repeat("b", 64)+`', 'c1', 'i1', '', '', ?, '')`, now); err != nil {
		t.Fatalf("seed bad empty user_id token: %v", err)
	}
	err = gatewayCopyCheckSourceForeignKeys(t.Context(), store.db)
	if err == nil || err.Error() != errGatewayCopySourceFK.Error() || gatewayCopyTableName(err) != "tokens" {
		t.Fatalf("bad token user_id='' got err=%v table=%q, want %v on tokens", err, gatewayCopyTableName(err), errGatewayCopySourceFK)
	}
}

// TestGatewayMySQLMigrationColumnWidthsUpgradeOnMySQL proves already-installed
// MySQL databases with pre-INT-108 VARCHAR(64)/TEXT widths reject long session/
// digest values with Error 1406, then upgrade cleanly and idempotently on reopen.
func TestGatewayMySQLMigrationColumnWidthsUpgradeOnMySQL(t *testing.T) {
	addr, database := gatewayMySQLHashWidthTestDatabase(t)
	store := openGatewayMySQLHashWidthStore(t, addr, database)

	// Downgrade to pre-INT-108 schema widths.
	for _, stmt := range []string{
		"ALTER TABLE audit_events MODIFY COLUMN session VARCHAR(64) NOT NULL DEFAULT ''",
		"ALTER TABLE audit_events MODIFY COLUMN digest VARCHAR(64) NOT NULL DEFAULT ''",
		"ALTER TABLE audit_events MODIFY COLUMN correlation_id VARCHAR(64) NOT NULL DEFAULT ''",
		"ALTER TABLE detached_tasks MODIFY COLUMN session VARCHAR(64) NOT NULL",
		"ALTER TABLE release_tickets MODIFY COLUMN plan_digest VARCHAR(64) NOT NULL DEFAULT ''",
		"ALTER TABLE inspection_receipts MODIFY COLUMN digest VARCHAR(64) NOT NULL",
		"ALTER TABLE inventory_import_attempts MODIFY COLUMN receipt TEXT NOT NULL",
	} {
		if _, err := store.db.Exec(stmt); err != nil {
			t.Fatalf("downgrade column width (%s): %v", stmt, err)
		}
	}
	longSession := "sess-" + strings.Repeat("a", 99)  // 104 chars, matching live prod max
	longDigest := "sha256:" + strings.Repeat("f", 64) // 71 chars
	_, tooLongErr := store.db.Exec(`INSERT INTO audit_events (user_id, action, status, session, digest, command_summary, error, tail, scope_json, started_at, started_at_ms, ended_at)
		VALUES ('u', 'exec', 'success', ?, ?, 'cmd', '', '', '', '2026-11-27T00:00:00Z', 1, '2026-11-27T00:00:01Z')`, longSession, longDigest)
	if tooLongErr == nil || !strings.Contains(tooLongErr.Error(), "1406") {
		t.Fatalf("legacy VARCHAR(64) audit_events must fail with error 1406, got %v", tooLongErr)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close downgraded store: %v", err)
	}

	upgraded := openGatewayMySQLHashWidthStore(t, addr, database)
	defer upgraded.Close()

	// After startup widening, 104-char session, 71-char digest, and 80 KiB receipt must succeed.
	if _, err := upgraded.db.Exec(`INSERT INTO audit_events (user_id, action, status, session, digest, correlation_id, command_summary, error, tail, scope_json, started_at, started_at_ms, ended_at)
		VALUES ('u', 'exec', 'success', ?, ?, ?, 'cmd', '', '', '', '2026-11-27T00:00:00Z', 1, '2026-11-27T00:00:01Z')`,
		longSession, longDigest, strings.Repeat("c", 80)); err != nil {
		t.Fatalf("insert wide audit row after upgrade: %v", err)
	}
	bigReceipt := strings.Repeat("r", 80*1024)
	if _, err := upgraded.db.Exec(`INSERT INTO inventory_import_attempts (batch_id, receipt) VALUES ('batch-1', ?)`, bigReceipt); err != nil {
		t.Fatalf("insert 80 KiB receipt after upgrade: %v", err)
	}
	// Second widening call must be a complete no-op.
	widened, err := gatewayMySQLWidenMigrationColumns(t.Context(), upgraded.db)
	if err != nil {
		t.Fatalf("second gatewayMySQLWidenMigrationColumns: %v", err)
	}
	if len(widened) != 0 {
		t.Fatalf("second widen was not idempotent, modified %v", widened)
	}
}

// TestGatewayMySQLCopyCommandProductionReadiness covers the four INT-108
// cutover fixes end-to-end against real MySQL 8.4:
//  1. Multi-batch copy (>1,000 rows, spanning multiple 500-row batches + tail)
//     with 104-char session, 71-char sha256: digest, and platform credentials;
//  2. Multi-row legacy absolute artifact_dir & storage_dir rewrite in both
//     dry-run and real copy (no MySQL busy-buffer error);
//  3. Empty-DB dry-run refuses source FK violations with exit 2 without creating
//     any table;
//  4. Destination DB with driver-unsupported collation (utf8mb4_0900_bin) is
//     refused up front before copying.
func TestGatewayMySQLCopyCommandProductionReadiness(t *testing.T) {
	port := gatewayMySQLCopyCommandPort(t)
	store, seed := gatewayMySQLCopyCommandSeedSource(t)
	now := formatTime(time.Now().UTC())

	// Seed a platform credential (owner_id='') and 1,150 wide audit_events rows.
	if _, err := store.db.Exec(`INSERT INTO credentials (id, name, scope, owner_id, type, state, created_by, created_at, updated_at, revoked_at)
		VALUES ('cred_int108_plat', 'plat-int108', 'platform', '', 'opaque', 'active', ?, ?, ?, '')`, seed.UserID, now, now); err != nil {
		t.Fatalf("seed platform credential: %v", err)
	}
	longSession := "session-" + strings.Repeat("x", 96) // 104 chars
	longDigest := "sha256:" + strings.Repeat("d", 64)   // 71 chars
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ins, err := tx.Prepare(`INSERT INTO audit_events (user_id, cluster, instance, action, session, digest, command_summary, status, error, tail, scope_json, started_at, started_at_ms, ended_at)
		VALUES (?, 'c1', 'i1', 'credential:use', ?, ?, ?, 'success', '', '', '', ?, ?, ?)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1150; i++ {
		if _, err := ins.Exec(seed.UserID, longSession, longDigest, fmt.Sprintf("batch row %d", i), now, int64(1000+i), now); err != nil {
			t.Fatalf("seed batch audit row %d: %v", i, err)
		}
	}
	if err := ins.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// Seed 2 legacy absolute artifact_dir rows and 2 legacy absolute storage_dir rows.
	sourceData := t.TempDir()
	for idx, rel := range []string{"2026-11-27/int108-run/item_1", "2026-11-27/int108-run/item_2"} {
		absDir := filepath.Join(sourceData, "artifacts", filepath.FromSlash(rel))
		if err := os.MkdirAll(absDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(absDir, monitorArtifactMarkdownName), []byte(fmt.Sprintf("# item %d\n", idx+1)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(absDir, monitorArtifactRawName), []byte(fmt.Sprintf("raw %d\n", idx+1)), 0o644); err != nil {
			t.Fatal(err)
		}
		size, sha, err := gatewayCopyArtifactDigest(absDir)
		if err != nil {
			t.Fatal(err)
		}
		if idx == 0 {
			if _, err := store.db.Exec(`UPDATE monitor_run_items SET artifact_dir=?, artifact_bytes=?, artifact_sha256=? WHERE run_id=?`, absDir, size, sha, seed.RunID); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err := store.db.Exec(`INSERT INTO monitor_run_items (run_id, cluster, instance, asset_id, asset_name, asset_kind, strategy, collector_method, status, reason, started_at_ms, finished_at_ms, duration_ms, metrics_json, checks_json, evidence_json, artifact_format, artifact_markdown, artifact_json, raw_output, artifact_dir, artifact_bytes, artifact_sha256, artifact_truncated)
				VALUES (?, 'int49-cluster', 'int49-instance', 'int108-asset-2', 'asset 2', 'machine', 'skill:int49-skill', 'skill', 'ok', '', 1, 2, 1, '{}', '[]', '{}', 'markdown', '', '', '', ?, ?, ?, 0)`,
				seed.RunID, absDir, size, sha); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Materialize all indexed platform_skills and turn 2 of them into legacy absolute paths.
	skRows, err := store.db.Query(`SELECT slug, storage_dir FROM platform_skills WHERE storage_dir != '' ORDER BY slug`)
	if err != nil {
		t.Fatal(err)
	}
	type skPair struct{ slug, dir string }
	var skList []skPair
	for skRows.Next() {
		var p skPair
		if err := skRows.Scan(&p.slug, &p.dir); err != nil {
			skRows.Close()
			t.Fatal(err)
		}
		skList = append(skList, p)
	}
	skRows.Close()
	if len(skList) < 2 {
		t.Fatalf("expected >= 2 seeded platform_skills, got %d", len(skList))
	}
	for i, item := range skList {
		abs := filepath.Join(sourceData, filepath.FromSlash(item.dir))
		if err := os.MkdirAll(abs, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(abs, "SKILL.md"), []byte("skill "+item.slug+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			if _, err := store.db.Exec(`UPDATE platform_skills SET storage_dir=? WHERE slug=?`, abs, item.slug); err != nil {
				t.Fatal(err)
			}
		}
	}

	snapshot := gatewayMySQLCopyCommandSnapshot(t, store)
	db, name := gatewayMySQLCopyCommandDestination(t, port)
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", "root@tcp(127.0.0.1:"+port+")/"+name+"?parseTime=true")
	targetData := t.TempDir()

	// Dry-run on empty DB must accept platform credentials + 4 legacy absolute paths.
	code, err, out := gatewayMySQLCopyCommandRun(t, GatewayMySQLCopyOptions{
		Source: snapshot, ConfirmDatabase: name, SourceDataDir: sourceData, TargetDataDir: targetData, DryRun: true,
	})
	if code != GatewayMySQLCopyExitOK || err != nil {
		t.Fatalf("empty dry-run with platform cred + legacy abs paths failed: code=%d err=%v\n%s", code, err, out)
	}
	if !strings.Contains(out, "paths_rewritten=4") {
		t.Fatalf("dry-run missing paths_rewritten=4:\n%s", out)
	}
	if tables := gatewayMySQLCopyCommandTables(t, db); len(tables) != 0 {
		t.Fatalf("dry-run created %d tables", len(tables))
	}

	// Real copy must move all 1,153 audit_events in batches and rewrite all 4 paths cleanly.
	code, err, out = gatewayMySQLCopyCommandRun(t, GatewayMySQLCopyOptions{
		Source: snapshot, ConfirmDatabase: name, SourceDataDir: sourceData, TargetDataDir: targetData,
	})
	if code != GatewayMySQLCopyExitOK || err != nil {
		t.Fatalf("real copy failed: code=%d err=%v\n%s", code, err, out)
	}
	if !strings.Contains(out, "paths_rewritten=4") || !strings.Contains(out, "verified=yes") {
		t.Fatalf("real copy output missing paths_rewritten=4 / verified=yes:\n%s", out)
	}
	var auditCount int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE session=? AND digest=?`, longSession, longDigest).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1150 {
		t.Fatalf("copied wide audit rows = %d, want 1150", auditCount)
	}
	var absRemaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM monitor_run_items WHERE artifact_dir LIKE '/%'`).Scan(&absRemaining); err != nil {
		t.Fatal(err)
	}
	if absRemaining != 0 {
		t.Fatalf("monitor_run_items still has %d absolute artifact_dir rows", absRemaining)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM platform_skills WHERE storage_dir LIKE '/%'`).Scan(&absRemaining); err != nil {
		t.Fatal(err)
	}
	if absRemaining != 0 {
		t.Fatalf("platform_skills still has %d absolute storage_dir rows", absRemaining)
	}

	// Empty-DB dry-run MUST refuse a source FK violation (tokens.user_id='') with exit 2 and 0 tables created.
	badStore, _ := gatewayMySQLCopyCommandSeedSource(t)
	if _, err := badStore.db.Exec(`INSERT INTO tokens (id, kind, user_id, name, prefix, hash, cluster, instance, expires_at, revoked_at, created_at, issued_via)
		VALUES ('tok_orphan_108', 'agent', '', 'bad', 'dgw_agent_ffff', 'sha256:`+strings.Repeat("e", 64)+`', 'c1', 'i1', '', '', ?, '')`, now); err != nil {
		t.Fatal(err)
	}
	badSnap := gatewayMySQLCopyCommandSnapshot(t, badStore)
	badDB, badName := gatewayMySQLCopyCommandDestination(t, port)
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", "root@tcp(127.0.0.1:"+port+")/"+badName+"?parseTime=true")
	dryCode, dryErr, _ := gatewayMySQLCopyCommandRun(t, GatewayMySQLCopyOptions{Source: badSnap, ConfirmDatabase: badName, DryRun: true})
	if dryCode != GatewayMySQLCopyExitUsage || dryErr == nil || !strings.Contains(dryErr.Error(), "mysql_copy_source_foreign_key_failed") || !strings.Contains(dryErr.Error(), "tokens") {
		t.Fatalf("empty dry-run on FK violation got code=%d err=%v, want exit 2 naming tokens", dryCode, dryErr)
	}
	if tables := gatewayMySQLCopyCommandTables(t, badDB); len(tables) != 0 {
		t.Fatalf("failed empty dry-run created %d tables", len(tables))
	}

	// Destination with driver-unsupported utf8mb4_0900_bin must be rejected up front.
	admin, err := sql.Open("mysql", "root@tcp(127.0.0.1:"+port+")/?parseTime=true")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	var raw [4]byte
	_, _ = rand.Read(raw[:])
	badColDB := "doops_int108_bin_" + hex.EncodeToString(raw[:])
	if _, err := admin.ExecContext(context.Background(), "CREATE DATABASE "+badColDB+" CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin"); err != nil {
		t.Fatalf("create utf8mb4_0900_bin db: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+badColDB) })
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", "root@tcp(127.0.0.1:"+port+")/"+badColDB+"?parseTime=true")
	colCode, colErr, _ := gatewayMySQLCopyCommandRun(t, GatewayMySQLCopyOptions{Source: snapshot, ConfirmDatabase: badColDB, DryRun: true})
	if colCode == GatewayMySQLCopyExitOK || colErr == nil || !strings.Contains(colErr.Error(), "gateway_mysql_collation_mismatch") {
		t.Fatalf("utf8mb4_0900_bin destination got code=%d err=%v, want gateway_mysql_collation_mismatch", colCode, colErr)
	}

	// Snapshot lacking monitor_run_items and platform_skills must succeed in
	// data-dir dry-run instead of failing with mysql_copy_read_failed.
	minStore, _ := gatewayMySQLCopyCommandFreshSource(t)
	if _, err := minStore.db.Exec(`DROP TABLE monitor_run_items`); err != nil {
		t.Fatal(err)
	}
	if _, err := minStore.db.Exec(`DROP TABLE platform_skills`); err != nil {
		t.Fatal(err)
	}
	minSnap := gatewayMySQLCopyCommandSnapshot(t, minStore)
	_, minName := gatewayMySQLCopyCommandDestination(t, port)
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", "root@tcp(127.0.0.1:"+port+")/"+minName+"?parseTime=true")
	minCode, minErr, minOut := gatewayMySQLCopyCommandRun(t, GatewayMySQLCopyOptions{
		Source: minSnap, ConfirmDatabase: minName, SourceDataDir: t.TempDir(), TargetDataDir: t.TempDir(), DryRun: true,
	})
	if minCode != GatewayMySQLCopyExitOK || minErr != nil {
		t.Fatalf("data-dir dry-run without monitor_run_items/platform_skills failed: code=%d err=%v\n%s", minCode, minErr, minOut)
	}
	if !strings.Contains(minOut, "artifact_rows=0 skill_rows=0 paths_rewritten=0") {
		t.Fatalf("minimal snapshot data-dir dry-run missing zero counts:\n%s", minOut)
	}
}

func TestAuditUTF8NormalizationRuntimeAndTail(t *testing.T) {
	tail := newAuditTailBuffer(8)
	// "中" is 3 bytes (\xe4\xb8\xad); feeding 10 bytes splits the first rune at
	// the buffer boundary and leaves an embedded invalid byte \xff in the middle.
	tail.WriteString("a中文\xffXY")
	gotTail := tail.String()
	if !strings.Contains(gotTail, "\uFFFDXY") || strings.Contains(gotTail, "\xff") {
		t.Fatalf("auditTailBuffer.String() = %q, want invalid bytes replaced with U+FFFD", gotTail)
	}

	store, _ := gatewayMySQLCopyCommandFreshSource(t)
	id, err := store.StartAudit(AuditEvent{
		Action:         ActionExec,
		Session:        "utf8-runtime-test",
		CommandSummary: "\x88\xb0 Helm 部署 \xe6\x88",
	})
	if err != nil {
		t.Fatalf("StartAudit invalid UTF-8: %v", err)
	}
	if err := store.FinishAudit(id, AuditFinish{
		Status:         "error",
		Error:          "upstream \x88\xb0 failed",
		Tail:           "\x88\xb0 Helm...\n正常中文🚀",
		CommandSummary: "\x88\xb0 cmd",
	}); err != nil {
		t.Fatalf("FinishAudit invalid UTF-8: %v", err)
	}
	var cmd, errText, tailText string
	if err := store.db.QueryRow(`SELECT command_summary, error, tail FROM audit_events WHERE id = ?`, id).Scan(&cmd, &errText, &tailText); err != nil {
		t.Fatal(err)
	}
	if cmd != "\uFFFD cmd" {
		t.Fatalf("command_summary = %q, want %q", cmd, "\uFFFD cmd")
	}
	if errText != "upstream \uFFFD failed" {
		t.Fatalf("error = %q, want %q", errText, "upstream \uFFFD failed")
	}
	if tailText != "\uFFFD Helm...\n正常中文🚀" {
		t.Fatalf("tail = %q, want %q", tailText, "\uFFFD Helm...\n正常中文🚀")
	}
}

func TestGatewayMySQLCopyInvalidUTF8Handling(t *testing.T) {
	port := gatewayMySQLCopyCommandPort(t)
	src, _ := gatewayMySQLCopyCommandFreshSource(t)

	// Insert a historical pre-rune-truncation audit row directly into SQLite with
	// split UTF-8 bytes in non-PK TEXT columns (tail & command_summary).
	badSummary := "\x88\x5f历史命令"
	badTail := "\x88\xb0 Helm...\n正常中文🚀"
	if _, err := src.db.Exec(`INSERT INTO audit_events
		(user_id, token_id, cluster, instance, action, session, command_summary, status, error, tail,
		 bytes_in, bytes_out, started_at, ended_at, scope_json, started_at_ms)
		VALUES ('', '', 'c1', 'i1', 'exec', 's-utf8', ?, 'success', '', ?, 0, 0,
		        '2026-05-11T02:23:39Z', '2026-05-11T02:23:40Z', '{}', 1778466219000)`,
		badSummary, badTail); err != nil {
		t.Fatalf("seed bad UTF-8 audit row: %v", err)
	}
	snapshot := gatewayMySQLCopyCommandSnapshot(t, src)
	db, dbName := gatewayMySQLCopyCommandDestination(t, port)
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", "root@tcp(127.0.0.1:"+port+")/"+dbName+"?parseTime=true")

	code, err, out := gatewayMySQLCopyCommandRun(t, GatewayMySQLCopyOptions{
		Source:          snapshot,
		ConfirmDatabase: dbName,
	})
	if code != GatewayMySQLCopyExitOK || err != nil {
		t.Fatalf("copy with historical invalid UTF-8 failed: code=%d err=%v\n%s", code, err, out)
	}
	if !strings.Contains(out, "utf8-normalized: cells=2 tables=audit_events=2") {
		t.Fatalf("expected explicit utf8-normalized count in output:\n%s", out)
	}
	var gotSummary, gotTail string
	if err := db.QueryRow(`SELECT command_summary, tail FROM audit_events WHERE session = 's-utf8'`).Scan(&gotSummary, &gotTail); err != nil {
		t.Fatalf("read copied audit row: %v", err)
	}
	if gotSummary != "\uFFFD_历史命令" || gotTail != "\uFFFD Helm...\n正常中文🚀" {
		t.Fatalf("copied UTF-8 normalized values = (%q, %q), want (%q, %q)", gotSummary, gotTail, "\uFFFD_历史命令", "\uFFFD Helm...\n正常中文🚀")
	}

	// BLOB columns must stay byte-exact (no UTF-8 repair), and invalid UTF-8 in
	// a TEXT primary key must fail closed.
	blobSrc := gatewayCopyTestSource(t)
	blobDst := gatewayCopyTestDestination(t)
	gatewayCopyTestSchema(t, blobDst)
	rawBlob := []byte{0x88, 0xb0, 0x00, 0xff}
	if _, err := blobSrc.Exec(`UPDATE a_child SET payload = ? WHERE k = 'a'`, rawBlob); err != nil {
		t.Fatal(err)
	}
	rep, err := copyGatewaySQLiteToMySQL(t.Context(), blobSrc, blobDst, false)
	if err != nil {
		t.Fatalf("copy binary BLOB failed: %v", err)
	}
	if rep.UTF8RepairedCells != 0 {
		t.Fatalf("BLOB copy reported UTF8RepairedCells=%d, want 0", rep.UTF8RepairedCells)
	}
	var gotBlob []byte
	if err := blobDst.QueryRow(`SELECT payload FROM a_child WHERE k = 'a'`).Scan(&gotBlob); err != nil {
		t.Fatal(err)
	}
	if string(gotBlob) != string(rawBlob) {
		t.Fatalf("BLOB corrupted: got %x want %x", gotBlob, rawBlob)
	}

	pkSrc := gatewayCopyTestSource(t)
	pkDst := gatewayCopyTestDestination(t)
	gatewayCopyTestSchema(t, pkDst)
	if _, err := pkSrc.Exec(`UPDATE a_child SET k = ? WHERE k = 'a'`, "\x88\xb0-bad-pk"); err != nil {
		t.Fatal(err)
	}
	if _, err := copyGatewaySQLiteToMySQL(t.Context(), pkSrc, pkDst, false); !errors.Is(err, errGatewayCopyInvalidUTF8PK) {
		t.Fatalf("invalid UTF-8 primary key got err=%v, want %v", err, errGatewayCopyInvalidUTF8PK)
	}
}
