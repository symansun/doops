package server

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Canaries for the client tools/call audit boundary. The userinfo half is
// already redacted by sanitizeSecretText's "scheme://userinfo@" rule; the query
// half is not, because "customer" is not a credential-looking key. The audit
// must drop both, and keep the scheme://host:port/path an operator needs to see
// which backend refused the call.
const (
	toolCallAuditUser     = "tool-call-user-9e2"
	toolCallAuditPass     = "tool-call-pass-5d1"
	toolCallAuditCustomer = "tool-call-customer-3b7"
	toolCallAuditHost     = "db.audit-scrub.test"
)

func toolCallAuditURL() string {
	return "postgres://" + toolCallAuditUser + ":" + toolCallAuditPass + "@" + toolCallAuditHost +
		":5432/app?customer=" + toolCallAuditCustomer + "&sslmode=disable"
}

func toolCallAuditLeaks(text string) []string {
	var leaked []string
	for _, canary := range []string{
		toolCallAuditUser, toolCallAuditPass, "customer=", toolCallAuditCustomer,
	} {
		if strings.Contains(text, canary) {
			leaked = append(leaked, canary)
		}
	}
	return leaked
}

// TestClientToolCallAuditScrubsAgentURL covers client_tool_call.go's finishAudit:
// an Agent tool failure is relayed to the client and into audit_events.error, and
// its text can quote the URL the tool tried to reach. The audit keeps the host and
// path, drops userinfo and query, and leaves the classification alone.
func TestClientToolCallAuditScrubsAgentURL(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	agent := f.agent(t, "tool-call-audit-scrub")
	client := f.client(t, f.admin)

	lifecycleSend(t, client, 11, "doops_shell", "tool-call-scrub", map[string]interface{}{
		"command": "psql " + toolCallAuditURL(),
	})
	call := agent.call(t, "doops_shell", "tool-call-scrub")
	agent.complete(t, call, fmt.Sprintf("fatal: unable to access %q: dial tcp: connection refused", toolCallAuditURL()))

	if err := client.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	msg := readUntilID(t, client, 11)
	_ = client.SetReadDeadline(time.Time{})
	if msg["error"] == nil {
		t.Fatalf("client tools/call = %#v, want a terminal error", msg)
	}

	events, err := f.store.ListAuditFiltered(AuditFilter{Session: "tool-call-scrub"})
	if err != nil || len(events) != 1 {
		t.Fatalf("tools/call audit history = %#v, %v", events, err)
	}
	event := events[0]
	if event.Status != "error" {
		t.Fatalf("audit status = %q, want error (classification must not change)", event.Status)
	}
	if leaked := toolCallAuditLeaks(event.Error); len(leaked) > 0 {
		t.Fatalf("audit error still carries %v: %s", leaked, event.Error)
	}
	// The row still says which backend failed.
	if !strings.Contains(event.Error, "postgres://"+toolCallAuditHost+":5432/app") {
		t.Fatalf("audit error lost the scheme, host and path: %q", event.Error)
	}
}

// TestClientToolCallAuditTextNeedsURLScrub pins why finishAudit calls
// scrubAuditError instead of sanitizeSecretText: the plain sanitizer drops the
// userinfo but keeps a query value under a harmless-looking key, so the site
// cannot fall back to it without leaking that half of the URL.
func TestClientToolCallAuditTextNeedsURLScrub(t *testing.T) {
	text := fmt.Sprintf("fatal: unable to access %q: dial tcp: connection refused", toolCallAuditURL())

	sanitized := sanitizeSecretText(text)
	if strings.Contains(sanitized, toolCallAuditPass) {
		t.Fatalf("sanitizeSecretText no longer drops userinfo, so this test no longer pins the query gap: %q", sanitized)
	}
	if !strings.Contains(sanitized, toolCallAuditCustomer) {
		t.Fatalf("sanitizeSecretText already drops the query, so scrubAuditError adds nothing here: %q", sanitized)
	}

	scrubbed := scrubAuditError(text)
	if leaked := toolCallAuditLeaks(scrubbed); len(leaked) > 0 {
		t.Fatalf("scrubAuditError still carries %v: %s", leaked, scrubbed)
	}
	if !strings.Contains(scrubbed, "postgres://"+toolCallAuditHost+":5432/app") {
		t.Fatalf("scrubAuditError lost the scheme, host and path: %q", scrubbed)
	}
	if !strings.Contains(scrubbed, "dial tcp: connection refused") {
		t.Fatalf("scrubAuditError lost the error body: %q", scrubbed)
	}
}
