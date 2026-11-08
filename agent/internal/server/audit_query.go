package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// auditQueryParams 是 doops_audit_query 工具的入参。
// 全部字段可选（omitempty），旧 CLI/旧 gateway 不携带这些字段时行为等同于全量查询。
type auditQueryParams struct {
	SessionID string `json:"session_id,omitempty"`
	Blocked   bool   `json:"blocked,omitempty"`
	Bypassed  bool   `json:"bypassed,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Format    string `json:"format,omitempty"`
}

// auditLogEntry 是 .doops-audit-log 中一条记录的结构化视图。
type auditLogEntry struct {
	Timestamp       string `json:"ts"`
	Session         string `json:"session"`
	Action          string `json:"action"`
	Command         string `json:"command"`
	ExitCode        *int   `json:"exit_code,omitempty"`
	Blocked         bool   `json:"blocked"`
	BypassedByScope bool   `json:"bypassed_by_scope"`
	Rule            string `json:"rule,omitempty"`

	raw  string   // 原始记录文本（text 输出时无损回放）
	body []string // header 之后的命令行
}

// handleAuditQuery 实现 doops_audit_query：读取 session 工作区的 .doops-audit-log，
// 支持 blocked / bypassed 过滤与 JSONL 导出。session_id 为空或 "*" 时聚合所有 session。
func handleAuditQuery(argBytes json.RawMessage) (string, error) {
	var args auditQueryParams
	if err := json.Unmarshal(argBytes, &args); err != nil {
		return "", fmt.Errorf("invalid audit query params: %w", err)
	}
	entries, err := collectAuditEntries(strings.TrimSpace(args.SessionID))
	if err != nil {
		return "", err
	}
	entries = filterAuditEntries(entries, args.Blocked, args.Bypassed)
	if args.Limit > 0 && len(entries) > args.Limit {
		entries = entries[len(entries)-args.Limit:]
	}

	if strings.EqualFold(strings.TrimSpace(args.Format), "jsonl") {
		var b strings.Builder
		for _, entry := range entries {
			line, err := json.Marshal(entry)
			if err != nil {
				return "", fmt.Errorf("marshal audit entry: %w", err)
			}
			b.Write(line)
			b.WriteByte('\n')
		}
		return b.String(), nil
	}

	if len(entries) == 0 {
		return "no matching audit records", nil
	}
	var b strings.Builder
	for _, entry := range entries {
		b.WriteString(entry.raw)
		if !strings.HasSuffix(entry.raw, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String(), nil
}

// collectAuditEntries 收集指定 session（或全部 session）的审计记录。
// 审计文件不存在视为空集，不视为错误。
func collectAuditEntries(sessionID string) ([]auditLogEntry, error) {
	if sessionID != "" && sessionID != "*" {
		wsPath, err := workspacePath(sessionID)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(filepath.Join(wsPath, ".doops-audit-log"))
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return parseAuditLog(string(data), sessionID), nil
	}

	root := workspaceRoot()
	dirEntries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []auditLogEntry
	for _, dirEntry := range dirEntries {
		if !dirEntry.IsDir() || validateSession(dirEntry.Name()) != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, dirEntry.Name(), ".doops-audit-log"))
		if err != nil {
			continue
		}
		out = append(out, parseAuditLog(string(data), dirEntry.Name())...)
	}
	return out, nil
}

// filterAuditEntries 按 blocked / bypassed 标记过滤；两个条件同时给出时取交集。
func filterAuditEntries(entries []auditLogEntry, blocked, bypassed bool) []auditLogEntry {
	if !blocked && !bypassed {
		return entries
	}
	out := make([]auditLogEntry, 0, len(entries))
	for _, entry := range entries {
		if blocked && !entry.Blocked {
			continue
		}
		if bypassed && !entry.BypassedByScope {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// parseAuditLog 解析 .doops-audit-log 文本：每条记录为 `# [ts] k=v ...` header
// 加随后命令体；kubectl apply 配置快照块（--- BEGIN/END ---）会被跳过。
func parseAuditLog(content, session string) []auditLogEntry {
	var entries []auditLogEntry
	var cur *auditLogEntry
	inSnapshot := false
	flush := func() {
		if cur == nil {
			return
		}
		cur.Command = strings.TrimRight(strings.Join(cur.body, "\n"), "\n")
		entries = append(entries, *cur)
		cur = nil
	}

	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "--- BEGIN ") {
			inSnapshot = true
			continue
		}
		if inSnapshot {
			if strings.HasPrefix(line, "--- END ") {
				inSnapshot = false
			}
			continue
		}
		if ts, kvs, ok := parseAuditHeader(line); ok {
			flush()
			cur = &auditLogEntry{Timestamp: ts, Session: session, Action: "exec", raw: line}
			for _, kv := range kvs {
				switch kv[0] {
				case "blocked":
					cur.Blocked = kv[1] == "1"
				case "bypassed_by_scope":
					cur.BypassedByScope = kv[1] == "1"
				case "rule":
					cur.Rule = kv[1]
				case "exit":
					if n, err := strconv.Atoi(kv[1]); err == nil {
						cur.ExitCode = &n
					}
				}
			}
			continue
		}
		if cur != nil {
			cur.body = append(cur.body, line)
			cur.raw += "\n" + line
		}
	}
	flush()
	return entries
}

// parseAuditHeader 解析记录头 `# [ts] key=value ...`，并把两种历史时间格式
// （RFC3339 与 "2006-01-02 15:04:05"，后者按 UTC 解释）统一规范化为 RFC3339。
func parseAuditHeader(line string) (string, [][2]string, bool) {
	if !strings.HasPrefix(line, "# [") {
		return "", nil, false
	}
	end := strings.Index(line, "]")
	if end < 0 {
		return "", nil, false
	}
	ts := line[len("# ["):end]
	var kvs [][2]string
	for _, field := range strings.Fields(line[end+1:]) {
		if i := strings.IndexByte(field, '='); i > 0 {
			kvs = append(kvs, [2]string{field[:i], field[i+1:]})
		}
	}
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		ts = t.UTC().Format(time.RFC3339)
	} else if t, err := time.ParseInLocation("2006-01-02 15:04:05", ts, time.UTC); err == nil {
		ts = t.Format(time.RFC3339)
	}
	return ts, kvs, true
}
