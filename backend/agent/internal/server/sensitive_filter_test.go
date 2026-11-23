package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func openSensitiveStore(t *testing.T) *GatewayStore {
	t.Helper()
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestSensitiveFilterRulesPersistAndMatch(t *testing.T) {
	store := openSensitiveStore(t)
	created, skipped, err := store.AddSensitiveRules([]SensitiveRuleInput{
		{Text: "  RM   -RF  ", Scope: "command"},
		{Text: "CANARY_SENSITIVE_TERM", Scope: "prompt"},
		{Text: "rm -rf", Scope: "command"},
	})
	if err != nil {
		t.Fatalf("add rules: %v", err)
	}
	if len(created) != 2 || len(skipped) != 1 || skipped[0] != "rm -rf" {
		t.Fatalf("created=%d skipped=%v", len(created), skipped)
	}
	if created[0].Scope != sensitiveScopeCommand || !created[0].Enabled || created[0].ID == "" {
		t.Fatalf("unexpected command rule: %+v", created[0])
	}

	path := store.sensitiveFilterFilePath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat filter file: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("filter file mode = %o, want 600", info.Mode().Perm())
	}

	view, err := store.ListSensitiveFilter()
	if err != nil {
		t.Fatalf("list filter: %v", err)
	}
	if len(view.Rules) != 2 {
		t.Fatalf("listed %d rules, want 2", len(view.Rules))
	}

	if rule, blocked := store.MatchSensitiveText("usr_a", "dev", "node", "please Run   rm -rf /tmp/x", sensitiveScopeCommand); !blocked {
		t.Fatal("expected command rule to match case-insensitively with folded whitespace")
	} else if rule != created[0].ID {
		t.Fatalf("matched rule %q, want %q", rule, created[0].ID)
	}
	if _, blocked := store.MatchSensitiveText("usr_a", "dev", "node", "run rm -rf /tmp/x", sensitiveScopePrompt); blocked {
		t.Fatal("command rule must not match prompts")
	}
	if _, blocked := store.MatchSensitiveText("usr_a", "dev", "node", "here is CANARY_SENSITIVE_TERM", sensitiveScopePrompt); !blocked {
		t.Fatal("expected prompt rule to match")
	}

	// Reopen the same database directory and prove the file is the source of truth.
	reopened, err := OpenGatewayStore(filepath.Join(store.dbDir, "gateway.db"))
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()
	view, err = reopened.ListSensitiveFilter()
	if err != nil || len(view.Rules) != 2 {
		t.Fatalf("reopened view=%d err=%v", len(view.Rules), err)
	}
}

func TestSensitiveFilterRuleLifecycle(t *testing.T) {
	store := openSensitiveStore(t)
	created, _, err := store.AddSensitiveRules([]SensitiveRuleInput{{Text: "kubectl delete", Scope: "all"}})
	if err != nil {
		t.Fatalf("add rule: %v", err)
	}
	rule := created[0]
	updated, err := store.SetSensitiveRuleEnabled(rule.ID, false)
	if err != nil || updated.Enabled {
		t.Fatalf("disable rule: enabled=%v err=%v", updated.Enabled, err)
	}
	if _, blocked := store.MatchSensitiveText("usr_a", "dev", "node", "kubectl delete pod", sensitiveScopeCommand); blocked {
		t.Fatal("disabled rule must not match")
	}
	if _, err := store.SetSensitiveRuleEnabled("sw_missing", true); !errors.Is(err, errSensitiveRuleNotFound) {
		t.Fatalf("missing rule err = %v", err)
	}
	if err := store.DeleteSensitiveRule(rule.ID); err != nil {
		t.Fatalf("delete rule: %v", err)
	}
	if err := store.DeleteSensitiveRule(rule.ID); !errors.Is(err, errSensitiveRuleNotFound) {
		t.Fatalf("second delete err = %v", err)
	}
	view, err := store.ListSensitiveFilter()
	if err != nil || len(view.Rules) != 0 {
		t.Fatalf("view=%d err=%v", len(view.Rules), err)
	}
}

func TestSensitiveFilterWhitelists(t *testing.T) {
	store := openSensitiveStore(t)
	if _, _, err := store.AddSensitiveRules([]SensitiveRuleInput{{Text: "drop database", Scope: "all"}}); err != nil {
		t.Fatalf("add rule: %v", err)
	}

	if _, blocked := store.MatchSensitiveText("usr_a", "dev", "node", "drop database prod", sensitiveScopeCommand); !blocked {
		t.Fatal("expected block before whitelisting")
	}
	if _, err := store.AddSensitiveWhitelist(SensitiveWhitelistInput{Kind: "user", UserID: "usr_a"}); err != nil {
		t.Fatalf("whitelist user: %v", err)
	}
	if _, blocked := store.MatchSensitiveText("usr_a", "other", "node", "drop database prod", sensitiveScopeCommand); blocked {
		t.Fatal("whitelisted user must be exempt on every target")
	}
	if _, blocked := store.MatchSensitiveText("usr_b", "dev", "node", "drop database prod", sensitiveScopeCommand); !blocked {
		t.Fatal("other users must still be filtered")
	}

	if _, err := store.AddSensitiveWhitelist(SensitiveWhitelistInput{Kind: "target", Cluster: "dev"}); err != nil {
		t.Fatalf("whitelist target: %v", err)
	}
	if _, blocked := store.MatchSensitiveText("usr_b", "dev", "any-instance", "drop database prod", sensitiveScopeCommand); blocked {
		t.Fatal("cluster-wide target whitelist must cover every instance")
	}
	if _, blocked := store.MatchSensitiveText("usr_b", "prod", "node", "drop database prod", sensitiveScopeCommand); !blocked {
		t.Fatal("other clusters must still be filtered")
	}

	if _, err := store.AddSensitiveWhitelist(SensitiveWhitelistInput{Kind: "target", Cluster: "dev"}); !errors.Is(err, errSensitiveWhitelistExists) {
		t.Fatalf("duplicate target err = %v", err)
	}
	if err := store.DeleteSensitiveWhitelist(SensitiveWhitelistInput{Kind: "user", UserID: "usr_a"}); err != nil {
		t.Fatalf("delete user whitelist: %v", err)
	}
	if _, blocked := store.MatchSensitiveText("usr_a", "prod", "node", "drop database prod", sensitiveScopeCommand); !blocked {
		t.Fatal("removed user whitelist must be filtered again")
	}
}

func TestSensitiveFilterValidation(t *testing.T) {
	store := openSensitiveStore(t)
	cases := []struct {
		name  string
		input SensitiveRuleInput
	}{
		{name: "empty text", input: SensitiveRuleInput{Scope: "all"}},
		{name: "newline", input: SensitiveRuleInput{Text: "line\nbreak", Scope: "all"}},
		{name: "control", input: SensitiveRuleInput{Text: "bell\a", Scope: "all"}},
		{name: "bad scope", input: SensitiveRuleInput{Text: "term", Scope: "other"}},
	}
	for _, tc := range cases {
		if _, _, err := store.AddSensitiveRules([]SensitiveRuleInput{tc.input}); !errors.Is(err, errSensitiveInvalid) {
			t.Fatalf("%s err = %v", tc.name, err)
		}
	}
	if _, err := store.AddSensitiveWhitelist(SensitiveWhitelistInput{Kind: "user"}); !errors.Is(err, errSensitiveInvalid) {
		t.Fatalf("empty user err = %v", err)
	}
	if _, err := store.AddSensitiveWhitelist(SensitiveWhitelistInput{Kind: "user", UserID: "usr_a", Cluster: "dev"}); !errors.Is(err, errSensitiveInvalid) {
		t.Fatalf("user with cluster err = %v", err)
	}
	if _, err := store.AddSensitiveWhitelist(SensitiveWhitelistInput{Kind: "target", UserID: "usr_a", Cluster: "dev"}); !errors.Is(err, errSensitiveInvalid) {
		t.Fatalf("target with user err = %v", err)
	}
	if _, err := store.AddSensitiveWhitelist(SensitiveWhitelistInput{Kind: "target", Cluster: "dev node"}); !errors.Is(err, errSensitiveInvalid) {
		t.Fatalf("target with space err = %v", err)
	}
}

func TestSensitiveFilterCorruptFileFailsOpen(t *testing.T) {
	store := openSensitiveStore(t)
	if _, _, err := store.AddSensitiveRules([]SensitiveRuleInput{{Text: "secret", Scope: "all"}}); err != nil {
		t.Fatalf("add rule: %v", err)
	}
	if err := os.WriteFile(store.sensitiveFilterFilePath(), []byte("{broken"), 0600); err != nil {
		t.Fatalf("corrupt filter file: %v", err)
	}
	store.sensitiveFilter = nil
	if _, blocked := store.MatchSensitiveText("usr_a", "dev", "node", "secret", sensitiveScopeCommand); blocked {
		t.Fatal("corrupt filter file must fail open")
	}
	if _, err := store.ListSensitiveFilter(); err == nil {
		t.Fatal("list must report the corrupt file")
	}
}

func TestSensitiveFilterExtractsOnlyUserText(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args string
		want []sensitiveFilterSegment
	}{
		{name: "prompt", tool: "doops_agent_prompt", args: `{"instruction":"deploy"}`,
			want: []sensitiveFilterSegment{{text: "deploy", scope: sensitiveScopePrompt}}},
		{name: "dosql", tool: "doops_agent_prompt",
			args: `{"instruction":"apply change","skill":"dosql","sql":"UPDATE t SET v = 1","sql_verification":"DELETE FROM t"}`,
			want: []sensitiveFilterSegment{
				{text: "apply change", scope: sensitiveScopePrompt},
				{text: "UPDATE t SET v = 1", scope: sensitiveScopeCommand},
				{text: "DELETE FROM t", scope: sensitiveScopeCommand},
			}},
		{name: "shell", tool: "doops_shell", args: `{"command":"ls -la"}`,
			want: []sensitiveFilterSegment{{text: "ls -la", scope: sensitiveScopeCommand}}},
		{name: "background", tool: "doops_bg", args: `{"command":"tail -f log"}`,
			want: []sensitiveFilterSegment{{text: "tail -f log", scope: sensitiveScopeCommand}}},
		{name: "ssh", tool: "doops_ssh", args: `{"command":["rm","-rf","/tmp"]}`,
			want: []sensitiveFilterSegment{{text: "rm -rf /tmp", scope: sensitiveScopeCommand}}},
		{name: "structured k8s", tool: "doops_k8s", args: `{"command":"delete"}`, want: nil},
	}
	for _, tc := range cases {
		got := sensitiveFilterSegmentsForTool(tc.tool, json.RawMessage(tc.args))
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: got %#v, want %#v", tc.name, got, tc.want)
		}
	}
}
