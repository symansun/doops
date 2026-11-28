package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeCOSBackupStore struct {
	mu      sync.Mutex
	bucket  string
	objects map[string]fakeCOSObject
	failKey string
}

type fakeCOSObject struct {
	data         []byte
	contentType  string
	lastModified time.Time
}

func newFakeCOSBackupStore(bucket string) *fakeCOSBackupStore {
	return &fakeCOSBackupStore{
		bucket:  bucket,
		objects: make(map[string]fakeCOSObject),
	}
}

func (f *fakeCOSBackupStore) BucketName() string { return f.bucket }

func (f *fakeCOSBackupStore) UploadFile(_ context.Context, key, contentType, localPath string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failKey != "" && strings.Contains(key, f.failKey) {
		return 0, errors.New("synthetic_cos_upload_failed")
	}
	data, err := os.ReadFile(localPath)
	if err != nil {
		return 0, err
	}
	f.objects[key] = fakeCOSObject{
		data:         append([]byte(nil), data...),
		contentType:  contentType,
		lastModified: time.Now().UTC(),
	}
	return int64(len(data)), nil
}

func (f *fakeCOSBackupStore) ListPrefix(_ context.Context, prefix string) ([]cosBackupObject, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []cosBackupObject
	for key, obj := range f.objects {
		if strings.HasPrefix(key, prefix) {
			out = append(out, cosBackupObject{
				Key:          key,
				Size:         int64(len(obj.data)),
				LastModified: obj.lastModified,
			})
		}
	}
	return out, nil
}

func (f *fakeCOSBackupStore) DeleteObject(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, key)
	return nil
}

func TestCleanCOSBackupPrefix(t *testing.T) {
	for _, good := range []string{"", "backups", "backups/gateway/audit", "backups/gateway/audit/", "a_b-c.d/1"} {
		if _, err := cleanCOSBackupPrefix(good); err != nil {
			t.Errorf("cleanCOSBackupPrefix(%q) returned unexpected err: %v", good, err)
		}
	}
	for _, bad := range []string{"/", "/leading", "../escape", "a/../b", "a\\b", "bad space", "a?b=1"} {
		if _, err := cleanCOSBackupPrefix(bad); err == nil {
			t.Errorf("cleanCOSBackupPrefix(%q) accepted invalid prefix", bad)
		}
	}
}

func TestReclaimTablesAllowlist(t *testing.T) {
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "reclaim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.ReclaimTables(context.Background(), "users; DROP TABLE users"); err == nil {
		t.Fatal("expected non-allowlisted table to be rejected")
	}
	if err := store.ReclaimTables(context.Background(), "audit_events", "agent_resource_audit"); err != nil {
		t.Fatalf("allowlisted SQLite reclaim failed: %v", err)
	}
}

func TestAuditExportFailClosedAndPurgeReclaim(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	store, err := OpenGatewayStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	old := now.Add(-2 * time.Hour)
	cutoff := now.Add(-1 * time.Hour)

	// Seed completed, unresolved, live, and upgrade rows.
	seed := func(action GatewayAction, status string, at time.Time, corr, tail string) int64 {
		id, err := store.StartAudit(AuditEvent{
			Cluster:        "c1",
			Instance:       "i1",
			Action:         action,
			Session:        "s1",
			CommandSummary: "cmd-" + status,
			CorrelationID:  corr,
			StartedAt:      at,
		})
		if err != nil {
			t.Fatal(err)
		}
		if status != "running" && status != "executing" {
			if err := store.FinishAudit(id, AuditFinish{Status: status, Tail: tail, EndedAt: at.Add(time.Second)}); err != nil {
				t.Fatal(err)
			}
		} else if status == "executing" {
			if _, err := store.db.Exec(`UPDATE audit_events SET status='executing' WHERE id=?`, id); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	idDone := seed(ActionExec, "success", old, "", "ok-中文")
	idUnresolved := seed(ActionExec, "outcome_unknown", old, "", "")
	idLive := seed(ActionExec, "executing", old, "", "")
	if _, err := store.db.Exec(`UPDATE audit_events SET status='executing' WHERE id=?`, idLive); err != nil {
		t.Fatal(err)
	}
	idFresh := seed(ActionExec, "success", now, "", "fresh")
	// Attach an agent_resource_audit row to idDone.
	if _, err := store.db.Exec(`INSERT INTO agent_resource_audit(audit_id, manifest_revision, resource_refs_json) VALUES(?, 7, '["machine/m1"]')`, idDone); err != nil {
		t.Fatal(err)
	}
	// Seed 2 platform_events: 1 terminal (should be exported+purged), 1 with pending delivery (must be kept).
	evOld, err := newPlatformEvent("execution.finished", old, "u1", "c1", "i1", "", map[string]any{"ok": true}, "pe-old")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.EnqueuePlatformEvent(context.Background(), evOld); err != nil {
		t.Fatal(err)
	}
	evPending, err := newPlatformEvent("execution.finished", old.Add(time.Minute), "u1", "c1", "i1", "", map[string]any{"pending": true}, "pe-pending")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.EnqueuePlatformEvent(context.Background(), evPending); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO webhook_deliveries(id, endpoint_id, event_id, status, attempt, next_attempt_at_ms, created_at_ms, updated_at_ms) VALUES('del-1','ep-1',?,'pending',0,0,0,0)`, evPending.ID); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	// 1. Fail-closed: COS upload error must abort BEFORE deleting a single row.
	fakeCOS := newFakeCOSBackupStore("test-bucket")
	fakeCOS.failKey = "audit-events-"
	origOpen := openCOSBackupStoreFunc
	openCOSBackupStoreFunc = func(context.Context) (cosBackupStore, error) { return fakeCOS, nil }
	t.Cleanup(func() { openCOSBackupStoreFunc = origOpen })

	var buf bytes.Buffer
	code, err := RunGatewayAuditExport(context.Background(), GatewayAuditExportOptions{
		DBPath:                dbPath,
		OutputDir:             t.TempDir(),
		Before:                &cutoff,
		IncludePlatformEvents: true,
		COSPrefix:             "backups/gateway/audit",
		Purge:                 true,
		Reclaim:               true,
	}, &buf)
	if code != GatewayMySQLCopyExitRuntime || err == nil {
		t.Fatalf("expected COS failure to abort purge: code=%d err=%v", code, err)
	}
	verifyStore, err := OpenGatewayStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	var countAfterFail int
	if err := verifyStore.db.QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&countAfterFail); err != nil || countAfterFail != 4 {
		t.Fatalf("failed COS upload mutated audit_events: count=%d err=%v, want 4", countAfterFail, err)
	}
	_ = verifyStore.Close()

	// 2. Successful COS upload + retention pruning + purge + reclaim.
	fakeCOS.failKey = ""
	fakeCOS.objects["backups/gateway/audit/2025-01-01/old.jsonl.gz"] = fakeCOSObject{
		data:         []byte("expired"),
		lastModified: now.Add(-60 * 24 * time.Hour),
	}
	outDir := t.TempDir()
	buf.Reset()
	code, err = RunGatewayAuditExport(context.Background(), GatewayAuditExportOptions{
		DBPath:                 dbPath,
		OutputDir:              outDir,
		Before:                 &cutoff,
		IncludePlatformEvents:  true,
		COSPrefix:              "backups/gateway/audit",
		Purge:                  true,
		Reclaim:                true,
		DeleteLocalAfterUpload: true,
		KeepCOSDays:            30,
	}, &buf)
	if code != GatewayMySQLCopyExitOK || err != nil {
		t.Fatalf("RunGatewayAuditExport failed: code=%d err=%v out=%s", code, err, buf.String())
	}

	// Local directory should be empty because KeepLocal=0 after verified COS upload.
	entries, err := os.ReadDir(outDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("expected KeepLocal=0 to remove local archive bundle after COS verify, got %d entries", len(entries))
	}
	// Expired COS object must be pruned; new archive + sha256 + platform_events + manifest must exist.
	if _, exists := fakeCOS.objects["backups/gateway/audit/2025-01-01/old.jsonl.gz"]; exists {
		t.Fatal("expected expired COS archive to be pruned")
	}
	var auditGzipBytes []byte
	var manifestBytes []byte
	for k, obj := range fakeCOS.objects {
		if strings.Contains(k, "audit-events-") && strings.HasSuffix(k, ".jsonl.gz") {
			auditGzipBytes = obj.data
		}
		if strings.HasSuffix(k, "/manifest.json") {
			manifestBytes = obj.data
		}
	}
	if len(auditGzipBytes) == 0 || len(manifestBytes) == 0 {
		t.Fatalf("missing uploaded COS objects: keys=%v", fakeCOS.objects)
	}
	// Verify gzipped JSONL contains idDone + embedded _resource_audit and valid UTF-8.
	gzr, err := gzip.NewReader(bytes.NewReader(auditGzipBytes))
	if err != nil {
		t.Fatal(err)
	}
	rawJSONL, err := io.ReadAll(gzr)
	_ = gzr.Close()
	if err != nil {
		t.Fatal(err)
	}
	var exportedRow map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(rawJSONL), &exportedRow); err != nil {
		t.Fatalf("unmarshal exported JSONL: %v (%s)", err, string(rawJSONL))
	}
	raMap, _ := exportedRow["_resource_audit"].(map[string]any)
	if exportedRow["tail"] != "ok-中文" || raMap == nil || !strings.Contains(strings.TrimSpace(raMap["resource_refs_json"].(string)), "machine/m1") {
		t.Fatalf("exported audit JSONL missing expected payload or resource audit: %#v", exportedRow)
	}
	// Verify SHA-256 matches manifest.
	var manifest auditExportManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(auditGzipBytes)
	if manifest.Audit.SHA256 != hex.EncodeToString(sum[:]) || manifest.Audit.Rows != 1 || manifest.PlatformEvents == nil || manifest.PlatformEvents.Rows != 1 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}

	// Verify DB state: idDone purged; idUnresolved, idLive, idFresh, and 1 audit:purge receipt remain.
	verifyStore, err = OpenGatewayStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	var remainingAudit, remainingPE int
	if err := verifyStore.db.QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&remainingAudit); err != nil || remainingAudit != 4 {
		t.Fatalf("remaining audit_events=%d err=%v, want 4 (unresolved+live+fresh+receipt)", remainingAudit, err)
	}
	if err := verifyStore.db.QueryRow(`SELECT COUNT(*) FROM platform_events`).Scan(&remainingPE); err != nil || remainingPE != 1 {
		t.Fatalf("remaining platform_events=%d err=%v, want 1 (pending delivery)", remainingPE, err)
	}
	_ = verifyStore.Close()

	// 3. IncludeUnresolved=true purges idUnresolved while still protecting live executing row.
	buf.Reset()
	code, err = RunGatewayAuditExport(context.Background(), GatewayAuditExportOptions{
		DBPath:            dbPath,
		OutputDir:         t.TempDir(),
		Before:            &cutoff,
		IncludeUnresolved: true,
		Purge:             true,
	}, &buf)
	if code != GatewayMySQLCopyExitOK || err != nil {
		t.Fatalf("RunGatewayAuditExport(include-unresolved) failed: code=%d err=%v", code, err)
	}
	verifyStore, err = OpenGatewayStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer verifyStore.Close()
	var unresolvedLeft, liveLeft int
	_ = verifyStore.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE id=?`, idUnresolved).Scan(&unresolvedLeft)
	_ = verifyStore.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE id=?`, idLive).Scan(&liveLeft)
	if unresolvedLeft != 0 || liveLeft != 1 {
		t.Fatalf("include-unresolved check: unresolvedLeft=%d (want 0), liveLeft=%d (want 1)", unresolvedLeft, liveLeft)
	}
	_ = idFresh
}

func TestStoreBackupSnapshotAndDataDir(t *testing.T) {
	tmpRoot := t.TempDir()
	dbPath := filepath.Join(tmpRoot, "gateway.db")
	store, err := OpenGatewayStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateUser("backup-user"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartAudit(AuditEvent{Action: ActionExec, CommandSummary: "excluded-audit"}); err != nil {
		t.Fatal(err)
	}
	// Seed paired skills/ and artifacts/ files under the SQLite data dir.
	if err := os.MkdirAll(filepath.Join(tmpRoot, "skills", "demo"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpRoot, "skills", "demo", "SKILL.md"), []byte("# Demo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmpRoot, "artifacts", "2026-11-28"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpRoot, "artifacts", "2026-11-28", "report.md"), []byte("ok\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	fakeCOS := newFakeCOSBackupStore("test-bucket")
	fakeCOS.objects["backups/gateway/daily/2025-01-01/stale.tar.gz"] = fakeCOSObject{
		data:         []byte("stale"),
		lastModified: time.Now().UTC().Add(-40 * 24 * time.Hour),
	}
	origOpen := openCOSBackupStoreFunc
	openCOSBackupStoreFunc = func(context.Context) (cosBackupStore, error) { return fakeCOS, nil }
	t.Cleanup(func() { openCOSBackupStoreFunc = origOpen })

	outDir := t.TempDir()
	var buf bytes.Buffer
	code, err := RunGatewayStoreBackup(context.Background(), GatewayStoreBackupOptions{
		DBPath:                 dbPath,
		OutputDir:              outDir,
		COSPrefix:              "backups/gateway/daily",
		ExcludeTables:          []string{"audit_events"},
		DeleteLocalAfterUpload: true,
		KeepCOSDays:            14,
	}, &buf)
	if code != GatewayMySQLCopyExitOK || err != nil {
		t.Fatalf("RunGatewayStoreBackup failed: code=%d err=%v out=%s", code, err, buf.String())
	}

	if _, exists := fakeCOS.objects["backups/gateway/daily/2025-01-01/stale.tar.gz"]; exists {
		t.Fatal("expected stale daily COS object to be pruned")
	}
	var dbTarGz, dataTarGz, manifestJSON []byte
	for k, obj := range fakeCOS.objects {
		switch {
		case strings.Contains(k, "db-tables-") && strings.HasSuffix(k, ".tar.gz"):
			dbTarGz = obj.data
		case strings.Contains(k, "data-dir-") && strings.HasSuffix(k, ".tar.gz"):
			dataTarGz = obj.data
		case strings.HasSuffix(k, "/manifest.json"):
			manifestJSON = obj.data
		}
	}
	if len(dbTarGz) == 0 || len(dataTarGz) == 0 || len(manifestJSON) == 0 {
		t.Fatalf("missing backup artifacts in COS: %v", fakeCOS.objects)
	}
	var manifest storeBackupManifest
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.DataDir == nil || manifest.DataDir.Files < 2 {
		t.Fatalf("expected at least 2 data-dir files in manifest, got %+v", manifest.DataDir)
	}
	// Verify data-dir tar.gz contains our seeded skill and artifact files.
	dgz, err := gzip.NewReader(bytes.NewReader(dataTarGz))
	if err != nil {
		t.Fatal(err)
	}
	dtr := tar.NewReader(dgz)
	foundSkill, foundArtifact := false, false
	for {
		hdr, err := dtr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Name == "skills/demo/SKILL.md" {
			foundSkill = true
		}
		if hdr.Name == "artifacts/2026-11-28/report.md" {
			foundArtifact = true
		}
	}
	_ = dgz.Close()
	if !foundSkill || !foundArtifact {
		t.Fatalf("data-dir tar.gz missing seeded files: foundSkill=%v foundArtifact=%v", foundSkill, foundArtifact)
	}
	// Verify db-tables tar.gz contains users.jsonl and excludes audit_events.jsonl.
	gzr, err := gzip.NewReader(bytes.NewReader(dbTarGz))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gzr)
	foundUsers := false
	foundAudit := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Name == "tables/users.jsonl" {
			data, _ := io.ReadAll(tr)
			if strings.Contains(string(data), "backup-user") {
				foundUsers = true
			}
		}
		if hdr.Name == "tables/audit_events.jsonl" {
			foundAudit = true
		}
	}
	_ = gzr.Close()
	if !foundUsers || foundAudit {
		t.Fatalf("db-tables tar.gz content mismatch: foundUsers=%v foundAudit=%v", foundUsers, foundAudit)
	}
}
