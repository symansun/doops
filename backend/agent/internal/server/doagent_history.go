package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	doagentHistorySchema           = "doops.sh/agent-history/v1"
	doagentHistoryMaxBytes         = 1024 * 1024     // 1 MiB serialized budget per history read
	doagentHistoryMaxFieldBytes    = 128 * 1024      // 128 KiB prefix cap per text field
	doagentHistoryMaxDiskFileBytes = 8 * 1024 * 1024 // 8 MiB cap when reading messages.json from disk
	doagentSessionsListLimit       = 50
	doagentSessionTitleMaxRunes    = 80
	doagentSessionPreviewMaxRunes  = 120
)

var doagentMachineJSONSessionPattern = regexp.MustCompile(`-json-\d+-\d+$`)

type agentSessionSummary struct {
	SessionID          string `json:"session_id"`
	Title              string `json:"title"`
	LastMessagePreview string `json:"last_message_preview,omitempty"`
	MessageCount       int    `json:"message_count"`
	TurnCount          int    `json:"turn_count"`
	CreatedAt          string `json:"created_at,omitempty"`
	UpdatedAt          string `json:"updated_at,omitempty"`
}

func isInternalMachineJSONSession(sessionID string) bool {
	return doagentMachineJSONSessionPattern.MatchString(strings.TrimSpace(sessionID))
}

func (gw *Gateway) handleAgentHistoryWS(ctx context.Context, doopsSessionID string) (string, error) {
	if err := validateSession(doopsSessionID); err != nil {
		return "", err
	}

	targetSessionID := doopsSessionID
	gw.sessionMapMu.RLock()
	if entry := gw.sessionMap[doopsSessionID]; entry != nil && strings.TrimSpace(entry.doagentSessionID) != "" {
		targetSessionID = strings.TrimSpace(entry.doagentSessionID)
	}
	gw.sessionMapMu.RUnlock()

	var (
		messages    []interface{}
		rpcErr      error
		rpcNotFound bool
	)

	doagentURL := configuredDoagentURL()
	if availErr := ensureDoagentAvailable(doagentURL); availErr == nil {
		resp, err := doagentRPCContext(ctx, doagentURL, map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      "history-" + doopsSessionID,
			"method":  "session/messages",
			"params": map[string]interface{}{
				"sessionId": targetSessionID,
			},
		})
		if err != nil {
			if isDoagentSessionNotFound(resp, err) {
				rpcNotFound = true
			} else {
				rpcErr = err
			}
		} else {
			messages = extractDoagentHistoryMessages(resp["result"])
		}
	} else {
		rpcErr = availErr
	}

	// Fallback to persisted file-backed messages when doagent returned not found,
	// empty (e.g. SQLite was recreated while /root/ws survived), or was unavailable.
	if len(messages) == 0 {
		diskMessages, diskFound := readPersistedDoagentMessages(doopsSessionID, targetSessionID)
		if diskFound {
			messages = diskMessages
			rpcErr = nil
		}
	}

	if rpcErr != nil && !rpcNotFound {
		return "", errors.New("doagent_history_unavailable")
	}
	return marshalDoagentHistory(doopsSessionID, messages)
}

func (gw *Gateway) handleAgentSessionsWS(ctx context.Context, callerSessionID string) (string, error) {
	_ = callerSessionID
	byID := scanPersistedAgentSessions(workspaceRoot())

	// Best-effort merge with live in-memory doagent sessions so active sessions
	// not yet flushed to disk also appear. Failures here never hide disk sessions.
	doagentURL := configuredDoagentURL()
	if doagentTCPReady(doagentURL, 300*time.Millisecond) {
		if resp, err := doagentRPCContext(ctx, doagentURL, map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      "sessions-list",
			"method":  "session/list",
			"params":  map[string]interface{}{},
		}); err == nil {
			mergeLiveDoagentSessions(byID, resp["result"])
		}
	}

	list := make([]agentSessionSummary, 0, len(byID))
	for _, item := range byID {
		if item.SessionID == "" || isInternalMachineJSONSession(item.SessionID) {
			continue
		}
		if item.MessageCount <= 0 && item.TurnCount <= 0 {
			continue
		}
		list = append(list, item)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].UpdatedAt != list[j].UpdatedAt {
			return list[i].UpdatedAt > list[j].UpdatedAt
		}
		return list[i].SessionID < list[j].SessionID
	})
	if len(list) > doagentSessionsListLimit {
		list = list[:doagentSessionsListLimit]
	}

	data, err := json.Marshal(map[string]interface{}{
		"source":         "doagent",
		"history_schema": doagentHistorySchema,
		"sessions":       list,
	})
	if err != nil {
		return "", fmt.Errorf("marshal doagent sessions: %w", err)
	}
	return string(data), nil
}

func isDoagentSessionNotFound(resp map[string]interface{}, err error) bool {
	if resp != nil {
		if rpcErr, ok := resp["error"].(map[string]interface{}); ok {
			switch code := rpcErr["code"].(type) {
			case float64:
				if int(code) == -32001 {
					return true
				}
			case int:
				if code == -32001 {
					return true
				}
			case int64:
				if code == -32001 {
					return true
				}
			}
			if msg, ok := rpcErr["message"].(string); ok && strings.Contains(strings.ToLower(msg), "session not found") {
				return true
			}
		}
	}
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "session not found")
}

// extractDoagentHistoryMessages unwraps both doagent's structured result
// ({sessionId, source, messageCount, messages: [...]}) and a bare message array.
func extractDoagentHistoryMessages(raw interface{}) []interface{} {
	switch typed := raw.(type) {
	case []interface{}:
		return typed
	case map[string]interface{}:
		if inner, ok := typed["messages"].([]interface{}); ok {
			return inner
		}
	}
	return nil
}

func readPersistedDoagentMessages(doopsSessionID, targetSessionID string) ([]interface{}, bool) {
	candidates := []string{targetSessionID}
	if doopsSessionID != targetSessionID {
		candidates = append(candidates, doopsSessionID)
	}
	base, err := os.OpenRoot(workspaceRoot())
	if err != nil {
		return nil, false
	}
	defer base.Close()
	info, err := base.Lstat(doopsSessionID)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, false
	}
	scoped, err := base.OpenRoot(doopsSessionID)
	if err != nil {
		return nil, false
	}
	defer scoped.Close()

	for _, sid := range candidates {
		if validateSession(sid) != nil {
			continue
		}
		relPath := filepath.Join(".agent", "sessions", sid, "messages.json")
		messages, _, ok := readMessagesFileFromRoot(scoped, relPath)
		if ok {
			return messages, true
		}
	}
	return nil, false
}

func readMessagesFileFromRoot(root *os.Root, relPath string) ([]interface{}, time.Time, bool) {
	info, err := root.Lstat(relPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > doagentHistoryMaxDiskFileBytes {
		return nil, time.Time{}, false
	}
	f, err := root.Open(relPath)
	if err != nil {
		return nil, time.Time{}, false
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, doagentHistoryMaxDiskFileBytes+1))
	if err != nil || int64(len(raw)) > doagentHistoryMaxDiskFileBytes {
		return nil, time.Time{}, false
	}
	var messages []interface{}
	if err := json.Unmarshal(raw, &messages); err != nil {
		return nil, time.Time{}, false
	}
	return messages, info.ModTime().UTC(), true
}

func scanPersistedAgentSessions(wsRoot string) map[string]agentSessionSummary {
	out := make(map[string]agentSessionSummary)
	base, err := os.OpenRoot(wsRoot)
	if err != nil {
		return out
	}
	defer base.Close()
	entries, err := os.ReadDir(wsRoot)
	if err != nil {
		return out
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || name == ".global" || validateSession(name) != nil || isInternalMachineJSONSession(name) {
			continue
		}
		info, err := base.Lstat(name)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			continue
		}
		scoped, err := base.OpenRoot(name)
		if err != nil {
			continue
		}
		func() {
			defer scoped.Close()
			sessionsDir := filepath.Join(wsRoot, name, ".agent", "sessions")
			sessionEntries, readErr := os.ReadDir(sessionsDir)
			if readErr != nil {
				return
			}
			for _, se := range sessionEntries {
				sid := se.Name()
				if !se.IsDir() || validateSession(sid) != nil || isInternalMachineJSONSession(sid) {
					continue
				}
				relPath := filepath.Join(".agent", "sessions", sid, "messages.json")
				messages, modTime, ok := readMessagesFileFromRoot(scoped, relPath)
				if !ok || len(messages) == 0 {
					continue
				}
				summary, visible := summarizePersistedSession(sid, messages, modTime)
				if !visible {
					continue
				}
				if existing, exists := out[sid]; !exists || summary.UpdatedAt > existing.UpdatedAt {
					out[sid] = summary
				}
			}
		}()
	}
	return out
}

func summarizePersistedSession(sessionID string, rawMessages []interface{}, fallbackModTime time.Time) (agentSessionSummary, bool) {
	summary := agentSessionSummary{SessionID: sessionID}
	var earliestMS, latestMS int64
	for _, item := range rawMessages {
		msg, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(stringField(msg, "role")))
		if role == "" || role == "system" {
			continue
		}
		if createdAtMS := int64Field(msg, "created_at"); createdAtMS > 0 {
			if earliestMS == 0 || createdAtMS < earliestMS {
				earliestMS = createdAtMS
			}
			if createdAtMS > latestMS {
				latestMS = createdAtMS
			}
		}
		switch role {
		case "user":
			text := stripDoopsInjectedUserContext(extractChatMessageText(msg))
			redacted := collapseWhitespace(redactSecretText(text))
			summary.TurnCount++
			summary.MessageCount++
			if summary.Title == "" && redacted != "" {
				summary.Title = truncateRunes(redacted, doagentSessionTitleMaxRunes)
			}
			if redacted != "" {
				summary.LastMessagePreview = truncateRunes(redacted, doagentSessionPreviewMaxRunes)
			}
		case "assistant":
			text := extractChatMessageText(msg)
			redacted := collapseWhitespace(redactSecretText(text))
			hasToolCalls := false
			if calls, ok := msg["tool_calls"].([]interface{}); ok && len(calls) > 0 {
				hasToolCalls = true
			}
			if redacted != "" || hasToolCalls {
				summary.MessageCount++
			}
			if redacted != "" {
				summary.LastMessagePreview = truncateRunes(redacted, doagentSessionPreviewMaxRunes)
			}
		}
	}
	if summary.MessageCount == 0 && summary.TurnCount == 0 {
		return summary, false
	}
	if summary.Title == "" {
		summary.Title = sessionID
	}
	if earliestMS > 0 {
		summary.CreatedAt = time.UnixMilli(earliestMS).UTC().Format(time.RFC3339)
	} else if !fallbackModTime.IsZero() {
		summary.CreatedAt = fallbackModTime.UTC().Format(time.RFC3339)
	}
	if latestMS > 0 {
		summary.UpdatedAt = time.UnixMilli(latestMS).UTC().Format(time.RFC3339)
	} else if !fallbackModTime.IsZero() {
		summary.UpdatedAt = fallbackModTime.UTC().Format(time.RFC3339)
	}
	return summary, true
}

func mergeLiveDoagentSessions(byID map[string]agentSessionSummary, rawResult interface{}) {
	result, ok := rawResult.(map[string]interface{})
	if !ok {
		return
	}
	sessions, ok := result["sessions"].([]interface{})
	if !ok {
		return
	}
	for _, entry := range sessions {
		item, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		sid := strings.TrimSpace(stringField(item, "id"))
		if sid == "" || validateSession(sid) != nil || isInternalMachineJSONSession(sid) {
			continue
		}
		msgCount := int(int64Field(item, "messageCount"))
		if msgCount <= 0 {
			continue
		}
		title := collapseWhitespace(redactSecretText(strings.TrimSpace(stringField(item, "title"))))
		if title == "" {
			title = sid
		}
		createdAt := strings.TrimSpace(stringField(item, "createdAt"))
		updatedAt := ""
		if ts := int64Field(item, "updatedAt"); ts > 0 {
			if ts > 1_000_000_000_000 {
				updatedAt = time.UnixMilli(ts).UTC().Format(time.RFC3339)
			} else {
				updatedAt = time.Unix(ts, 0).UTC().Format(time.RFC3339)
			}
		}
		if updatedAt == "" {
			updatedAt = createdAt
		}
		existing, exists := byID[sid]
		if !exists {
			byID[sid] = agentSessionSummary{
				SessionID:    sid,
				Title:        truncateRunes(title, doagentSessionTitleMaxRunes),
				MessageCount: msgCount,
				TurnCount:    max(1, msgCount/2),
				CreatedAt:    createdAt,
				UpdatedAt:    updatedAt,
			}
			continue
		}
		if existing.Title == "" || existing.Title == sid {
			existing.Title = truncateRunes(title, doagentSessionTitleMaxRunes)
		}
		if updatedAt > existing.UpdatedAt {
			existing.UpdatedAt = updatedAt
		}
		if msgCount > existing.MessageCount {
			existing.MessageCount = msgCount
		}
		byID[sid] = existing
	}
}

func marshalDoagentHistory(doopsSessionID string, raw interface{}) (string, error) {
	items := extractDoagentHistoryMessages(raw)
	visible := make([]interface{}, 0, len(items))
	for _, item := range items {
		msg, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(stringField(msg, "role")))
		if role == "system" {
			continue
		}
		cleaned := sanitizeHistoryMessage(msg, role)
		if cleaned != nil {
			visible = append(visible, cleaned)
		}
	}

	totalMessages := len(visible)
	kept, truncated := boundHistoryMessages(visible, doagentHistoryMaxBytes)

	data, err := json.Marshal(map[string]interface{}{
		"source":         "doagent",
		"history_schema": doagentHistorySchema,
		"sessionId":      doopsSessionID,
		"total_messages": totalMessages,
		"truncated":      truncated,
		"messages":       kept,
	})
	if err != nil {
		return "", fmt.Errorf("marshal doagent history: %w", err)
	}
	return string(data), nil
}

func sanitizeHistoryMessage(msg map[string]interface{}, role string) map[string]interface{} {
	copied := make(map[string]interface{}, len(msg))
	for k, v := range msg {
		copied[k] = v
	}
	if role == "user" {
		if text, ok := copied["content"].(string); ok {
			copied["content"] = stripDoopsInjectedUserContext(text)
		}
		if parts, ok := copied["parts"].([]interface{}); ok {
			strippedParts := make([]interface{}, 0, len(parts))
			for idx, part := range parts {
				pm, ok := part.(map[string]interface{})
				if !ok {
					strippedParts = append(strippedParts, part)
					continue
				}
				clonePart := make(map[string]interface{}, len(pm))
				for k, v := range pm {
					clonePart[k] = v
				}
				if idx == 0 && stringField(clonePart, "type") == "text" {
					if text, ok := clonePart["text"].(string); ok {
						clonePart["text"] = stripDoopsInjectedUserContext(text)
					}
				}
				strippedParts = append(strippedParts, clonePart)
			}
			copied["parts"] = strippedParts
		}
	}
	redacted := mapDoagentValue(copied, redactHistoryText)
	out, _ := redacted.(map[string]interface{})
	return out
}

func redactHistoryText(s string) string {
	redacted := redactSecretText(s)
	return utf8PrefixWithEllipsis(redacted, doagentHistoryMaxFieldBytes)
}

func utf8PrefixWithEllipsis(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	const suffix = "\n... (truncated)"
	budget := maxBytes - len(suffix)
	if budget <= 0 {
		budget = maxBytes
	}
	cut := budget
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + suffix
}

func boundHistoryMessages(messages []interface{}, maxBytes int) ([]interface{}, bool) {
	if len(messages) == 0 {
		return []interface{}{}, false
	}
	encoded, err := json.Marshal(messages)
	if err == nil && len(encoded) <= maxBytes {
		return messages, false
	}
	// Keep the newest messages within budget.
	kept := make([]interface{}, 0, len(messages))
	used := 2 // "[]"
	for i := len(messages) - 1; i >= 0; i-- {
		chunk, err := json.Marshal(messages[i])
		if err != nil {
			continue
		}
		needed := len(chunk) + 1
		if used+needed > maxBytes && len(kept) > 0 {
			break
		}
		kept = append(kept, messages[i])
		used += needed
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	// Drop any leading orphan tool result whose matching assistant tool_call was trimmed.
	for len(kept) > 0 {
		first, ok := kept[0].(map[string]interface{})
		if !ok || strings.ToLower(strings.TrimSpace(stringField(first, "role"))) != "tool" {
			break
		}
		kept = kept[1:]
	}
	return kept, len(kept) < len(messages)
}

const (
	doopsCredentialBindingsMarker = "\n\nGateway-managed credential bindings (metadata only; never read Secret values)."
	doopsDoSQLResolutionFailed    = "\n\n[DoSQL server-side resolution failed]"
	doopsManifestFactsPrefix      = "当前目标清单版本 "
	doopsRuntimeFactsHeader       = "# 当前 target 运行时"
)

// stripDoopsInjectedUserContext removes the deterministic metadata blocks that
// doops-agent prepends/appends to user prompts on reused or credential-bound turns.
func stripDoopsInjectedUserContext(raw string) string {
	s := raw
	// Strip runtime-injected credential binding / DoSQL failure suffixes.
	for _, marker := range []string{doopsCredentialBindingsMarker, doopsDoSQLResolutionFailed} {
		if idx := strings.Index(s, marker); idx >= 0 {
			s = s[:idx]
		}
	}
	// Strip leading manifest promptFacts block (doagent_prompt.go reused session prefix).
	trimmed := strings.TrimLeft(s, "\r\n")
	if strings.HasPrefix(trimmed, doopsManifestFactsPrefix) {
		lines := strings.Split(trimmed, "\n")
		idx := 1
		for idx < len(lines) {
			line := lines[idx]
			if strings.HasPrefix(line, "- ") && strings.Contains(line, " 项：") {
				idx++
				continue
			}
			break
		}
		s = strings.Join(lines[idx:], "\n")
		trimmed = strings.TrimLeft(s, "\r\n")
	}
	// Strip leading targetRuntime promptFacts block.
	if strings.HasPrefix(trimmed, doopsRuntimeFactsHeader) {
		lines := strings.Split(trimmed, "\n")
		idx := 1
		for idx < len(lines) {
			line := strings.TrimSpace(lines[idx])
			if line == "" ||
				strings.HasPrefix(line, "- Agent 进程：") ||
				strings.HasPrefix(line, "- Kubernetes API：") ||
				strings.HasPrefix(line, "- 容器运行时 CLI：") {
				idx++
				continue
			}
			break
		}
		s = strings.Join(lines[idx:], "\n")
	}
	return strings.TrimSpace(s)
}

func extractChatMessageText(msg map[string]interface{}) string {
	if parts, ok := msg["parts"].([]interface{}); ok && len(parts) > 0 {
		var pieces []string
		for _, part := range parts {
			pm, ok := part.(map[string]interface{})
			if !ok {
				continue
			}
			if stringField(pm, "type") == "text" {
				if t := strings.TrimSpace(stringField(pm, "text")); t != "" {
					pieces = append(pieces, t)
				}
			}
		}
		if len(pieces) > 0 {
			return strings.Join(pieces, "\n")
		}
	}
	return stringField(msg, "content")
}

func int64Field(m map[string]interface{}, key string) int64 {
	switch v := m[key].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	default:
		return 0
	}
}

func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes-1]) + "…"
}
