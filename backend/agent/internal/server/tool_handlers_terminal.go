package server

// doops_terminal 的 Agent 端实现：把 PTY 会话的生命周期暴露成普通工具调用。
//
// 语义约定：
//   - op=open  启动 shell，返回 term 与初始 cursor；
//   - op=input 把 base64 字节原样写进 PTY（键盘输入、粘贴、Ctrl-C 都是字节流）；
//   - op=read  从 cursor 起取新输出，返回新的 cursor；输出已 base64 编码，
//     避免多字节字符被 JSON 字符串编码破坏；
//   - op=resize 更新 PTY 尺寸；op=close 结束会话。

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"unicode"

	"github.com/user/doops/agent/api"
	"github.com/user/doops/agent/internal/dispatcher"
)

const terminalInputSourceAISuggestion = "ai_suggestion"

// terminalToolParams 扩展 api.TerminalParams，支持长轮询 wait_ms 与输入来源标记。
type terminalToolParams struct {
	api.TerminalParams
	WaitMS       int    `json:"wait_ms,omitempty"`
	Source       string `json:"source,omitempty"`
	SuggestionID string `json:"suggestion_id,omitempty"`
}

// normalizeTerminalSuggestionCommand 从 AI 建议插入的 PTY 字节流中剥离
// bracketed paste 包装与控制前缀，还原待校验的命令文本。
func normalizeTerminalSuggestionCommand(payload []byte) string {
	text := string(payload)
	text = strings.ReplaceAll(text, "\x1b[200~", "")
	text = strings.ReplaceAll(text, "\x1b[201~", "")
	text = strings.TrimLeft(text, "\x15") // Ctrl-U 清行前缀
	return strings.TrimSpace(strings.TrimRight(text, "\r\n"))
}

func sanitizeTerminalSuggestionID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range trimmed {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
			if b.Len() >= 64 {
				break
			}
		}
	}
	return b.String()
}

func (gw *Gateway) handleToolTerminal(call toolInvocation) {
	reqID, writeJSON := call.reqID, call.writeJSON
	var args terminalToolParams
	if err := json.Unmarshal(call.args, &args); err != nil {
		writeJSON(buildErrorResponse(reqID, -32603, err.Error()))
		return
	}

	sessionID := strings.TrimSpace(args.SessionID)
	if sessionID == "" {
		sessionID = call.sessionID
	}
	if sessionID == "" {
		writeJSON(buildErrorResponse(reqID, -32602, "session_id is required for terminal calls"))
		return
	}

	if gw.terminals == nil {
		writeJSON(buildErrorResponse(reqID, -32603, "terminal support is not initialized"))
		return
	}

	switch strings.ToLower(strings.TrimSpace(args.Op)) {
	case "open":
		session, err := gw.terminals.open(sessionID, args.Cols, args.Rows)
		if err != nil {
			writeJSON(buildErrorResponse(reqID, -32603, err.Error()))
			return
		}
		writeJSON(buildSuccessResponse(reqID, terminalJSON(map[string]interface{}{
			"ok":       true,
			"term":     session.id,
			"cursor":   session.base,
			"cols":     session.cols,
			"rows":     session.rows,
			"alive":    true,
			"cwd":      session.initialCwd,
			"features": terminalFeatures(session.shellIntegration),
		})))

	case "input":
		payload, err := base64.StdEncoding.DecodeString(args.Data)
		if err != nil {
			writeJSON(buildErrorResponse(reqID, -32602, "data must be base64 encoded"))
			return
		}
		source := strings.ToLower(strings.TrimSpace(args.Source))
		suggestionID := sanitizeTerminalSuggestionID(args.SuggestionID)
		if source == terminalInputSourceAISuggestion {
			cmdText := normalizeTerminalSuggestionCommand(payload)
			if cmdText != "" {
				if err := dispatcher.Check(cmdText); err != nil {
					if call.writePolicyDenial != nil && call.writePolicyDenial(err) {
						return
					}
					writeJSON(buildErrorResponse(reqID, -32003, err.Error()))
					return
				}
			}
		}
		if err := gw.terminals.write(sessionID, args.Term, payload); err != nil {
			writeJSON(buildErrorResponse(reqID, -32603, err.Error()))
			return
		}
		if summary := terminalAuditSummary(payload); summary != "" {
			prefix := "terminal input: "
			if source == terminalInputSourceAISuggestion {
				if suggestionID != "" {
					prefix = "terminal input[ai_suggestion:" + suggestionID + "]: "
				} else {
					prefix = "terminal input[ai_suggestion]: "
				}
			}
			writeSessionCommandAudit(sessionID, prefix+summary, nil)
		}
		writeJSON(buildSuccessResponse(reqID, terminalJSON(map[string]interface{}{
			"ok":   true,
			"term": args.Term,
		})))

	case "read":
		cursor := args.Cursor
		if cursor < 0 {
			cursor = 0
		}
		session, lookupErr := gw.terminals.lookup(sessionID, args.Term)
		if lookupErr != nil {
			writeJSON(buildErrorResponse(reqID, -32603, lookupErr.Error()))
			return
		}
		wait := clampTerminalReadWait(args.WaitMS)
		var (
			data      []byte
			next      int64
			truncated bool
			alive     bool
			code      int
		)
		if wait > 0 {
			data, next, truncated, alive, code = session.readWait(call.ctx, cursor, wait)
		} else {
			data, next, truncated, alive, code = session.readFrom(cursor)
		}
		writeJSON(buildSuccessResponse(reqID, terminalJSON(map[string]interface{}{
			"ok":        true,
			"term":      args.Term,
			"cursor":    next,
			"data":      base64.StdEncoding.EncodeToString(data),
			"alive":     alive,
			"exit":      code,
			"truncated": truncated,
			"features":  terminalFeatures(session.shellIntegration),
		})))

	case "resize":
		if err := gw.terminals.resize(sessionID, args.Term, args.Cols, args.Rows); err != nil {
			writeJSON(buildErrorResponse(reqID, -32603, err.Error()))
			return
		}
		writeJSON(buildSuccessResponse(reqID, terminalJSON(map[string]interface{}{
			"ok":   true,
			"term": args.Term,
		})))

	case "close":
		if err := gw.terminals.close(sessionID, args.Term); err != nil {
			writeJSON(buildErrorResponse(reqID, -32603, err.Error()))
			return
		}
		writeJSON(buildSuccessResponse(reqID, terminalJSON(map[string]interface{}{
			"ok":   true,
			"term": args.Term,
		})))

	default:
		writeJSON(buildErrorResponse(reqID, -32602, "unsupported terminal op: use open|input|read|resize|close"))
	}
}

func terminalJSON(payload map[string]interface{}) string {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "{\"ok\":false,\"error\":\"encode failed\"}"
	}
	return string(encoded)
}

// terminalAuditSummary 只把可见字符写进审计，避免控制序列污染审计日志。
func terminalAuditSummary(payload []byte) string {
	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" {
		return ""
	}
	cleaned := strings.Map(func(r rune) rune {
		if r == 10 || r == 9 { // \n 与 \t 保留为空格分隔
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, trimmed)
	if runes := []rune(cleaned); len(runes) > 200 {
		cleaned = string(runes[:200])
	}
	return strings.TrimSpace(cleaned)
}
