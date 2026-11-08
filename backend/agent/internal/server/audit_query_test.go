package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	for index, raw := range strings.Split(content, "\n# [") {
		if index != 0 {
			raw = "# [" + raw
		}
		path := filepath.Join(root, "execution", session+".log")
		if strings.Contains(strings.SplitN(raw, "\n", 2)[0], " blocked=1") {
			path = filepath.Join(root, "workspace", session, ".doops-audit-log")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := fmt.Fprintln(file, strings.TrimRight(raw, "\n"))
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatalf("write audit fixture: %v, %v", writeErr, closeErr)
		}
	}
}

func queryAuditFixture(t *testing.T, root string, args json.RawMessage) (string, error) {
	t.Helper()
	return handleAuditQueryAt(args, filepath.Join(root, "execution"), filepath.Join(root, "workspace"))
}

func TestHandleAuditQueryFiltersLimitAndJSONL(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", filepath.Join(root, "workspace"))
	writeAuditFixture(t, root, "prod", mixedAuditLog)

	// text 模式：默认全量，输出原始记录块
	text, err := queryAuditFixture(t, root, mustJSON(t, map[string]interface{}{"audit_session": "prod"}))
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
	filtered, err := queryAuditFixture(t, root, mustJSON(t, map[string]interface{}{
		"audit_session": "prod",
		"blocked":       true,
		"limit":         5,
	}))
	if err != nil {
		t.Fatalf("query blocked: %v", err)
	}
	if !strings.Contains(filtered, "rm -rf /") || strings.Contains(filtered, "kubectl") {
		t.Fatalf("blocked filter output wrong:\n%s", filtered)
	}

	// limit 截取最新 N 条
	limited, err := queryAuditFixture(t, root, mustJSON(t, map[string]interface{}{
		"audit_session": "prod",
		"limit":         1,
	}))
	if err != nil {
		t.Fatalf("query limit: %v", err)
	}
	if !strings.Contains(limited, "dd if=/dev/zero") || strings.Contains(limited, "rm -rf /") {
		t.Fatalf("limit should keep only the newest entry:\n%s", limited)
	}

	// JSONL 导出：每行可独立解析，字段齐全
	jsonl, err := queryAuditFixture(t, root, mustJSON(t, map[string]interface{}{
		"audit_session": "prod",
		"format":        "jsonl",
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
	blockedJSONL, err := queryAuditFixture(t, root, mustJSON(t, map[string]interface{}{
		"audit_session": "prod",
		"blocked":       true,
		"format":        "jsonl",
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
	t.Setenv("DOOPS_WORKSPACE_ROOT", filepath.Join(root, "workspace"))
	writeAuditFixture(t, root, "sess-a", "# [2026-11-08T09:00:00Z] blocked=1 rule=sys.shutdown\nshutdown -h now\n")
	writeAuditFixture(t, root, "sess-b", "# [2026-11-08 09:05:00] exit=0\nuptime\n")

	// 空 audit_session 聚合所有 session，不改变操作上下文。
	out, err := queryAuditFixture(t, root, mustJSON(t, map[string]interface{}{
		"audit_session": "",
		"format":        "jsonl",
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
	empty, err := queryAuditFixture(t, root, mustJSON(t, map[string]interface{}{"audit_session": "missing"}))
	if err != nil {
		t.Fatalf("missing session should not error: %v", err)
	}
	if !strings.Contains(empty, "no matching audit records") {
		t.Fatalf("missing session output mismatch: %q", empty)
	}

	// 空工作区根目录同样不报错
	root = filepath.Join(t.TempDir(), "nonexistent")
	if _, err := queryAuditFixture(t, root, mustJSON(t, map[string]interface{}{"audit_session": ""})); err != nil {
		t.Fatalf("empty workspace root should not error: %v", err)
	}
}

// 集成：经 WS 全链路产生 blocked / 正常 / bypassed 三类记录后，用 doops_audit_query 检索。
func TestAuditQueryViaWS(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", filepath.Join(root, "workspace"))
	t.Setenv("DOOPS_BLOCK_EXTRA", "audit-bypass-canary")
	gw := NewGateway()
	gw.auditRoot = filepath.Join(root, "execution")
	server := newGatewayAuthorizedAgentServer(t, gw)
	defer server.Close()
	conn := dialAgentTestWS(t, server.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

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
		"command":    "printf audit-bypass-canary",
	}, []string{"admin"})

	// 无过滤：三类记录齐全
	all := callTool(t, conn, "doops_audit_query", map[string]interface{}{
		"session_id": "audit-query-operation", "audit_session": "audit-ws",
	})
	for _, want := range []string{"blocked=1 rule=disk.rm_root", "exit=0", "bypassed_by_scope=1"} {
		if !strings.Contains(all, want) {
			t.Fatalf("unfiltered audit missing %q:\n%s", want, all)
		}
	}

	// blocked 过滤：只剩拦截记录
	blockedOnly := callTool(t, conn, "doops_audit_query", map[string]interface{}{
		"session_id": "audit-query-operation", "audit_session": "audit-ws",
		"blocked": true,
	})
	if !strings.Contains(blockedOnly, "rm -rf /") || strings.Contains(blockedOnly, "audit-ws-ok") {
		t.Fatalf("blocked filter over WS wrong:\n%s", blockedOnly)
	}

	// bypassed 过滤：只剩豁免记录
	bypassedOnly := callTool(t, conn, "doops_audit_query", map[string]interface{}{
		"session_id": "audit-query-operation", "audit_session": "audit-ws",
		"bypassed": true,
	})
	if !strings.Contains(bypassedOnly, "audit-bypass-canary") || strings.Contains(bypassedOnly, "rm -rf /") {
		t.Fatalf("bypassed filter over WS wrong:\n%s", bypassedOnly)
	}

	// JSONL 导出：每行合法 JSON，且 blocked/bypassed 标记保留
	jsonl := callTool(t, conn, "doops_audit_query", map[string]interface{}{
		"session_id": "audit-query-operation", "audit_session": "audit-ws",
		"format": "jsonl",
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

	// 聚合查询保留非空操作 session，不按操作 session 隐式过滤。
	aggregated := callTool(t, conn, "doops_audit_query", map[string]interface{}{
		"session_id": "audit-query-operation", "audit_session": "",
		"blocked": true,
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

func TestAuditQueryReadsBothLogLocationsAndSortsBeforeLimit(t *testing.T) {
	root := t.TempDir()
	writeAuditFixture(t, root, "a-newest", "# [2026-11-08T10:30:00Z] exit=0 bypassed_by_scope=1\nnewest command\n")
	writeAuditFixture(t, root, "z-oldest", "# [2026-11-08T09:00:00Z] blocked=1 rule=custom\noldest command\n")
	writeAuditFixture(t, root, "m-middle", "# [2026-11-08T10:00:00Z] exit=0\nmiddle command\n")
	entries, err := collectAuditEntriesAt("", filepath.Join(root, "execution"), filepath.Join(root, "workspace"))
	if err != nil || len(entries) != 3 {
		t.Fatalf("both log locations must contribute: entries=%+v err=%v", entries, err)
	}
	for index, want := range []string{"z-oldest", "m-middle", "a-newest"} {
		if entries[index].Session != want {
			t.Fatalf("records are not chronological: %+v", entries)
		}
	}
	out, err := queryAuditFixture(t, root, mustJSON(t, map[string]interface{}{
		"session_id": "query-operation", "limit": 1, "format": "jsonl",
	}))
	var entry auditLogEntry
	if err != nil || json.Unmarshal([]byte(out), &entry) != nil || entry.Session != "a-newest" || !entry.BypassedByScope {
		t.Fatalf("limit must follow global chronological order: output=%q err=%v", out, err)
	}
	selected, err := queryAuditFixture(t, root, mustJSON(t, map[string]interface{}{
		"session_id": "query-operation", "audit_session": "z-oldest", "format": "jsonl",
	}))
	if err != nil || json.Unmarshal([]byte(selected), &entry) != nil || entry.Session != "z-oldest" || !entry.Blocked {
		t.Fatalf("query selection was confused with operation context: output=%q err=%v", selected, err)
	}
}

func TestAuditQueryRejectsSymlinkFilesAndSessions(t *testing.T) {
	for _, kind := range []string{"execution-log", "workspace-session", "workspace-log"} {
		for _, selection := range []string{"protected", ""} {
			t.Run(kind+"/"+selection, func(t *testing.T) {
				root, outside := t.TempDir(), t.TempDir()
				canary := "audit-query-private-canary"
				outsideFile := filepath.Join(outside, ".doops-audit-log")
				if err := os.WriteFile(outsideFile, []byte(canary), 0600); err != nil {
					t.Fatal(err)
				}
				link, destination := filepath.Join(root, "execution", "protected.log"), outsideFile
				switch kind {
				case "workspace-session":
					link, destination = filepath.Join(root, "workspace", "protected"), outside
				case "workspace-log":
					link = filepath.Join(root, "workspace", "protected", ".doops-audit-log")
				}
				if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(destination, link); err != nil {
					t.Fatal(err)
				}
				out, err := queryAuditFixture(t, root, mustJSON(t, map[string]interface{}{"audit_session": selection}))
				if err == nil || !strings.Contains(err.Error(), "audit_query_unavailable") || out != "" {
					t.Fatalf("symlink must fail explicitly without records: out=%q err=%v", out, err)
				}
				if strings.Contains(err.Error(), outside) || strings.Contains(err.Error(), canary) {
					t.Fatalf("private filesystem path or contents escaped in error: %v", err)
				}
				if data, err := os.ReadFile(outsideFile); err != nil || string(data) != canary {
					t.Fatalf("query modified outside file: data=%q err=%v", data, err)
				}
			})
		}
	}
}

func TestAuditQueryUnavailableSourceCannotReturnPartialRecords(t *testing.T) {
	for _, unavailable := range []string{"execution", "workspace"} {
		t.Run(unavailable, func(t *testing.T) {
			root := t.TempDir()
			if unavailable == "execution" {
				writeAuditFixture(t, root, "known", "# [2026-11-08T09:00:00Z] blocked=1 rule=custom\nknown blocked\n")
			} else {
				writeAuditFixture(t, root, "known", "# [2026-11-08T09:00:00Z] exit=0\nknown execution\n")
			}
			if err := os.WriteFile(filepath.Join(root, unavailable), []byte("not an audit directory"), 0600); err != nil {
				t.Fatal(err)
			}
			out, err := queryAuditFixture(t, root, mustJSON(t, map[string]interface{}{}))
			if err == nil || !strings.Contains(err.Error(), "audit_query_unavailable") || out != "" {
				t.Fatalf("unavailable audit source was silently omitted: out=%q err=%v", out, err)
			}
			if strings.Contains(err.Error(), root) {
				t.Fatalf("private audit root escaped in error: %v", err)
			}
		})
	}
}

func TestAuditQueryRejectsNonRegularLogAndInvalidArguments(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "execution", "bad.log"), 0700); err != nil {
		t.Fatal(err)
	}
	if out, err := queryAuditFixture(t, root, mustJSON(t, map[string]interface{}{})); err == nil || out != "" {
		t.Fatalf("directory cannot be treated as a log: out=%q err=%v", out, err)
	}
	for _, input := range []string{`{`, `{"limit":-1}`, `{"format":"xml"}`, `{"audit_session":"../outside"}`} {
		if out, err := queryAuditFixture(t, t.TempDir(), json.RawMessage(input)); err == nil || out != "" {
			t.Fatalf("invalid query accepted: input=%s out=%q err=%v", input, out, err)
		}
	}
	err := auditQueryIOError("read fixture", &os.PathError{Op: "open", Path: filepath.Join(root, "private-file"), Err: os.ErrPermission})
	if !errors.Is(err, os.ErrPermission) || strings.Contains(err.Error(), root) {
		t.Fatalf("permission cause must remain visible without a private path: %v", err)
	}
}

func TestAuditQueryCLIThroughGatewayAndRealAgent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", filepath.Join(root, "workspace"))
	binary := filepath.Join(root, "doops")
	buildCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", binary, "./cli/doops")
	build.Dir = "../../../cli"
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build query CLI: %v\n%s", err, out)
	}
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	agent := NewGateway()
	agent.auditRoot = filepath.Join(root, "execution")
	conn := dialFakeGatewayAgent(t, f.server.URL, "dev", "local")
	done := make(chan struct{})
	go func() {
		defer close(done)
		agent.ServeGatewayWebSocketConnWithReady(conn, "audit-query-cli", nil)
	}()
	t.Cleanup(func() {
		_ = conn.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("audit query Agent did not stop")
		}
	})
	waitForGatewayAgent(t, f.hub, "dev", "local")
	writeAuditFixture(t, root, "selected", "# [2026-11-08T09:00:00Z] blocked=1 rule=custom\nselected rejected command\n")
	writeAuditFixture(t, root, "other", "# [2026-11-08T09:05:00Z] exit=0 bypassed_by_scope=1\nother authorized command\n")
	configDir := t.TempDir()
	for name, value := range map[string]interface{}{
		"config.json": map[string]interface{}{"servers": []map[string]string{
			{"name": "audit-fixture", "gateway": f.server.URL, "cluster": "dev", "instance": "local"},
		}},
		"auth.json": map[string]interface{}{"version": 2, "credentials": map[string]interface{}{
			f.server.URL: map[string]string{"token": f.admin.Plaintext, "source": "audit-query-test"},
		}},
	} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(configDir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) (string, error) {
		ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		cmd := exec.CommandContext(ctx, binary, append([]string{"-session", "query-operation", "audit", "--target", "audit-fixture"}, args...)...)
		cmd.Env = append(os.Environ(), "DOOPS_CONFIG="+filepath.Join(configDir, "config.json"))
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	all, err := run("--json")
	if err != nil || !strings.Contains(all, `"session":"selected"`) || !strings.Contains(all, `"session":"other"`) {
		t.Fatalf("CLI aggregate query failed: out=%q err=%v", all, err)
	}
	selected, err := run("--session", "selected", "--blocked", "--limit", "1", "--json")
	var entry auditLogEntry
	if err != nil || json.Unmarshal([]byte(selected), &entry) != nil || entry.Session != "selected" || !entry.Blocked {
		t.Fatalf("CLI selected query failed: out=%q err=%v", selected, err)
	}
	bypassed, err := run("--bypassed", "--json")
	if err != nil || json.Unmarshal([]byte(bypassed), &entry) != nil || entry.Session != "other" || !entry.BypassedByScope {
		t.Fatalf("CLI bypassed query failed: out=%q err=%v", bypassed, err)
	}
	if out, err := run("--limit", "-1"); err == nil || !strings.Contains(out, "--limit must not be negative") {
		t.Fatalf("CLI accepted invalid limit: out=%q err=%v", out, err)
	}
	events, err := f.store.ListAudit(20)
	if err != nil {
		t.Fatal(err)
	}
	var queries int
	for _, event := range events {
		if strings.Contains(event.CommandSummary, "doops_audit_query") {
			queries++
			if event.Session != "query-operation" || event.Action != ActionRead || event.Status != "success" {
				t.Fatalf("query lost operation audit context: %+v", event)
			}
		}
	}
	if queries != 3 {
		t.Fatalf("expected three audited queries, got %d: %+v", queries, events)
	}
}
