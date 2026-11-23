package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// SF-16 contract: a run detail must say when an indexed artifact payload is no
// longer readable, instead of silently returning 200 with empty text. missing is
// a normal lifecycle state (retention deletes files before their index rows,
// spec §3.3), not a client error.

func decodeMonitorDetail(t *testing.T, raw []byte) (map[string]any, []map[string]any) {
	t.Helper()
	var detail struct {
		Run   map[string]any   `json:"run"`
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatalf("decode run detail: %v: %s", err, raw)
	}
	return detail.Run, detail.Items
}

func TestMonitorRunDetailArtifactStateMissingAfterPayloadDeleted(t *testing.T) {
	f := monitorEngineFixture(t)
	startedAtMs := time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC).UnixMilli()
	run := persistMonitorArtifactRun(t, f.store, "run_state_missing", startedAtMs, monitorRunItemRecord{
		Cluster:          "local",
		Instance:         "host-state",
		AssetID:          "vm-host-state",
		AssetName:        "host-state",
		AssetKind:        "host",
		Strategy:         "host",
		CollectorMethod:  "script",
		Status:           "pass",
		ArtifactFormat:   "markdown+json",
		ArtifactMarkdown: "### 巡检报告\n- 磁盘容量正常 pass",
		ArtifactJSON:     json.RawMessage(`{"status":"pass"}`),
		RawOutput:        "raw stdout",
	})
	row := readPersistedArtifactRow(t, f.store, run.ID)
	itemDir := filepath.Join(f.store.dbDir, "artifacts", filepath.FromSlash(row.dir))

	// Control: while the payload is on disk the same endpoint reports present.
	raw := f.http(t, f.admin, "GET", "/v1/monitor/runs/"+run.ID, "", 200)
	detailRun, items := decodeMonitorDetail(t, raw)
	if detailRun["artifact_state"] != monitorArtifactStatePresent {
		t.Fatalf("run artifact_state = %v before deletion, want present: %s", detailRun["artifact_state"], raw)
	}
	if len(items) != 1 || items[0]["artifact_state"] != monitorArtifactStatePresent {
		t.Fatalf("item artifact_state = %v before deletion, want present: %s", items, raw)
	}

	if err := os.RemoveAll(itemDir); err != nil {
		t.Fatalf("remove artifact dir: %v", err)
	}

	// Deleting the files is exactly retention's files-before-rows window and an
	// operator deleting the directory by hand. The response must stay a 200 with
	// the full run/item structure and an explicit missing state.
	raw = f.http(t, f.admin, "GET", "/v1/monitor/runs/"+run.ID, "", 200)
	detailRun, items = decodeMonitorDetail(t, raw)
	if detailRun["artifact_state"] != monitorArtifactStateMissing {
		t.Fatalf("run artifact_state = %v after deletion, want missing: %s", detailRun["artifact_state"], raw)
	}
	if detailRun["id"] != run.ID || detailRun["status"] != "success" {
		t.Fatalf("run structure incomplete after deletion: %s", raw)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d after deletion, want 1: %s", len(items), raw)
	}
	item := items[0]
	if item["artifact_state"] != monitorArtifactStateMissing {
		t.Fatalf("item artifact_state = %v after deletion, want missing: %s", item["artifact_state"], raw)
	}
	if item["id"] == nil || item["run_id"] != run.ID || item["status"] != "pass" || item["artifact_format"] != "markdown+json" {
		t.Fatalf("item structure incomplete after deletion: %s", raw)
	}
	if _, exposed := item["artifact_dir"]; exposed {
		t.Fatalf("internal artifact_dir must not be exposed: %s", raw)
	}
	if row.dir == "" || row.bytes <= 0 {
		t.Fatalf("expected the index row to survive with dir=%q bytes=%d", row.dir, row.bytes)
	}
	for _, key := range []string{"artifact_markdown", "artifact_json", "raw_output"} {
		if value, present := item[key]; present && value != "" {
			t.Fatalf("%s = %v after deletion, want empty body", key, value)
		}
	}
}

func TestMonitorArtifactStatePresentForFileBackedAndLegacyRows(t *testing.T) {
	store := openMonitorArtifactStore(t)
	auth := monitorArtifactAdminAuth(t, store)
	startedAtMs := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC).UnixMilli()

	// A truncated payload is still present: artifact_truncated is a sub-state of
	// present, never a third value.
	run := persistMonitorArtifactRun(t, store, "run_state_present", startedAtMs, monitorRunItemRecord{
		Cluster:          "local",
		Instance:         "host-truncated",
		AssetID:          "vm-host-truncated",
		AssetKind:        "host",
		Strategy:         "host",
		CollectorMethod:  "script",
		Status:           "pass",
		ArtifactFormat:   "markdown",
		ArtifactMarkdown: strings.Repeat("A", maxInspectionArtifactBytes+4096),
	})
	detailRun, items, err := store.GetMonitorRunDetail(context.Background(), auth, run.ID)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if len(items) != 1 || items[0].ArtifactState != monitorArtifactStatePresent || !items[0].ArtifactTruncated {
		t.Fatalf("truncated artifact must be present, got state=%q truncated=%v", items[0].ArtifactState, items[0].ArtifactTruncated)
	}
	if detailRun.ArtifactState != monitorArtifactStatePresent {
		t.Fatalf("run artifact_state = %q, want present", detailRun.ArtifactState)
	}
	encoded, err := json.Marshal(map[string]any{"run": detailRun, "items": items})
	if err != nil {
		t.Fatalf("marshal detail: %v", err)
	}
	if !strings.Contains(string(encoded), `"artifact_state":"present"`) {
		t.Fatalf("present artifact_state missing from JSON contract: %s", encoded)
	}

	// Legacy rows written before the file-first change have no artifact_dir and
	// still read their SQLite text columns. They are present, not missing: the
	// state distinguishes "payload lost" from "never landed on disk".
	legacyRunID := "run_state_legacy"
	legacyRun := monitorRunRecord{
		ID: legacyRunID, TriggerKind: "manual", TriggeredBy: "artifact-admin", Status: "success",
		StartedAtMs: startedAtMs + 60000, FinishedAtMs: startedAtMs + 61000,
	}
	if err := store.CreateMonitorRun(context.Background(), legacyRun); err != nil {
		t.Fatalf("create legacy run: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO monitor_run_items(
		run_id, cluster, instance, asset_id, asset_kind, strategy, collector_method, status, started_at_ms,
		artifact_format, artifact_markdown, artifact_json, raw_output
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		legacyRunID, "local", "host-legacy", "vm-host-legacy", "host", "host", "script", "pass", startedAtMs+60000,
		"markdown+json", "### legacy markdown", `{"status":"warning"}`, "legacy raw",
	); err != nil {
		t.Fatalf("insert legacy item: %v", err)
	}
	legacyDetailRun, legacyItems, err := store.GetMonitorRunDetail(context.Background(), auth, legacyRunID)
	if err != nil {
		t.Fatalf("legacy detail: %v", err)
	}
	if len(legacyItems) != 1 {
		t.Fatalf("legacy items = %d, want 1", len(legacyItems))
	}
	legacy := legacyItems[0]
	if legacy.ArtifactState != monitorArtifactStatePresent {
		t.Fatalf("legacy artifact_state = %q, want present", legacy.ArtifactState)
	}
	if legacy.ArtifactDir != "" || legacy.ArtifactMarkdown == "" || legacy.RawOutput == "" {
		t.Fatalf("legacy row must keep its text fallback and empty index, got dir=%q markdown=%q raw=%q",
			legacy.ArtifactDir, legacy.ArtifactMarkdown, legacy.RawOutput)
	}
	if legacyDetailRun.ArtifactState != monitorArtifactStatePresent {
		t.Fatalf("legacy run artifact_state = %q, want present", legacyDetailRun.ArtifactState)
	}
	encoded, err = json.Marshal(map[string]any{"run": legacyDetailRun, "items": legacyItems})
	if err != nil {
		t.Fatalf("marshal legacy detail: %v", err)
	}
	if !strings.Contains(string(encoded), `"artifact_state":"present"`) {
		t.Fatalf("legacy present artifact_state missing from JSON contract: %s", encoded)
	}
}
