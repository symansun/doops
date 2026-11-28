package server

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

var doagentUISecretTextPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?is)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|$)`),
	regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/@\s]+@`),
	regexp.MustCompile(`(?i)([?&](?:[A-Za-z0-9._-]*(?:token|password|secret|signature|credential|api[_-]?key)|sig|auth|authorization|cookie)=)[^&#\s]+`),
	regexp.MustCompile(`sk-[A-Za-z0-9_-]+`),
	regexp.MustCompile(`(?i)(api[_-]?key[=: ]+)[^ ,}\n]+`),
	regexp.MustCompile(`(?i)(Bearer )[A-Za-z0-9._-]+`),
	regexp.MustCompile(`(?i)(Basic )[A-Za-z0-9+/=._-]+`),
	regexp.MustCompile(`(?i)((?:authorization|cookie|password|token|secret)[_-]?[A-Za-z0-9_-]*[=: ]+)[^ ,}\n]+`),
}

const doagentUIPrivateFieldPattern = `(?:[A-Za-z0-9_.-]*(?:token|password|secret|api[_-]?key|private[_-]?key|credential|signature|proof)|auth|authorization|cookie|passwd|data|string[_-]?data|credentials|credentialBindings|credentialPayload|attestation[A-Za-z0-9_.-]*|rawOutput|outputRef|canonicalScope|(?:input|result|publicResult|scope|context|plan|planBinding)Digest|mutation(?:Authorized|AuthorizationId|AuthorizationIssuedAt|AuthorityDigest|ContextDigest))`

var doagentUIPrivateField = regexp.MustCompile(`(?i)^` + doagentUIPrivateFieldPattern + `$`)
var doagentUIPrivateComposite = regexp.MustCompile(`(?i)["']?` + doagentUIPrivateFieldPattern + `["']?\s*[:=]\s*[\[{]`)

var doagentUIPrivateScalarPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(["']?` + doagentUIPrivateFieldPattern + `["']?\s*[:=]\s*)"(?:\\.|[^"\\])*"`),
	regexp.MustCompile(`(?i)(["']?` + doagentUIPrivateFieldPattern + `["']?\s*[:=]\s*)'(?:\\.|[^'\\])*'`),
	regexp.MustCompile(`(?i)(["']?` + doagentUIPrivateFieldPattern + `["']?\s*[:=]\s*)[^\s"',}\]]+`),
}

const (
	doagentUIDisplayCommandMaxRunes = 4096
	doagentUIDisplaySummaryMaxRunes = 240
	doagentUIDisplayCwdMaxRunes     = 256
)

// publicDoagentUINotification projects display data, never execution authority.
// The bridge must buffer text chunks before calling it to redact split secrets.
// Invalid display data produces an explicit error notification.
func publicDoagentUINotification(update map[string]interface{}) notificationEvent {
	public, err := doagentUIStrings(update, []string{
		"sessionUpdate", "turnId", "toolCallId", "toolName", "status", "title", "text",
		"stopReason", "failureClass", "failureReason", "error", "message", "reason", "model",
		"displayCommand", "displaySummary", "cwd",
	})
	if err != nil {
		return errorNotification(err.Error())
	}
	if kind, _ := public["sessionUpdate"].(string); strings.TrimSpace(kind) == "" {
		return errorNotification("doagent_acp_update_invalid: sessionUpdate is required")
	}
	kind, _ := public["sessionUpdate"].(string)
	if kind == "tool_call" || kind == "tool_call_update" || kind == "tool_call_progress" {
		if value, exists := update["exitCode"]; exists && value != nil {
			code, err := doagentUISignedInt32(value)
			if err != nil {
				return errorNotification("doagent_acp_update_invalid: exitCode must be an integer")
			}
			public["exitCode"] = code
		}
		if displayCmd, displaySummary, displayCwd, err := extractDoagentUIDisplayInput(update); err != nil {
			return errorNotification(err.Error())
		} else {
			if displayCmd != "" {
				if _, exists := public["displayCommand"]; !exists {
					public["displayCommand"] = displayCmd
				}
			}
			if displaySummary != "" {
				if _, exists := public["displaySummary"]; !exists {
					public["displaySummary"] = displaySummary
				}
			}
			if displayCwd != "" {
				if _, exists := public["cwd"]; !exists {
					public["cwd"] = displayCwd
				}
			}
		}
		if cmd, ok := public["displayCommand"].(string); ok {
			public["displayCommand"] = truncateRunes(cmd, doagentUIDisplayCommandMaxRunes)
		}
		if sum, ok := public["displaySummary"].(string); ok {
			public["displaySummary"] = truncateRunes(sum, doagentUIDisplaySummaryMaxRunes)
		}
		if dir, ok := public["cwd"].(string); ok {
			public["cwd"] = truncateRunes(dir, doagentUIDisplayCwdMaxRunes)
		}
	}
	if content, exists := update["content"]; exists && content != nil {
		public["content"], err = doagentUIContent(content)
		if err != nil {
			return errorNotification(err.Error())
		}
	}
	for _, key := range []string{"entries", "todos"} {
		if value, exists := update[key]; exists && value != nil {
			items, ok := value.([]interface{})
			if !ok {
				return errorNotification(fmt.Sprintf("doagent_acp_update_invalid: %s must be an array", key))
			}
			entries := make([]interface{}, 0, len(items))
			for _, item := range items {
				entry, ok := item.(map[string]interface{})
				if !ok {
					return errorNotification(fmt.Sprintf("doagent_acp_update_invalid: %s entry must be an object", key))
				}
				projected, err := doagentUIStrings(entry, []string{"id", "content", "text", "title", "status", "priority", "activeForm"})
				if err != nil {
					return errorNotification(err.Error())
				}
				entries = append(entries, projected)
			}
			public[key] = entries
		}
	}
	if value, exists := update["usage"]; exists && value != nil {
		usage, ok := value.(map[string]interface{})
		if !ok {
			return errorNotification("doagent_acp_update_invalid: usage must be an object")
		}
		counts := make(map[string]interface{})
		if err := doagentUINumbers(usage, counts, []string{"inputTokens", "outputTokens", "totalTokens"}); err != nil {
			return errorNotification(err.Error())
		}
		public["usage"] = counts
	}
	if err := doagentUINumbers(update, public, []string{"inputTokens", "outputTokens", "totalTokens", "elapsedMs"}); err != nil {
		return errorNotification(err.Error())
	}
	if value, exists := update["replay"]; exists && value != nil {
		replay, ok := value.(bool)
		if !ok {
			return errorNotification("doagent_acp_update_invalid: replay must be a boolean")
		}
		public["replay"] = replay
	}
	encoded, err := json.Marshal(public)
	if err != nil {
		return errorNotification("doagent_acp_update_invalid: cannot encode display data")
	}
	return notificationEvent{Kind: "acp_update", Data: string(encoded)}
}

func doagentUIStrings(source map[string]interface{}, fields []string) (map[string]interface{}, error) {
	public := make(map[string]interface{})
	for _, key := range fields {
		if value, exists := source[key]; exists && value != nil {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("doagent_acp_update_invalid: %s must be text", key)
			}
			redacted, err := redactDoagentUIText(text)
			if err != nil {
				return nil, err
			}
			public[key] = redacted
		}
	}
	return public, nil
}

func doagentUIContent(value interface{}) (interface{}, error) {
	switch content := value.(type) {
	case string:
		return redactDoagentUIText(content)
	case []interface{}:
		blocks := make([]interface{}, 0, len(content))
		for _, block := range content {
			projected, err := doagentUIContent(block)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, projected)
		}
		return blocks, nil
	case map[string]interface{}:
		switch content["type"] {
		case "text":
			text, ok := content["text"].(string)
			if ok {
				redacted, err := redactDoagentUIText(text)
				if err != nil {
					return nil, err
				}
				return map[string]interface{}{"type": "text", "text": redacted}, nil
			}
		case "content":
			inner, err := doagentUIContent(content["content"])
			if err != nil {
				return nil, err
			}
			return map[string]interface{}{"type": "content", "content": inner}, nil
		}
	}
	return nil, fmt.Errorf("doagent_acp_update_invalid: content must contain text blocks")
}

func doagentUINumbers(source, public map[string]interface{}, fields []string) error {
	for _, key := range fields {
		if value, exists := source[key]; exists && value != nil {
			encoded, err := json.Marshal(value)
			var count uint64
			if err != nil || json.Unmarshal(encoded, &count) != nil {
				return fmt.Errorf("doagent_acp_update_invalid: %s must be an unsigned integer", key)
			}
			public[key] = count
		}
	}
	return nil
}

func doagentUISignedInt32(value interface{}) (int32, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	var number int64
	if err := json.Unmarshal(encoded, &number); err != nil {
		return 0, err
	}
	if number < -2147483648 || number > 2147483647 {
		return 0, fmt.Errorf("exitCode out of int32 range")
	}
	return int32(number), nil
}

// extractDoagentUIDisplayInput extracts strictly allowlisted, redacted display-only
// fields (command / file path / safe metadata summary) from structured tool inputs
// without ever forwarding rawInput, arbitrary keys, or credential payloads.
func extractDoagentUIDisplayInput(update map[string]interface{}) (displayCommand string, displaySummary string, cwd string, err error) {
	var rawObj map[string]interface{}
	for _, field := range []string{"input", "args", "rawInput"} {
		value, exists := update[field]
		if !exists || value == nil {
			continue
		}
		normalized, _ := normalizeDoagentToolInput(value)
		if m, ok := normalized.(map[string]interface{}); ok {
			rawObj = m
			break
		}
		if rawStr, ok := normalized.(string); ok {
			trimmed := strings.TrimSpace(rawStr)
			if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
				if decoded, dErr := decodeDoagentJSONObject(trimmed); dErr == nil {
					if m, ok := normalizeDoagentToolInputMap(decoded); ok {
						rawObj = m
						break
					}
				}
			}
		}
	}
	if rawObj == nil {
		return "", "", "", nil
	}

	toolName := strings.ToLower(strings.TrimSpace(doagentToolName(update)))
	redactAndCap := func(raw string, limit int) (string, error) {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return "", nil
		}
		redacted, rErr := redactDoagentUIText(trimmed)
		if rErr != nil {
			return "", rErr
		}
		return truncateRunes(redacted, limit), nil
	}

	switch toolName {
	case "bash", "shell", "doops_shell", "mcp_doops_shell", "doops_exec":
		for _, k := range []string{"command", "cmd"} {
			if v, ok := rawObj[k].(string); ok && strings.TrimSpace(v) != "" {
				displayCommand, err = redactAndCap(v, doagentUIDisplayCommandMaxRunes)
				if err != nil {
					return "", "", "", err
				}
				break
			}
		}
		if v, ok := rawObj["description"].(string); ok && strings.TrimSpace(v) != "" {
			displaySummary, err = redactAndCap(v, doagentUIDisplaySummaryMaxRunes)
			if err != nil {
				return "", "", "", err
			}
		}
		for _, k := range []string{"cwd", "workdir"} {
			if v, ok := rawObj[k].(string); ok && strings.TrimSpace(v) != "" {
				cwd, err = redactAndCap(v, doagentUIDisplayCwdMaxRunes)
				if err != nil {
					return "", "", "", err
				}
				break
			}
		}
		return displayCommand, displaySummary, cwd, nil

	case "read", "glob", "grep", "write", "edit":
		var parts []string
		if pat, ok := rawObj["pattern"].(string); ok && strings.TrimSpace(pat) != "" {
			parts = append(parts, strings.TrimSpace(pat))
		}
		for _, k := range []string{"file_path", "path", "filePath"} {
			if p, ok := rawObj[k].(string); ok && strings.TrimSpace(p) != "" {
				parts = append(parts, strings.TrimSpace(p))
				break
			}
		}
		if len(parts) > 0 {
			displaySummary, err = redactAndCap(strings.Join(parts, " · "), doagentUIDisplaySummaryMaxRunes)
			if err != nil {
				return "", "", "", err
			}
		}
		return "", displaySummary, "", nil

	case "mcp_doops_k8s_read", "mcp_doops_helm", "mcp_doops_web", "mcp_doops_web_read", "mcp_doops_kb_read", "mcp_dosql_read", "mcp_dosql_exec":
		safeKeys := []string{"operation", "action", "sql_action", "kind", "namespace", "name", "release", "site_id", "domain", "database", "table"}
		var parts []string
		for _, k := range safeKeys {
			if v, ok := rawObj[k].(string); ok && strings.TrimSpace(v) != "" {
				parts = append(parts, k+"="+strings.TrimSpace(v))
			}
		}
		if len(parts) > 0 {
			displaySummary, err = redactAndCap(strings.Join(parts, " "), doagentUIDisplaySummaryMaxRunes)
			if err != nil {
				return "", "", "", err
			}
		}
		return "", displaySummary, "", nil
	}
	return "", "", "", nil
}

func normalizeDoagentToolInputMap(obj map[string]interface{}) (map[string]interface{}, bool) {
	normalized, _ := normalizeDoagentToolInput(obj)
	m, ok := normalized.(map[string]interface{})
	return m, ok
}

// Unlike sanitizeSecretText, UI text has no audit-tail truncation.
func redactDoagentUIText(text string) (string, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value interface{}
	if decoder.Decode(&value) == nil {
		var extra interface{}
		if decoder.Decode(&extra) == io.EOF {
			projected, changed, err := redactDoagentUIJSON(value)
			if err != nil {
				return "", err
			}
			if changed {
				encoded, err := json.Marshal(projected)
				if err != nil {
					return "", fmt.Errorf("doagent_acp_update_invalid: cannot redact structured text")
				}
				text = string(encoded)
			}
		}
	}
	var err error
	text, err = redactDoagentUICompositeText(text)
	if err != nil {
		return "", err
	}
	// Quoted values must be removed before scalar rules split on whitespace.
	replacements := []string{`${1}"...REDACTED"`, `${1}'...REDACTED'`}
	for index, pattern := range doagentUIPrivateScalarPatterns[:2] {
		text = pattern.ReplaceAllString(text, replacements[index])
	}
	text = doagentUISecretTextPatterns[0].ReplaceAllString(text, "...REDACTED PRIVATE KEY...")
	for _, pattern := range doagentUISecretTextPatterns[1:] {
		text = pattern.ReplaceAllString(text, "${1}...REDACTED")
	}
	text = doagentUIPrivateScalarPatterns[2].ReplaceAllString(text, "${1}...REDACTED")
	for _, pattern := range credentialInlineMaterialPatterns {
		text = pattern.ReplaceAllString(text, "...REDACTED")
	}
	return text, nil
}

func redactDoagentUICompositeText(text string) (string, error) {
	var public strings.Builder
	for {
		match := doagentUIPrivateComposite.FindStringIndex(text)
		if match == nil {
			public.WriteString(text)
			return public.String(), nil
		}
		start := match[1] - 1
		decoder := json.NewDecoder(strings.NewReader(text[start:]))
		decoder.UseNumber()
		var value interface{}
		if decoder.Decode(&value) != nil {
			return "", fmt.Errorf("doagent_acp_update_invalid: cannot redact private text payload")
		}
		public.WriteString(text[:start])
		public.WriteString(`"...REDACTED"`)
		text = text[start+int(decoder.InputOffset()):]
	}
}

func redactDoagentUIJSON(value interface{}) (interface{}, bool, error) {
	changed := false
	switch source := value.(type) {
	case map[string]interface{}:
		public := make(map[string]interface{})
		for key, item := range source {
			if credentialSecretLikeKey(key) || doagentUIPrivateField.MatchString(key) {
				changed = true
				continue
			}
			projected, itemChanged, err := redactDoagentUIJSON(item)
			if err != nil {
				return nil, false, err
			}
			public[key] = projected
			changed = changed || itemChanged
		}
		return public, changed, nil
	case []interface{}:
		public := make([]interface{}, 0, len(source))
		for _, item := range source {
			projected, itemChanged, err := redactDoagentUIJSON(item)
			if err != nil {
				return nil, false, err
			}
			public = append(public, projected)
			changed = changed || itemChanged
		}
		return public, changed, nil
	case string:
		public, err := redactDoagentUIText(source)
		return public, public != source, err
	default:
		return value, false, nil
	}
}
