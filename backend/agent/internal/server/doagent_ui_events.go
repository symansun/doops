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

// publicDoagentUINotification projects display data, never execution authority.
// The bridge must buffer text chunks before calling it to redact split secrets.
// Invalid display data produces an explicit error notification.
func publicDoagentUINotification(update map[string]interface{}) notificationEvent {
	public, err := doagentUIStrings(update, []string{
		"sessionUpdate", "turnId", "toolCallId", "toolName", "status", "title", "text",
		"stopReason", "failureClass", "failureReason", "error", "message", "reason",
	})
	if err != nil {
		return errorNotification(err.Error())
	}
	if kind, _ := public["sessionUpdate"].(string); strings.TrimSpace(kind) == "" {
		return errorNotification("doagent_acp_update_invalid: sessionUpdate is required")
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
