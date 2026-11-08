package main

import (
	"errors"
	"strings"
	"testing"
)

func TestErrorHintClassification(t *testing.T) {
	cases := []struct {
		name    string
		err     string
		wantSub string // hint 中必须出现的关键词
	}{
		{"401 via ws dial", "failed to connect to agent WS: websocket: bad handshake (HTTP 401: Unauthorized)", "doops login"},
		{"401 bare", "Unauthorized", "doops login"},
		{"403 forbidden", "remote error: Forbidden", "授权"},
		{"403 missing scope", "remote error: missing scope exec", "授权"},
		{"guardrail", `command blocked by agent guardrail [disk.rm_root]: 递归强制删除根目录或系统目录`, "DOOPS_BLOCK_DISABLE"},
		{"agent offline", "remote error: agent prod/master-1 offline", "doops targets"},
		{"not registered", "instance not registered", "doops-agent -gateway-url"},
		{"connection refused", "failed to connect to agent WS: dial tcp: connection refused", "gateway 地址"},
		{"connection lost", "connection lost: {\"type\":\"WS_CLOSE\"}", "gateway 地址"},
		{"timeout", "tool call timed out after 30m0s", "doops bg"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hint := errorHint(errors.New(tc.err))
			if hint == "" {
				t.Fatalf("expected hint for %q", tc.err)
			}
			if !strings.Contains(hint, tc.wantSub) {
				t.Fatalf("hint for %q missing %q: %s", tc.err, tc.wantSub, hint)
			}
		})
	}
}

// 鉴权提示必须优先于连接提示（401 常包裹在 "failed to connect" 里）。
func TestErrorHintAuthBeatsConnection(t *testing.T) {
	hint := errorHint(errors.New("failed to connect to agent WS: dial error (HTTP 401: Unauthorized)"))
	if !strings.Contains(hint, "doops login") {
		t.Fatalf("401 should route to login hint, got: %s", hint)
	}
}

func TestErrorHintUnknownReturnsEmpty(t *testing.T) {
	if hint := errorHint(errors.New("resolve write content: some local problem")); hint != "" {
		t.Fatalf("expected empty hint, got: %s", hint)
	}
}
