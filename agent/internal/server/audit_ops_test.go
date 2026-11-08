package server

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func seedAudit(t *testing.T, store *GatewayStore, cluster, command string, startedAt time.Time) {
	t.Helper()
	id, err := store.StartAudit(AuditEvent{
		UserID: "usr_1", TokenID: "tok_1", Cluster: cluster, Instance: "web-1",
		Action: ActionExec, Session: "s1", CommandSummary: command, StartedAt: startedAt,
	})
	if err != nil {
		t.Fatalf("start audit: %v", err)
	}
	if err := store.FinishAudit(id, AuditFinish{
		Status: "success", Tail: "line1\nline2, \"quoted\"", BytesOut: 12,
		EndedAt: startedAt.Add(time.Second),
	}); err != nil {
		t.Fatalf("finish audit: %v", err)
	}
}

func TestAuditCSVExport(t *testing.T) {
	store := openApprovalTestStore(t)
	now := time.Now().UTC()
	seedAudit(t, store, "prod", "echo hi", now.Add(-2*time.Hour))
	seedAudit(t, store, "prod", "echo bye", now.Add(-time.Hour))

	events, err := store.ListAuditFiltered(AuditFilter{Cluster: "prod", Export: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("want 2 events, got %d", len(events))
	}
	// 导出按时间升序
	if events[0].CommandSummary != "echo hi" {
		t.Fatalf("export must be ascending, first=%q", events[0].CommandSummary)
	}

	rec := httptest.NewRecorder()
	if err := writeAuditCSV(rec, events); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/csv") {
		t.Fatalf("unexpected content-type %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "doops-audit.csv") {
		t.Fatalf("unexpected content-disposition %q", cd)
	}

	rows, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("want header+2 rows, got %d", len(rows))
	}
	if rows[0][0] != "id" || rows[0][10] != "error" {
		t.Fatalf("unexpected header: %v", rows[0])
	}
	// 命令摘要含逗号与引号也必须保持单列
	if rows[1][9] != "success" || rows[1][13] != "12" {
		t.Fatalf("unexpected row: %v", rows[1])
	}
	if !strings.Contains(rows[1][14], "\"quoted\"") {
		t.Fatalf("tail not preserved: %q", rows[1][14])
	}
}

func TestAuditJSONLExport(t *testing.T) {
	store := openApprovalTestStore(t)
	seedAudit(t, store, "prod", "echo hi", time.Now().UTC().Add(-time.Hour))
	events, err := store.ListAuditFiltered(AuditFilter{Export: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	rec := httptest.NewRecorder()
	if err := writeAuditJSONL(rec, events); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("want 1 jsonl line, got %d", len(lines))
	}
	var record auditRecord
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("parse jsonl: %v", err)
	}
	if record.CommandSummary != "echo hi" || record.Status != "success" {
		t.Fatalf("unexpected record: %#v", record)
	}
}

func TestAuditFilterTimeRange(t *testing.T) {
	store := openApprovalTestStore(t)
	now := time.Now().UTC()
	seedAudit(t, store, "prod", "old", now.Add(-48*time.Hour))
	seedAudit(t, store, "prod", "new", now.Add(-time.Hour))

	events, err := store.ListAuditFiltered(AuditFilter{Export: true, Since: now.Add(-24 * time.Hour)})
	if err != nil {
		t.Fatalf("list since: %v", err)
	}
	if len(events) != 1 || events[0].CommandSummary != "new" {
		t.Fatalf("since filter failed: %#v", events)
	}

	events, err = store.ListAuditFiltered(AuditFilter{Export: true, Until: now.Add(-24 * time.Hour)})
	if err != nil {
		t.Fatalf("list until: %v", err)
	}
	if len(events) != 1 || events[0].CommandSummary != "old" {
		t.Fatalf("until filter failed: %#v", events)
	}
}

func TestAuditRetentionPrune(t *testing.T) {
	store := openApprovalTestStore(t)
	now := time.Now().UTC()
	seedAudit(t, store, "prod", "old", now.Add(-200*24*time.Hour))
	seedAudit(t, store, "prod", "recent", now.Add(-time.Hour))

	hub := NewGatewayHub(store, GatewayHubOptions{})
	deleted, err := hub.pruneAuditOnce(90, now)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("want 1 pruned, got %d", deleted)
	}
	events, err := store.ListAuditFiltered(AuditFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 1 || events[0].CommandSummary != "recent" {
		t.Fatalf("recent event should survive: %#v", events)
	}

	// days<=0 表示关闭留存，不删除任何记录
	if deleted, err := hub.pruneAuditOnce(0, now); err != nil || deleted != 0 {
		t.Fatalf("retention disabled must noop: deleted=%d err=%v", deleted, err)
	}
}

func TestPostAlertFeishuPayload(t *testing.T) {
	var got map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method %s", r.Method)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	text := auditAlertText("guardrail_blocked", "usr_1", "prod", "web-1", "exec", "rm -rf /")
	if err := postAlert(context.Background(), srv.URL, text); err != nil {
		t.Fatalf("post alert: %v", err)
	}
	if got["msg_type"] != "text" {
		t.Fatalf("unexpected msg_type: %v", got["msg_type"])
	}
	content, ok := got["content"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing content: %#v", got)
	}
	gotText, _ := content["text"].(string)
	for _, want := range []string{"guardrail_blocked", "usr_1", "prod/web-1", "exec", "rm -rf /"} {
		if !strings.Contains(gotText, want) {
			t.Fatalf("alert text missing %q: %s", want, gotText)
		}
	}
}

func TestPostAlertFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	if err := postAlert(context.Background(), srv.URL, "x"); err == nil {
		t.Fatal("non-2xx webhook must return error")
	}
}

func TestAuditAlertTextTruncation(t *testing.T) {
	detail := strings.Repeat("超", 300)
	text := auditAlertText("approval_required", "usr_1", "prod", "web-1", "exec", detail)
	// detail 截断到 200 字符 + 省略号，加上前缀总长应远小于 300
	if len([]rune(text)) > 300 {
		t.Fatalf("alert text not truncated: %d runes", len([]rune(text)))
	}
	if !strings.HasSuffix(text, "...") {
		t.Fatalf("truncated text should end with ellipsis: %s", text)
	}
}
