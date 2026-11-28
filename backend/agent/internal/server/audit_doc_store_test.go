package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditStorageModeRejectsUnknownValue(t *testing.T) {
	t.Setenv("DOOPS_AUDIT_STORAGE", "bogus")
	_, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err == nil || !strings.Contains(err.Error(), "gateway_audit_storage_invalid") {
		t.Fatalf("expected gateway_audit_storage_invalid, got err=%v", err)
	}
}

func TestAuditDocStoreFileFirstWriteHydrateFallbackAndPrune(t *testing.T) {
	t.Setenv("DOOPS_AUDIT_STORAGE", "file")
	rootDir := t.TempDir()
	dbPath := filepath.Join(rootDir, "gateway.db")
	store, err := OpenGatewayStore(dbPath)
	if err != nil {
		t.Fatalf("OpenGatewayStore(file): %v", err)
	}
	if store.AuditStorageMode() != "file" || store.AuditDocDir() != filepath.Join(rootDir, "audit") {
		t.Fatalf("unexpected audit storage mode=%q dir=%q", store.AuditStorageMode(), store.AuditDocDir())
	}

	now := time.Now().UTC()
	old := now.Add(-48 * time.Hour)
	cutoff := now.Add(-24 * time.Hour)
	largeTail := strings.Repeat("审计日志正文-中文输出-", 200) // > 5 KiB

	// 1. Ordinary action externalizes tail to <dataDir>/audit/docs/ and appends
	// full JSONL document to <dataDir>/audit/records/.
	id1, err := store.StartAudit(AuditEvent{
		UserID:         "u-alice",
		Cluster:        "prod-c1",
		Instance:       "node-1",
		Action:         ActionExec,
		Session:        "sess-doc-1",
		CommandSummary: "kubectl get pods",
		StartedAt:      old,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishAudit(id1, AuditFinish{
		Status:   "success",
		Tail:     largeTail,
		BytesOut: int64(len(largeTail)),
		EndedAt:  old.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("FinishAudit(file): %v", err)
	}

	// Verify DB column holds compact $audit_doc pointer, NOT the 5 KiB body.
	var rawDBTail string
	if err := store.db.QueryRow(`SELECT tail FROM audit_events WHERE id = ?`, id1).Scan(&rawDBTail); err != nil {
		t.Fatal(err)
	}
	ptr, isPtr := parseAuditDocPointer(rawDBTail)
	if !isPtr || len(rawDBTail) > 180 {
		t.Fatalf("expected compact $audit_doc pointer in DB, got len=%d raw=%q", len(rawDBTail), rawDBTail)
	}
	tailDocPath := filepath.Join(rootDir, "audit", "docs", filepath.FromSlash(ptr.Doc))
	tailBytes, err := os.ReadFile(tailDocPath)
	if err != nil || string(tailBytes) != largeTail {
		t.Fatalf("tail doc mismatch: err=%v len=%d want=%d", err, len(tailBytes), len(largeTail))
	}

	// 2. agent:upgrade keeps its small JSON tail inline so SQL upgrade evidence rules keep working.
	idUpgrade, err := store.StartAudit(AuditEvent{
		UserID:        "u-alice",
		Cluster:       "prod-c1",
		Instance:      "node-1",
		Action:        ActionAgentUpgrade,
		CorrelationID: "upg-1",
		StartedAt:     old.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	upgradeTail := `{"result":{"job":"job-1","outcome":"succeeded"}}`
	if err := store.FinishAudit(idUpgrade, AuditFinish{Status: "success", Tail: upgradeTail, EndedAt: old.Add(2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	var rawUpgradeTail string
	if err := store.db.QueryRow(`SELECT tail FROM audit_events WHERE id = ?`, idUpgrade).Scan(&rawUpgradeTail); err != nil {
		t.Fatal(err)
	}
	if rawUpgradeTail != upgradeTail {
		t.Fatalf("agent:upgrade tail must remain inline, got %q", rawUpgradeTail)
	}

	// 3. Simulate a tx-committed row (finishAuditWith) that bypassed FinishAudit:
	// audit export must backfill it into records/YYYY-MM-DD.jsonl before DB purge.
	idTx, err := store.StartAudit(AuditEvent{
		UserID:         "u-bob",
		Cluster:        "prod-c2",
		Instance:       "node-2",
		Action:         GatewayAction("credential:create"),
		Session:        "cred-sess",
		CommandSummary: "create cred",
		StartedAt:      old.Add(3 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := finishAuditWith(tx, idTx, AuditFinish{Status: "success", EndedAt: old.Add(4 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// 4. Fresh hot row stays in DB after 24h cutoff.
	idFresh, err := store.StartAudit(AuditEvent{
		UserID:         "u-alice",
		Cluster:        "prod-c1",
		Instance:       "node-1",
		Action:         ActionExec,
		Session:        "sess-fresh",
		CommandSummary: "uptime",
		StartedAt:      now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishAudit(idFresh, AuditFinish{Status: "success", Tail: "up 10 days", EndedAt: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}

	// Verify ListAuditFiltered hydrates the $audit_doc tail transparently.
	listed, err := store.ListAuditFiltered(AuditFilter{Cluster: "prod-c1", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	foundHydrated := false
	for _, ev := range listed {
		if ev.ID == id1 && ev.Tail == largeTail {
			foundHydrated = true
		}
	}
	if !foundHydrated {
		t.Fatalf("ListAuditFiltered failed to hydrate $audit_doc tail for id=%d", id1)
	}

	// Seed an expired 40-day-old record file to verify -keep-doc-days pruning.
	expiredDate := now.AddDate(0, 0, -40).Format("2006-01-02")
	expiredRecordFile := filepath.Join(rootDir, "audit", "records", expiredDate+".jsonl")
	if err := os.WriteFile(expiredRecordFile, []byte(`{"id":999999,"action":"exec","status":"success","started_at_ms":1,"started_at":"2020-01-01T00:00:00Z"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	// 5. Run audit export with -purge, -reclaim, -keep-doc-days 30.
	outDir := t.TempDir()
	var buf bytes.Buffer
	code, err := RunGatewayAuditExport(context.Background(), GatewayAuditExportOptions{
		DBPath:      dbPath,
		OutputDir:   outDir,
		Before:      &cutoff,
		Purge:       true,
		Reclaim:     true,
		KeepDocDays: 30,
	}, &buf)
	if code != GatewayMySQLCopyExitOK || err != nil {
		t.Fatalf("RunGatewayAuditExport(file mode) failed: code=%d err=%v out=%s", code, err, buf.String())
	}

	// Verify exported .jsonl.gz contains hydrated full text (NOT $audit_doc pointer!).
	entries, err := os.ReadDir(outDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected 1 export bundle, got %d (err=%v)", len(entries), err)
	}
	bundleFiles, _ := filepath.Glob(filepath.Join(outDir, entries[0].Name(), "audit-events-*.jsonl.gz"))
	if len(bundleFiles) != 1 {
		t.Fatalf("expected 1 audit archive in bundle, got %v", bundleFiles)
	}
	f, err := os.Open(bundleFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	rawExport, err := io.ReadAll(gz)
	_ = gz.Close()
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rawExport), largeTail) || strings.Contains(string(rawExport), `"$audit_doc"`) {
		t.Fatalf("exported archive should contain hydrated tail text and no raw $audit_doc pointer")
	}

	// Purged row's standalone tail file should be cleaned up, and expired 40-day record pruned.
	if _, err := os.Stat(tailDocPath); !os.IsNotExist(err) {
		t.Fatalf("expected purged tail doc %s to be deleted", tailDocPath)
	}
	if _, err := os.Stat(expiredRecordFile); !os.IsNotExist(err) {
		t.Fatalf("expected expired record file %s to be pruned by KeepDocDays=30", expiredRecordFile)
	}

	// 6. Reopen store: DB only holds idFresh + audit:purge receipt, yet ListAuditFiltered
	// seamlessly returns BOTH the DB hot rows AND the purged records (including id1 with
	// full largeTail AND tx-committed idTx!) from <dataDir>/audit/records/!
	store2, err := OpenGatewayStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()

	var dbCount int
	if err := store2.db.QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&dbCount); err != nil || dbCount != 2 {
		t.Fatalf("expected only 2 hot rows in DB (fresh + receipt), got %d (err=%v)", dbCount, err)
	}

	allEvents, err := store2.ListAuditFiltered(AuditFilter{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[int64]AuditEvent, len(allEvents))
	for _, ev := range allEvents {
		byID[ev.ID] = ev
	}
	if got, ok := byID[id1]; !ok || got.Tail != largeTail {
		t.Fatalf("document fallback missing purged id1 with full tail: ok=%v tailLen=%d", ok, len(got.Tail))
	}
	if got, ok := byID[idTx]; !ok || got.UserID != "u-bob" || got.Action != "credential:create" {
		t.Fatalf("document fallback missing backfilled tx-committed idTx: ok=%v got=%+v", ok, got)
	}
	if got, ok := byID[idFresh]; !ok || got.Tail != "up 10 days" {
		t.Fatalf("missing fresh hot row idFresh: ok=%v got=%+v", ok, got)
	}

	// Verify filters work on document-backed rows (Cluster + UserID + BeforeID).
	bobOnly, err := store2.ListAuditFiltered(AuditFilter{UserID: "u-bob", Cluster: "prod-c2", Limit: 10})
	if err != nil || len(bobOnly) != 1 || bobOnly[0].ID != idTx {
		t.Fatalf("filtered document fallback mismatch: len=%d err=%v events=%+v", len(bobOnly), err, bobOnly)
	}
	// Verify valid JSON lines in today's/old record file.
	recPath := filepath.Join(rootDir, "audit", "records", old.UTC().Format("2006-01-02")+".jsonl")
	rawLines, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(rawLines)), "\n") {
		var check auditDocRecord
		if err := json.Unmarshal([]byte(line), &check); err != nil {
			t.Fatalf("invalid JSONL record line: %v", err)
		}
	}
}
