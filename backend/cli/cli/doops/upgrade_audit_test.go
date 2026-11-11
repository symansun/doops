package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const (
	upgradeTestCommit  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	rollbackTestCommit = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

type upgradeGatewayScenario struct {
	auditPhases           map[string]bool
	failedPhase           string
	runningObservations   int
	wrongUpgradeCommit    bool
	rollbackCommitMissing bool
	rollbackCommitWrong   bool
	invalidResult         bool
	wrongAuditOperation   bool
	missingAuditResult    bool
	sensitiveResult       bool
	verificationError     string
	verificationReply     func(map[string]interface{}, string) map[string]interface{}
	verificationResponse  func(map[string]interface{}, map[string]interface{}) bool
}

type upgradeGatewayTrace struct {
	mu      sync.Mutex
	calls   map[string]int
	inits   []map[string]interface{}
	args    []map[string]interface{}
	auditID int
}

func newUpgradeAuditGateway(t *testing.T, scenario upgradeGatewayScenario) (Server, *upgradeGatewayTrace) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DOOPS_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	trace := &upgradeGatewayTrace{calls: map[string]int{}}
	upgrader := websocket.Upgrader{}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/targets" {
			_, _ = w.Write([]byte(`{"targets":[{"cluster":"test","instance":"node"},{"cluster":"test","instance":"other"}]}`))
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("websocket upgrade: %v", err)
			return
		}
		defer conn.Close()
		for {
			var req map[string]interface{}
			if err := conn.ReadJSON(&req); err != nil {
				return
			}
			trace.mu.Lock()
			response := map[string]interface{}{"jsonrpc": "2.0", "id": req["id"]}
			switch req["method"] {
			case "initialize":
				params := req["params"].(map[string]interface{})
				trace.inits = append(trace.inits, params)
				commit := upgradeTestCommit
				if trace.calls["rollback"] > 0 && !scenario.rollbackCommitWrong {
					commit = rollbackTestCommit
				} else if trace.calls["apply"] > 0 && scenario.wrongUpgradeCommit {
					commit = strings.Repeat("c", 40)
				}
				response["result"] = map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"capabilities": map[string]interface{}{
						"releaseCommit": commit,
						"protocols": map[string]interface{}{
							"structuredSSH": map[string]interface{}{
								"schemaVersion": "doops.sh/structured-ssh/v1",
								"tool":          "doops_ssh", "structuredArgv": true,
							},
						},
					},
				}
				if expected, _ := params["expected_release_commit"].(string); expected != "" && expected != commit {
					delete(response, "result")
					response["error"] = upgradeVerificationRPCError(params, commit, "upgrade_verification_commit_mismatch", false)
				}
				if params["required_protocol"] != nil && scenario.verificationError != "" {
					message := "upgrade_verification_failed password=SYNTHETIC_VERIFY_CANARY"
					if scenario.verificationError == "rpc" {
						delete(response, "result")
						response["error"] = map[string]interface{}{"code": -32003, "message": message}
					} else {
						// Even matching advertised capabilities cannot override a tool error.
						result := response["result"].(map[string]interface{})
						result["isError"] = true
						result["content"] = []map[string]string{{"type": "text", "text": message}}
					}
				}
				if scenario.verificationReply != nil && (params["required_protocol"] != nil || params["expected_release_commit"] != nil) {
					if rpcErr := scenario.verificationReply(params, commit); rpcErr != nil {
						delete(response, "result")
						response["error"] = rpcErr
					}
				}
				if scenario.verificationResponse != nil && (params["required_protocol"] != nil || params["expected_release_commit"] != nil) &&
					!scenario.verificationResponse(params, response) {
					trace.mu.Unlock()
					return
				}
			case "tools/call":
				params := req["params"].(map[string]interface{})
				if params["name"] != "doops_agent_upgrade" {
					t.Errorf("unexpected tool %v", params["name"])
				}
				args := params["arguments"].(map[string]interface{})
				trace.args = append(trace.args, args)
				phase := "apply"
				if args["preflight"] == true {
					phase = "preflight"
				} else if args["rollback"] == true {
					phase = "rollback"
				} else if job, ok := args["observe_job"].(string); ok {
					phase = "observe-" + strings.TrimSuffix(job, "-job")
				}
				trace.calls[phase]++
				jobPhase := strings.TrimPrefix(phase, "observe-")
				status := "accepted"
				if strings.HasPrefix(phase, "observe-") {
					status = "succeeded"
					if jobPhase == "apply" && trace.calls[phase] <= scenario.runningObservations {
						status = "running"
					}
				}
				if scenario.failedPhase == phase {
					status = "failed"
				}
				commit := upgradeTestCommit
				if jobPhase == "rollback" {
					commit = rollbackTestCommit
				}
				text := fmt.Sprintf("self-upgrade-%s job=%s-job namespace=ops release=agent upgrade_operation_id=%s phase=%s",
					status, jobPhase, args["upgrade_operation_id"], jobPhase)
				if jobPhase != "rollback" || !scenario.rollbackCommitMissing {
					text += " expected_release_commit=" + commit
				}
				if status == "failed" {
					text += " category=upgrade_job_failed\nfull job log: connection offline; self-upgrade-succeeded is only a log"
				}
				_, header, _ := parseUpgradeResponse(text)
				structured := map[string]interface{}{"outcome": status}
				for key, value := range header {
					structured[key] = value
				}
				if scenario.sensitiveResult {
					text += "\npassword=SYNTHETIC_TEXT_CANARY\nAuthorization: Bearer SYNTHETIC_BEARER_CANARY\n" +
						"https://user:SYNTHETIC_URL_CANARY@ci.example/build?token=SYNTHETIC_QUERY_CANARY\n" +
						"-----BEGIN PRIVATE KEY-----\nSYNTHETIC_PEM_CANARY\n-----END PRIVATE KEY-----\n" +
						"safe diagnostic remains intact; synthetic-upgrade-audit-token"
					structured["diagnostics"] = map[string]interface{}{
						"password": "SYNTHETIC_FIELD_CANARY", "items": []interface{}{map[string]interface{}{
							"api_key": "SYNTHETIC_NESTED_CANARY", "pod_uid": "safe-pod-uid",
						}},
					}
				}
				result := map[string]interface{}{
					"content":           []map[string]string{{"type": "text", "text": text}},
					"structuredContent": structured,
					"isError":           status == "failed",
				}
				if scenario.invalidResult {
					response["error"] = map[string]interface{}{"code": -32603, "message": "upgrade_result_invalid"}
				} else if scenario.auditPhases[phase] {
					trace.auditID++
					operationID := args["upgrade_operation_id"]
					if scenario.wrongAuditOperation {
						operationID = "another-operation"
					}
					data := map[string]interface{}{
						"audit_id": trace.auditID, "upgrade_operation_id": operationID, "result": result,
					}
					if scenario.missingAuditResult {
						delete(data, "result")
					}
					response["error"] = map[string]interface{}{
						"code": -32603, "message": "audit_completion_failed", "data": data,
					}
				} else {
					response["result"] = result
				}
			default:
				trace.mu.Unlock()
				continue
			}
			trace.mu.Unlock()
			if err := conn.WriteJSON(response); err != nil {
				return
			}
		}
	}))
	t.Cleanup(gateway.Close)
	auth := AuthStore{}
	auth.SetGateway(gateway.URL, "synthetic-upgrade-audit-token")
	if err := auth.Save(); err != nil {
		t.Fatal(err)
	}
	return Server{Name: "test", Gateway: gateway.URL, Cluster: "test", Instance: "node"}, trace
}

func TestUpgradeAuditGatewayWorkflow(t *testing.T) {
	for _, tc := range []struct {
		name          string
		scenario      upgradeGatewayScenario
		rollback      bool
		wantError     string
		wantApply     int
		wantRollback  int
		wantObserved  int
		wantRecovered bool
	}{
		{name: "verified upgrade", wantApply: 1, wantObserved: 1},
		{name: "failed preflight", scenario: upgradeGatewayScenario{failedPhase: "observe-preflight"}, wantError: "upgrade_job_failed"},
		{name: "failed apply", scenario: upgradeGatewayScenario{failedPhase: "apply"}, wantError: "upgrade_job_failed", wantApply: 1},
		{name: "failed Job content survives isError", scenario: upgradeGatewayScenario{failedPhase: "observe-apply"}, wantError: "upgrade_job_failed", wantApply: 1, wantObserved: 1},
		{name: "audit failure on accepted Job", scenario: upgradeGatewayScenario{auditPhases: map[string]bool{"apply": true}}, wantError: "audit_completion_failed", wantApply: 1, wantObserved: 1},
		{name: "repeated audit failures keep observing", scenario: upgradeGatewayScenario{
			auditPhases: map[string]bool{"apply": true, "observe-apply": true}, runningObservations: 2,
		}, wantError: "audit_completion_failed", wantApply: 1, wantObserved: 3},
		{name: "failed Job inside audit error", scenario: upgradeGatewayScenario{
			auditPhases: map[string]bool{"observe-apply": true}, failedPhase: "observe-apply",
		}, wantError: "upgrade_job_failed", wantApply: 1, wantObserved: 1},
		{name: "audit failure still rolls back failed verification", scenario: upgradeGatewayScenario{
			auditPhases:        map[string]bool{"apply": true, "observe-apply": true, "rollback": true, "observe-rollback": true},
			wrongUpgradeCommit: true,
		}, wantError: "audit_completion_failed", wantApply: 1, wantObserved: 1, wantRollback: 1, wantRecovered: true},
		{name: "preflight audit failure observes without applying", scenario: upgradeGatewayScenario{
			auditPhases: map[string]bool{"preflight": true, "observe-preflight": true},
		}, wantError: "audit_completion_failed"},
		{name: "invalid Gateway result stops explicitly", scenario: upgradeGatewayScenario{invalidResult: true}, wantError: "upgrade_result_invalid"},
		{name: "unrelated operation evidence is rejected", scenario: upgradeGatewayScenario{
			auditPhases: map[string]bool{"apply": true}, wrongAuditOperation: true,
		}, wantError: "matching operation and result evidence", wantApply: 1},
		{name: "missing recovery evidence is rejected", scenario: upgradeGatewayScenario{
			auditPhases: map[string]bool{"apply": true}, missingAuditResult: true,
		}, wantError: "matching operation and result evidence", wantApply: 1},
		{name: "verified rollback", rollback: true, wantRollback: 1},
		{name: "rollback missing commit", rollback: true, scenario: upgradeGatewayScenario{rollbackCommitMissing: true}, wantError: "missing a valid expected_release_commit", wantRollback: 1},
		{name: "rollback wrong commit", rollback: true, scenario: upgradeGatewayScenario{rollbackCommitWrong: true}, wantError: "upgrade_verification_commit_mismatch", wantRollback: 1},
		{name: "automatic rollback missing commit", scenario: upgradeGatewayScenario{
			wrongUpgradeCommit: true, rollbackCommitMissing: true,
		}, wantError: "rollback failed", wantApply: 1, wantObserved: 1, wantRollback: 1},
		{name: "automatic rollback wrong commit", scenario: upgradeGatewayScenario{
			wrongUpgradeCommit: true, rollbackCommitWrong: true,
		}, wantError: "rollback failed", wantApply: 1, wantObserved: 1, wantRollback: 1},
		{name: "rollback audit error preserves failure", rollback: true, scenario: upgradeGatewayScenario{
			auditPhases: map[string]bool{"rollback": true, "observe-rollback": true},
		}, wantError: "audit_completion_failed", wantRollback: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previousCommit, previousPoll := cliBuildCommit, upgradePollInterval
			cliBuildCommit, upgradePollInterval = upgradeTestCommit, time.Millisecond
			t.Cleanup(func() { cliBuildCommit, upgradePollInterval = previousCommit, previousPoll })
			server, trace := newUpgradeAuditGateway(t, tc.scenario)
			opts := UpgradeOptions{
				Image:   "registry.example/doops@sha256:" + strings.Repeat("a", 64),
				Session: "upgrade.audit", Rollback: tc.rollback,
				SourceCommit: upgradeTestCommit, CIURL: "https://ci.example/build/1",
			}
			if tc.rollback {
				opts.Image = ""
			}
			var runErr error
			stdout := captureStdout(t, func() { runErr = UpgradeAgents(server, opts, false) })
			if tc.wantError == "" {
				if runErr != nil || !strings.Contains(stdout, "OK test/node") {
					t.Fatalf("verified operation failed: %v\n%s", runErr, stdout)
				}
			} else if runErr == nil || !strings.Contains(runErr.Error(), tc.wantError) || strings.Contains(stdout, "OK test/node") {
				t.Fatalf("want non-success %q, got %v\n%s", tc.wantError, runErr, stdout)
			}
			if tc.wantRecovered && (!strings.Contains(stdout, "release_commit="+rollbackTestCommit+" status=ready") ||
				!strings.Contains(runErr.Error(), "release was rolled back")) {
				t.Fatalf("rollback did not verify restored commit: %v\n%s", runErr, stdout)
			}
			historyBytes, err := os.ReadFile(historyLogPath())
			if err != nil {
				t.Fatal(err)
			}
			var history HistoryEntry
			if err := json.Unmarshal(historyBytes, &history); err != nil {
				t.Fatalf("expected one complete history record: %v", err)
			}
			wantStatus := "success"
			if tc.wantError != "" {
				wantStatus = "error"
			}
			if history.HistoryResult == nil || history.Status != wantStatus || history.Session != opts.Session || history.Target != "test/node" {
				t.Fatalf("history lost outcome or identity: %s", historyBytes)
			}
			if tc.wantError != "" && !strings.Contains(history.Error, tc.wantError) {
				t.Fatalf("history lost final error: %s", historyBytes)
			}
			if !strings.Contains(stdout, "operation="+history.UpgradeOperationID+" session="+history.Session) {
				t.Fatalf("output did not expose history/audit correlation: %s", stdout)
			}
			if strings.Contains(string(historyBytes), "synthetic-upgrade-audit-token") {
				t.Fatal("history included the Gateway token")
			}
			trace.mu.Lock()
			defer trace.mu.Unlock()
			for phase, want := range map[string]int{
				"apply": tc.wantApply, "rollback": tc.wantRollback, "observe-apply": tc.wantObserved,
			} {
				if trace.calls[phase] != want {
					t.Errorf("%s calls=%d, want %d; mutations must not be replayed", phase, trace.calls[phase], want)
				}
			}
			if tc.wantRollback > 0 && trace.calls["observe-rollback"] != 1 {
				t.Error("accepted rollback was not observed to completion")
			}
			if tc.scenario.auditPhases["preflight"] && trace.calls["observe-preflight"] != 1 {
				t.Error("audit error abandoned accepted preflight")
			}
			if trace.auditID > 0 {
				if !strings.Contains(stdout, "WARNING: audit_completion_failed") || !strings.Contains(history.Error, "audit_id=1 ") {
					t.Fatalf("first audit failure was lost: %v\n%s", runErr, stdout)
				}
				if strings.Count(stdout, "WARNING: audit_completion_failed") != trace.auditID {
					t.Fatal("not every audit failure was reported")
				}
			}
			for _, args := range trace.args {
				if args["upgrade_operation_id"] != history.UpgradeOperationID || args["session_id"] != opts.Session ||
					args["source_commit"] != opts.SourceCommit || args["ci_url"] != opts.CIURL {
					t.Fatalf("uncorrelated upgrade call: %#v", args)
				}
				if args["image"] != nil && args["expected_release_commit"] != upgradeTestCommit {
					t.Fatalf("upgrade omitted expected commit: %#v", args)
				}
				if args["observe_job"] == "rollback-job" && !tc.scenario.rollbackCommitMissing &&
					args["expected_release_commit"] != rollbackTestCommit {
					t.Fatalf("rollback observe used wrong expected commit: %#v", args)
				}
			}
			for _, params := range trace.inits {
				if params["upgrade_operation_id"] != history.UpgradeOperationID || params["session_id"] != opts.Session {
					t.Fatalf("uncorrelated initialize/probe: %#v", params)
				}
			}
			if !tc.scenario.invalidResult && !strings.Contains(history.Output, `"structuredContent"`) {
				t.Fatal("history discarded the full structured MCP result")
			}
			if tc.scenario.failedPhase != "" && !strings.Contains(history.Output, "full job log: connection offline") {
				t.Fatal("history discarded failed MCP content")
			}
		})
	}
}

func TestUpgradeCallAndCapturePreservesAuditResultAndError(t *testing.T) {
	server, _ := newUpgradeAuditGateway(t, upgradeGatewayScenario{auditPhases: map[string]bool{"apply": true}})
	client := NewMCPClient(server, NewSessionStore(), "upgrade-audit", false)
	client.Token = "synthetic-upgrade-audit-token"
	client.UpgradeOperationID = "upgrade-capture"
	defer client.Close()
	text, err := client.CallAndCapture("doops_agent_upgrade", map[string]interface{}{"image": "registry.example/doops:v1"})
	var rpcErr *gatewayRPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32603 || rpcErr.Message != "audit_completion_failed" {
		t.Fatalf("lost typed RPC error: %v", err)
	}
	result, ok := rpcErr.Data["result"].(map[string]interface{})
	if !ok || result["structuredContent"] == nil || !strings.HasPrefix(text, "self-upgrade-accepted ") {
		t.Fatalf("lost original result or captured text: %q %#v", text, rpcErr.Data)
	}
}

func TestUpgradeAuditOperationIsUniquePerTarget(t *testing.T) {
	previousCommit := cliBuildCommit
	cliBuildCommit = upgradeTestCommit
	t.Cleanup(func() { cliBuildCommit = previousCommit })
	server, trace := newUpgradeAuditGateway(t, upgradeGatewayScenario{})
	server.Instance = "*"
	if err := UpgradeAgents(server, UpgradeOptions{Image: "registry.example/doops:v1", Session: "shared-upgrade"}, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(historyLogPath())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected history for both targets: %s", data)
	}
	operations := map[string]bool{}
	targets := map[string]bool{}
	for _, line := range lines {
		var entry HistoryEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		if entry.HistoryResult == nil || entry.UpgradeOperationID == "" || operations[entry.UpgradeOperationID] ||
			entry.Session != "shared-upgrade" || entry.Status != "success" {
			t.Fatalf("target operation is not uniquely correlated: %s", line)
		}
		operations[entry.UpgradeOperationID] = true
		targets[entry.Target] = true
	}
	if !targets["test/node"] || !targets["test/other"] {
		t.Fatalf("history did not identify both targets: %s", data)
	}
	trace.mu.Lock()
	defer trace.mu.Unlock()
	if trace.calls["preflight"] != 2 || trace.calls["apply"] != 2 {
		t.Fatalf("unexpected mutation count: %#v", trace.calls)
	}
	for _, args := range trace.args {
		if !operations[fmt.Sprint(args["upgrade_operation_id"])] {
			t.Fatalf("request operation absent from history: %#v", args)
		}
	}
}

func TestUpgradeAuditHistoryFailureCannotPrintOK(t *testing.T) {
	previousCommit := cliBuildCommit
	cliBuildCommit = upgradeTestCommit
	t.Cleanup(func() { cliBuildCommit = previousCommit })
	server, _ := newUpgradeAuditGateway(t, upgradeGatewayScenario{})
	if err := os.Mkdir(historyLogPath(), 0700); err != nil {
		t.Fatal(err)
	}
	var runErr error
	output := captureStdout(t, func() {
		runErr = UpgradeAgents(server, UpgradeOptions{Image: "registry.example/doops:v1", Session: "history-failure"}, false)
	})
	if runErr == nil || !strings.Contains(runErr.Error(), "record upgrade history") || strings.Contains(output, "OK test/node") {
		t.Fatalf("history failure reported success: %v\n%s", runErr, output)
	}
}

func TestUpgradeSensitiveInputsRejectedBeforeNetworkOrHistory(t *testing.T) {
	const canary = "SYNTHETIC_INPUT_CANARY"
	for _, tc := range []struct {
		field string
		set   func(*UpgradeOptions)
	}{
		{"ci-url", func(o *UpgradeOptions) { o.CIURL = "https://ci.example/build?token=" + canary }},
		{"ci-url", func(o *UpgradeOptions) { o.CIURL = "https://user:" + canary + "@ci.example/build" }},
		{"ci-url", func(o *UpgradeOptions) { o.CIURL = "https://ci.example/build#" + canary }},
		{"ci-url", func(o *UpgradeOptions) { o.CIURL = "http://ci.example/" + canary }},
		{"ci-url", func(o *UpgradeOptions) { o.CIURL = "https://ci.example/%invalid" + canary }},
		{"source-commit", func(o *UpgradeOptions) { o.SourceCommit = "password=" + canary }},
		{"image", func(o *UpgradeOptions) { o.Image = "registry.example/doops:v1?token=" + canary }},
		{"image", func(o *UpgradeOptions) { o.Image = "user:" + canary + "@registry.example/doops:v1" }},
		{"session", func(o *UpgradeOptions) { o.Session = "token=" + canary }},
		{"session", func(o *UpgradeOptions) { o.Session = "invalid.." + canary }},
		{"namespace", func(o *UpgradeOptions) { o.Namespace = "password=" + canary }},
		{"release", func(o *UpgradeOptions) { o.Release = "token=" + canary }},
		{"workload", func(o *UpgradeOptions) { o.Workload = "secret=" + canary }},
		{"container", func(o *UpgradeOptions) { o.Container = "secret=" + canary }},
		{"mode", func(o *UpgradeOptions) { o.Mode = "secret=" + canary }},
	} {
		t.Run(tc.field, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			var requests atomic.Int32
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
			}))
			defer gateway.Close()
			opts := UpgradeOptions{Image: "registry.example/doops:v1", Session: "input-check", DryRun: true}
			tc.set(&opts)
			var runErr error
			stdout := captureStdout(t, func() {
				runErr = UpgradeAgents(Server{Name: "test", Gateway: gateway.URL, Cluster: "test", Instance: "node"}, opts, true)
			})
			if runErr == nil || !strings.Contains(runErr.Error(), tc.field) || requests.Load() != 0 {
				t.Fatalf("input was not rejected locally: %v; requests=%d", runErr, requests.Load())
			}
			if strings.Contains(stdout+runErr.Error(), canary) {
				t.Fatal("rejected input escaped through output/error")
			}
			if _, err := os.Stat(historyLogPath()); !os.IsNotExist(err) {
				t.Fatalf("rejected input created history: %v", err)
			}
		})
	}
}

func TestUpgradeAuditRedactsRemoteEvidenceWithoutLosingReceipts(t *testing.T) {
	for _, phase := range []string{"success", "failure", "audit_failure"} {
		t.Run(phase, func(t *testing.T) {
			previousCommit := cliBuildCommit
			cliBuildCommit = upgradeTestCommit
			t.Cleanup(func() { cliBuildCommit = previousCommit })
			scenario := upgradeGatewayScenario{sensitiveResult: true}
			if phase == "failure" {
				scenario.failedPhase = "observe-apply"
			} else if phase == "audit_failure" {
				scenario.auditPhases = map[string]bool{"apply": true, "observe-apply": true}
			}
			server, _ := newUpgradeAuditGateway(t, scenario)
			var runErr error
			stdout := captureStdout(t, func() {
				runErr = UpgradeAgents(server, UpgradeOptions{Image: "registry.example/doops:v1", Session: "redaction"}, true)
			})
			if (runErr == nil) != (phase == "success") {
				t.Fatalf("redaction changed the outcome: %v", runErr)
			}
			data, err := os.ReadFile(historyLogPath())
			if err != nil {
				t.Fatal(err)
			}
			all := stdout + string(data) + fmt.Sprint(runErr)
			for _, canary := range []string{"SYNTHETIC_TEXT_CANARY", "SYNTHETIC_BEARER_CANARY", "SYNTHETIC_URL_CANARY", "SYNTHETIC_QUERY_CANARY", "SYNTHETIC_PEM_CANARY", "SYNTHETIC_FIELD_CANARY", "SYNTHETIC_NESTED_CANARY", "synthetic-upgrade-audit-token"} {
				if strings.Contains(all, canary) {
					t.Fatalf("payload escaped redaction: %s", canary)
				}
			}
			var history HistoryEntry
			if err := json.Unmarshal(data, &history); err != nil {
				t.Fatal(err)
			}
			if history.HistoryResult == nil || !strings.Contains(history.Output, "safe diagnostic remains intact") ||
				!strings.Contains(history.Output, "safe-pod-uid") || !strings.Contains(history.Output, upgradeTestCommit) {
				t.Fatalf("redaction removed verification evidence: %s", data)
			}
			steps := 0
			for _, line := range strings.Split(history.Output, "\n") {
				if !strings.HasPrefix(line, "{") {
					continue
				}
				var step map[string]interface{}
				if err := json.Unmarshal([]byte(line), &step); err != nil {
					t.Fatalf("redaction corrupted receipt JSON: %v", err)
				}
				result := step["result"].(map[string]interface{})
				structured := result["structuredContent"].(map[string]interface{})
				if structured["upgrade_operation_id"] != history.UpgradeOperationID || structured["job"] == "" || structured["outcome"] == "" {
					t.Fatalf("receipt lost operation/job/outcome: %#v", structured)
				}
				steps++
			}
			if steps < 4 {
				t.Fatalf("missing stage receipts: %d", steps)
			}
		})
	}
}

func TestUpgradeAuditRedactsRejectedStepArguments(t *testing.T) {
	server, _ := newUpgradeAuditGateway(t, upgradeGatewayScenario{invalidResult: true})
	op := upgradeOperation{server: server, token: "synthetic-upgrade-audit-token", id: "upg-rejected", session: "redaction",
		ciURL: "https://ci.example/build?token=SYNTHETIC_ARGUMENT_CANARY"}
	if _, err := op.callUpgradeTool(map[string]interface{}{"image": "registry.example/doops:v1"}); err == nil {
		t.Fatal("expected Gateway rejection")
	}
	if err := RecordHistory("test/node", op.session, "upgrade", HistoryResult{UpgradeOperationID: op.id, Status: "error", Output: op.output.String()}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(historyLogPath())
	if err != nil || strings.Contains(string(data), "SYNTHETIC_ARGUMENT_CANARY") || !strings.Contains(string(data), "upgrade_result_invalid") {
		t.Fatalf("history leaked rejected input or lost the error: %s %v", data, err)
	}
}

func TestUpgradeVerificationInitializeCarriesExpectations(t *testing.T) {
	server, trace := newUpgradeAuditGateway(t, upgradeGatewayScenario{})
	client := NewMCPClient(server, NewSessionStore(), "verify-session", false)
	client.Token, client.UpgradeOperationID = "synthetic-upgrade-audit-token", "upg-verify"
	defer client.Close()
	if err := client.RequireProtocol("structuredSSH"); err != nil {
		t.Fatal(err)
	}
	if err := client.RequireAgentRelease(upgradeTestCommit); err != nil {
		t.Fatal(err)
	}
	if err := client.RequireAgentRelease(rollbackTestCommit); err == nil || !strings.Contains(err.Error(), "initialize failed") {
		t.Fatalf("Gateway verification rejection was ignored: %v", err)
	}
	trace.mu.Lock()
	defer trace.mu.Unlock()
	if len(trace.inits) != 3 || len(trace.calls) != 0 {
		t.Fatalf("expected three audited probes and no tool replay: %#v", trace)
	}
	for i, params := range trace.inits {
		if params["upgrade_operation_id"] != "upg-verify" || params["session_id"] != "verify-session" {
			t.Fatalf("verification lost ownership: %#v", params)
		}
		if i == 0 {
			if params["required_protocol"] != "structuredSSH" || params["expected_release_commit"] != nil {
				t.Fatalf("protocol expectation missing: %#v", params)
			}
		} else {
			expected := upgradeTestCommit
			if i == 2 {
				expected = rollbackTestCommit
			}
			if params["expected_release_commit"] != expected || params["required_protocol"] != nil {
				t.Fatalf("commit expectation missing or stale: %#v", params)
			}
		}
	}
}

func TestUpgradeVerificationErrorsCannotPrintOK(t *testing.T) {
	for _, kind := range []string{"rpc", "tool"} {
		t.Run(kind, func(t *testing.T) {
			server, trace := newUpgradeAuditGateway(t, upgradeGatewayScenario{verificationError: kind})
			var runErr error
			stdout := captureStdout(t, func() {
				runErr = UpgradeAgents(server, UpgradeOptions{Rollback: true, Session: "verify-failure"}, true)
			})
			data, err := os.ReadFile(historyLogPath())
			if err != nil {
				t.Fatal(err)
			}
			if runErr == nil || !strings.Contains(runErr.Error(), "upgrade_verification_failed") || strings.Contains(stdout, "OK test/node") {
				t.Fatalf("failed Gateway verification reported success: %v\n%s", runErr, stdout)
			}
			if strings.Contains(stdout+string(data)+runErr.Error(), "SYNTHETIC_VERIFY_CANARY") {
				t.Fatal("verification error leaked a payload")
			}
			trace.mu.Lock()
			defer trace.mu.Unlock()
			if trace.calls["rollback"] != 1 || trace.calls["observe-rollback"] != 1 {
				t.Fatalf("verification error replayed rollback: %#v", trace.calls)
			}
		})
	}
}
