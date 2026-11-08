package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// AuditSession selects the records; session_id remains the RPC operation context.
type auditQueryParams struct {
	AuditSession string `json:"audit_session,omitempty"`
	Blocked      bool   `json:"blocked,omitempty"`
	Bypassed     bool   `json:"bypassed,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	Format       string `json:"format,omitempty"`
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

// Both execution logs and workspace guardrail logs contribute their own records.
// An empty audit_session or "*" selects every session on the authorized target.
func handleAuditQuery(argBytes json.RawMessage) (string, error) {
	return handleAuditQueryAt(argBytes, auditLogRoot, workspaceRoot())
}

func handleAuditQueryAt(argBytes json.RawMessage, executionRoot, blockedRoot string) (string, error) {
	var args auditQueryParams
	if err := json.Unmarshal(argBytes, &args); err != nil {
		return "", fmt.Errorf("invalid audit query params: %w", err)
	}
	if args.Limit < 0 {
		return "", fmt.Errorf("audit limit must not be negative")
	}
	format := strings.ToLower(strings.TrimSpace(args.Format))
	if format != "" && format != "text" && format != "jsonl" {
		return "", fmt.Errorf("audit format must be text or jsonl")
	}
	entries, err := collectAuditEntriesAt(strings.TrimSpace(args.AuditSession), executionRoot, blockedRoot)
	if err != nil {
		return "", err
	}
	entries = filterAuditEntries(entries, args.Blocked, args.Bypassed)
	if args.Limit > 0 && len(entries) > args.Limit {
		entries = entries[len(entries)-args.Limit:]
	}

	if format == "jsonl" {
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

// Missing audit files mean no records. Other I/O errors must be visible.
func collectAuditEntries(sessionID string) ([]auditLogEntry, error) {
	return collectAuditEntriesAt(sessionID, auditLogRoot, workspaceRoot())
}

func collectAuditEntriesAt(sessionID, executionRoot, blockedRoot string) ([]auditLogEntry, error) {
	if sessionID != "" && sessionID != "*" {
		if err := validateSession(sessionID); err != nil {
			return nil, err
		}
	}
	var out []auditLogEntry
	for _, source := range []struct {
		path      string
		workspace bool
	}{{executionRoot, false}, {blockedRoot, true}} {
		entries, err := readAuditRoot(sessionID, source.path, source.workspace)
		if err != nil {
			return nil, err
		}
		out = append(out, entries...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Timestamp != out[j].Timestamp {
			return out[i].Timestamp < out[j].Timestamp
		}
		return out[i].Session < out[j].Session
	})
	return out, nil
}

func readAuditRoot(sessionID, path string, workspace bool) ([]auditLogEntry, error) {
	root, err := os.OpenRoot(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, auditQueryIOError("open audit root", err)
	}
	defer root.Close()
	if sessionID != "" && sessionID != "*" {
		return readAuditSession(root, sessionID, workspace)
	}
	directory, err := root.Open(".")
	if err != nil {
		return nil, auditQueryIOError("open audit directory", err)
	}
	defer directory.Close()
	files, err := directory.ReadDir(-1)
	if err != nil {
		return nil, auditQueryIOError("list audit sessions", err)
	}
	var entries []auditLogEntry
	for _, file := range files {
		name := file.Name()
		if workspace {
			if !file.IsDir() && file.Type()&os.ModeSymlink == 0 {
				continue
			}
		} else {
			if !strings.HasSuffix(name, ".log") {
				continue
			}
			name = strings.TrimSuffix(name, ".log")
		}
		if validateSession(name) != nil {
			continue
		}
		current, err := readAuditSession(root, name, workspace)
		if err != nil {
			return nil, err
		}
		entries = append(entries, current...)
	}
	return entries, nil
}

func readAuditSession(root *os.Root, sessionID string, workspace bool) ([]auditLogEntry, error) {
	name := sessionID + ".log"
	if workspace {
		info, err := root.Lstat(sessionID)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, auditQueryIOError("inspect audit session", err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("audit_query_unavailable: audit session must be a directory without symlinks")
		}
		scoped, err := root.OpenRoot(sessionID)
		if err != nil {
			return nil, auditQueryIOError("open audit session", err)
		}
		defer scoped.Close()
		root = scoped
		name = ".doops-audit-log"
	}
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, auditQueryIOError("inspect audit file", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("audit_query_unavailable: audit file must be regular and without symlinks")
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, auditQueryIOError("open audit file", err)
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil {
		return nil, auditQueryIOError("inspect open audit file", err)
	}
	if !actual.Mode().IsRegular() {
		return nil, fmt.Errorf("audit_query_unavailable: opened audit file must be regular")
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, auditQueryIOError("read audit file", err)
	}
	return parseAuditLog(string(data), sessionID), nil
}

func auditQueryIOError(operation string, err error) error {
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		err = pathError.Err
	}
	return fmt.Errorf("audit_query_unavailable: %s: %w", operation, err)
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
