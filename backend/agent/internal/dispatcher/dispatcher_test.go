package dispatcher

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestConfiguredRuleRejectsShellAndArgv(t *testing.T) {
	t.Setenv(RulesEnv, `[{"name":"test_rule","match":"^printf 'two words'$","hint":"use a single word"}]`)
	_, argvErr := CommandContext(context.Background(), "printf", "two words")
	for _, err := range []error{Check("printf 'two words'"), argvErr} {
		var denied *BlockedError
		if !errors.As(err, &denied) || denied.Rule != "test_rule" || denied.Hint != "use a single word" {
			t.Fatalf("unexpected denial: %v", err)
		}
	}
	cmd, err := CommandContext(context.Background(), "printf", "two", "words")
	if err != nil || len(cmd.Args) != 3 {
		t.Fatalf("distinct arguments were merged: cmd=%v err=%v", cmd, err)
	}
	if err := Check("rm -rf /"); err == nil {
		t.Fatal("configured rule displaced the built-in rule")
	}
}

func TestInvalidRuleConfigFailsClosed(t *testing.T) {
	for _, raw := range []string{"", "null", "{", `[] []`, `[{"name":"missing_hint","match":"^test$"}]`, `[{"name":"bad_regex","match":"[","hint":"stop"}]`, `[{"name":"delete_root_filesystem","match":"x","hint":"stop"}]`, `[{"name":"typo","match":"x","hint":"stop","other":1}]`} {
		t.Setenv(RulesEnv, raw)
		_, err := CommandContext(context.Background(), "printf", "ok")
		if ValidateConfig() == nil || Check("printf ok") == nil || err == nil {
			t.Fatalf("invalid rules accepted: %s", raw)
		}
	}
}

func TestCheckBlocksOnlyRootFilesystemDeletion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		command string
		// want is the rule that must refuse the command; empty means it runs.
		// The argv layer ("deny_rm_root_filesystem") and the regex fallback
		// ("delete_root_filesystem") have distinct names on purpose, so each row
		// records which layer is expected to do the work.
		want string
	}{
		{"rm -rf root", "rm -rf /", "deny_rm_root_filesystem"},
		{"rm -rf root glob", "rm -rf /*", "deny_rm_root_filesystem"},
		{"rm -fr root in chain", "cd /tmp && rm -fr / ; echo done", "deny_rm_root_filesystem"},
		{"rm with split flags", "rm -r -f /", "deny_rm_root_filesystem"},
		// "RM" is not the tool name "rm" for the argv layer, so the
		// case-insensitive regex fallback is what refuses it.
		{"uppercase", "RM -RF /", "delete_root_filesystem"},

		// Everything else a target grant allows runs as given.
		{"rm -rf a subdirectory", "rm -rf /tmp/doops-build", ""},
		{"rm -rf relative", "rm -rf ./dist", ""},
		{"mkfs", "mkfs.ext4 /dev/sdb1", ""},
		{"fdisk", "fdisk /dev/sdb", ""},
		{"apply a NodePort service", "kubectl -n test apply -f test-mysql-nodeport.yaml", ""},
		{"base64 manifest into apply", "echo eA== | base64 -d | kubectl -n test apply -f -", ""},
		{"heredoc into apply", "kubectl apply -f - <<'EOF'\nkind: Service\nEOF", ""},
		{"set image on agent", "kubectl -n doops-system set image deployment/doops-agent doops-agent=image:v2", ""},
		{"restart agent container", "docker restart doops-agent", ""},
		{"helm upgrade", "helm upgrade --install app ./chart -n apps", ""},
		{"ssh with remote command", "ssh ops@app.internal 'sudo reboot'", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := Check(tt.command)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Check(%q) = %v, want allowed", tt.command, err)
				}
				return
			}
			var blocked *BlockedError
			if !errors.As(err, &blocked) || blocked.Rule != tt.want {
				t.Fatalf("Check(%q) = %v, want %s", tt.command, err, tt.want)
			}
			if !strings.Contains(err.Error(), blocked.Rule) || !strings.Contains(err.Error(), blocked.Hint) {
				t.Fatalf("blocked error must name the rule and the alternative: %q", err)
			}
		})
	}
}

func TestDenialReportsWhereTheRuleMatched(t *testing.T) {
	t.Setenv(RulesEnv, `[{"name":"test_rule","match":"DENIED_MARKER","hint":"use a single word"}]`)
	err := Check("touch denied; printf '%s' DENIED_MARKER")
	denied, ok := err.(*BlockedError)
	if !ok || denied.Rule != "test_rule" || denied.Hint != "use a single word" {
		t.Fatalf("unexpected denial: %v", err)
	}
	if !strings.HasPrefix(err.Error(), "blocked_command(test_rule): next: use a single word; matched: ") {
		t.Fatalf("denial must keep the parseable prefix: %q", err)
	}
	if !strings.Contains(denied.Matched, "DENIED_MARKER") || !strings.Contains(err.Error(), `matched: "`) {
		t.Fatalf("denial must show where the rule matched: %#v / %q", denied, err)
	}
	if err := Check("printf '%s' 'safe text'"); err != nil {
		t.Fatalf("unmatched command was denied: %v", err)
	}
}

func TestDenialMatchHandlesMultilineAndLongCommands(t *testing.T) {
	t.Setenv(RulesEnv, `[{"name":"marker","match":"(?m)^kubectl drain","hint":"wait for the window"}]`)
	command := "set -e\necho first\nkubectl drain node-1 --ignore-daemonsets --delete-emptydir-data\necho done"
	denied, ok := Check(command).(*BlockedError)
	if !ok {
		t.Fatal("multiline command was not denied")
	}
	if want := "set -e\necho first\nkubectl drain"; !strings.Contains(denied.Matched, want) {
		t.Fatalf("matched excerpt = %q, want context around %q", denied.Matched, want)
	}
	for _, line := range strings.Split(denied.Matched, "\n") {
		if len(line) > MatchedLimit {
			t.Fatalf("matched excerpt line exceeds %d bytes: %q", MatchedLimit, line)
		}
	}

	long := strings.Repeat("x", 4000) + " DENIED_MARKER " + strings.Repeat("y", 4000)
	t.Setenv(RulesEnv, `[{"name":"marker","match":"DENIED_MARKER","hint":"stop"}]`)
	denied, ok = Check(long).(*BlockedError)
	if !ok {
		t.Fatal("long command was not denied")
	}
	if len(denied.Matched) > MatchedLimit+len("......") {
		t.Fatalf("long match was not bounded: %d bytes", len(denied.Matched))
	}
	if !strings.Contains(denied.Matched, "DENIED_MARKER") || !strings.HasPrefix(denied.Matched, "...") || !strings.HasSuffix(denied.Matched, "...") {
		t.Fatalf("long match lacks a bounded excerpt: %q", denied.Matched)
	}
}

func TestDenialMatchUsesTheInstalledRedactor(t *testing.T) {
	t.Setenv(RulesEnv, `[{"name":"marker","match":"DENIED_MARKER","hint":"stop"}]`)
	Redactor, MatchedCap = func(string) string { return "[REDACTED]" }, nil
	t.Cleanup(func() { Redactor, MatchedCap = nil, nil })
	denied, ok := Check("curl -H 'Authorization: Bearer synthetic-canary' DENIED_MARKER").(*BlockedError)
	if !ok {
		t.Fatal("command was not denied")
	}
	if strings.Contains(denied.Matched, "synthetic-canary") || !strings.Contains(denied.Matched, "[REDACTED]") {
		t.Fatalf("matched excerpt was not redacted: %q", denied.Matched)
	}
}

func TestReportedDenialAcceptsCurrentAndLegacyMessages(t *testing.T) {
	t.Setenv(RulesEnv, `[{"name":"marker","match":"DENIED_MARKER","hint":"stop now"}]`)
	current := `some shell noise
blocked_command(marker): next: stop now; matched: "DENIED_MARKER"` + "\n"
	for _, output := range []string{current, "blocked_command(marker): next: stop now\n"} {
		denied := ReportedDenial(output)
		if denied == nil || denied.Rule != "marker" || denied.Hint != "stop now" || denied.Matched != "" {
			t.Fatalf("ReportedDenial(%q) = %#v", output, denied)
		}
	}
	for _, output := range []string{
		"",
		"blocked_command(marker): next: stop now-suffix\n",
		"unrelated output\n",
	} {
		if denied := ReportedDenial(output); denied != nil {
			t.Fatalf("ReportedDenial(%q) = %#v, want nil", output, denied)
		}
	}
}
