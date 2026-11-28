package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Canaries for the audit write points that persist text the Gateway did not
// author. They are inert strings, never real credentials: a test fails when any
// of them survives into audit_events.error.
const (
	auditSiteURLUser     = "audit-site-user-4a1"
	auditSiteURLPass     = "audit-site-pass-6b2"
	auditSiteURLCustomer = "audit-site-customer-7c3"
	auditSiteURLSig      = "audit-site-sig-1d8"
	auditSiteURLFragment = "audit-site-frag-2e9"
)

func auditSiteURL() string {
	return "https://" + auditSiteURLUser + ":" + auditSiteURLPass + "@" + auditScrubHost +
		"/v1/git-upload-pack?customer=" + auditSiteURLCustomer + "&sig=" + auditSiteURLSig +
		"#" + auditSiteURLFragment
}

func auditSiteLeaks(text string) []string {
	var leaked []string
	for _, canary := range []string{
		auditSiteURLUser, auditSiteURLPass, "customer=", auditSiteURLCustomer,
		auditSiteURLSig, auditSiteURLFragment,
	} {
		if strings.Contains(text, canary) {
			leaked = append(leaked, canary)
		}
	}
	return leaked
}

// TestInternalToolCallAuditScrubsAgentURL covers internal_call.go's terminal
// audit line: an internal call relays the Agent's JSON-RPC error verbatim
// (fmt.Errorf("%v", rpcErr)), and that envelope can quote the URL the Agent's
// tool tried to reach. The audit must keep the host and path (so the operator
// can see what failed) while dropping userinfo, query and fragment.
func TestInternalToolCallAuditScrubsAgentURL(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	agent := f.agent(t, "internal-audit-scrub")

	type result struct {
		output string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		output, err := f.hub.RunInternalToolCall(context.Background(), "dev", "local", "doops_shell",
			map[string]interface{}{"session_id": "internal-scrub", "command": "git clone " + auditSiteURL()})
		done <- result{output: output, err: err}
	}()
	agent.complete(t, agent.call(t, "doops_shell", "internal-scrub"),
		fmt.Sprintf("fatal: unable to access %q: The requested URL returned error: 403", auditSiteURL()))

	var got result
	select {
	case got = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("internal tool call did not return")
	}
	if got.err == nil || !strings.Contains(got.err.Error(), auditSiteURLPass) {
		t.Fatalf("Agent error text did not reach the internal call, so the audit assertion would be vacuous: %v", got.err)
	}

	events, err := f.store.ListAuditFiltered(AuditFilter{Session: "internal-scrub"})
	if err != nil || len(events) != 1 {
		t.Fatalf("internal audit history = %#v, %v", events, err)
	}
	event := events[0]
	if event.Status != "error" {
		t.Fatalf("audit status = %q, want error (classification must not change)", event.Status)
	}
	if !strings.Contains(event.Error, auditScrubHost+"/v1/git-upload-pack") {
		t.Fatalf("audit error lost the host and path: %q", event.Error)
	}
	if leaked := auditSiteLeaks(event.Error); len(leaked) > 0 {
		t.Fatalf("audit error still carries %v: %s", leaked, event.Error)
	}
}

// TestInternalToolCallFixedCategoryAuditHasNoURL covers the internal audit lines
// that record a Gateway-authored category (cos_snapshot_executable rejection is
// the reachable one; the remaining lines use the same shape). The call arguments
// carry a credentialed URL, and the audit must not echo any of it.
func TestInternalToolCallFixedCategoryAuditHasNoURL(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	f.agent(t, "internal-audit-cos")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := f.hub.RunInternalToolCall(ctx, "dev", "local", "doops_cos_sync", map[string]interface{}{
		"session_id": "internal-scrub-cos", "files": []interface{}{}, "prefix": auditSiteURL(),
	})
	if cosCategory(err) != cosErrAgentSnapshotUnsupported {
		t.Fatalf("cos rejection err = %v, want %s", err, cosErrAgentSnapshotUnsupported)
	}
	events, err := f.store.ListAuditFiltered(AuditFilter{Session: "internal-scrub-cos"})
	if err != nil || len(events) != 1 {
		t.Fatalf("cos audit history = %#v, %v", events, err)
	}
	event := events[0]
	if event.Status != "blocked" || event.Error != cosFail(cosErrAgentSnapshotUnsupported).Error() {
		t.Fatalf("cos audit = status %q error %q, want blocked %q", event.Status, event.Error, cosErrAgentSnapshotUnsupported)
	}
	if leaked := auditSiteLeaks(event.Error); len(leaked) > 0 {
		t.Fatalf("fixed-category audit error carries %v: %s", leaked, event.Error)
	}
}

// TestGitTunnelAuditKeepsOnlyRelayCategory pins the git tunnel's evidence-based
// non-leak: relayGitHTTPRequest returns the Agent's error text (which here
// quotes a credentialed URL), but git_tunnel.go records only the fixed
// "git_relay_failed" category. If a future change writes relayErr.Error() into
// the audit, this test fails on the canaries.
func TestGitTunnelAuditKeepsOnlyRelayCategory(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	user, _ := store.CreateUser("alice")
	userToken, _ := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "alice"})
	if err := store.GrantUser(user.ID, ScopeGrant{Cluster: "dev", Instance: "local"}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	gw := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, MaxQueuedPerTarget: -1, AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	gw.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	agentConn := dialFakeGatewayAgent(t, ts.URL, "dev", "local")
	defer agentConn.Close()
	go serveFailingGitAgent(t, agentConn, fmt.Sprintf("remote unreachable: %s", auditSiteURL()))

	req, _ := http.NewRequest(http.MethodGet,
		ts.URL+"/v1/git/dev/local/release.git/info/refs?service=git-upload-pack&token="+auditSiteURLCustomer, nil)
	req.SetBasicAuth("doops", userToken.Plaintext)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("git request through gateway: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("git relay failure status = %d: %s", resp.StatusCode, body)
	}

	events, err := store.ListAuditFiltered(AuditFilter{Session: "release"})
	if err != nil || len(events) != 1 {
		t.Fatalf("git audit history = %#v, %v", events, err)
	}
	event := events[0]
	if event.Status != "error" || event.Error != "git_relay_failed" {
		t.Fatalf("git audit = status %q error %q, want error git_relay_failed", event.Status, event.Error)
	}
	if leaked := auditSiteLeaks(event.Error); len(leaked) > 0 {
		t.Fatalf("git audit error carries %v: %s", leaked, event.Error)
	}
}

func serveFailingGitAgent(t *testing.T, conn *websocket.Conn, failure string) {
	t.Helper()
	for {
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		switch msg["method"] {
		case "initialize":
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result":  gatewayTestInitializeResult(""),
			})
		case "git/http":
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"error":   map[string]interface{}{"code": -32000, "message": failure},
			})
		}
	}
}
