package main

import (
	"errors"
	"strings"
	"testing"
)

func TestErrorHintClassification(t *testing.T) {
	cases := []struct {
		name    string
		message string
		want    string
	}{
		{"Gateway auth via WS", "failed to connect: gateway /v1/rpc failed: gateway_auth_failed (auth_domain=gateway): token rejected", "doops login --target"},
		{"missing login", "no login for Gateway https://gateway.example.com; run doops login --target demo", "共享登录态"},
		{"bare HTTP 401", "failed to connect: HTTP 401 Unauthorized", "响应来源"},
		{"bare unauthorized", "Unauthorized", "响应来源"},
		{"forbidden", "remote error: Forbidden", "cluster/instance"},
		{"typed HTTP forbidden", "gateway targets failed: http_403 Forbidden", "doops targets --target"},
		{"guardrail", "blocked_command(disk.rm_root): root deletion is refused", "DOOPS_BLOCK_DISABLE"},
		{"guardrail diagnostic", "command blocked by agent guardrail [disk.rm_root]", "不绕过拦截"},
		{"Agent offline", "remote error: agent prod/master-1 offline", "doops targets --target"},
		{"not registered", "instance not registered", "doops-agent -gateway-url"},
		{"connection refused", "failed to connect to gateway /v1/rpc: dial tcp: connection refused", "gateway 地址"},
		{"connection lost", `connection lost: {"type":"WS_CLOSE"}`, "gateway 地址"},
		{"connection timeout", "failed to connect: i/o timeout", "gateway 地址"},
		{"timeout", "tool call timed out after 30m0s", "doops exec --bg"},
		{"unknown outcome", "execution_outcome_unknown", "不重放变更"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hint := errorHint(errors.New(tc.message))
			if !strings.Contains(hint, tc.want) {
				t.Fatalf("hint for %q missing %q: %s", tc.message, tc.want, hint)
			}
			for _, retired := range []string{"doops bg", "doops task", "target/action", "--scope", "--gateway"} {
				if strings.Contains(hint, retired) {
					t.Fatalf("hint recommends an unsupported command or authority: %s", hint)
				}
			}
		})
	}
}

func TestErrorHintAuthBeatsConnection(t *testing.T) {
	hint := errorHint(errors.New("failed to connect: gateway_auth_failed: token rejected"))
	if !strings.Contains(hint, "doops login") || strings.Contains(hint, "网络可达性") {
		t.Fatalf("Gateway auth should route to login, got: %s", hint)
	}
}

func TestErrorHintDoesNotInferCredentialAuthorityFromHTTPStatus(t *testing.T) {
	for _, message := range []string{
		"credential_verification_failed: HTTP 401 Unauthorized",
		"credential_forbidden: HTTP 403 Forbidden",
		"edge_enrollment_auth_failed: unauthorized",
	} {
		if hint := errorHint(errors.New(message)); hint != "" {
			t.Fatalf("specific credential or edge failure must keep its own recovery instructions: %s", hint)
		}
	}
	bare := errorHint(errors.New("HTTP 401 Unauthorized"))
	if strings.Contains(bare, "已过期") || strings.Contains(bare, "被撤销") {
		t.Fatalf("bare 401 must not diagnose Gateway token state: %s", bare)
	}
}

func TestErrorHintUnknownReturnsEmpty(t *testing.T) {
	for _, err := range []error{nil, errors.New("resolve write content: some local problem"), errors.New("file not found")} {
		if hint := errorHint(err); hint != "" {
			t.Fatalf("unrecognized error %v should have no inferred diagnosis: %s", err, hint)
		}
	}
}
