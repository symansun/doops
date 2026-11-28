package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type auditScrubSeed struct {
	Action  string
	Summary string
	Error   string
}

type auditScrubSnapshotRow struct {
	ID      int64
	Action  string
	Summary string
	Error   string
}

func openAuditScrubMySQL(t *testing.T) *sql.DB {
	t.Helper()
	port := os.Getenv("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT")
	if port == "" {
		t.Skip("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT is not set")
	}
	adminDSN := fmt.Sprintf("root@tcp(127.0.0.1:%s)/?parseTime=true", port)
	admin, err := sql.Open("mysql", adminDSN)
	if err != nil {
		t.Fatalf("open mysql admin connection: %v", err)
	}
	if err := admin.Ping(); err != nil {
		admin.Close()
		t.Fatalf("mysql at port %s is not reachable: %v", port, err)
	}
	name := fmt.Sprintf("audit_scrub_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + name + " CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"); err != nil {
		admin.Close()
		t.Fatalf("create scratch database: %v", err)
	}
	var collation string
	if err := admin.QueryRow("SELECT DEFAULT_COLLATION_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = ?", name).Scan(&collation); err != nil {
		admin.Close()
		t.Fatalf("read scratch database collation: %v", err)
	}
	if collation != "utf8mb4_bin" {
		admin.Close()
		t.Fatalf("scratch database collation = %q, want utf8mb4_bin", collation)
	}
	dbDSN := fmt.Sprintf("root@tcp(127.0.0.1:%s)/%s?parseTime=true", port, name)
	oldDSN, hadDSN := os.LookupEnv("DOOPS_GATEWAY_MYSQL_DSN")
	if err := os.Setenv("DOOPS_GATEWAY_MYSQL_DSN", dbDSN); err != nil {
		admin.Close()
		t.Fatalf("set DOOPS_GATEWAY_MYSQL_DSN: %v", err)
	}
	t.Cleanup(func() {
		if hadDSN {
			os.Setenv("DOOPS_GATEWAY_MYSQL_DSN", oldDSN)
		} else {
			os.Unsetenv("DOOPS_GATEWAY_MYSQL_DSN")
		}
	})
	db, err := sql.Open("mysql", dbDSN)
	if err != nil {
		admin.Close()
		t.Fatalf("open scratch database: %v", err)
	}
	if _, err := installGatewayMySQLSchema(context.Background(), db); err != nil {
		db.Close()
		admin.Close()
		t.Fatalf("install mysql schema: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		if _, err := admin.Exec("DROP DATABASE " + name); err != nil {
			t.Errorf("drop scratch database %s: %v", name, err)
		}
		admin.Close()
	})
	return db
}

func auditScrubSeedRow(t *testing.T, db *sql.DB, seed auditScrubSeed) int64 {
	t.Helper()
	res, err := db.Exec("INSERT INTO audit_events "+
		"(user_id, action, status, command_summary, error, tail, scope_json, started_at, started_at_ms, ended_at) "+
		"VALUES ('tester', ?, 'success', ?, ?, '', '', '2026-01-01T00:00:00Z', 1, '2026-01-01T00:00:01Z')",
		seed.Action, seed.Summary, seed.Error)
	if err != nil {
		t.Fatalf("seed audit row: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed audit row id: %v", err)
	}
	return id
}

func auditScrubSnapshot(t *testing.T, db *sql.DB) []auditScrubSnapshotRow {
	t.Helper()
	rows, err := db.Query("SELECT id, action, command_summary, error FROM audit_events WHERE action <> 'audit:scrub' ORDER BY id")
	if err != nil {
		t.Fatalf("snapshot audit rows: %v", err)
	}
	defer rows.Close()
	var out []auditScrubSnapshotRow
	for rows.Next() {
		var row auditScrubSnapshotRow
		if err := rows.Scan(&row.ID, &row.Action, &row.Summary, &row.Error); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("snapshot audit rows: %v", err)
	}
	return out
}

func auditScrubContentHash(t *testing.T, db *sql.DB) string {
	t.Helper()
	h := sha256.New()
	for _, row := range auditScrubSnapshot(t, db) {
		fmt.Fprintf(h, "%d\x00%s\x00%s\x00%s\x00", row.ID, row.Action, row.Summary, row.Error)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func auditScrubChecksum(t *testing.T, db *sql.DB) string {
	t.Helper()
	var table, checksum string
	if err := db.QueryRow("CHECKSUM TABLE audit_events").Scan(&table, &checksum); err != nil {
		t.Fatalf("checksum audit_events: %v", err)
	}
	return checksum
}

func auditScrubSeedEight(t *testing.T, db *sql.DB) {
	t.Helper()
	seeds := []auditScrubSeed{
		{Action: "proxy", Summary: "GET /v1/proxy/https/example.com/api?token=alpha&x=1"},
		{Action: "proxy", Summary: "GET /v1/proxy/https/example.com/health"},
		{Action: "proxy", Summary: "GET /v1/proxy/https/example.com/api?token=beta", Error: "upstream error: Get \"https://example.com/api?token=beta\": dial tcp 10.0.0.1:443: timeout"},
		{Action: "pull", Summary: "POST /v1/git/pull?repo=private"},
		{Action: "push", Summary: "POST /v1/git/push"},
		{Action: "exec", Summary: "POST /v1/exec?cmd=secret"},
		{Action: "proxy", Summary: "GET /v1/foo", Error: "https://user:pass@example.com/private?q=1#frag"},
		{Action: "proxy", Summary: "GET /v1/foo", Error: "see https://example.com/path?a=b). done"},
		{Action: "proxy", Summary: "GET /v1/already?<redacted>"},
	}
	for _, seed := range seeds {
		auditScrubSeedRow(t, db, seed)
	}
}

func TestAuditScrubMySQLEndToEnd(t *testing.T) {
	db := openAuditScrubMySQL(t)
	auditScrubSeedEight(t, db)
	ctx := context.Background()
	dir := t.TempDir()
	backup := filepath.Join(dir, "preimage.jsonl")

	before := auditScrubSnapshot(t, db)
	hashBefore := auditScrubContentHash(t, db)
	checksumBefore := auditScrubChecksum(t, db)

	var dry bytes.Buffer
	code, err := RunAuditScrub(ctx, AuditScrubOptions{
		ConfirmDatabase: auditScrubDatabaseName(t, db),
		BackupPath:      backup,
		Actions:         "proxy,pull,push",
		DryRun:          true,
	}, &dry)
	if err != nil || code != AuditScrubExitSuccess {
		t.Fatalf("dry-run code=%d err=%v output=%s", code, err, dry.String())
	}
	if checksumBefore != auditScrubChecksum(t, db) {
		t.Fatalf("dry-run changed audit_events checksum")
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("dry-run created backup file: err=%v", err)
	}
	for _, want := range []string{
		"command_summary candidates=3 rewrite=3",
		"error            candidates=3 rewrite=3",
		"estimated rows=5 batches=1",
		"dry-run wrote nothing",
	} {
		if !strings.Contains(dry.String(), want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, dry.String())
		}
	}

	var real bytes.Buffer
	code, err = RunAuditScrub(ctx, AuditScrubOptions{
		ConfirmDatabase: auditScrubDatabaseName(t, db),
		BackupPath:      backup,
		Actions:         "proxy,pull,push",
	}, &real)
	if err != nil || code != AuditScrubExitSuccess {
		t.Fatalf("real run code=%d err=%v output=%s", code, err, real.String())
	}
	if !strings.Contains(real.String(), "updated=6 batches=1 deleted=0") {
		t.Fatalf("real run output missing summary: %s", real.String())
	}
	info, err := os.Stat(backup)
	if err != nil {
		t.Fatalf("stat backup: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("backup mode = %o, want 0600", info.Mode().Perm())
	}
	rawBackup, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	backupLines := strings.Split(strings.TrimSpace(string(rawBackup)), "\n")
	if len(backupLines) != 6 {
		t.Fatalf("backup lines = %d, want 6", len(backupLines))
	}
	beforeByID := map[int64]auditScrubSnapshotRow{}
	for _, row := range before {
		beforeByID[row.ID] = row
	}
	seenBackup := map[string]bool{}
	for _, line := range backupLines {
		var entry auditScrubBackupEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decode backup line %q: %v", line, err)
		}
		row, ok := beforeByID[entry.ID]
		if !ok {
			t.Fatalf("backup names unknown row %d", entry.ID)
		}
		want := row.Summary
		if entry.Column == "error" {
			want = row.Error
		}
		if entry.Column != "command_summary" && entry.Column != "error" {
			t.Fatalf("backup column = %q", entry.Column)
		}
		if entry.Old != want {
			t.Fatalf("backup row %d %s old = %q, want %q", entry.ID, entry.Column, entry.Old, want)
		}
		seenBackup[fmt.Sprintf("%d/%s", entry.ID, entry.Column)] = true
	}
	if len(seenBackup) != 6 {
		t.Fatalf("backup distinct entries = %d, want 6", len(seenBackup))
	}

	after := auditScrubSnapshot(t, db)
	if len(after) != len(before) {
		t.Fatalf("row count changed from %d to %d", len(before), len(after))
	}
	wantSummary := map[int64]string{
		before[0].ID: "GET /v1/proxy/https/example.com/api?<redacted>",
		before[1].ID: "GET /v1/proxy/https/example.com/health",
		before[2].ID: "GET /v1/proxy/https/example.com/api?<redacted>",
		before[3].ID: "POST /v1/git/pull?<redacted>",
		before[4].ID: "POST /v1/git/push",
		before[5].ID: "POST /v1/exec?cmd=secret",
		before[6].ID: "GET /v1/foo",
		before[7].ID: "GET /v1/foo",
		before[8].ID: "GET /v1/already?<redacted>",
	}
	wantError := map[int64]string{}
	for _, row := range before {
		wantError[row.ID] = row.Error
	}
	wantError[before[2].ID] = "upstream error: Get \"https://example.com/api\": dial tcp 10.0.0.1:443: timeout"
	wantError[before[6].ID] = "https://example.com/private"
	// The shared runtime scrubber treats the closing ")." as the URL query
	// delimiter, so only the space-delimited prose survives.
	wantError[before[7].ID] = "see https://example.com/path done"
	for _, row := range after {
		if want, ok := wantSummary[row.ID]; ok && row.Summary != want {
			t.Errorf("row %d summary = %q, want %q", row.ID, row.Summary, want)
		}
		if want, ok := wantError[row.ID]; ok && row.Error != want {
			t.Errorf("row %d error = %q, want %q", row.ID, row.Error, want)
		}
	}

	var status, tail, receiptError string
	if err := db.QueryRow("SELECT status, tail, error FROM audit_events WHERE action = 'audit:scrub' ORDER BY id DESC LIMIT 1").Scan(&status, &tail, &receiptError); err != nil {
		t.Fatalf("read audit scrub receipt: %v", err)
	}
	if status != "success" || receiptError != "" {
		t.Fatalf("receipt status=%q error=%q", status, receiptError)
	}
	var counts map[string]int64
	if err := json.Unmarshal([]byte(tail), &counts); err != nil {
		t.Fatalf("decode receipt tail %q: %v", tail, err)
	}
	if counts["updated"] != 6 || counts["batches"] != 1 || counts["deleted"] != 0 {
		t.Fatalf("receipt tail = %v", counts)
	}

	var again bytes.Buffer
	code, err = RunAuditScrub(ctx, AuditScrubOptions{
		ConfirmDatabase: auditScrubDatabaseName(t, db),
		BackupPath:      backup,
		Actions:         "proxy,pull,push",
		DryRun:          true,
	}, &again)
	if err != nil || code != AuditScrubExitSuccess {
		t.Fatalf("second dry-run code=%d err=%v output=%s", code, err, again.String())
	}
	for _, want := range []string{
		"command_summary candidates=0 rewrite=0",
		"error            candidates=0 rewrite=0",
		"estimated rows=0 batches=0",
	} {
		if !strings.Contains(again.String(), want) {
			t.Fatalf("second dry-run output missing %q:\n%s", want, again.String())
		}
	}

	var rollback bytes.Buffer
	code, err = RunAuditScrub(ctx, AuditScrubOptions{
		ConfirmDatabase: auditScrubDatabaseName(t, db),
		BackupPath:      backup,
		Rollback:        true,
	}, &rollback)
	if err != nil || code != AuditScrubExitSuccess {
		t.Fatalf("rollback code=%d err=%v output=%s", code, err, rollback.String())
	}
	restored := auditScrubSnapshot(t, db)
	if len(restored) != len(before) {
		t.Fatalf("rollback row count = %d, want %d", len(restored), len(before))
	}
	for i := range before {
		if restored[i] != before[i] {
			t.Fatalf("rollback row %d = %+v, want %+v", i, restored[i], before[i])
		}
	}
	if got := auditScrubContentHash(t, db); got != hashBefore {
		t.Fatalf("rollback content hash = %s, want %s", got, hashBefore)
	}
}

func auditScrubDatabaseName(t *testing.T, db *sql.DB) string {
	t.Helper()
	var name string
	if err := db.QueryRow("SELECT DATABASE()").Scan(&name); err != nil {
		t.Fatalf("read current database: %v", err)
	}
	return name
}

func TestAuditScrubMySQLConfirmDatabaseMismatch(t *testing.T) {
	db := openAuditScrubMySQL(t)
	auditScrubSeedRow(t, db, auditScrubSeed{Action: "proxy", Summary: "GET /v1/foo?q=1"})
	before := auditScrubChecksum(t, db)
	backup := filepath.Join(t.TempDir(), "preimage.jsonl")
	code, err := RunAuditScrub(context.Background(), AuditScrubOptions{
		ConfirmDatabase: "some_other_database",
		BackupPath:      backup,
		Actions:         "proxy,pull,push",
	}, &bytes.Buffer{})
	if code != AuditScrubExitUsage {
		t.Fatalf("code = %d, want %d (err=%v)", code, AuditScrubExitUsage, err)
	}
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("error = %v, want a confirm-database mismatch", err)
	}
	if before != auditScrubChecksum(t, db) {
		t.Fatalf("confirm mismatch changed audit_events")
	}
	if _, statErr := os.Stat(backup); !os.IsNotExist(statErr) {
		t.Fatalf("confirm mismatch created backup: err=%v", statErr)
	}
}

func TestAuditScrubMySQLFailClosed(t *testing.T) {
	db := openAuditScrubMySQL(t)
	auditScrubSeedRow(t, db, auditScrubSeed{Action: "proxy", Summary: "GET /v1/good?q=1"})
	badID := auditScrubSeedRow(t, db, auditScrubSeed{Action: "proxy", Summary: "GET /v1/%zz?q=1"})
	before := auditScrubSnapshot(t, db)
	backup := filepath.Join(t.TempDir(), "preimage.jsonl")
	code, err := RunAuditScrub(context.Background(), AuditScrubOptions{
		ConfirmDatabase: auditScrubDatabaseName(t, db),
		BackupPath:      backup,
		Actions:         "proxy,pull,push",
	}, &bytes.Buffer{})
	if code != AuditScrubExitFailure {
		t.Fatalf("code = %d, want %d (err=%v)", code, AuditScrubExitFailure, err)
	}
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("audit row %d column command_summary", badID)) {
		t.Fatalf("error = %v, want row %d column command_summary", err, badID)
	}
	after := auditScrubSnapshot(t, db)
	if len(after) != len(before) {
		t.Fatalf("fail-closed row count = %d, want %d", len(after), len(before))
	}
	for i := range before {
		if after[i] != before[i] {
			t.Fatalf("fail-closed changed row %+v to %+v", before[i], after[i])
		}
	}
	var status, errorText string
	if err := db.QueryRow("SELECT status, error FROM audit_events WHERE action = 'audit:scrub' ORDER BY id DESC LIMIT 1").Scan(&status, &errorText); err != nil {
		t.Fatalf("read failure receipt: %v", err)
	}
	if status != "error" || errorText != "audit_scrub_failed" {
		t.Fatalf("failure receipt status=%q error=%q", status, errorText)
	}
}

func TestAuditScrubMySQLFailClosedErrorURL(t *testing.T) {
	db := openAuditScrubMySQL(t)
	badID := auditScrubSeedRow(t, db, auditScrubSeed{
		Action:  "proxy",
		Summary: "GET /v1/good",
		Error:   "dial http://[::1: connection refused",
	})
	before := auditScrubSnapshot(t, db)
	backup := filepath.Join(t.TempDir(), "preimage.jsonl")
	code, err := RunAuditScrub(context.Background(), AuditScrubOptions{
		ConfirmDatabase: auditScrubDatabaseName(t, db),
		BackupPath:      backup,
		Actions:         "proxy,pull,push",
	}, &bytes.Buffer{})
	if code != AuditScrubExitFailure {
		t.Fatalf("code = %d, want %d (err=%v)", code, AuditScrubExitFailure, err)
	}
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("audit row %d column error", badID)) {
		t.Fatalf("error = %v, want row %d column error", err, badID)
	}
	after := auditScrubSnapshot(t, db)
	if len(after) != len(before) || after[0] != before[0] {
		t.Fatalf("fail-closed error URL changed rows: before=%+v after=%+v", before, after)
	}
	var status, errorText string
	if err := db.QueryRow("SELECT status, error FROM audit_events WHERE action = 'audit:scrub' ORDER BY id DESC LIMIT 1").Scan(&status, &errorText); err != nil {
		t.Fatalf("read failure receipt: %v", err)
	}
	if status != "error" || errorText != "audit_scrub_failed" {
		t.Fatalf("failure receipt status=%q error=%q", status, errorText)
	}
}

func TestAuditScrubRejectsWildcardAndMissingBackup(t *testing.T) {
	code, err := RunAuditScrub(context.Background(), AuditScrubOptions{
		ConfirmDatabase: "ignored",
		BackupPath:      "ignored",
		Actions:         "*",
	}, &bytes.Buffer{})
	if code != AuditScrubExitUsage || err == nil || !strings.Contains(err.Error(), "unsupported action") {
		t.Fatalf("wildcard code=%d err=%v", code, err)
	}
	code, err = RunAuditScrub(context.Background(), AuditScrubOptions{
		ConfirmDatabase: "ignored",
		BackupPath:      "",
		Actions:         "proxy",
	}, &bytes.Buffer{})
	if code != AuditScrubExitUsage || err == nil || !strings.Contains(err.Error(), "-backup is required") {
		t.Fatalf("missing backup code=%d err=%v", code, err)
	}
}

// Historical rows also hold non-http scheme targets. scrubbing must treat them
// like http(s): userinfo and query carry credentials, so both go, and the
// scheme/host/port/path survive.
func TestAuditScrubMySQLOtherSchemeURL(t *testing.T) {
	db := openAuditScrubMySQL(t)
	const (
		secret   = "password"
		rowError = "dial postgres://user:" + secret + "@db:5432/app?" + secret + "=canary"
		wantErr  = "dial postgres://db:5432/app"
		upError  = "dial HTTPS://user:" + secret + "@example.com/v1?" + secret + "=canary"
		wantUp   = "dial https://example.com/v1"
	)
	id := auditScrubSeedRow(t, db, auditScrubSeed{Action: "proxy", Summary: "GET /v1/foo", Error: rowError})
	upID := auditScrubSeedRow(t, db, auditScrubSeed{Action: "proxy", Summary: "GET /v1/up", Error: upError})
	proseID := auditScrubSeedRow(t, db, auditScrubSeed{Action: "proxy", Summary: "GET /v1/plain", Error: "connection refused by upstream"})
	backup := filepath.Join(t.TempDir(), "preimage.jsonl")
	checksumBefore := auditScrubChecksum(t, db)

	var dry bytes.Buffer
	code, err := RunAuditScrub(context.Background(), AuditScrubOptions{
		ConfirmDatabase: auditScrubDatabaseName(t, db),
		BackupPath:      backup,
		Actions:         "proxy,pull,push",
		DryRun:          true,
	}, &dry)
	if err != nil || code != AuditScrubExitSuccess {
		t.Fatalf("dry-run code=%d err=%v output=%s", code, err, dry.String())
	}
	if !strings.Contains(dry.String(), "error            candidates=2 rewrite=2") {
		t.Fatalf("dry-run did not see both postgres and uppercase HTTPS rows:\n%s", dry.String())
	}
	if checksumBefore != auditScrubChecksum(t, db) {
		t.Fatalf("dry-run changed audit_events checksum")
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("dry-run created backup file: err=%v", err)
	}

	var real bytes.Buffer
	code, err = RunAuditScrub(context.Background(), AuditScrubOptions{
		ConfirmDatabase: auditScrubDatabaseName(t, db),
		BackupPath:      backup,
		Actions:         "proxy,pull,push",
	}, &real)
	if err != nil || code != AuditScrubExitSuccess {
		t.Fatalf("real run code=%d err=%v output=%s", code, err, real.String())
	}
	if !strings.Contains(real.String(), "updated=2 batches=1 deleted=0") {
		t.Fatalf("real run output missing summary: %s", real.String())
	}

	rawBackup, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	var entry auditScrubBackupEntry
	lines := strings.Split(strings.TrimSpace(string(rawBackup)), "\n")
	if len(lines) != 2 {
		t.Fatalf("backup lines = %d, want 2: %s", len(lines), rawBackup)
	}
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("decode backup line %q: %v", lines[0], err)
	}
	if entry.ID != id || entry.Column != "error" || entry.Old != rowError {
		t.Fatalf("backup entry = %+v, want id=%d column=error old=%q", entry, id, rowError)
	}
	var upGotErr string
	if err := db.QueryRow("SELECT error FROM audit_events WHERE id = ?", upID).Scan(&upGotErr); err != nil {
		t.Fatalf("read scrubbed uppercase-scheme row: %v", err)
	}
	if upGotErr != wantUp {
		t.Fatalf("scrubbed uppercase-scheme error = %q, want %q", upGotErr, wantUp)
	}

	var summary, gotErr, tail string
	if err := db.QueryRow("SELECT command_summary, error, tail FROM audit_events WHERE id = ?", id).Scan(&summary, &gotErr, &tail); err != nil {
		t.Fatalf("read scrubbed row: %v", err)
	}
	if gotErr != wantErr {
		t.Fatalf("scrubbed error = %q, want %q", gotErr, wantErr)
	}
	if strings.Contains(gotErr, secret) {
		t.Fatalf("audit row %d error still carries the credential: %q", id, gotErr)
	}
	if summary != "GET /v1/foo" || tail != "" {
		t.Fatalf("scrub changed other columns: summary=%q tail=%q", summary, tail)
	}

	var proseErr string
	if err := db.QueryRow("SELECT error FROM audit_events WHERE id = ?", proseID).Scan(&proseErr); err != nil {
		t.Fatalf("read prose row: %v", err)
	}
	if proseErr != "connection refused by upstream" {
		t.Fatalf("prose error was rewritten to %q", proseErr)
	}

	var secondDry bytes.Buffer
	code, err = RunAuditScrub(context.Background(), AuditScrubOptions{
		ConfirmDatabase: auditScrubDatabaseName(t, db),
		BackupPath:      filepath.Join(t.TempDir(), "second.jsonl"),
		Actions:         "proxy,pull,push",
		DryRun:          true,
	}, &secondDry)
	if err != nil || code != AuditScrubExitSuccess {
		t.Fatalf("second dry-run code=%d err=%v output=%s", code, err, secondDry.String())
	}
	if !strings.Contains(secondDry.String(), "error            candidates=0 rewrite=0") {
		t.Fatalf("second dry-run still proposes the row:\n%s", secondDry.String())
	}
}

// auditScrubErrorSQLPattern is the only way the offline command can see a
// historical row, and it must keep the same scheme definition as the runtime
// scrubber. If the two drift apart, a postgres:// row is either invisible
// (nothing is cleaned) or cleaned under a rule the command never selected.
// The real-MySQL test above proves the REGEXP works; this test pins the literal
// so a revert to https?:// fails here as well as there.
func TestAuditScrubErrorPatterns(t *testing.T) {
	if got, want := auditScrubErrorSQLPattern, "[A-Za-z][A-Za-z0-9+.-]*://[^[:space:]\"<>]+"; got != want {
		t.Fatalf("auditScrubErrorSQLPattern = %q, want %q", got, want)
	}
	if strings.Contains(auditScrubErrorSQLPattern, "(?i)") {
		t.Fatalf("auditScrubErrorSQLPattern must not use MySQL 8-only (?i) flag (fails on MySQL 5.7 with ERROR 1139): %q", auditScrubErrorSQLPattern)
	}
	for _, test := range []struct {
		name string
		row  string
		want bool
	}{
		{name: "postgres userinfo and query", row: "dial postgres://user:password@db:5432/app?password=x", want: true},
		{name: "uppercase postgres scheme", row: "dial POSTGRES://user:password@db:5432/app?password=x", want: true},
		{name: "redis password only", row: "dial redis://:password@cache:6379/0", want: true},
		{name: "mysql userinfo and query", row: "dial mysql://user:password@db:3306/app?charset=utf8", want: true},
		{name: "http url", row: "Get \"https://example.com/a?b=c\": EOF", want: true},
		{name: "uppercase https url", row: "Get \"HTTPS://example.com/a?b=c\": EOF", want: true},
		{name: "plain prose", row: "connection refused by upstream", want: false},
		{name: "userinfo without a scheme", row: "dial user:password@host:5432: refused", want: false},
		{name: "scp-like git target", row: "git@host:repo: permission denied", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := auditScrubErrorURLPattern.MatchString(test.row); got != test.want {
				t.Fatalf("auditScrubErrorURLPattern.MatchString(%q) = %t, want %t", test.row, got, test.want)
			}
		})
	}
}
