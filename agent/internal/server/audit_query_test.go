package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 混合样本：blocked 拦截记录、普通执行记录、scope 豁免记录、kubectl 快照块、多行命令。
const mixedAuditLog = `# [2026-11-08T10:00:00Z] blocked=1 rule=disk.rm_root
rm -rf /
# [2026-11-08 10:01:00] exit=0
kubectl apply -f deploy.yaml
--- BEGIN deploy.yaml ---
apiVersion: v1
kind: Pod
--- END deploy.yaml ---
# [2026-11-08 10:02:00] exit=1
echo first
echo second
# [2026-11-08 10:03:00] exit=0 bypassed_by_scope=1
dd if=/dev/zero of=/dev/sda count=0
`

func TestParseAuditLogMixedEntries(t *testing.T) {
	entries := parseAuditLog(mixedAuditLog, "prod")
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries, got %d: %+v", len(entries), entries)
	}

	blocked := entries[0]
	if !blocked.Blocked || blocked.Rule != "disk.rm_root" {
		t.Fatalf("blocked entry parsed wrong: %+v", blocked)
	}
	if blocked.ExitCode != nil {
		t.Fatalf("blocked entry must not carry exit code: %+v", blocked)
	}
	if blocked.Command != "rm -rf /" {
		t.Fatalf("blocked command mismatch: %q", blocked.Command)
	}
	if blocked.Timestamp != "2026-11-08T10:00:00Z" {
		t.Fatalf("blocked ts mismatch: %q", blocked.Timestamp)
	}

	normal := entries[1]
	if normal.Blocked || normal.BypassedByScope {
		t.Fatalf("normal entry flags wrong: %+v", normal)
	}
	if normal.ExitCode == nil || *normal.ExitCode != 0 {
		t.Fatalf("normal exit code mismatch: %+v", normal)
	}
	if normal.Command != "kubectl apply -f deploy.yaml" {
		t.Fatalf("snapshot block leaked into command: %q", normal.Command)
	}
	// 历史时间格式（无时区）按 UTC 规范化为 RFC3339
	if normal.Timestamp != "2026-11-08T10:01:00Z" {
		t.Fatalf("legacy ts not normalized: %q", normal.Timestamp)
	}

	multi := entries[2]
	if multi.Command != "echo first\necho second" {
		t.Fatalf("multi-line command mismatch: %q", multi.Command)
	}
	if multi.ExitCode == nil || *multi.ExitCode != 1 {
		t.Fatalf("multi exit code mismatch: %+v", multi)
	}

	bypassed := entries[3]
	if !bypassed.BypassedByScope || bypassed.Blocked {
		t.Fatalf("bypassed entry flags wrong: %+v", bypassed)
	}
	if bypassed.Session != "prod" || bypassed.Action != "exec" {
		t.Fatalf("entry metadata mismatch: %+v", bypassed)
	}
}

func TestFilterAuditEntriesBlockedAndBypassed(t *testing.T) {
	entries := parseAuditLog(mixedAuditLog, "prod")

	blockedOnly := filterAuditEntries(entries, true, false)
	if len(blockedOnly) != 1 || blockedOnly[0].Rule != "disk.rm_root" {
		t.Fatalf("blocked filter mismatch: %+v", blockedOnly)
	}

	bypassedOnly := filterAuditEntries(entries, false, true)
	if len(bypassedOnly) != 1 || !bypassedOnly[0].BypassedByScope {
		t.Fatalf("bypassed filter mismatch: %+v", bypassedOnly)
	}

	// 两个条件同时给出时取交集：样本中没有同时 blocked 且 bypassed 的记录
	intersection := filterAuditEntries(entries, true, true)
	if len(intersection) != 0 {
		t.Fatalf("intersection should be empty: %+v", intersection)
	}

	// 不过滤时原样返回
	if got := filterAuditEntries(entries, false, false); len(got) != len(entries) {
		t.Fatalf("unfiltered count mismatch: %d", len(got))
	}
}

func writeAuditFixture(t *testing.T, root, session, content string) {
	t.Helper()
	dir := filepath.Join(root, session)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".doops-audit-log"), []byte(content), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func TestHandleAuditQueryFiltersLimitAndJSONL(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	writeAuditFixture(t, root, "prod", mixedAuditLog)

	// text 模式：默认全量，输出原始记录块
	text, err := handleAuditQuery(mustJSON(t, map[string]interface{}{"session_id": "prod"}))
	if err != nil {
		t.Fatalf("query text: %v", err)
	}
	for _, want := range []string{"blocked=1 rule=disk.rm_root", "bypassed_by_scope=1", "kubectl apply -f deploy.yaml"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "--- BEGIN") {
		t.Fatalf("text output should not contain snapshot block:\n%s", text)
	}

	// blocked 过滤 + limit
	filtered, err := handleAuditQuery(mustJSON(t, map[string]interface{}{
		"session_id": "prod",
		"blocked":    true,
		"limit":      5,
	}))
	if err != nil {
		t.Fatalf("query blocked: %v", err)
	}
	if !strings.Contains(filtered, "rm -rf /") || strings.Contains(filtered, "kubectl") {
		t.Fatalf("blocked filter output wrong:\n%s", filtered)
	}

	// limit 截取最新 N 条
	limited, err := handleAuditQuery(mustJSON(t, map[string]interface{}{
		"session_id": "prod",
		"limit":      1,
	}))
	if err != nil {
		t.Fatalf("query limit: %v", err)
	}
	if !strings.Contains(limited, "dd if=/dev/zero") || strings.Contains(limited, "rm -rf /") {
		t.Fatalf("limit should keep only the newest entry:\n%s", limited)
	}

	// JSONL 导出：每行可独立解析，字段齐全
	jsonl, err := handleAuditQuery(mustJSON(t, map[string]interface{}{
		"session_id": "prod",
		"format":     "jsonl",
	}))
	if err != nil {
		t.Fatalf("query jsonl: %v", err)
	}
	lines := strings.Split(strings.TrimRight(jsonl, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 jsonl lines, got %d:\n%s", len(lines), jsonl)
	}
	var seenBlocked, seenBypassed bool
	for _, line := range lines {
		var entry auditLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("jsonl line not valid JSON: %q: %v", line, err)
		}
		if entry.Session != "prod" || entry.Action != "exec" || entry.Command == "" || entry.Timestamp == "" {
			t.Fatalf("jsonl entry incomplete: %+v", entry)
		}
		seenBlocked = seenBlocked || entry.Blocked
		seenBypassed = seenBypassed || entry.BypassedByScope
	}
	if !seenBlocked || !seenBypassed {
		t.Fatalf("jsonl export lost blocked/bypassed markers:\n%s", jsonl)
	}

	// JSONL + blocked 过滤组合
	blockedJSONL, err := handleAuditQuery(mustJSON(t, map[string]interface{}{
		"session_id": "prod",
		"blocked":    true,
		"format":     "jsonl",
	}))
	if err != nil {
		t.Fatalf("query blocked jsonl: %v", err)
	}
	blockedLines := strings.Split(strings.TrimRight(blockedJSONL, "\n"), "\n")
	if len(blockedLines) != 1 {
		t.Fatalf("expected 1 blocked jsonl line, got %d:\n%s", len(blockedLines), blockedJSONL)
	}
	var blockedEntry auditLogEntry
	if err := json.Unmarshal([]byte(blockedLines[0]), &blockedEntry); err != nil {
		t.Fatalf("decode blocked jsonl: %v", err)
	}
	if !blockedEntry.Blocked || blockedEntry.Rule != "disk.rm_root" || blockedEntry.Command != "rm -rf /" {
		t.Fatalf("blocked jsonl entry wrong: %+v", blockedEntry)
	}
}

func TestHandleAuditQueryAggregatesAllSessions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	writeAuditFixture(t, root, "sess-a", "# [2026-11-08T09:00:00Z] blocked=1 rule=sys.shutdown\nshutdown -h now\n")
	writeAuditFixture(t, root, "sess-b", "# [2026-11-08 09:05:00] exit=0\nuptime\n")

	// 空 session_id 聚合所有 session
	out, err := handleAuditQuery(mustJSON(t, map[string]interface{}{
		"session_id": "",
		"format":     "jsonl",
	}))
	if err != nil {
		t.Fatalf("aggregate query: %v", err)
	}
	sessions := map[string]bool{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		var entry auditLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decode aggregate jsonl: %v", err)
		}
		sessions[entry.Session] = true
	}
	if !sessions["sess-a"] || !sessions["sess-b"] || len(sessions) != 2 {
		t.Fatalf("aggregate should cover both sessions, got %v:\n%s", sessions, out)
	}

	// 不存在的 session 返回空集而非错误
	empty, err := handleAuditQuery(mustJSON(t, map[string]interface{}{"session_id": "missing"}))
	if err != nil {
		t.Fatalf("missing session should not error: %v", err)
	}
	if !strings.Contains(empty, "no matching audit records") {
		t.Fatalf("missing session output mismatch: %q", empty)
	}

	// 空工作区根目录同样不报错
	t.Setenv("DOOPS_WORKSPACE_ROOT", filepath.Join(t.TempDir(), "nonexistent"))
	if _, err := handleAuditQuery(mustJSON(t, map[string]interface{}{"session_id": ""})); err != nil {
		t.Fatalf("empty workspace root should not error: %v", err)
	}
}

// 集成：经 WS 全链路产生 blocked / 正常 / bypassed 三类记录后，用 doops_audit_query 检索。
func TestAuditQueryViaWS(t *testing.T) {
	conn, _ := setupGuardrailWS(t)

	// 1. 护栏拦截（blocked=1）
	callToolResult(t, conn, "doops_shell", map[string]interface{}{
		"session_id": "audit-ws",
		"command":    "rm -rf /",
	})
	// 2. 正常命令（exit=0）
	callTool(t, conn, "doops_shell", map[string]interface{}{
		"session_id": "audit-ws",
		"command":    "echo audit-ws-ok",
	})
	// 3. scope 豁免放行（bypassed_by_scope=1）
	callToolResultWithScopes(t, conn, "doops_shell", map[string]interface{}{
		"session_id": "audit-ws",
		"command":    "dd if=/dev/zero of=/dev/sda count=0",
	}, []string{"admin"})

	// 无过滤：三类记录齐全
	all := callTool(t, conn, "doops_audit_query", map[string]interface{}{
		"session_id": "audit-ws",
	})
	for _, want := range []string{"blocked=1 rule=disk.rm_root", "exit=0", "bypassed_by_scope=1"} {
		if !strings.Contains(all, want) {
			t.Fatalf("unfiltered audit missing %q:\n%s", want, all)
		}
	}

	// blocked 过滤：只剩拦截记录
	blockedOnly := callTool(t, conn, "doops_audit_query", map[string]interface{}{
		"session_id": "audit-ws",
		"blocked":    true,
	})
	if !strings.Contains(blockedOnly, "rm -rf /") || strings.Contains(blockedOnly, "audit-ws-ok") {
		t.Fatalf("blocked filter over WS wrong:\n%s", blockedOnly)
	}

	// bypassed 过滤：只剩豁免记录
	bypassedOnly := callTool(t, conn, "doops_audit_query", map[string]interface{}{
		"session_id": "audit-ws",
		"bypassed":   true,
	})
	if !strings.Contains(bypassedOnly, "dd if=/dev/zero") || strings.Contains(bypassedOnly, "rm -rf /") {
		t.Fatalf("bypassed filter over WS wrong:\n%s", bypassedOnly)
	}

	// JSONL 导出：每行合法 JSON，且 blocked/bypassed 标记保留
	jsonl := callTool(t, conn, "doops_audit_query", map[string]interface{}{
		"session_id": "audit-ws",
		"format":     "jsonl",
	})
	var sawBlocked, sawBypassed bool
	for _, line := range strings.Split(strings.TrimRight(jsonl, "\n"), "\n") {
		var entry auditLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("ws jsonl line invalid: %q: %v", line, err)
		}
		if entry.Session != "audit-ws" {
			t.Fatalf("ws jsonl session mismatch: %+v", entry)
		}
		sawBlocked = sawBlocked || entry.Blocked
		sawBypassed = sawBypassed || entry.BypassedByScope
	}
	if !sawBlocked || !sawBypassed {
		t.Fatalf("ws jsonl lost markers:\n%s", jsonl)
	}

	// 空 session_id 聚合查询不被 validateSession 拦截
	aggregated := callTool(t, conn, "doops_audit_query", map[string]interface{}{
		"session_id": "",
		"blocked":    true,
	})
	if !strings.Contains(aggregated, "rm -rf /") {
		t.Fatalf("aggregate audit query over WS wrong:\n%s", aggregated)
	}
}

func TestGatewayActionMappingAuditQuery(t *testing.T) {
	if got := actionForTool("doops_audit_query", nil); got != ActionRead {
		t.Fatalf("doops_audit_query should map to read action, got %q", got)
	}
}
