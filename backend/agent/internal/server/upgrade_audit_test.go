package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type upgradeAuditCall struct {
	id   interface{}
	args map[string]interface{}
}

func startUpgradeAuditAgent(t *testing.T, f *executionLifecycleFixture) (*websocket.Conn, <-chan upgradeAuditCall) {
	t.Helper()
	conn := dialFakeGatewayAgent(t, f.server.URL, "dev", "local")
	t.Cleanup(func() { _ = conn.Close() })
	calls := make(chan upgradeAuditCall, 10)
	go func() {
		for {
			var msg map[string]interface{}
			if conn.ReadJSON(&msg) != nil {
				return
			}
			switch msg["method"] {
			case "initialize":
				_ = conn.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "id": msg["id"], "result": gatewayTestInitializeResult("")})
			case "tools/call":
				params, _ := msg["params"].(map[string]interface{})
				args, _ := params["arguments"].(map[string]interface{})
				calls <- upgradeAuditCall{id: msg["id"], args: args}
			case "tools/cancel":
				_ = conn.WriteJSON(buildErrorResponse(msg["id"], -32007, "operation canceled"))
			}
		}
	}()
	waitForGatewayAgent(t, f.hub, "dev", "local")
	return conn, calls
}

func sendUpgradeAuditCall(t *testing.T, conn *websocket.Conn, id int, args map[string]interface{}) {
	t.Helper()
	writeGatewayJSON(t, conn, map[string]interface{}{
		"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]interface{}{"name": "doops_agent_upgrade", "arguments": args},
	})
}

func readUpgradeAuditCall(t *testing.T, calls <-chan upgradeAuditCall) upgradeAuditCall {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(3 * time.Second):
		t.Fatal("upgrade was not forwarded")
		return upgradeAuditCall{}
	}
}

func upgradeAuditTestResult(call upgradeAuditCall, outcome, text string) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{{"type": "text", "text": text}},
		"structuredContent": map[string]interface{}{
			"upgrade_operation_id": call.args["upgrade_operation_id"],
			"outcome":              outcome, "job": "doops-self-upgrade-12345678",
			"namespace": "doops-system", "release": "doops-agent",
		},
	}
}

func TestGatewayUpgradeAuditPersistsResultsAndAttribution(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{OperationTimeout: 5 * time.Second})
	user := f.user(t, "upgrade-operator", ScopeGrant{Cluster: "dev", Instance: "local"})
	auth, err := f.store.VerifyUserToken(user.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	agent, calls := startUpgradeAuditAgent(t, f)
	client := f.client(t, user)
	for index, tc := range []struct {
		outcome string
		status  string
		phase   string
	}{
		{"accepted", "accepted", "preflight"},
		{"accepted", "accepted", "apply"},
		{"running", "observing", "observe"},
		{"failed", "error", "observe"},
		{"busy", "busy", "apply"},
		{"succeeded", "success", "observe"},
		{"dry_run", "success", "dry_run"},
		{"accepted", "accepted", "rollback"},
	} {
		t.Run(tc.outcome+"-"+tc.phase, func(t *testing.T) {
			args := map[string]interface{}{
				"session_id": "upgrade-session", "upgrade_operation_id": "upg-audit-flow",
				"image":                           "registry.example/doops@sha256:" + strings.Repeat("a", 64),
				"expected_release_commit":         strings.Repeat("b", 40),
				"allow_missing_rollback_baseline": true,
				"requester_id":                    "forged-user", "audit_id": 999999,
			}
			switch tc.phase {
			case "observe":
				args["observe_job"] = "doops-self-upgrade-12345678"
			case "preflight":
				args["preflight"] = true
			case "rollback":
				delete(args, "image")
				args["rollback"] = true
			case "dry_run":
				args["dry_run"] = true
			}
			id := index + 10
			sendUpgradeAuditCall(t, client, id, args)
			call := readUpgradeAuditCall(t, calls)
			if call.args["requester_id"] != auth.UserID || call.args["audit_id"] == float64(999999) {
				t.Fatalf("caller forged attribution: %#v", call.args)
			}
			text := fmt.Sprintf("self-upgrade-%s job=doops-self-upgrade-12345678 namespace=doops-system\npassword=SYNTHETIC_UPGRADE_SECRET\n%s token=SYNTHETIC_UPGRADE_SECRET",
				tc.outcome, strings.Repeat("safe-evidence ", 150))
			result := upgradeAuditTestResult(call, tc.outcome, text)
			if tc.outcome == "failed" {
				// Even a faulty Agent cannot turn a structured failed outcome
				// into success just by omitting isError.
				result["structuredContent"].(map[string]interface{})["category"] = "rollback_failed"
			}
			writeGatewayJSON(t, agent, map[string]interface{}{"jsonrpc": "2.0", "id": call.id, "result": result})
			response := readUntilID(t, client, float64(id))
			if response["error"] != nil {
				t.Fatalf("unexpected RPC failure: %#v", response)
			}
			events, err := f.store.ListAuditFiltered(AuditFilter{CorrelationID: "upg-audit-flow", Limit: 1})
			if err != nil || len(events) != 1 {
				t.Fatalf("missing upgrade audit: %#v %v", events, err)
			}
			event := events[0]
			if event.Status != tc.status || event.CorrelationID != "upg-audit-flow" || event.BytesOut <= 0 ||
				!strings.Contains(event.CommandSummary, "phase="+tc.phase) ||
				!strings.Contains(event.CommandSummary, "allow_missing_rollback_baseline=true") {
				t.Fatalf("incorrect audit outcome: %#v", event)
			}
			var evidence map[string]interface{}
			if json.Unmarshal([]byte(event.Tail), &evidence) != nil || strings.Contains(event.Tail, "SYNTHETIC_UPGRADE_SECRET") ||
				!strings.Contains(event.Tail, "safe-evidence") || event.UserID != auth.UserID {
				t.Fatalf("invalid or leaking evidence: %#v", event)
			}
			if tc.status == "error" && (event.Error != "rollback_failed" ||
				response["result"].(map[string]interface{})["isError"] != true) {
				t.Fatalf("failed upgrade was not surfaced: event=%#v response=%#v", event, response)
			}
		})
	}
	other := f.user(t, "other-operator", ScopeGrant{Cluster: "dev", Instance: "local"})
	for _, tc := range []struct {
		token CreatedToken
		count int
	}{{user, 7}, {f.admin, 7}, {other, 0}} {
		data := f.http(t, tc.token, http.MethodGet, "/v1/audit?correlation_id=upg-audit-flow&hide_upgrade_observe=true", "", http.StatusOK)
		var response auditResponse
		if json.Unmarshal(data, &response) != nil || len(response.Events) != tc.count {
			t.Fatalf("operation visibility: %s", data)
		}
	}
}

func TestGatewayUpgradeAuditUnavailableNeverDispatches(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	_, calls := startUpgradeAuditAgent(t, f)
	client := f.client(t, f.admin)
	if _, err := f.store.db.Exec(`CREATE TRIGGER fail_upgrade_audit BEFORE INSERT ON audit_events
		BEGIN SELECT RAISE(FAIL, 'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	sendUpgradeAuditCall(t, client, 10, map[string]interface{}{"image": "registry.example/doops:latest"})
	response := readUntilID(t, client, 10)
	if !strings.Contains(fmt.Sprint(response["error"]), "audit_unavailable") {
		t.Fatalf("upgrade did not fail closed: %#v", response)
	}
	select {
	case call := <-calls:
		t.Fatalf("unaudited upgrade was dispatched: %#v", call)
	default:
	}
}

func TestGatewayUpgradeAuditProtocolGatePrecedesMutation(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	conn := dialFakeGatewayAgent(t, f.server.URL, "dev", "local")
	t.Cleanup(func() { _ = conn.Close() })
	calls := make(chan bool, 1)
	go func() {
		for {
			var msg map[string]interface{}
			if conn.ReadJSON(&msg) != nil {
				return
			}
			if msg["method"] == "initialize" {
				result := gatewayTestInitializeResult("")
				result["capabilities"] = map[string]interface{}{}
				_ = conn.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "id": msg["id"], "result": result})
			} else if msg["method"] == "tools/call" {
				calls <- true
			}
		}
	}()
	waitForGatewayAgent(t, f.hub, "dev", "local")
	client := f.client(t, f.admin)
	sendUpgradeAuditCall(t, client, 10, map[string]interface{}{
		"image": "registry.example/doops:latest", "upgrade_operation_id": "upg-old-protocol",
	})
	response := readUntilID(t, client, 10)
	if !strings.Contains(fmt.Sprint(response["error"]), "upgrade_audit_protocol_required") {
		t.Fatalf("old protocol was not blocked before dispatch: %#v", response)
	}
	select {
	case <-calls:
		t.Fatal("upgrade reached an Agent without the evidence protocol")
	default:
	}
	events, err := f.store.ListAuditFiltered(AuditFilter{CorrelationID: "upg-old-protocol"})
	if err != nil || len(events) != 1 || events[0].Status != "blocked" {
		t.Fatalf("protocol gate audit missing: %#v %v", events, err)
	}
}

func TestGatewayUpgradeAuditCompletionErrorPreservesAcceptance(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	agent, calls := startUpgradeAuditAgent(t, f)
	client := f.client(t, f.admin)
	sendUpgradeAuditCall(t, client, 10, map[string]interface{}{
		"image": "registry.example/doops:latest", "upgrade_operation_id": "upg-pending-audit",
	})
	call := readUpgradeAuditCall(t, calls)
	if _, err := f.store.db.Exec(`CREATE TRIGGER fail_upgrade_finish BEFORE UPDATE OF status ON audit_events
		WHEN NEW.status = 'accepted' BEGIN SELECT RAISE(FAIL, 'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	result := upgradeAuditTestResult(call, "accepted", "self-upgrade-accepted job=doops-self-upgrade-12345678 namespace=doops-system release=doops-agent")
	writeGatewayJSON(t, agent, map[string]interface{}{"jsonrpc": "2.0", "id": call.id, "result": result})
	response := readUntilID(t, client, 10)
	rpcError, ok := response["error"].(map[string]interface{})
	if !ok || !strings.Contains(fmt.Sprint(rpcError["message"]), "audit_completion_failed") {
		t.Fatalf("audit failure disappeared: %#v", response)
	}
	data, ok := rpcError["data"].(map[string]interface{})
	if !ok || data["upgrade_operation_id"] != "upg-pending-audit" ||
		!strings.Contains(fmt.Sprint(data["result"]), "doops-self-upgrade-12345678") {
		t.Fatalf("acceptance recovery evidence was lost: %#v", response)
	}
	if _, err := f.store.db.Exec("DROP TRIGGER fail_upgrade_finish"); err != nil {
		t.Fatal(err)
	}
	requireEventually(t, 3*time.Second, func() bool {
		events, err := f.store.ListAuditFiltered(AuditFilter{CorrelationID: "upg-pending-audit"})
		return err == nil && len(events) == 1 && events[0].Status == "accepted" && events[0].Tail != ""
	})
}

func TestGatewayUpgradeDisconnectedMutationIsUnknown(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{OperationTimeout: time.Second})
	agent, calls := startUpgradeAuditAgent(t, f)
	client := f.client(t, f.admin)
	sendUpgradeAuditCall(t, client, 10, map[string]interface{}{
		"image": "registry.example/doops:latest", "upgrade_operation_id": "upg-disconnected",
	})
	_ = readUpgradeAuditCall(t, calls)
	_ = agent.Close()
	response := readUntilID(t, client, 10)
	if !strings.Contains(fmt.Sprint(response["error"]), "outcome unknown") {
		t.Fatalf("lost dispatch claimed a known result: %#v", response)
	}
	events, err := f.store.ListAuditFiltered(AuditFilter{CorrelationID: "upg-disconnected"})
	if err != nil || len(events) != 1 || events[0].Status != "outcome_unknown" {
		t.Fatalf("incorrect disconnected audit: %#v %v", events, err)
	}
}

func TestAuditOperationFilteringAndPurgeAreAudited(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	old := time.Now().UTC().Add(-time.Hour)
	for _, status := range []string{"accepted", "observing", "success", "error", "outcome_unknown"} {
		id, err := f.store.StartAudit(AuditEvent{Action: ActionAgentUpgrade, CorrelationID: "upg-purge", StartedAt: old})
		if err != nil {
			t.Fatal(err)
		}
		outcome := map[string]string{"accepted": "accepted", "observing": "running", "success": "succeeded", "error": "failed", "outcome_unknown": "unknown"}[status]
		tail, _ := json.Marshal(map[string]interface{}{"result": map[string]string{"job": "completed-job", "outcome": outcome}})
		if err := f.store.FinishAudit(id, AuditFinish{Status: status, Tail: string(tail)}); err != nil {
			t.Fatal(err)
		}
	}
	since, until := url.QueryEscape(old.Add(-time.Minute).Format(time.RFC3339)), url.QueryEscape(old.Add(time.Minute).Format(time.RFC3339))
	path := "/v1/audit?correlation_id=upg-purge&hide_upgrade_observe=true&limit=2&since=" + since + "&until=" + until
	first := f.http(t, f.admin, http.MethodGet, path, "", http.StatusOK)
	var page auditResponse
	if err := json.Unmarshal(first, &page); err != nil || len(page.Events) != 2 {
		t.Fatalf("bad first page: %s", first)
	}
	second := f.http(t, f.admin, http.MethodGet, path+fmt.Sprintf("&before_id=%d", page.Events[1].ID), "", http.StatusOK)
	if err := json.Unmarshal(second, &page); err != nil || len(page.Events) != 2 {
		t.Fatalf("bad second page: %s", second)
	}
	f.http(t, f.admin, http.MethodGet, "/v1/audit?before_id=invalid", "", http.StatusBadRequest)
	before := url.QueryEscape(time.Now().UTC().Add(-time.Minute).Format(time.RFC3339))
	if _, err := f.store.db.Exec(`CREATE TRIGGER fail_purge_receipt BEFORE UPDATE OF status ON audit_events
		WHEN OLD.action = 'audit:purge' BEGIN SELECT RAISE(FAIL, 'receipt unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	f.http(t, f.admin, http.MethodDelete, "/v1/audit?before="+before, "", http.StatusServiceUnavailable)
	events, err := f.store.ListAuditFiltered(AuditFilter{CorrelationID: "upg-purge"})
	if err != nil || len(events) != 5 {
		t.Fatalf("failed purge removed records: %#v %v", events, err)
	}
	if _, err := f.store.db.Exec("DROP TRIGGER fail_purge_receipt"); err != nil {
		t.Fatal(err)
	}
	f.http(t, f.admin, http.MethodDelete, "/v1/audit?before="+before, "", http.StatusOK)
	events, err = f.store.ListAuditFiltered(AuditFilter{CorrelationID: "upg-purge"})
	if err != nil || len(events) != 1 || events[0].Status != "outcome_unknown" {
		t.Fatalf("purge erased unresolved outcome: %#v %v", events, err)
	}
	receipts, err := f.store.ListAuditFiltered(AuditFilter{Action: GatewayAction("audit:purge")})
	if err != nil || len(receipts) != 1 || receipts[0].Status != "success" || !strings.Contains(receipts[0].Tail, `"deleted":4`) {
		t.Fatalf("purge receipt missing: %#v %v", receipts, err)
	}
	ordinary := f.user(t, "purge-denied", ScopeGrant{Cluster: "dev", Instance: "local"})
	f.http(t, ordinary, http.MethodDelete, "/v1/audit?before="+before, "", http.StatusForbidden)
}

func TestAuditPurgeRetainsOutstandingControllerEvidence(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	seed := func(status, operation, job, outcome string) {
		t.Helper()
		id, err := f.store.StartAudit(AuditEvent{Action: ActionAgentUpgrade, Cluster: "dev", Instance: "local", CorrelationID: operation, StartedAt: time.Now().Add(-time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		tail, _ := json.Marshal(map[string]interface{}{"result": map[string]string{"job": job, "outcome": outcome}})
		if err := f.store.FinishAudit(id, AuditFinish{Status: status, Tail: string(tail)}); err != nil {
			t.Fatal(err)
		}
	}
	seed("accepted", "op-a", "job-a", "accepted")
	seed("observing", "op-a", "job-a", "running")
	seed("success", "op-a", "job-b", "succeeded")
	seed("success", "op-b", "job-a", "succeeded")
	seed("outcome_unknown", "op-a", "job-a", "unknown")
	if deleted, err := f.store.PurgeAuditBefore(auth, time.Now()); err != nil || deleted != 2 {
		t.Fatalf("purge deleted unfinished controller evidence: count=%d err=%v", deleted, err)
	}
	seed("success", "op-a", "job-a", "succeeded")
	if deleted, err := f.store.PurgeAuditBefore(auth, time.Now()); err != nil || deleted != 3 {
		t.Fatalf("verified controller history was not eligible: count=%d err=%v", deleted, err)
	}
	events, err := f.store.ListAuditFiltered(AuditFilter{CorrelationID: "op-a"})
	if err != nil || len(events) != 1 || events[0].Status != "outcome_unknown" {
		t.Fatalf("unknown outcome disappeared: %#v %v", events, err)
	}
}

func TestAuditTimeRangePreservesSecondBoundary(t *testing.T) {
	store := openCredentialTestStore(t)
	base := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	for _, offset := range []time.Duration{-time.Second, 0, 500 * time.Millisecond, time.Second} {
		id, err := store.StartAudit(AuditEvent{StartedAt: base.Add(offset)})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.FinishAudit(id, AuditFinish{Status: "success"}); err != nil {
			t.Fatal(err)
		}
	}
	events, err := store.ListAuditFiltered(AuditFilter{Since: base, Until: base.Add(time.Second)})
	if err != nil || len(events) != 2 || !events[0].StartedAt.Equal(base.Add(500*time.Millisecond)) || !events[1].StartedAt.Equal(base) {
		t.Fatalf("second-boundary range lost or included wrong events: %#v %v", events, err)
	}
	if deleted, err := store.DeleteAuditBefore(base); err != nil || deleted != 1 {
		t.Fatalf("purge crossed the cutoff: count=%d err=%v", deleted, err)
	}
}

func TestGatewayUpgradeFinalVerificationIsAudited(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	agent := dialFakeGatewayAgent(t, f.server.URL, "dev", "local")
	t.Cleanup(func() { _ = agent.Close() })
	go func() {
		for {
			var msg map[string]interface{}
			if agent.ReadJSON(&msg) != nil {
				return
			}
			if msg["method"] == "initialize" {
				result := gatewayTestInitializeResult("")
				result["serverInfo"] = map[string]interface{}{"releaseCommit": strings.Repeat("a", 40)}
				_ = agent.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "id": msg["id"], "result": result})
			}
		}
	}()
	waitForGatewayAgent(t, f.hub, "dev", "local")
	client := f.client(t, f.admin)
	for index, tc := range []struct{ expected, protocol, status, category string }{
		{strings.Repeat("a", 40), "", "success", ""},
		{strings.Repeat("b", 40), "", "error", "upgrade_verification_commit_mismatch"},
		{"", "structuredSSH", "error", "upgrade_verification_protocol_missing"},
	} {
		id := index + 10
		writeGatewayJSON(t, client, map[string]interface{}{"jsonrpc": "2.0", "id": id, "method": "initialize", "params": map[string]interface{}{
			"upgrade_operation_id": "op-verify", "session_id": "verify.session", "expected_release_commit": tc.expected, "required_protocol": tc.protocol,
		}})
		response := readUntilID(t, client, float64(id))
		if tc.category == "" && response["error"] != nil || tc.category != "" && !strings.Contains(fmt.Sprint(response["error"]), tc.category) {
			t.Fatalf("wrong verification response: %#v", response)
		}
		events, err := f.store.ListAuditFiltered(AuditFilter{CorrelationID: "op-verify", Limit: 1})
		if err != nil || len(events) != 1 || events[0].Status != tc.status || events[0].Error != tc.category ||
			!strings.Contains(events[0].CommandSummary, "phase=verify") || !strings.Contains(events[0].Tail, strings.Repeat("a", 40)) {
			t.Fatalf("verification evidence missing or incorrect: %#v %v", events, err)
		}
	}
}
