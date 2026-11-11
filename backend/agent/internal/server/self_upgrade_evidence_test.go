package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testOperationReceipt(operation, namespace, phase string) []byte {
	job := selfUpgradeJobName(operation, phase)
	receipt := map[string]interface{}{
		"schemaVersion": 1, "upgrade_operation_id": operation, "job": job,
		"phase": phase, "namespace": namespace, "release": "doops-agent", "workload": "doops-agent",
		"image": testPinnedImage(), "expected_release_commit": testUpgradeCommit,
		"outcome": "succeeded", "requester_id": "user-1", "session_id": "session-1", "audit_id": "42",
		"verification": map[string]interface{}{
			"upgrade_operation_id": operation, "job": job, "namespace": namespace,
			"release": "doops-agent", "workload": "doops-agent", "image": testPinnedImage(),
			"outcome": "succeeded", "release_commit": testUpgradeCommit,
			"expected_release_commit": testUpgradeCommit, "revision": 6,
			"podUID": "pod-1", "node": "node-a", "runtimeDigest": "sha256:" + testPinnedDigest,
			"verified_at": "2026-11-11T00:00:00Z", "verification_stage": "candidate",
		},
	}
	encoded, _ := json.Marshal(receipt)
	stored, _ := json.Marshal(map[string]interface{}{"data": map[string]string{"evidence.json": string(encoded)}})
	return stored
}

func TestSelfUpgradeResponseUsesStructuredOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, state, logs, outcome, category string
		isError                              bool
	}{
		{"running", `{"active":1}`, "", "running", "", false},
		{"accepted completion", `{"succeeded":1}`, "release_commit=" + testUpgradeCommit, "succeeded", "", false},
		{"failed", `{"failed":1}`, "category=release_commit_mismatch", "failed", "release_commit_mismatch", true},
		{"no logs", `{"failed":1}`, "", "failed", "upgrade_job_failed", true},
		{"deadline", `{"conditions":[{"type":"Failed","status":"True","reason":"DeadlineExceeded"}]}`, "", "failed", "upgrade_job_deadline_exceeded", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proc := upgradeProcess{namespace: "test", run: func(_ context.Context, _ string, _ string, args ...string) ([]byte, error) {
				if strings.Contains(strings.Join(args, " "), "get job ") {
					return []byte(`{"metadata":{"annotations":{"doops.sh/upgrade-operation-id":"op-1","doops.sh/phase":"apply","doops.sh/expected-release-commit":"` + testUpgradeCommit + `"}},"status":` + tc.state + `}`), nil
				}
				if strings.Contains(strings.Join(args, " "), "get configmap ") {
					if tc.outcome == "succeeded" {
						return testOperationReceipt("op-1", "test", "apply"), nil
					}
					return nil, nil
				}
				return []byte(tc.logs), nil
			}}
			args, _ := json.Marshal(agentUpgradeParams{ObserveJob: selfUpgradeJobName("op-1", "apply"), UpgradeOperationID: "op-1"})
			response := buildAgentUpgradeResponseWithProcess(12, args, proc)
			result := response.Result.(map[string]interface{})
			fields := result["structuredContent"].(map[string]interface{})
			if response.Error != nil || result["isError"] != tc.isError || fields["outcome"] != tc.outcome ||
				fields["phase"] != "observe" || fields["job_phase"] != "apply" || fields["upgrade_operation_id"] != "op-1" ||
				fields["expected_release_commit"] != testUpgradeCommit {
				t.Fatalf("incorrect observation response: %#v", result)
			}
			if tc.category != "" && fields["category"] != tc.category {
				t.Fatalf("category = %v, want %s", fields["category"], tc.category)
			}
			encoded, _ := json.Marshal(result)
			var wireResult map[string]interface{}
			_ = json.Unmarshal(encoded, &wireResult)
			header, _, _ := strings.Cut(resultContentText(wireResult), "\n")
			if len(strings.Fields(header)) < 2 {
				t.Fatalf("missing response header: %s", header)
			}
			seen := make(map[string]bool)
			for _, token := range strings.Fields(header)[1:] {
				key, _, ok := strings.Cut(token, "=")
				if !ok || seen[key] {
					t.Fatalf("response header cannot be parsed by CLI: %s", header)
				}
				seen[key] = true
			}
		})
	}
}

func TestSelfUpgradeReceiptRemainsVerifiableAfterJobGC(t *testing.T) {
	for _, corruption := range []string{"", "commit", "operation", "missing"} {
		t.Run(corruption, func(t *testing.T) {
			proc := upgradeProcess{namespace: "test", run: func(_ context.Context, _, _ string, args ...string) ([]byte, error) {
				command := strings.Join(args, " ")
				if strings.Contains(command, "get job ") {
					return []byte("Error from server (NotFound): job was garbage collected"), fmt.Errorf("exit status 1")
				}
				if !strings.Contains(command, "get configmap ") {
					t.Fatalf("unexpected evidence command %s", command)
				}
				if corruption == "missing" {
					return nil, nil
				}
				raw := testOperationReceipt("op-1", "test", "apply")
				if corruption == "operation" {
					raw = []byte(strings.ReplaceAll(string(raw), "op-1", "foreign-operation"))
				}
				if corruption == "commit" {
					var cm struct{ Data map[string]string }
					_ = json.Unmarshal(raw, &cm)
					var receipt map[string]interface{}
					_ = json.Unmarshal([]byte(cm.Data["evidence.json"]), &receipt)
					receipt["verification"].(map[string]interface{})["release_commit"] = testBaselineCommit
					encoded, _ := json.Marshal(receipt)
					cm.Data["evidence.json"] = string(encoded)
					raw, _ = json.Marshal(cm)
				}
				return raw, nil
			}}
			raw, _ := json.Marshal(agentUpgradeParams{ObserveJob: selfUpgradeJobName("op-1", "apply"), UpgradeOperationID: "op-1"})
			result := buildAgentUpgradeResponseWithProcess(1, raw, proc).Result.(map[string]interface{})
			fields := result["structuredContent"].(map[string]interface{})
			if corruption == "" {
				if result["isError"] != false || fields["release_commit"] != testUpgradeCommit || fields["receipt_name"] == "" || fields["outcome"] != "succeeded" {
					t.Fatalf("durable evidence lost after Job GC: %#v", result)
				}
			} else if result["isError"] != true || fields["outcome"] != "unknown" {
				t.Fatalf("invalid receipt claimed a known result: %#v", result)
			}
		})
	}
}

func TestSelfUpgradeResponseRejectsInvalidRequestsAndForeignOperation(t *testing.T) {
	for _, raw := range []string{`{`, `{}`, `{"upgrade_operation_id":"bad id"}`} {
		response := buildAgentUpgradeResponseWithProcess(1, json.RawMessage(raw), upgradeProcess{
			run: func(_ context.Context, _, _ string, _ ...string) ([]byte, error) {
				t.Fatal("invalid request invoked a cluster command")
				return nil, nil
			},
		})
		if response.Result.(map[string]interface{})["isError"] != true {
			t.Fatalf("invalid request was accepted: %#v", response)
		}
	}
	proc := upgradeProcess{namespace: "test", run: func(_ context.Context, _ string, _ string, args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "logs") {
			t.Fatal("foreign operation logs were read")
		}
		return []byte(`{"metadata":{"annotations":{"doops.sh/upgrade-operation-id":"other"}},"status":{"succeeded":1}}`), nil
	}}
	args, _ := json.Marshal(agentUpgradeParams{ObserveJob: selfUpgradeJobName("op-1", "apply"), UpgradeOperationID: "op-1"})
	response := buildAgentUpgradeResponseWithProcess(1, args, proc)
	result := response.Result.(map[string]interface{})
	if result["isError"] != true || result["structuredContent"].(map[string]interface{})["category"] != "upgrade_operation_mismatch" {
		t.Fatalf("foreign operation accepted: %#v", result)
	}
}

func TestSelfUpgradeJobPreservesEachPhaseAndRefusesReplay(t *testing.T) {
	jobs := map[string]map[string]interface{}{}
	proc := upgradeProcess{namespace: "test", hostname: "agent", cluster: "cluster", instance: "instance",
		run: func(_ context.Context, stdin, command string, args ...string) ([]byte, error) {
			joined := strings.Join(args, " ")
			switch {
			case command == "helm" && strings.HasPrefix(joined, "list"):
				return []byte(`[{"name":"agent","chart":"doops-agent-1"}]`), nil
			case command == "helm" && strings.HasPrefix(joined, "get values"):
				return []byte(`{"gateway":{"cluster":"cluster","instance":"instance"},"workload":{"name":"agent"}}`), nil
			case strings.Contains(joined, "get pod "):
				return []byte(selfUpgradePodFixture), nil
			case strings.Contains(joined, "get jobs"):
				return []byte(`{"items":[]}`), nil
			case strings.Contains(joined, "create -f -"):
				var job map[string]interface{}
				if err := json.Unmarshal([]byte(stdin), &job); err != nil {
					t.Fatal(err)
				}
				name := job["metadata"].(map[string]interface{})["name"].(string)
				if jobs[name] != nil {
					return []byte("Error from server (AlreadyExists)"), fmt.Errorf("exit status 1")
				}
				jobs[name] = job
				return []byte("created"), nil
			default:
				t.Fatalf("unexpected command, including destructive Job replacement: %s %s", command, joined)
				return nil, nil
			}
		}}
	args := agentUpgradeParams{Mode: "k8s", Image: testPinnedImage(), UpgradeOperationID: "op-1",
		ExpectedReleaseCommit: testUpgradeCommit, RequesterID: "user-1", AuditID: 123, SessionID: "session-1",
		SourceCommit: testUpgradeCommit, CIURL: "https://ci.example/build/1", Preflight: true}
	for _, phase := range []string{"preflight", "apply"} {
		args.Preflight = phase == "preflight"
		raw, _ := json.Marshal(args)
		result := buildAgentUpgradeResponseWithProcess(1, raw, proc).Result.(map[string]interface{})
		fields := result["structuredContent"].(map[string]interface{})
		if result["isError"] != false || fields["outcome"] != "accepted" || fields["phase"] != phase {
			t.Fatalf("unexpected acceptance: %#v", result)
		}
		name := selfUpgradeJobName("op-1", phase)
		job := jobs[name]
		annotations := job["metadata"].(map[string]interface{})["annotations"].(map[string]interface{})
		for key, want := range map[string]string{
			"upgrade-operation-id": "op-1", "requester-id": "user-1", "audit-id": "123", "session-id": "session-1",
			"expected-release-commit": testUpgradeCommit, "source-commit": testUpgradeCommit, "ci-url": args.CIURL,
			"phase": phase, "image": testPinnedImage(), "digest": "sha256:" + testPinnedDigest,
		} {
			if annotations["doops.sh/"+key] != want {
				t.Fatalf("annotation %s = %v, want %s", key, annotations["doops.sh/"+key], want)
			}
		}
		if annotations["doops.sh/created-at"] == "" || job["spec"].(map[string]interface{})["ttlSecondsAfterFinished"] != float64(604800) {
			t.Fatalf("timestamp or retention missing: %#v", job)
		}
		replay := buildAgentUpgradeResponseWithProcess(2, raw, proc).Result.(map[string]interface{})
		if replay["isError"] != true || replay["structuredContent"].(map[string]interface{})["category"] != "upgrade_job_exists" {
			t.Fatalf("replay must fail explicitly: %#v", replay)
		}
	}
	if len(jobs) != 2 {
		t.Fatalf("preflight evidence was lost or duplicate Job created: %#v", jobs)
	}
}

func TestSelfUpgradePendingJobIsBusy(t *testing.T) {
	proc := upgradeProcess{run: func(_ context.Context, _, _ string, _ ...string) ([]byte, error) {
		return []byte(`{"items":[{"metadata":{"name":"pending-job"},"status":{}}]}`), nil
	}}
	if err := proc.requireNoActiveUpgrade(t.Context(), "test"); err == nil || selfUpgradeFailureCategory(err.Error()) != "upgrade_busy" {
		t.Fatalf("unscheduled controller was not treated as busy: %v", err)
	}
}

func TestSelfUpgradeCreationFailureKeepsRecoveryIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, outcome, category string
	}{
		{"transport", "unknown", "upgrade_job_create_unknown"},
		{"pending", "busy", "upgrade_busy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proc := upgradeProcess{namespace: "test", hostname: "agent", cluster: "cluster", instance: "instance",
				run: func(_ context.Context, _, command string, args ...string) ([]byte, error) {
					joined := strings.Join(args, " ")
					switch {
					case command == "helm" && strings.HasPrefix(joined, "list"):
						return []byte(`[{"name":"agent","chart":"doops-agent-1"}]`), nil
					case command == "helm":
						return []byte(`{"gateway":{"cluster":"cluster","instance":"instance"},"workload":{"name":"agent"}}`), nil
					case strings.Contains(joined, "get pod "):
						return []byte(selfUpgradePodFixture), nil
					case strings.Contains(joined, "get jobs"):
						if tc.name == "pending" {
							return []byte(`{"items":[{"metadata":{"name":"pending-job"},"status":{}}]}`), nil
						}
						return []byte(`{"items":[]}`), nil
					case strings.Contains(joined, "create -f -"):
						return nil, fmt.Errorf("connection reset after request body")
					default:
						t.Fatalf("unexpected command: %s %s", command, joined)
						return nil, nil
					}
				}}
			raw, _ := json.Marshal(agentUpgradeParams{Mode: "k8s", Image: testPinnedImage(), UpgradeOperationID: "op-1",
				ExpectedReleaseCommit: testUpgradeCommit})
			result := buildAgentUpgradeResponseWithProcess(1, raw, proc).Result.(map[string]interface{})
			fields := result["structuredContent"].(map[string]interface{})
			if result["isError"] != true || fields["outcome"] != tc.outcome || fields["category"] != tc.category {
				t.Fatalf("wrong create failure result: %#v", result)
			}
			if tc.outcome == "unknown" && (fields["job"] != selfUpgradeJobName("op-1", "apply") || fields["namespace"] != "test") {
				t.Fatalf("ambiguous create lost its recovery identity: %#v", result)
			}
		})
	}
}

func TestSelfUpgradeRollbackAcceptanceUsesVerifiedCommit(t *testing.T) {
	previousImage := "registry.example/doops@sha256:" + strings.Repeat("b", 64)
	var created map[string]interface{}
	proc := upgradeProcess{namespace: "test", hostname: "agent", cluster: "cluster", instance: "instance",
		run: func(_ context.Context, stdin, command string, args ...string) ([]byte, error) {
			joined := strings.Join(args, " ")
			switch {
			case command == "helm" && strings.HasPrefix(joined, "list"):
				return []byte(`[{"name":"agent","chart":"doops-agent-1"}]`), nil
			case command == "helm" && strings.HasPrefix(joined, "get values"):
				return []byte(`{"gateway":{"cluster":"cluster","instance":"instance"},"workload":{"name":"agent"}}`), nil
			case command == "helm" && strings.HasPrefix(joined, "history"):
				return []byte(`[{"revision":7,"status":"superseded"},{"revision":8,"status":"deployed"}]`), nil
			case command == "helm" && strings.HasPrefix(joined, "get manifest"):
				return []byte(`{"kind":"Deployment","metadata":{"name":"agent"},"spec":{"template":{"spec":{"containers":[{"name":"doops-agent","image":"` + previousImage + `"}]}}}}`), nil
			case strings.Contains(joined, "get configmap"):
				evidence, _ := json.Marshal(map[string]interface{}{
					"schemaVersion": 1, "release": "agent", "workload": "agent",
					"revisions": map[string]interface{}{"7": map[string]string{"image": previousImage, "outcome": "succeeded",
						"release_commit": testBaselineCommit}},
				})
				return json.Marshal(map[string]interface{}{"data": map[string]string{"evidence.json": string(evidence)}})
			case strings.Contains(joined, "get pod "):
				return []byte(strings.ReplaceAll(selfUpgradePodFixture, "registry.example/doops:old", testPinnedImage())), nil
			case strings.Contains(joined, "get jobs"):
				return []byte(`{"items":[]}`), nil
			case strings.Contains(joined, "create -f -"):
				if err := json.Unmarshal([]byte(stdin), &created); err != nil {
					t.Fatal(err)
				}
				return []byte("created"), nil
			default:
				t.Fatalf("unexpected command: %s %s", command, joined)
				return nil, nil
			}
		}}
	raw, _ := json.Marshal(agentUpgradeParams{Rollback: true, UpgradeOperationID: "op-1", ExpectedReleaseCommit: testUpgradeCommit})
	result := buildAgentUpgradeResponseWithProcess(1, raw, proc).Result.(map[string]interface{})
	fields := result["structuredContent"].(map[string]interface{})
	if result["isError"] != false || fields["expected_release_commit"] != testBaselineCommit ||
		fields["phase"] != "rollback" || fields["image"] != previousImage {
		t.Fatalf("rollback admission used caller's current commit: %#v", result)
	}
	spec := created["spec"].(map[string]interface{})["template"].(map[string]interface{})["spec"].(map[string]interface{})
	if spec["containers"].([]interface{})[0].(map[string]interface{})["image"] != testPinnedImage() {
		t.Fatal("rollback must run current verification code, not the historical image's helpers")
	}
}

func TestSelfUpgradeCommitAndGatewayEvidenceAreRequired(t *testing.T) {
	for _, tc := range []struct {
		key, value, category string
		mutated              bool
	}{
		{"ACTUAL_COMMIT", "cccccccccccccccccccccccccccccccccccccccc", "release_commit_mismatch", true},
		{"ACTUAL_COMMIT", "unknown", "release_commit_unknown", true},
		{"BASELINE_COMMIT", "unknown", "release_commit_unknown", false},
		{"GATEWAY_NOT_READY", "1", "release_probe_failed", true},
	} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "calls.log")
			env := selfUpgradeScriptEnv(path)
			env[tc.key] = tc.value
			out, err := runSelfUpgradeScript(t, selfUpgradeApplyScript, selfUpgradeFakeCommands(), env)
			if err == nil || !strings.Contains(out, "category="+tc.category) || strings.Contains(out, "self-upgrade complete") {
				t.Fatalf("invalid runtime evidence accepted: %v\n%s", err, out)
			}
			if tc.mutated && !strings.Contains(out, "automatic rollback complete") {
				t.Fatalf("failure did not restore the verified baseline:\n%s", out)
			}
		})
	}
}

func TestSelfUpgradeReceiptRetainsActualBaselineAndCandidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.log")
	env := selfUpgradeScriptEnv(path)
	out, err := runSelfUpgradeScript(t, selfUpgradeApplyScript, selfUpgradeFakeCommands(), env)
	if err != nil {
		t.Fatalf("upgrade failed: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(env["RECEIPT_FILE"])
	if err != nil {
		t.Fatal(err)
	}
	var cm struct{ Data map[string]string }
	if err := json.Unmarshal(raw, &cm); err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Revisions map[string]map[string]interface{} `json:"revisions"`
	}
	if err := json.Unmarshal([]byte(cm.Data["evidence.json"]), &receipt); err != nil {
		t.Fatal(err)
	}
	for revision, commit := range map[string]string{"5": testBaselineCommit, "6": testUpgradeCommit} {
		entry := receipt.Revisions[revision]
		if entry["release_commit"] != commit || entry["outcome"] != "succeeded" ||
			entry["upgrade_operation_id"] != "test-operation" || entry["requester_id"] != "operator-1" ||
			entry["session_id"] != "session-1" || entry["audit_id"] != "42" ||
			entry["source_commit"] != testUpgradeCommit || entry["time"] == "" {
			t.Fatalf("receipt revision %s is incomplete: %#v", revision, entry)
		}
	}
}
