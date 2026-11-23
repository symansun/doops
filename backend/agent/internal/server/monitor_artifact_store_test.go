package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// SF-03 contract: inspection artifacts are file-first. The payload lands under
// <dbDir>/artifacts/<date>/<run_id>/item_<id>/, SQLite keeps only the light
// index, the detail API reads the full content back, oversized payloads are
// truncated and flagged, secrets never reach disk, and rows without files still
// read their legacy text columns.
func openMonitorArtifactStore(t *testing.T) *GatewayStore {
	t.Helper()
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("open gateway store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func monitorArtifactAdminAuth(t *testing.T, store *GatewayStore) TokenAuth {
	t.Helper()
	user, err := store.CreateUser("artifact-admin")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := store.SetUserAdmin(user.ID, true); err != nil {
		t.Fatalf("set admin: %v", err)
	}
	return TokenAuth{UserID: user.ID, Kind: TokenKindUser}
}

type persistedArtifactRow struct {
	id        int64
	markdown  string
	json      string
	raw       string
	dir       string
	bytes     int64
	sha256    string
	truncated int64
}

func readPersistedArtifactRow(t *testing.T, store *GatewayStore, runID string) persistedArtifactRow {
	t.Helper()
	var row persistedArtifactRow
	if err := store.db.QueryRow(`SELECT id, artifact_markdown, artifact_json, raw_output,
		artifact_dir, artifact_bytes, artifact_sha256, artifact_truncated
		FROM monitor_run_items WHERE run_id=? ORDER BY id ASC LIMIT 1`, runID).Scan(
		&row.id, &row.markdown, &row.json, &row.raw, &row.dir, &row.bytes, &row.sha256, &row.truncated,
	); err != nil {
		t.Fatalf("read monitor_run_items row: %v", err)
	}
	return row
}

func persistMonitorArtifactRun(t *testing.T, store *GatewayStore, runID string, startedAtMs int64, item monitorRunItemRecord) monitorRunRecord {
	t.Helper()
	ctx := context.Background()
	run := monitorRunRecord{
		ID:           runID,
		TriggerKind:  "manual",
		TriggeredBy:  "artifact-admin",
		Status:       "running",
		StartedAtMs:  startedAtMs,
		FinishedAtMs: startedAtMs + 1500,
	}
	if err := store.CreateMonitorRun(ctx, run); err != nil {
		t.Fatalf("create monitor run: %v", err)
	}
	item.RunID = runID
	if item.StartedAtMs == 0 {
		item.StartedAtMs = startedAtMs
	}
	if item.FinishedAtMs == 0 {
		item.FinishedAtMs = startedAtMs + 1200
	}
	run.Status = "success"
	if _, err := store.PersistMonitorRunResults(ctx, run, []monitorRunItemRecord{item}, nil, nil); err != nil {
		t.Fatalf("persist monitor run results: %v", err)
	}
	return run
}

func TestMonitorArtifactFilesFirstEndToEnd(t *testing.T) {
	store := openMonitorArtifactStore(t)
	auth := monitorArtifactAdminAuth(t, store)
	startedAtMs := time.Date(2026, 11, 23, 9, 30, 0, 0, time.UTC).UnixMilli()
	markdown := "### 巡检报告\n- 磁盘容量正常 pass"
	artifactJSON := json.RawMessage(`{"status":"pass","summary":"磁盘正常","metrics":[{"key":"disk_percent","value":2.4}]}`)
	rawOutput := "raw stdout line\nsecond line"

	run := persistMonitorArtifactRun(t, store, "run_artifact01", startedAtMs, monitorRunItemRecord{
		Cluster:          "local",
		Instance:         "host-a",
		AssetID:          "vm-host-a",
		AssetName:        "host-a",
		AssetKind:        "host",
		Strategy:         "host",
		CollectorMethod:  "script",
		Status:           "pass",
		ArtifactFormat:   "markdown+json",
		ArtifactMarkdown: markdown,
		ArtifactJSON:     artifactJSON,
		RawOutput:        rawOutput,
	})

	row := readPersistedArtifactRow(t, store, run.ID)
	if row.markdown != "" || row.json != "" || row.raw != "" {
		t.Fatalf("expected SQLite text columns empty for file-backed row, got markdown=%q json=%q raw=%q", row.markdown, row.json, row.raw)
	}
	wantDir := "2026-11-23/run_artifact01/item_" + strconv.FormatInt(row.id, 10)
	if row.dir != wantDir {
		t.Fatalf("artifact_dir = %q, want %q", row.dir, wantDir)
	}
	if row.bytes <= 0 {
		t.Fatalf("artifact_bytes = %d, want > 0", row.bytes)
	}
	if len(row.sha256) != 64 || strings.ToLower(row.sha256) != row.sha256 {
		t.Fatalf("artifact_sha256 = %q, want 64 lowercase hex chars", row.sha256)
	}
	if row.truncated != 0 {
		t.Fatalf("artifact_truncated = %d, want 0", row.truncated)
	}

	itemDir := filepath.Join(store.dbDir, "artifacts", filepath.FromSlash(row.dir))
	mdBody, err := os.ReadFile(filepath.Join(itemDir, "artifact.md"))
	if err != nil {
		t.Fatalf("artifact.md missing: %v", err)
	}
	if string(mdBody) != markdown {
		t.Fatalf("artifact.md = %q, want %q", string(mdBody), markdown)
	}
	jsonBody, err := os.ReadFile(filepath.Join(itemDir, "artifact.json"))
	if err != nil {
		t.Fatalf("artifact.json missing: %v", err)
	}
	compactExpected, ok := compactValidJSON(string(artifactJSON))
	if !ok || string(compactExpected) != string(jsonBody) {
		t.Fatalf("artifact.json = %s, want compact %s", jsonBody, compactExpected)
	}
	rawBody, err := os.ReadFile(filepath.Join(itemDir, "raw.txt"))
	if err != nil {
		t.Fatalf("raw.txt missing: %v", err)
	}
	if string(rawBody) != rawOutput {
		t.Fatalf("raw.txt = %q, want %q", string(rawBody), rawOutput)
	}
	var totalBytes int64
	for _, body := range [][]byte{mdBody, jsonBody, rawBody} {
		totalBytes += int64(len(body))
	}
	if row.bytes != totalBytes {
		t.Fatalf("artifact_bytes = %d, want on-disk total %d", row.bytes, totalBytes)
	}

	detailRun, items, err := store.GetMonitorRunDetail(context.Background(), auth, run.ID)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if detailRun.ID != run.ID || len(items) != 1 {
		t.Fatalf("detail returned run=%q items=%d", detailRun.ID, len(items))
	}
	item := items[0]
	if item.ArtifactMarkdown != markdown {
		t.Fatalf("detail markdown = %q, want file content", item.ArtifactMarkdown)
	}
	if string(item.ArtifactJSON) != string(compactExpected) {
		t.Fatalf("detail json = %s, want %s", item.ArtifactJSON, compactExpected)
	}
	if item.RawOutput != rawOutput {
		t.Fatalf("detail raw_output = %q, want file content", item.RawOutput)
	}
	if item.ArtifactDir != wantDir || item.ArtifactBytes != row.bytes || item.ArtifactSHA256 != row.sha256 {
		t.Fatalf("detail index = (%q, %d, %q), want (%q, %d, %q)",
			item.ArtifactDir, item.ArtifactBytes, item.ArtifactSHA256, wantDir, row.bytes, row.sha256)
	}
}

func TestMonitorArtifactTruncationAndRedaction(t *testing.T) {
	store := openMonitorArtifactStore(t)
	startedAtMs := time.Date(2026, 11, 23, 11, 0, 0, 0, time.UTC).UnixMilli()
	oversizedMarkdown := "### 超限报告\nAuthorization: Bearer abc123def456\n" + strings.Repeat("A", maxInspectionArtifactBytes+4096)
	run := persistMonitorArtifactRun(t, store, "run_artifact02", startedAtMs, monitorRunItemRecord{
		Cluster:          "local",
		Instance:         "host-b",
		AssetID:          "vm-host-b",
		AssetKind:        "host",
		Strategy:         "host",
		CollectorMethod:  "script",
		Status:           "pass",
		ArtifactFormat:   "markdown",
		ArtifactMarkdown: oversizedMarkdown,
	})
	row := readPersistedArtifactRow(t, store, run.ID)
	if row.truncated != 1 {
		t.Fatalf("artifact_truncated = %d, want 1", row.truncated)
	}
	mdBody, err := os.ReadFile(filepath.Join(store.dbDir, "artifacts", filepath.FromSlash(row.dir), "artifact.md"))
	if err != nil {
		t.Fatalf("read artifact.md: %v", err)
	}
	if !strings.HasSuffix(string(mdBody), "\n... [truncated]") {
		t.Fatalf("truncated artifact.md must end with the truncation marker")
	}
	if len(mdBody) > maxInspectionArtifactBytes+len("\n... [truncated]") {
		t.Fatalf("truncated artifact.md = %d bytes, exceeds the cap", len(mdBody))
	}
	if strings.Contains(string(mdBody), "abc123def456") {
		t.Fatalf("secret reached disk")
	}
	if !strings.Contains(string(mdBody), "...REDACTED") {
		t.Fatalf("expected redaction marker in artifact.md")
	}

	// Oversized JSON must stay valid: it becomes a truthful truncation envelope
	// and the leading redacted original is kept in raw.txt.
	bigBlob, err := json.Marshal(map[string]any{
		"status": "pass",
		"blob":   strings.Repeat("B", maxInspectionArtifactBytes+4096),
	})
	if err != nil {
		t.Fatalf("marshal oversized json: %v", err)
	}
	jsonRun := persistMonitorArtifactRun(t, store, "run_artifact03", startedAtMs+60000, monitorRunItemRecord{
		Cluster:         "local",
		Instance:        "host-c",
		AssetID:         "vm-host-c",
		AssetKind:       "host",
		Strategy:        "host",
		CollectorMethod: "script",
		Status:          "pass",
		ArtifactFormat:  "json",
		ArtifactJSON:    json.RawMessage(bigBlob),
	})
	jsonRow := readPersistedArtifactRow(t, store, jsonRun.ID)
	if jsonRow.truncated != 1 {
		t.Fatalf("json artifact_truncated = %d, want 1", jsonRow.truncated)
	}
	itemDir := filepath.Join(store.dbDir, "artifacts", filepath.FromSlash(jsonRow.dir))
	jsonBody, err := os.ReadFile(filepath.Join(itemDir, "artifact.json"))
	if err != nil {
		t.Fatalf("read artifact.json: %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(jsonBody, &envelope); err != nil {
		t.Fatalf("truncated JSON envelope must stay valid JSON: %v", err)
	}
	if envelope["truncated"] != true {
		t.Fatalf("expected truncation envelope, got %s", jsonBody)
	}
	if originalBytes, _ := envelope["original_bytes"].(float64); int(originalBytes) <= maxInspectionArtifactBytes {
		t.Fatalf("envelope original_bytes = %v, want > %d", envelope["original_bytes"], maxInspectionArtifactBytes)
	}
	rawBody, err := os.ReadFile(filepath.Join(itemDir, "raw.txt"))
	if err != nil {
		t.Fatalf("oversized JSON must keep a raw.txt evidence file: %v", err)
	}
	if len(rawBody) == 0 || !strings.HasSuffix(string(rawBody), "\n... [truncated]") {
		t.Fatalf("raw.txt must hold the truncated original")
	}
}

func TestMonitorArtifactMissingFilesFallBackToDatabase(t *testing.T) {
	store := openMonitorArtifactStore(t)
	auth := monitorArtifactAdminAuth(t, store)
	startedAtMs := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC).UnixMilli()
	run := persistMonitorArtifactRun(t, store, "run_artifact04", startedAtMs, monitorRunItemRecord{
		Cluster:          "local",
		Instance:         "host-d",
		AssetID:          "vm-host-d",
		AssetKind:        "host",
		Strategy:         "host",
		CollectorMethod:  "script",
		Status:           "pass",
		ArtifactFormat:   "markdown",
		ArtifactMarkdown: "### file-backed",
	})
	row := readPersistedArtifactRow(t, store, run.ID)
	if err := os.RemoveAll(filepath.Join(store.dbDir, "artifacts", filepath.FromSlash(row.dir))); err != nil {
		t.Fatalf("remove artifact dir: %v", err)
	}
	if _, items, err := store.GetMonitorRunDetail(context.Background(), auth, run.ID); err != nil {
		t.Fatalf("detail must not fail when files are gone: %v", err)
	} else if len(items) != 1 || items[0].ArtifactMarkdown != "" {
		t.Fatalf("missing files should degrade to the (empty) DB columns, got %+v", items)
	}

	// Legacy rows written before the file-first change carry no artifact_dir and
	// must still return their stored text.
	legacyRunID := "run_artifact05"
	legacyRun := monitorRunRecord{
		ID: legacyRunID, TriggerKind: "manual", TriggeredBy: "artifact-admin", Status: "success",
		StartedAtMs: startedAtMs + 120000, FinishedAtMs: startedAtMs + 121000,
	}
	if err := store.CreateMonitorRun(context.Background(), legacyRun); err != nil {
		t.Fatalf("create legacy run: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO monitor_run_items(
		run_id, cluster, instance, asset_id, asset_kind, strategy, collector_method, status, started_at_ms,
		artifact_format, artifact_markdown, artifact_json, raw_output
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		legacyRunID, "local", "host-e", "vm-host-e", "host", "host", "script", "pass", startedAtMs+120000,
		"markdown+json", "### legacy markdown", `{"status":"warning"}`, "legacy raw",
	); err != nil {
		t.Fatalf("insert legacy item: %v", err)
	}
	_, items, err := store.GetMonitorRunDetail(context.Background(), auth, legacyRunID)
	if err != nil {
		t.Fatalf("legacy detail: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("legacy items = %d, want 1", len(items))
	}
	if items[0].ArtifactMarkdown != "### legacy markdown" || items[0].RawOutput != "legacy raw" {
		t.Fatalf("legacy fallback broken: markdown=%q raw=%q", items[0].ArtifactMarkdown, items[0].RawOutput)
	}
	if compact, ok := compactValidJSON(string(items[0].ArtifactJSON)); !ok || string(compact) != `{"status":"warning"}` {
		t.Fatalf("legacy artifact_json = %s", items[0].ArtifactJSON)
	}
	if items[0].ArtifactDir != "" || items[0].ArtifactBytes != 0 {
		t.Fatalf("legacy item must have an empty file index, got dir=%q bytes=%d", items[0].ArtifactDir, items[0].ArtifactBytes)
	}
}

func TestMonitorArtifactPruneRemovesFiles(t *testing.T) {
	store := openMonitorArtifactStore(t)
	oldMs := time.Now().UTC().Add(-40 * 24 * time.Hour).UnixMilli()
	run := persistMonitorArtifactRun(t, store, "run_artifact06", oldMs, monitorRunItemRecord{
		Cluster:          "local",
		Instance:         "host-f",
		AssetID:          "vm-host-f",
		AssetKind:        "host",
		Strategy:         "host",
		CollectorMethod:  "script",
		Status:           "pass",
		ArtifactFormat:   "markdown",
		ArtifactMarkdown: "### pruned soon",
	})
	row := readPersistedArtifactRow(t, store, run.ID)
	itemDir := filepath.Join(store.dbDir, "artifacts", filepath.FromSlash(row.dir))
	if _, err := os.Stat(itemDir); err != nil {
		t.Fatalf("artifact dir missing before prune: %v", err)
	}
	store.PruneMonitorEngineHistory(time.Now().UTC())
	if _, err := os.Stat(itemDir); !os.IsNotExist(err) {
		t.Fatalf("artifact dir must be removed with the expired run, stat err=%v", err)
	}
}

func TestMonitorArtifactFallsBackWhenStorageDisabled(t *testing.T) {
	store := openMonitorArtifactStore(t)
	store.dbDir = ""
	startedAtMs := time.Date(2026, 11, 23, 13, 0, 0, 0, time.UTC).UnixMilli()
	run := persistMonitorArtifactRun(t, store, "run_artifact07", startedAtMs, monitorRunItemRecord{
		Cluster:          "local",
		Instance:         "host-g",
		AssetID:          "vm-host-g",
		AssetKind:        "host",
		Strategy:         "host",
		CollectorMethod:  "script",
		Status:           "pass",
		ArtifactFormat:   "markdown",
		ArtifactMarkdown: "### no db dir",
	})
	row := readPersistedArtifactRow(t, store, run.ID)
	if row.dir != "" || row.bytes != 0 || row.sha256 != "" {
		t.Fatalf("storage disabled must not invent a file index: dir=%q bytes=%d sha=%q", row.dir, row.bytes, row.sha256)
	}
	if row.markdown != "### no db dir" {
		t.Fatalf("storage disabled must keep the payload in SQLite, got %q", row.markdown)
	}
}
