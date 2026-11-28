package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgentHistoryUnwrapsDoagentResultPreservesLongTextAndStripsInjectedContext(t *testing.T) {
	t.Setenv("DOOPS_WORKSPACE_ROOT", t.TempDir())
	longAnswer := "BEGIN_LONG_ANSWER:" + strings.Repeat("诊断详情一行内容。\n", 260) + ":END_LONG_ANSWER"
	if len(longAnswer) <= 3000 {
		t.Fatalf("test fixture answer must exceed legacy 1200-byte truncation limit, got %d", len(longAnswer))
	}

	doagent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode rpc: %v", err)
		}
		if req["method"] != "session/messages" {
			t.Fatalf("unexpected method: %v", req["method"])
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"result": map[string]interface{}{
				"sessionId":    "console-hist01",
				"source":       "file",
				"messageCount": 4,
				"messages": []interface{}{
					map[string]interface{}{
						"role":    "system",
						"content": "INTERNAL_SYSTEM_PROMPT_MUST_NOT_LEAK",
					},
					map[string]interface{}{
						"role":       "user",
						"content":    "\n当前目标清单版本 4，接入模式 linux，Kubernetes API 可用：false。详细元数据可用 Read 读取工作区 target.json。\n- machine 1 项：node-1\n\n# 当前 target 运行时\n\n- Agent 进程：Linux 主机原生进程\n- Kubernetes API：不可用\n- 容器运行时 CLI：docker\n\n检查一下宿主机磁盘和 CPU 水位\n\nGateway-managed credential bindings (metadata only; never read Secret values). Use name or credential_id as credential_ref; grant_id and version_id are audit metadata:\n[{\"name\":\"prod-db\"}]",
						"created_at": int64(1764240000000),
					},
					map[string]interface{}{
						"role":    "assistant",
						"content": longAnswer + "\nAuthorization: Bearer secret-history-bearer-canary",
						"tool_calls": []interface{}{
							map[string]interface{}{
								"id":   "call_1",
								"type": "function",
								"function": map[string]interface{}{
									"name":      "bash",
									"arguments": `{"command":"df -h"}`,
								},
							},
						},
						"created_at": int64(1764240005000),
					},
					map[string]interface{}{
						"role":         "tool",
						"tool_call_id": "call_1",
						"content":      "Filesystem Size Used Avail Use% Mounted on\n/dev/sda1 100G 42G 58G 42% /",
						"created_at":   int64(1764240006000),
					},
				},
			},
		})
	}))
	defer doagent.Close()
	t.Setenv("DO_AGENT_URL", doagent.URL)

	gw := NewGateway()
	// Intentionally leave gw.sessionMap empty to simulate 2h TTL expiry or Agent restart.
	raw, err := gw.handleAgentHistoryWS(context.Background(), "console-hist01")
	if err != nil {
		t.Fatalf("handleAgentHistoryWS: %v", err)
	}
	if strings.Contains(raw, "INTERNAL_SYSTEM_PROMPT_MUST_NOT_LEAK") {
		t.Fatalf("system prompt leaked in history response: %s", raw)
	}
	if strings.Contains(raw, "secret-history-bearer-canary") {
		t.Fatalf("bearer token leaked in history response: %s", raw)
	}

	var payload struct {
		Source        string                   `json:"source"`
		HistorySchema string                   `json:"history_schema"`
		SessionID     string                   `json:"sessionId"`
		TotalMessages int                      `json:"total_messages"`
		Truncated     bool                     `json:"truncated"`
		Messages      []map[string]interface{} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("unmarshal history: %v\nraw=%s", err, raw)
	}
	if payload.HistorySchema != doagentHistorySchema || payload.SessionID != "console-hist01" {
		t.Fatalf("unexpected header: %+v", payload)
	}
	if payload.TotalMessages != 3 || len(payload.Messages) != 3 || payload.Truncated {
		t.Fatalf("expected 3 unwrapped non-system messages, got total=%d len=%d truncated=%v", payload.TotalMessages, len(payload.Messages), payload.Truncated)
	}
	if gotUser, _ := payload.Messages[0]["content"].(string); gotUser != "检查一下宿主机磁盘和 CPU 水位" {
		t.Fatalf("user prompt did not strip injected context cleanly: %q", gotUser)
	}
	gotAssistant, _ := payload.Messages[1]["content"].(string)
	if !strings.HasPrefix(gotAssistant, "BEGIN_LONG_ANSWER:") || !strings.Contains(gotAssistant, ":END_LONG_ANSWER") {
		t.Fatalf("long assistant message was truncated (len=%d)", len(gotAssistant))
	}
	if !strings.Contains(gotAssistant, "REDACTED") {
		t.Fatalf("expected redacted bearer marker in assistant content")
	}
}

func TestAgentHistoryFallsBackToDiskWhenDoagentDBLostSession(t *testing.T) {
	wsRoot := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", wsRoot)
	sessionID := "console-diskfb"
	msgFile := filepath.Join(wsRoot, sessionID, ".agent", "sessions", sessionID, "messages.json")
	if err := os.MkdirAll(filepath.Dir(msgFile), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	diskPayload := `[
		{"role":"system","content":"sys"},
		{"role":"user","content":"查看集群节点状态","created_at":1764241000000},
		{"role":"assistant","content":"所有节点 Ready","created_at":1764241002000}
	]`
	if err := os.WriteFile(msgFile, []byte(diskPayload), 0600); err != nil {
		t.Fatalf("write messages.json: %v", err)
	}

	doagent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"error":   map[string]interface{}{"code": -32001, "message": "Session not found: " + sessionID},
		})
	}))
	defer doagent.Close()
	t.Setenv("DO_AGENT_URL", doagent.URL)

	raw, err := NewGateway().handleAgentHistoryWS(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("handleAgentHistoryWS disk fallback: %v", err)
	}
	var payload struct {
		Messages []map[string]interface{} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(payload.Messages) != 2 || payload.Messages[0]["content"] != "查看集群节点状态" || payload.Messages[1]["content"] != "所有节点 Ready" {
		t.Fatalf("unexpected disk fallback messages: %+v", payload.Messages)
	}
}

func TestAgentSessionsListsPersistedAndLiveSessionsFilteringNoiseAndRedactingSecrets(t *testing.T) {
	wsRoot := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", wsRoot)

	writeSession := func(sid string, raw string) {
		t.Helper()
		p := filepath.Join(wsRoot, sid, ".agent", "sessions", sid, "messages.json")
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(raw), 0600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	// Valid older session
	writeSession("console-old1", `[
		{"role":"system","content":"sys"},
		{"role":"user","content":"排查 nginx 502 错误 password=super-secret-canary","created_at":1764200000000},
		{"role":"assistant","content":"已定位上游超时","created_at":1764200010000}
	]`)
	// Valid newer custom-named session (not console- prefix)
	writeSession("deploy-check", `[
		{"role":"user","content":"核对 orders 服务副本数","created_at":1764250000000},
		{"role":"assistant","content":"3/3 Ready","created_at":1764250005000}
	]`)
	// Internal one-shot machine JSON session (must be excluded)
	writeSession("console-x-json-1764250000000-1", `[
		{"role":"user","content":"json probe","created_at":1764260000000},
		{"role":"assistant","content":"{}","created_at":1764260001000}
	]`)
	// System-only empty session (must be excluded)
	writeSession("console-empty", `[{"role":"system","content":"sys only"}]`)

	// Mock doagent session/list returning a live session not yet flushed to disk.
	doagent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["method"] != "session/list" {
			t.Fatalf("unexpected method: %v", req["method"])
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"result": map[string]interface{}{
				"sessions": []interface{}{
					map[string]interface{}{
						"id":           "console-live9",
						"title":        "实时排障会话",
						"messageCount": 2,
						"updatedAt":    int64(1764270000),
					},
				},
			},
		})
	}))
	defer doagent.Close()
	t.Setenv("DO_AGENT_URL", doagent.URL)

	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	got := callTool(t, conn, "doops_agent_prompt", map[string]interface{}{
		"session_id": "console-list-caller",
		"mode":       "sessions",
	})
	if strings.Contains(got, "super-secret-canary") {
		t.Fatalf("secret leaked in sessions list: %s", got)
	}
	var payload struct {
		HistorySchema string                `json:"history_schema"`
		Sessions      []agentSessionSummary `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("unmarshal sessions: %v\nraw=%s", err, got)
	}
	if payload.HistorySchema != doagentHistorySchema {
		t.Fatalf("unexpected schema: %q", payload.HistorySchema)
	}
	if len(payload.Sessions) != 3 {
		t.Fatalf("want 3 visible sessions (live9, deploy-check, old1), got %d: %+v", len(payload.Sessions), payload.Sessions)
	}
	if payload.Sessions[0].SessionID != "console-live9" || payload.Sessions[1].SessionID != "deploy-check" || payload.Sessions[2].SessionID != "console-old1" {
		t.Fatalf("sessions not sorted newest-first: %+v", payload.Sessions)
	}
	if !strings.Contains(payload.Sessions[2].Title, "排查 nginx 502 错误") || !strings.Contains(payload.Sessions[2].Title, "REDACTED") {
		t.Fatalf("expected redacted title for old1, got %q", payload.Sessions[2].Title)
	}
}

func TestWorkspaceGCAndCleanWorkspacePreserveActiveAgentSessions(t *testing.T) {
	wsRoot := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", wsRoot)

	now := time.Now()
	// Session A: workspace artifact is 48h old (> 24h GC), session history is 48h old (<= 30d retention).
	wsA := filepath.Join(wsRoot, "console-keep")
	artifactA := filepath.Join(wsA, "build.log")
	sessionFileA := filepath.Join(wsA, ".agent", "sessions", "console-keep", "messages.json")
	uploadFileA := filepath.Join(wsA, ".agent", "uploads", "tmp.txt")
	for _, p := range []string{artifactA, sessionFileA, uploadFileA} {
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(`[{"role":"user","content":"hi"}]`), 0600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
		old := now.Add(-48 * time.Hour)
		_ = os.Chtimes(p, old, old)
	}
	old48 := now.Add(-48 * time.Hour)
	for _, p := range []string{
		filepath.Dir(sessionFileA),
		filepath.Join(wsA, ".agent", "sessions"),
		filepath.Join(wsA, ".agent", "uploads"),
		filepath.Join(wsA, ".agent"),
		wsA,
	} {
		_ = os.Chtimes(p, old48, old48)
	}

	// Session B: both workspace and session history are 40 days old (> 30d retention) -> must be fully removed.
	wsB := filepath.Join(wsRoot, "console-expired")
	sessionFileB := filepath.Join(wsB, ".agent", "sessions", "console-expired", "messages.json")
	if err := os.MkdirAll(filepath.Dir(sessionFileB), 0755); err != nil {
		t.Fatalf("mkdir B: %v", err)
	}
	if err := os.WriteFile(sessionFileB, []byte(`[{"role":"user","content":"old"}]`), 0600); err != nil {
		t.Fatalf("write B: %v", err)
	}
	old40d := now.Add(-40 * 24 * time.Hour)
	for _, p := range []string{sessionFileB, filepath.Dir(sessionFileB), filepath.Join(wsB, ".agent", "sessions"), filepath.Join(wsB, ".agent"), wsB} {
		_ = os.Chtimes(p, old40d, old40d)
	}

	performCleanupAt(now, 24*time.Hour, 30*24*time.Hour)

	if _, err := os.Stat(artifactA); !os.IsNotExist(err) {
		t.Fatalf("expired workspace artifact must be pruned at 24h")
	}
	if _, err := os.Stat(uploadFileA); !os.IsNotExist(err) {
		t.Fatalf("expired .agent/uploads file must be pruned at 24h")
	}
	if _, err := os.Stat(sessionFileA); err != nil {
		t.Fatalf("active .agent/sessions history must survive 24h workspace GC: %v", err)
	}
	if _, err := os.Stat(wsB); !os.IsNotExist(err) {
		t.Fatalf("session older than 30d retention must be completely removed")
	}

	// Explicit doops_clean_workspace must also remove artifacts while preserving .agent/sessions.
	extraArtifact := filepath.Join(wsA, "deploy.yaml")
	if err := os.WriteFile(extraArtifact, []byte("kind: Pod"), 0600); err != nil {
		t.Fatalf("write extraArtifact: %v", err)
	}
	if _, err := handleCleanWorkspace(json.RawMessage(`{"workspace":"console-keep"}`)); err != nil {
		t.Fatalf("handleCleanWorkspace: %v", err)
	}
	if _, err := os.Stat(extraArtifact); !os.IsNotExist(err) {
		t.Fatalf("clean workspace must remove user workspace files")
	}
	if _, err := os.Stat(sessionFileA); err != nil {
		t.Fatalf("clean workspace must preserve .agent/sessions history: %v", err)
	}
}
