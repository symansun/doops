package api

import (
	"encoding/json"
	"testing"
)

// 版本兼容矩阵（帧级）：新旧 CLI/gateway/agent 任意组合时，tools/call 帧必须互通。
// 关键约束：caller_scopes 是 v0.2 新增的可选字段。

// 新 agent 收到旧 gateway 帧（无 caller_scopes）：护栏无豁免。
func TestToolCallParams_LegacyFrameWithoutCallerScopes(t *testing.T) {
	raw := `{"name":"doops_shell","arguments":{"command":"ls"}}`
	var p ToolCallParams
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal legacy frame: %v", err)
	}
	if p.CallerScopes != nil {
		t.Fatalf("legacy frame must yield nil CallerScopes, got %v", p.CallerScopes)
	}
	if p.Name != "doops_shell" {
		t.Fatalf("name mismatch: %q", p.Name)
	}
}

// 旧 agent 收到新 gateway 帧（含 caller_scopes 等未知字段）：必须静默忽略，不报错。
// Go 的 encoding/json 对未知字段默认忽略，这里固化为契约，防止未来改用 DisallowUnknownFields 破坏兼容。
func TestToolCallParams_UnknownFieldsIgnored(t *testing.T) {
	raw := `{"name":"doops_shell","arguments":{"command":"ls"},"caller_scopes":["exec"],"future_field_v9":{"x":1}}`
	var p ToolCallParams
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unknown fields must be ignored: %v", err)
	}
	if len(p.CallerScopes) != 1 || p.CallerScopes[0] != "exec" {
		t.Fatalf("caller_scopes mismatch: %v", p.CallerScopes)
	}
}

// 序列化方向：无 CallerScopes 时帧中不得出现 caller_scopes 键（omitempty 契约），
// 保证新 gateway 对旧 agent 发出的帧与旧 gateway 完全一致。
func TestToolCallParams_OmitemptyContract(t *testing.T) {
	out, err := json.Marshal(ToolCallParams{Name: "doops_shell", Arguments: json.RawMessage(`{"command":"ls"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["caller_scopes"]; ok {
		t.Fatalf("caller_scopes must be omitted when nil: %s", out)
	}
}

// 空数组语义：omitempty 会把空 scopes 与 nil 一样省略——即"调用方无匹配授权"
// 与"旧 gateway 未注入"在帧上不可区分，agent 侧统一按无豁免处理。固化该契约。
func TestToolCallParams_EmptyScopesEquivalentToNil(t *testing.T) {
	out, err := json.Marshal(ToolCallParams{Name: "doops_shell", CallerScopes: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["caller_scopes"]; ok {
		t.Fatalf("empty scopes must be omitted like nil: %s", out)
	}
}
