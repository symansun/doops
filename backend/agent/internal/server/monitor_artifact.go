package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/user/doops/agent/api"
)

const (
	maxInspectionArtifactBytes = 256 * 1024
	maxInspectionPromptBytes   = 8192
	maxInspectionCommandBytes  = 4096
)

var fencedJSONBlockPattern = regexp.MustCompile("(?s)```(?:json|JSON)?\\s*\\n(.*?)\\n```")

type parsedInspectionArtifact struct {
	Format   string
	Markdown string
	JSON     json.RawMessage
	Raw      string
	Status   string
	Summary  string
	Checks   []api.MonitorInspectCheck
}

func validateInspectionFreeText(raw string, maxBytes int) (string, bool) {
	if strings.ContainsRune(raw, '\x00') {
		return "", false
	}
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) > maxBytes {
		return "", false
	}
	return trimmed, true
}

func truncateUTF8Head(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "…"
}

func sanitizeAndCapArtifact(raw string) string {
	clean := normalizeUTF8ForStore(redactSecretText(raw))
	if len(clean) > maxInspectionArtifactBytes {
		clean = truncateUTF8Bytes(clean, maxInspectionArtifactBytes) + "\n... [truncated]"
	}
	return clean
}

// shouldAppendInternalResultText reports whether RunInternalToolCall should
// append the terminal MCP result text into collected streaming output.
// Upgraded Agents set structuredContent.streamed == true on streaming command
// completions so placeholder text ("Operation complete.") is never appended;
// errors always retain their diagnostic text.
func shouldAppendInternalResultText(result map[string]interface{}, isErr bool) bool {
	if isErr {
		return true
	}
	return !resultHasStreamedCommandContent(result)
}

// stripShellCompletionSuffix removes the final RPC status trailer appended by
// RunInternalToolCall when collecting streaming output from un-upgraded legacy
// Agents that do not set structuredContent.streamed == true.
func stripShellCompletionSuffix(raw string) string {
	s := strings.ReplaceAll(raw, "\r\n", "\n")
	trimmedRight := strings.TrimRight(s, " \t\n")
	if trimmedRight == "Operation complete." {
		return ""
	}
	if strings.HasSuffix(trimmedRight, "\nOperation complete.") {
		return strings.TrimRight(strings.TrimSuffix(trimmedRight, "\nOperation complete."), "\n")
	}
	return strings.TrimRight(s, "\n")
}

func compactValidJSON(candidate string) (json.RawMessage, bool) {
	trimmed := strings.TrimSpace(candidate)
	if trimmed == "" {
		return nil, false
	}
	first := trimmed[0]
	if first != '{' && first != '[' {
		return nil, false
	}
	if !json.Valid([]byte(trimmed)) {
		return nil, false
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(trimmed)); err != nil {
		return nil, false
	}
	return json.RawMessage(buf.Bytes()), true
}

func normalizeArtifactStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "pass", "ok", "healthy", "success", "normal", "passed":
		return "pass"
	case "warning", "warn", "degraded", "partial":
		return "warning"
	case "critical", "error", "fail", "failed", "unhealthy", "alert":
		return "critical"
	case "skipped", "skip", "unknown", "na":
		return "skipped"
	default:
		return ""
	}
}

func looksLikeMarkdown(s string) bool {
	for _, token := range []string{"# ", "## ", "### ", "```", "**", "\n- ", "\n* ", "\n| "} {
		if strings.Contains(s, token) {
			return true
		}
	}
	return strings.HasPrefix(s, "#") || strings.HasPrefix(s, "| ")
}

// extractInspectionArtifact parses arbitrary output from a Skill prompt or shell
// command into structured Markdown + JSON artifacts without enforcing a rigid schema.
func extractInspectionArtifact(raw, defaultName, defaultCategory string) parsedInspectionArtifact {
	clean := sanitizeAndCapArtifact(strings.TrimSpace(raw))
	out := parsedInspectionArtifact{
		Raw:    clean,
		Status: "pass",
	}
	if clean == "" {
		out.Format = "text"
		out.Markdown = "_命令/巡检执行完成，未产生输出内容。_"
		out.Summary = "执行完成（无输出）"
		out.Checks = []api.MonitorInspectCheck{{
			ID:       "artifact.result",
			Name:     defaultName,
			Category: defaultCategory,
			Status:   "pass",
			Value:    "空输出",
			Detail:   "执行成功，未返回标准输出内容",
		}}
		return out
	}

	// 1. Pure valid JSON object or array.
	if compact, ok := compactValidJSON(clean); ok {
		out.Format = "json"
		out.JSON = compact
	} else {
		// 2. Markdown containing one or more fenced JSON blocks.
		matches := fencedJSONBlockPattern.FindAllStringSubmatch(clean, -1)
		var bestJSON json.RawMessage
		for _, m := range matches {
			if len(m) < 2 {
				continue
			}
			if compact, ok := compactValidJSON(m[1]); ok {
				// Prefer the last substantial JSON block (typically the structured report payload).
				if len(bestJSON) == 0 || len(compact) >= len(bestJSON)/2 {
					bestJSON = compact
				}
			}
		}
		if len(bestJSON) > 0 {
			out.Format = "markdown+json"
			out.Markdown = clean
			out.JSON = bestJSON
		} else if looksLikeMarkdown(clean) {
			out.Format = "markdown"
			out.Markdown = clean
		} else {
			out.Format = "text"
			out.Markdown = "```text\n" + clean + "\n```"
		}
	}

	// 3. Best-effort status/summary/checks extraction from arbitrary JSON (never required).
	if len(out.JSON) > 0 && out.JSON[0] == '{' {
		var obj map[string]any
		if json.Unmarshal(out.JSON, &obj) == nil {
			for _, key := range []string{"status", "health", "result", "level"} {
				if v, ok := obj[key].(string); ok {
					if st := normalizeArtifactStatus(v); st != "" {
						out.Status = st
						break
					}
				}
			}
			for _, key := range []string{"summary", "headline", "message", "conclusion", "title"} {
				if v, ok := obj[key].(string); ok && strings.TrimSpace(v) != "" {
					out.Summary = trimTail(strings.TrimSpace(v), 240)
					break
				}
			}
			if rawChecks, ok := obj["checks"]; ok {
				if b, err := json.Marshal(rawChecks); err == nil {
					var parsedChecks []api.MonitorInspectCheck
					if json.Unmarshal(b, &parsedChecks) == nil && len(parsedChecks) > 0 {
						normalized := make([]api.MonitorInspectCheck, 0, len(parsedChecks))
						for i, c := range parsedChecks {
							if strings.TrimSpace(c.Name) == "" && strings.TrimSpace(c.ID) == "" && strings.TrimSpace(c.Detail) == "" {
								continue
							}
							if c.ID == "" {
								c.ID = fmt.Sprintf("artifact.check.%d", i+1)
							}
							if c.Name == "" {
								c.Name = c.ID
							}
							if c.Category == "" {
								c.Category = defaultCategory
							}
							if st := normalizeArtifactStatus(c.Status); st != "" {
								c.Status = st
							} else {
								c.Status = "pass"
							}
							normalized = append(normalized, c)
						}
						if len(normalized) > 0 {
							out.Checks = normalized
							if normalizeArtifactStatus(fmt.Sprint(obj["status"])) == "" {
								out.Status = aggregateCheckStatus(normalized)
							}
						}
					}
				}
			}
		}
	}

	if len(out.Checks) == 0 {
		detail := out.Summary
		if detail == "" {
			detail = trimTail(strings.ReplaceAll(clean, "\n", " "), 240)
		}
		out.Checks = []api.MonitorInspectCheck{{
			ID:       "artifact.result",
			Name:     defaultName,
			Category: defaultCategory,
			Status:   out.Status,
			Value:    out.Format,
			Detail:   detail,
		}}
	}
	return out
}

// findSkillExecutableScript returns the first supported executable script path
// bundled inside a Skill (e.g. scripts/inspect.sh or scripts/run.sh).
func findSkillExecutableScript(sk platformSkillRecord) string {
	preferred := []string{"scripts/inspect.sh", "scripts/run.sh", "inspect.sh", "run.sh"}
	for _, want := range preferred {
		for _, f := range sk.Files {
			if f.Path == want {
				return f.Path
			}
		}
	}
	return ""
}

func buildSkillPromptInstruction(sk *platformSkillRecord, userPrompt string, inputs map[string]string) string {
	var sb strings.Builder
	sb.WriteString("你正在执行 DoOps 巡检任务。你可以在当前目标上执行完成本次巡检所需的任何命令以获取真实观测数据，切勿编造或臆测结果，严禁输出任何凭据明文。\n")
	if sk != nil {
		fmt.Fprintf(&sb, "\n请加载并遵循已同步至本机的 Skill：`%s`（路径 `/root/.agent/skills/%s/SKILL.md`）。\n", sk.Slug, sk.Slug)
	}
	if strings.TrimSpace(userPrompt) != "" {
		fmt.Fprintf(&sb, "\n本次巡检任务与提示词：\n%s\n", strings.TrimSpace(userPrompt))
	}
	if block := formatPluginInputsPromptBlock(inputs); block != "" {
		sb.WriteString(block)
	}
	sb.WriteString("\n输出产物要求（便于前端动态可视化呈现）：\n")
	sb.WriteString("1. 请输出一份结构清晰的 Markdown 巡检报告，并在末尾附带一个合法的 ```json ... ``` 代码块（或者直接输出纯 JSON）。\n")
	sb.WriteString("2. JSON 结构完全自由，可按实际巡检对象组织（例如包含 status、summary、各类数值指标字段，以及任意命名的对象数组明细供自动渲染表格）。\n")
	return sb.String()
}
