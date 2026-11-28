package server

import (
	"strings"
	"testing"

	"github.com/user/doops/agent/internal/dispatcher"
)

// TestDenialMatchIsRedactedByTheCommandSummaryHelper proves the bridge the
// package installs at startup: a denial excerpt in the server must be redacted
// exactly like an audit command summary, and must never show a payload.
func TestDenialMatchIsRedactedByTheCommandSummaryHelper(t *testing.T) {
	t.Setenv(dispatcher.RulesEnv, `[{"name":"marker","match":"DENIED_MARKER","hint":"stop"}]`)
	command := "curl -H 'Authorization: Bearer synthetic-canary' https://user:pw@example.test/x?token=synthetic DENIED_MARKER --retry 3"
	denied, ok := dispatcher.Check(command).(*dispatcher.BlockedError)
	if !ok {
		t.Fatal("command was not denied")
	}
	if denied.Matched != redactSecretText(denied.Matched) {
		t.Fatalf("matched excerpt was not redacted with the command-summary helper: %q", denied.Matched)
	}
	for _, secret := range []string{"synthetic-canary", "user:pw@"} {
		if strings.Contains(denied.Error(), secret) {
			t.Fatalf("denial leaked %q: %q", secret, denied.Error())
		}
	}
	if !strings.Contains(denied.Matched, "REDACTED") || !strings.Contains(denied.Matched, "DENIED_MARKER") {
		t.Fatalf("matched excerpt lost its redacted context: %q", denied.Matched)
	}
	if len(denied.Matched) > dispatcher.MatchedLimit+len("......") {
		t.Fatalf("matched excerpt was not capped: %d bytes", len(denied.Matched))
	}
	if matcher := dispatcher.ReportedDenial("noise\n" + denied.Error() + "\nmore"); matcher == nil || matcher.Rule != "marker" {
		t.Fatalf("ReportedDenial lost the current Agent message: %#v", matcher)
	}
}
