package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestUpgradeBareVerificationAuditFailureDoesNotRollback(t *testing.T) {
	var mu sync.Mutex
	rollbackCalls, verifyFailures := 0, 0
	upgrader := websocket.Upgrader{}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		for {
			var req map[string]interface{}
			if conn.ReadJSON(&req) != nil {
				return
			}
			mu.Lock()
			response := map[string]interface{}{"jsonrpc": "2.0", "id": req["id"]}
			params, _ := req["params"].(map[string]interface{})
			switch req["method"] {
			case "initialize":
				commit := upgradeTestCommit
				if rollbackCalls > 0 {
					commit = rollbackTestCommit
				}
				if params["required_protocol"] != nil && verifyFailures == 0 {
					verifyFailures++
					response["error"] = map[string]interface{}{"code": -32603, "message": "audit_completion_failed"}
				} else {
					response["result"] = map[string]interface{}{"capabilities": map[string]interface{}{
						"releaseCommit": commit, "protocols": map[string]interface{}{
							"structuredSSH": map[string]interface{}{"schemaVersion": "doops.sh/structured-ssh/v1", "tool": "doops_ssh", "structuredArgv": true},
						},
					}}
				}
			case "tools/call":
				args := params["arguments"].(map[string]interface{})
				outcome, phase, job, commit := "succeeded", "observe", "apply-job", upgradeTestCommit
				if args["rollback"] == true {
					rollbackCalls++
					outcome, phase, job, commit = "accepted", "rollback", "rollback-job", rollbackTestCommit
				} else if args["observe_job"] == "rollback-job" {
					job, commit = "rollback-job", rollbackTestCommit
				}
				text := fmt.Sprintf("self-upgrade-%s job=%s namespace=test release=agent upgrade_operation_id=probe-op phase=%s expected_release_commit=%s", outcome, job, phase, commit)
				response["result"] = map[string]interface{}{
					"isError": false, "content": []map[string]string{{"type": "text", "text": text}},
					"structuredContent": map[string]interface{}{"outcome": outcome, "phase": phase, "job": job, "namespace": "test", "release": "agent", "upgrade_operation_id": "probe-op", "expected_release_commit": commit},
				}
			default:
				mu.Unlock()
				continue
			}
			mu.Unlock()
			if conn.WriteJSON(response) != nil {
				return
			}
		}
	}))
	defer gateway.Close()
	op := &upgradeOperation{
		server: Server{Gateway: gateway.URL, Cluster: "test", Instance: "node"},
		token:  "synthetic-probe-token", session: "probe", id: "probe-op", expectedRelease: upgradeTestCommit,
	}
	err := op.waitForSelfUpgrade("self-upgrade-accepted job=apply-job namespace=test release=agent", true, upgradeTestCommit)
	mu.Lock()
	defer mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "audit_completion_failed") || rollbackCalls != 0 || op.auditErr == nil {
		t.Fatalf("audit-only failure must remain explicit without rollback: err=%v rollbacks=%d rememberedAuditError=%v", err, rollbackCalls, op.auditErr)
	}
}

func upgradeVerificationRPCError(params map[string]interface{}, commit, category string, auditFailure bool) map[string]interface{} {
	expected, _ := params["expected_release_commit"].(string)
	protocol, _ := params["required_protocol"].(string)
	fields := map[string]interface{}{
		"phase": "verify", "upgrade_operation_id": params["upgrade_operation_id"],
		"release_commit": commit, "expected_release_commit": expected, "required_protocol": protocol,
		"structured_ssh": true, "outcome": "success", "category": category,
		"password": "SYNTHETIC_VERIFY_EVIDENCE_CANARY",
	}
	if category != "" {
		fields["outcome"] = "error"
	}
	switch category {
	case "upgrade_verification_protocol_missing":
		fields["structured_ssh"] = false
	case "upgrade_verification_target_offline":
		fields["outcome"], fields["release_commit"], fields["structured_ssh"] = "offline", "", false
	case "upgrade_verification_forbidden":
		fields["outcome"] = "forbidden"
	}
	code, message := -32003, category
	if auditFailure {
		code, message = -32603, "audit_completion_failed"
	}
	return map[string]interface{}{
		"code": code, "message": message,
		"data": map[string]interface{}{
			"audit_id": 41, "upgrade_operation_id": params["upgrade_operation_id"],
			"result": map[string]interface{}{"isError": category != "", "structuredContent": fields},
		},
	}
}

func TestUpgradeVerificationAuditEvidenceWorkflow(t *testing.T) {
	for _, tc := range []struct {
		name          string
		probe         string
		category      string
		deterministic bool
		rollback      bool
		wantRollback  bool
		mutate        func(map[string]interface{})
	}{
		{name: "healthy protocol", probe: "protocol"},
		{name: "healthy release", probe: "release"},
		{name: "healthy both probes", probe: "both"},
		{name: "healthy explicit rollback", probe: "both", rollback: true},
		{name: "audit protocol failure", probe: "protocol", category: "upgrade_verification_protocol_missing", wantRollback: true},
		{name: "audit release failure", probe: "release", category: "upgrade_verification_commit_mismatch", wantRollback: true},
		{name: "audit offline", probe: "protocol", category: "upgrade_verification_target_offline"},
		{name: "audit forbidden", probe: "protocol", category: "upgrade_verification_forbidden"},
		{name: "audit release offline", probe: "release", category: "upgrade_verification_target_offline"},
		{name: "audit release forbidden", probe: "release", category: "upgrade_verification_forbidden"},
		{name: "deterministic offline", probe: "protocol", category: "upgrade_verification_target_offline", deterministic: true},
		{name: "deterministic forbidden", probe: "protocol", category: "upgrade_verification_forbidden", deterministic: true},
		{name: "deterministic protocol failure", probe: "protocol", category: "upgrade_verification_protocol_missing", deterministic: true, wantRollback: true},
		{name: "deterministic release failure", probe: "release", category: "upgrade_verification_commit_mismatch", deterministic: true, wantRollback: true},
		{name: "bare audit error", probe: "protocol", mutate: func(e map[string]interface{}) { delete(e, "data") }},
		{name: "bare release audit error", probe: "release", mutate: func(e map[string]interface{}) { delete(e, "data") }},
		{name: "deterministic missing evidence", probe: "protocol", category: "upgrade_verification_protocol_missing", deterministic: true,
			mutate: func(e map[string]interface{}) { delete(e, "data") }},
		{name: "wrong operation", probe: "protocol", mutate: func(e map[string]interface{}) {
			e["data"].(map[string]interface{})["upgrade_operation_id"] = "other-operation"
		}},
		{name: "missing result", probe: "protocol", mutate: func(e map[string]interface{}) {
			delete(e["data"].(map[string]interface{}), "result")
		}},
		{name: "fractional audit id", probe: "protocol", mutate: func(e map[string]interface{}) {
			e["data"].(map[string]interface{})["audit_id"] = 1.5
		}},
		{name: "missing isError", probe: "protocol", mutate: func(e map[string]interface{}) {
			delete(e["data"].(map[string]interface{})["result"].(map[string]interface{}), "isError")
		}},
		{name: "inconsistent isError", probe: "protocol", mutate: func(e map[string]interface{}) {
			e["data"].(map[string]interface{})["result"].(map[string]interface{})["isError"] = true
		}},
		{name: "uncorrelated target failure", probe: "release", category: "upgrade_verification_commit_mismatch", mutate: func(e map[string]interface{}) {
			e["data"].(map[string]interface{})["upgrade_operation_id"] = "other-operation"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previousCommit, previousPoll := cliBuildCommit, upgradePollInterval
			cliBuildCommit, upgradePollInterval = upgradeTestCommit, 5*time.Second
			t.Cleanup(func() { cliBuildCommit, upgradePollInterval = previousCommit, previousPoll })
			replies := 0
			server, trace := newUpgradeAuditGateway(t, upgradeGatewayScenario{
				verificationReply: func(params map[string]interface{}, commit string) map[string]interface{} {
					if commit == rollbackTestCommit && !tc.rollback ||
						tc.probe == "protocol" && params["required_protocol"] == nil ||
						tc.probe == "release" && params["expected_release_commit"] == nil {
						return nil
					}
					replies++
					if tc.category == "upgrade_verification_commit_mismatch" {
						commit = strings.Repeat("c", 40)
					}
					e := upgradeVerificationRPCError(params, commit, tc.category, !tc.deterministic)
					if tc.mutate != nil {
						tc.mutate(e)
					}
					return e
				},
			})
			opts := UpgradeOptions{Image: "registry.example/doops:v1", Session: "verify-audit", Rollback: tc.rollback}
			if tc.rollback {
				opts.Image = ""
			}
			var runErr error
			started := time.Now()
			stdout := captureStdout(t, func() { runErr = UpgradeAgents(server, opts, true) })
			if elapsed := time.Since(started); elapsed >= 2*time.Second {
				t.Fatalf("completed verification retried or blocked: %s", elapsed)
			}
			if runErr == nil || strings.Contains(stdout, "OK test/node") {
				t.Fatalf("verification error reported success: %v\n%s", runErr, stdout)
			}
			data, err := os.ReadFile(historyLogPath())
			if err != nil {
				t.Fatal(err)
			}
			var history HistoryEntry
			if err := json.Unmarshal(data, &history); err != nil {
				t.Fatal(err)
			}
			if history.HistoryResult == nil || history.Status != "error" || history.UpgradeOperationID == "" || history.Session != opts.Session {
				t.Fatalf("history lost final failure or correlation: %s", data)
			}
			if !tc.deterministic && (!strings.Contains(history.Error, "audit_completion_failed") || !strings.Contains(stdout, "WARNING: audit_completion_failed")) {
				t.Fatalf("audit failure was not retained: %v\n%s", runErr, data)
			}
			if strings.Contains(stdout+string(data)+runErr.Error(), "SYNTHETIC_VERIFY_EVIDENCE_CANARY") {
				t.Fatal("verification evidence leaked payload")
			}
			if tc.mutate != nil {
				if !strings.Contains(history.Error, "missing or malformed matching verification evidence") || strings.Contains(stdout, "release_commit="+upgradeTestCommit+" status=ready") {
					t.Fatalf("malformed evidence accepted: %v\n%s", runErr, stdout)
				}
			} else {
				if !strings.Contains(history.Output, `"structuredContent"`) || !strings.Contains(history.Output, `"phase":"verify"`) {
					t.Fatalf("verification receipt was not persisted: %s", data)
				}
				if tc.category == "" {
					commit := upgradeTestCommit
					if tc.rollback {
						commit = rollbackTestCommit
					}
					if !strings.Contains(stdout, "release_commit="+commit+" status=ready") {
						t.Fatalf("healthy target was not verified: %v\n%s", runErr, stdout)
					}
				} else if tc.wantRollback {
					if !strings.Contains(history.Error, tc.category) || !strings.Contains(history.Error, "release was rolled back") {
						t.Fatalf("confirmed target failure did not restore previous release: %v\n%s", runErr, stdout)
					}
				} else if !strings.Contains(history.Error, "verification inconclusive") || !strings.Contains(history.Error, tc.category) {
					t.Fatalf("unconfirmed verification was not reported as inconclusive: %v\n%s", runErr, stdout)
				}
			}
			trace.mu.Lock()
			defer trace.mu.Unlock()
			wantReplies, wantRollbacks := 1, 0
			if tc.probe == "both" {
				wantReplies = 2
			}
			if tc.wantRollback || tc.rollback {
				wantRollbacks = 1
			}
			if replies != wantReplies || trace.calls["rollback"] != wantRollbacks || trace.calls["observe-rollback"] != wantRollbacks || trace.calls["apply"] > 1 {
				t.Fatalf("unexpected retries or rollback: replies=%d calls=%#v", replies, trace.calls)
			}
		})
	}
}

func TestUpgradeVerificationRejectsMalformedStructuredEvidence(t *testing.T) {
	for _, tc := range []struct {
		field    string
		value    interface{}
		category string
		release  bool
	}{
		{field: "phase", value: "observe"}, {field: "upgrade_operation_id", value: "another-operation"},
		{field: "expected_release_commit", value: rollbackTestCommit}, {field: "required_protocol", value: "executionLifecycle"},
		{field: "release_commit", value: 123}, {field: "structured_ssh", value: "true"}, {field: "category", value: nil},
		{field: "outcome", value: "error"}, {field: "structured_ssh", value: false}, {field: "category", value: "unknown_category"},
		{field: "release_commit", value: rollbackTestCommit, release: true},
		{field: "structured_ssh", value: true, category: "upgrade_verification_protocol_missing"},
		{field: "release_commit", value: upgradeTestCommit, category: "upgrade_verification_commit_mismatch", release: true},
		{field: "outcome", value: "success", category: "upgrade_verification_forbidden"},
		{field: "structured_ssh", value: true, category: "upgrade_verification_target_offline"},
		{field: "category", value: "unknown_category", category: "upgrade_verification_protocol_missing"},
	} {
		t.Run(tc.field+fmt.Sprint(tc.value), func(t *testing.T) {
			server, trace := newUpgradeAuditGateway(t, upgradeGatewayScenario{
				verificationReply: func(params map[string]interface{}, commit string) map[string]interface{} {
					if tc.release && params["expected_release_commit"] == nil {
						return nil
					}
					if tc.category == "upgrade_verification_commit_mismatch" {
						commit = strings.Repeat("c", 40)
					}
					e := upgradeVerificationRPCError(params, commit, tc.category, true)
					fields := e["data"].(map[string]interface{})["result"].(map[string]interface{})["structuredContent"].(map[string]interface{})
					fields[tc.field] = tc.value
					return e
				},
			})
			op := &upgradeOperation{server: server, id: "probe-op", session: "probe", token: "synthetic-upgrade-audit-token"}
			err := op.waitForSelfUpgrade("self-upgrade-accepted job=apply-job namespace=ops release=agent", true, upgradeTestCommit)
			trace.mu.Lock()
			defer trace.mu.Unlock()
			if err == nil || !strings.Contains(err.Error(), "verification inconclusive") || op.auditErr == nil || trace.calls["rollback"] != 0 ||
				strings.Contains(op.output.String(), "release_commit="+upgradeTestCommit+" status=ready") {
				t.Fatalf("malformed verification evidence authorized an outcome: %v audit=%v", err, op.auditErr)
			}
		})
	}
}

func TestUpgradeInitializePreservesTypedRedactedEvidence(t *testing.T) {
	server, _ := newUpgradeAuditGateway(t, upgradeGatewayScenario{
		verificationReply: func(params map[string]interface{}, commit string) map[string]interface{} {
			return upgradeVerificationRPCError(params, commit, "", true)
		},
	})
	client := NewMCPClient(server, NewSessionStore(), "verify-session", false)
	client.Token, client.UpgradeOperationID = "synthetic-upgrade-audit-token", "probe-op"
	defer client.Close()
	for _, connected := range []bool{false, true} {
		if connected {
			if err := client.connect(); err != nil {
				t.Fatal(err)
			}
		}
		err := client.RequireProtocol("structuredSSH")
		var rpcErr *gatewayRPCError
		if !errors.As(err, &rpcErr) || rpcErr.Code != -32603 || rpcErr.Message != "audit_completion_failed" || rpcErr.Data["upgrade_operation_id"] != "probe-op" {
			t.Fatalf("initialize lost typed evidence (connected=%v): %v", connected, err)
		}
		data, marshalErr := json.Marshal(rpcErr.Data)
		if marshalErr != nil || strings.Contains(string(data), "SYNTHETIC_VERIFY_EVIDENCE_CANARY") || !strings.Contains(string(data), `"phase":"verify"`) {
			t.Fatalf("initialize evidence was not safely preserved: %s %v", data, marshalErr)
		}
	}
}

func TestUpgradeVerificationRollbackRequiresConfirmedRejection(t *testing.T) {
	for _, probe := range []string{"protocol", "release"} {
		for _, kind := range []string{
			"offline", "denied", "unauthorized", "network", "missing result", "missing capabilities",
			"malformed capability", "unstructured rejection", "local rejection", "missing capability",
		} {
			t.Run(probe+"/"+kind, func(t *testing.T) {
				previousCommit := cliBuildCommit
				cliBuildCommit = upgradeTestCommit
				t.Cleanup(func() { cliBuildCommit = previousCommit })
				failures := 0
				server, trace := newUpgradeAuditGateway(t, upgradeGatewayScenario{
					verificationResponse: func(params, response map[string]interface{}) bool {
						result := response["result"].(map[string]interface{})
						caps := result["capabilities"].(map[string]interface{})
						if caps["releaseCommit"] == rollbackTestCommit ||
							probe == "protocol" && params["required_protocol"] == nil ||
							probe == "release" && params["expected_release_commit"] == nil {
							return true
						}
						failures++
						switch kind {
						case "network":
							return false // Drop the real WebSocket before returning an initialize response.
						case "offline", "denied", "unauthorized", "unstructured rejection":
							code, message := -32003, "upgrade_verification_forbidden"
							switch kind {
							case "offline":
								message = "upgrade_verification_target_offline"
							case "unauthorized":
								code, message = -32001, "upgrade_verification_unauthorized"
							case "unstructured rejection":
								message = "target Agent release commit mismatch; does not advertise protocol structuredSSH"
							}
							delete(response, "result")
							response["error"] = map[string]interface{}{"code": code, "message": message}
						case "missing result":
							delete(response, "result")
						case "missing capabilities":
							delete(result, "capabilities")
						case "malformed capability":
							key := "protocols"
							if probe == "release" {
								key = "releaseCommit"
							}
							caps[key] = []string{"malformed"}
						case "local rejection", "missing capability":
							if probe == "protocol" {
								caps["protocols"] = map[string]interface{}{}
							} else if kind == "missing capability" {
								delete(caps, "releaseCommit")
							} else {
								caps["releaseCommit"] = strings.Repeat("c", 40)
							}
						}
						return true
					},
				})
				var runErr error
				started := time.Now()
				stdout := captureStdout(t, func() {
					runErr = UpgradeAgents(server, UpgradeOptions{Image: "registry.example/doops:v1", Session: "verify-rejection"}, false)
				})
				limit := 2 * time.Second
				if kind == "network" {
					limit = 7 * time.Second // One existing five-second initialize deadline, with scheduling margin.
				}
				if elapsed := time.Since(started); elapsed >= limit {
					t.Fatalf("verification retried or exceeded its bound: %s", elapsed)
				}
				if runErr == nil || strings.Contains(stdout, "OK test/node") {
					t.Fatalf("failed verification reported success: %v\n%s", runErr, stdout)
				}
				data, err := os.ReadFile(historyLogPath())
				if err != nil {
					t.Fatal(err)
				}
				var history HistoryEntry
				if err := json.Unmarshal(data, &history); err != nil {
					t.Fatal(err)
				}
				wantRollbacks, wantError := 0, "verification inconclusive"
				if kind == "local rejection" || kind == "missing capability" {
					wantRollbacks, wantError = 1, "release was rolled back"
					if !strings.Contains(stdout, "release_commit="+rollbackTestCommit+" status=ready") {
						t.Fatalf("restored release was not verified: %v\n%s", runErr, stdout)
					}
				}
				if history.HistoryResult == nil || history.Status != "error" || !strings.Contains(history.Error, wantError) {
					t.Fatalf("history lost verification outcome: %s", data)
				}
				trace.mu.Lock()
				defer trace.mu.Unlock()
				if failures != 1 || trace.calls["rollback"] != wantRollbacks || trace.calls["observe-rollback"] != wantRollbacks ||
					trace.calls["apply"] != 1 || trace.calls["observe-apply"] != 1 {
					t.Fatalf("unexpected verification retries or mutations: failures=%d calls=%#v", failures, trace.calls)
				}
			})
		}
	}
}
