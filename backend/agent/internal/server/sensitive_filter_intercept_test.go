package server

import (
	"testing"
)

func TestSensitiveFilterBlocksGatewayToolCalls(t *testing.T) {
	fixture := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	startClusterAgent(t, fixture, "dev")
	client := fixture.client(t, fixture.admin)
	auth, err := fixture.store.VerifyUserToken(fixture.admin.Plaintext)
	if err != nil {
		t.Fatalf("verify admin token: %v", err)
	}
	if _, _, err := fixture.store.AddSensitiveRules([]SensitiveRuleInput{
		{Text: "rm -rf /", Scope: "command"},
		{Text: "CANARY_PROMPT_TERM", Scope: "prompt"},
		{Text: "DROP CANARY_TABLE", Scope: "command"},
	}); err != nil {
		t.Fatalf("add rules: %v", err)
	}
	if err := fixture.store.SetSensitiveFilterEnabled(true); err != nil {
		t.Fatalf("enable filter: %v", err)
	}

	lifecycleSend(t, client, 2, "doops_shell", "sensitive-session", map[string]any{"command": "echo rm -rf /"})
	response := readK8SFunctionalResponse(t, client, 2)
	rpcError, _ := response["error"].(map[string]any)
	if rpcError == nil || rpcError["code"] != float64(-32003) || rpcError["message"] != "sensitive_command_blocked" {
		t.Fatalf("expected command denial: %#v", response)
	}
	if response["result"] != nil {
		t.Fatalf("denied command must not reach the Agent: %#v", response)
	}

	lifecycleSend(t, client, 3, "doops_agent_prompt", "sensitive-session", map[string]any{"instruction": "run CANARY_PROMPT_TERM"})
	response = readK8SFunctionalResponse(t, client, 3)
	rpcError, _ = response["error"].(map[string]any)
	if rpcError == nil || rpcError["code"] != float64(-32003) || rpcError["message"] != "sensitive_prompt_blocked" {
		t.Fatalf("expected prompt denial: %#v", response)
	}

	// A dosql prompt carries SQL outside the instruction; command rules still apply.
	lifecycleSend(t, client, 4, "doops_agent_prompt", "sensitive-session", map[string]any{
		"instruction": "apply the migration", "skill": "dosql", "sql": "DROP CANARY_TABLE",
	})
	response = readK8SFunctionalResponse(t, client, 4)
	rpcError, _ = response["error"].(map[string]any)
	if rpcError == nil || rpcError["code"] != float64(-32003) || rpcError["message"] != "sensitive_command_blocked" {
		t.Fatalf("expected dosql command denial: %#v", response)
	}

	// The whitelisted user is exempt even though the same text matches a rule.
	if _, err := fixture.store.AddSensitiveWhitelist(SensitiveWhitelistInput{Kind: "user", UserID: auth.UserID}); err != nil {
		t.Fatalf("whitelist admin: %v", err)
	}
	if rule, blocked := fixture.store.MatchSensitiveText(auth.UserID, "dev", "local", "echo rm -rf /", sensitiveScopeCommand); blocked {
		t.Fatalf("whitelisted user matched rule %q", rule)
	}
}
