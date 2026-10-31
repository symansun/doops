package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"
)

func TestFilterUpgradeTargets(t *testing.T) {
	targets := []GatewayTarget{
		{Cluster: "doops-jm", Instance: "jm-228"},
		{Cluster: "doops-89", Instance: "master-node"},
		{Cluster: "doops-89", Instance: "worker"},
	}
	got := filterUpgradeTargets(targets, "doops-89", "*")
	if len(got) != 2 {
		t.Fatalf("expected two doops-89 targets, got %#v", got)
	}
	got = filterUpgradeTargets(targets, "*", "jm-228")
	if len(got) != 1 || got[0].Cluster != "doops-jm" {
		t.Fatalf("expected jm target, got %#v", got)
	}
	got = filterUpgradeTargets(targets, "missing", "*")
	if len(got) != 0 {
		t.Fatalf("expected no targets, got %#v", got)
	}
}

func TestUpgradeAgentsRejectsInvalidImage(t *testing.T) {
	err := UpgradeAgents(Server{}, UpgradeOptions{
		Image: "not-an-image",
	}, false)
	if err == nil || !strings.Contains(err.Error(), "repository:tag") {
		t.Fatalf("expected invalid image rejection, got %v", err)
	}
}

func TestUpgradeAgentsRejectsInvalidDryRunImage(t *testing.T) {
	err := UpgradeAgents(Server{}, UpgradeOptions{
		Image:  "not-an-image",
		DryRun: true,
	}, false)
	if err == nil || !strings.Contains(err.Error(), "repository:tag") {
		t.Fatalf("expected invalid dry-run image rejection, got %v", err)
	}
}

func TestUpgradeAgentsAllowsTaggedImageToReachTargetLookup(t *testing.T) {
	err := UpgradeAgents(Server{}, UpgradeOptions{
		Image: "registry.example/doops:v1.3.40",
	}, false)
	if err == nil || !strings.Contains(err.Error(), "configured Gateway target is required") {
		t.Fatalf("expected configured target requirement, got %v", err)
	}
}

func TestUpgradeAgentsAllowsPinnedImageToReachTargetLookup(t *testing.T) {
	err := UpgradeAgents(Server{}, UpgradeOptions{
		Image: "registry.example/doops@sha256:" + strings.Repeat("a", 64),
	}, false)
	if err == nil || !strings.Contains(err.Error(), "configured Gateway target is required") {
		t.Fatalf("expected configured target requirement, got %v", err)
	}
}

func TestParseUpgradeAcceptance(t *testing.T) {
	job, namespace, release, err := parseUpgradeAcceptance(
		"self-upgrade-accepted job=doops-self-upgrade-aaaaaaaa namespace=kz-ops release=doops-agent-live workload=doops-agent image=registry.example/doops@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n",
	)
	if err != nil {
		t.Fatal(err)
	}
	if job != "doops-self-upgrade-aaaaaaaa" || namespace != "kz-ops" || release != "doops-agent-live" {
		t.Fatalf("parsed job=%s namespace=%s release=%s", job, namespace, release)
	}
}

func TestParseUpgradeAcceptanceRejectsLogAndDuplicateFields(t *testing.T) {
	for _, out := range []string{
		"OK\nself-upgrade-accepted job=job namespace=ops release=agent",
		"self-upgrade-accepted job=first job=second namespace=ops release=agent",
		"self-upgrade-accepted job=job namespace=ops",
	} {
		if _, _, _, err := parseUpgradeAcceptance(out); err == nil {
			t.Fatalf("invalid acceptance was accepted: %s", out)
		}
	}
}

func TestClassifyUpgradeObservation(t *testing.T) {
	if classifyUpgradeObservation("self-upgrade-running job=doops-self-upgrade-aaaaaaaa namespace=kz-ops", nil) != "running" {
		t.Fatal("running observation was not classified")
	}
	if classifyUpgradeObservation("", fmt.Errorf("target offline: doops-edu/edu-coder")) != "retry" {
		t.Fatal("offline observation was not retried")
	}
	if classifyUpgradeObservation("", fmt.Errorf("agent upgrade image must be repository:tag or repository@sha256:<64 hex digits>")) != "error" {
		t.Fatal("validation error was retried")
	}
	if classifyUpgradeObservation("self-upgrade-failed job=doops-self-upgrade-aaaaaaaa namespace=kz-ops\nlogs: self-upgrade-succeeded", nil) != "failed" {
		t.Fatal("job logs overrode the failed status")
	}
	if classifyUpgradeObservation("logs mention self-upgrade-succeeded", nil) != "error" {
		t.Fatal("unstructured text was accepted as a successful observation")
	}
}

func TestUpgradeAgentsRequiresVerifiedResult(t *testing.T) {
	const accepted = "self-upgrade-accepted job=doops-self-upgrade-aaaaaaaa namespace=ops release=agent"
	previousCommit := cliBuildCommit
	cliBuildCommit = strings.Repeat("a", 40)
	defer func() { cliBuildCommit = previousCommit }()
	for _, test := range []struct {
		name         string
		acceptance   string
		observation  string
		dryRun       bool
		rollback     bool
		noProtocol   bool
		wantRollback int32
		wantSuccess  bool
		wantObserved int32
		wantApplies  int32
	}{
		{name: "missing acceptance", acceptance: "upgrade completed"},
		{name: "preview", acceptance: "dry-run: image=example", dryRun: true, wantSuccess: true},
		{name: "invalid preview", acceptance: "upgrade completed", dryRun: true},
		{name: "wrong job", acceptance: accepted, observation: "self-upgrade-succeeded job=other namespace=ops", wantObserved: 1, wantApplies: 0},
		{name: "wrong namespace", acceptance: accepted, observation: "self-upgrade-succeeded job=doops-self-upgrade-aaaaaaaa namespace=other", wantObserved: 1},
		{name: "failed with success in logs", acceptance: accepted, observation: "self-upgrade-failed job=doops-self-upgrade-aaaaaaaa namespace=ops\nlog: self-upgrade-succeeded", wantObserved: 1},
		{name: "verified", acceptance: accepted, observation: "self-upgrade-succeeded job=doops-self-upgrade-aaaaaaaa namespace=ops", wantObserved: 2, wantApplies: 1, wantSuccess: true},
		{name: "rollback verified", acceptance: accepted, observation: "self-upgrade-succeeded job=doops-self-upgrade-aaaaaaaa namespace=ops", rollback: true, wantRollback: 1, wantObserved: 1, wantSuccess: true},
		{name: "rollback protocol failure", acceptance: accepted, observation: "self-upgrade-succeeded job=doops-self-upgrade-aaaaaaaa namespace=ops", rollback: true, noProtocol: true, wantRollback: 1, wantObserved: 1},
		{name: "recovery protocol failure", acceptance: accepted, observation: "self-upgrade-succeeded job=doops-self-upgrade-aaaaaaaa namespace=ops", noProtocol: true, wantRollback: 1, wantObserved: 3, wantApplies: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("DOOPS_CONFIG", filepath.Join(t.TempDir(), "config.json"))
			var observed atomic.Int32
			var rollbacks atomic.Int32
			var applies atomic.Int32
			upgrader := websocket.Upgrader{}
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/targets" {
					_, _ = w.Write([]byte(`{"targets":[{"cluster":"test","instance":"node"}]}`))
					return
				}
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Errorf("upgrade: %v", err)
					return
				}
				defer conn.Close()
				for {
					var req map[string]interface{}
					if err := conn.ReadJSON(&req); err != nil {
						return
					}
					result := map[string]interface{}{
						"protocolVersion": "2024-11-05",
						"capabilities": map[string]interface{}{
							"releaseCommit": strings.Repeat("a", 40),
							"protocols": map[string]interface{}{
								"structuredSSH": map[string]interface{}{
									"schemaVersion": "doops.sh/structured-ssh/v1",
									"tool":          "doops_ssh", "structuredArgv": true,
								},
							},
						},
					}
					switch req["method"] {
					case "initialize":
						if test.noProtocol && observed.Load() > 0 {
							result["capabilities"] = map[string]interface{}{}
						}
					case "tools/call":
						params := req["params"].(map[string]interface{})
						args := params["arguments"].(map[string]interface{})
						text := test.acceptance
						if args["rollback"] == true {
							rollbacks.Add(1)
						}
						if args["image"] != nil && args["preflight"] != true && args["dry_run"] != true && args["rollback"] != true {
							applies.Add(1)
						}
						if args["observe_job"] != nil {
							observed.Add(1)
							text = test.observation
						}
						result = map[string]interface{}{
							"content": []map[string]string{{"type": "text", "text": text}},
						}
					default:
						continue
					}
					_ = conn.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "id": req["id"], "result": result})
				}
			}))
			defer gateway.Close()
			auth := AuthStore{Tokens: map[string]string{}}
			auth.SetGateway(gateway.URL, "synthetic-upgrade-token")
			if err := auth.Save(); err != nil {
				t.Fatal(err)
			}
			image := "registry.example/doops@sha256:" + strings.Repeat("a", 64)
			if test.rollback {
				image = ""
			}
			err := UpgradeAgents(Server{
				Name: "test", Gateway: gateway.URL, Cluster: "test", Instance: "node",
			}, UpgradeOptions{
				Image: image, Session: "upgrade-contract", DryRun: test.dryRun, Rollback: test.rollback,
			}, false)
			if (err == nil) != test.wantSuccess {
				t.Fatalf("upgrade result = %v, want success=%t", err, test.wantSuccess)
			}
			if observed.Load() != test.wantObserved {
				t.Fatalf("Job observations = %d, want %d", observed.Load(), test.wantObserved)
			}
			if !test.dryRun && !test.rollback && test.acceptance == accepted && applies.Load() != test.wantApplies {
				t.Fatalf("real upgrade requests = %d, want %d (a failed preflight must not start one)", applies.Load(), test.wantApplies)
			}
			if rollbacks.Load() != test.wantRollback {
				t.Fatalf("rollback requests = %d, want %d", rollbacks.Load(), test.wantRollback)
			}
		})
	}
}

// A missing session is generated, not an error; the upgrade still stops on the
// next precondition (an unpublished test CLI) before contacting Gateway.
func TestUpgradeAgentsGeneratesMissingSession(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("an unpublished CLI must fail before contacting Gateway")
	}))
	defer gateway.Close()
	err := UpgradeAgents(Server{
		Name: "test", Gateway: gateway.URL, Cluster: "test", Instance: "node",
	}, UpgradeOptions{Image: "registry.example/doops@sha256:" + strings.Repeat("a", 64)}, false)
	if err == nil || strings.Contains(err.Error(), "-session") {
		t.Fatalf("missing session must be generated, not rejected; got %v", err)
	}
}

func TestUpgradeAgentsUsesConfiguredGatewayLogin(t *testing.T) {
	var gotAuth string
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"targets":[]}`))
	}))
	defer gateway.Close()
	home := t.TempDir()
	t.Setenv("HOME", home)
	auth := AuthStore{Tokens: map[string]string{}}
	auth.SetGateway(gateway.URL, "cached-token")
	data, err := json.Marshal(&auth)
	if err != nil {
		t.Fatalf("marshal auth: %v", err)
	}
	authPath := filepath.Join(home, ".agent", "skills", "doops", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o755); err != nil {
		t.Fatalf("create auth dir: %v", err)
	}
	if err := os.WriteFile(authPath, data, 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	err = UpgradeAgents(Server{
		Name:     "configured",
		Gateway:  gateway.URL,
		Cluster:  "prod",
		Instance: "node",
	}, UpgradeOptions{
		Image:   "registry.example/doops@sha256:" + strings.Repeat("a", 64),
		DryRun:  true,
		Session: "upgrade-auth",
	}, false)
	if err == nil || !strings.Contains(err.Error(), "configured target is offline") {
		t.Fatalf("expected empty target result, got %v", err)
	}
	if gotAuth != "Bearer cached-token" {
		t.Fatalf("cached Gateway login was not used: %q", gotAuth)
	}
}

func TestUpgradeAgentsRequiresConfiguredTarget(t *testing.T) {
	err := UpgradeAgents(Server{}, UpgradeOptions{
		Image:  "registry.example/doops@sha256:" + strings.Repeat("a", 64),
		DryRun: true,
	}, false)
	if err == nil || !strings.Contains(err.Error(), "configured Gateway target is required") {
		t.Fatalf("expected configured target requirement, got %v", err)
	}
}

// A preflight that refuses an upgrade must leave the release alone, and it must
// name the stable category so an operator reads why instead of guessing.
func TestUpgradeAgentsPreflightFailureChangesNothingAndReportsTheCategory(t *testing.T) {
	for _, tc := range []struct {
		name         string
		observation  string
		wantCategory string
	}{
		{
			name:         "the rollback baseline image is gone",
			observation:  "self-upgrade-failed job=doops-self-upgrade-aaaaaaaa namespace=ops category=rollback_baseline_image_unavailable\nrollback_baseline_image_unavailable: image=registry.example/doops@sha256:bbbb reason=the image could not be pulled",
			wantCategory: "rollback_baseline_image_unavailable",
		},
		{
			name:         "the rollback baseline image is unresolved",
			observation:  "self-upgrade-failed job=doops-self-upgrade-aaaaaaaa namespace=ops category=rollback_baseline_image_unresolved\nrollback_baseline_image_unresolved: the revision must contain exactly one doops-agent image",
			wantCategory: "rollback_baseline_image_unresolved",
		},
		{
			name:        "the preflight failed for another reason",
			observation: "self-upgrade-failed job=doops-self-upgrade-aaaaaaaa namespace=ops\nhelm dry-run failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("DOOPS_CONFIG", filepath.Join(t.TempDir(), "config.json"))
			previousCommit := cliBuildCommit
			cliBuildCommit = strings.Repeat("a", 40)
			defer func() { cliBuildCommit = previousCommit }()
			var observed atomic.Int32
			var applies atomic.Int32
			upgrader := websocket.Upgrader{}
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/targets" {
					_, _ = w.Write([]byte(`{"targets":[{"cluster":"test","instance":"node"}]}`))
					return
				}
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Errorf("upgrade: %v", err)
					return
				}
				defer conn.Close()
				for {
					var req map[string]interface{}
					if err := conn.ReadJSON(&req); err != nil {
						return
					}
					result := map[string]interface{}{
						"protocolVersion": "2024-11-05",
						"capabilities":    map[string]interface{}{"releaseCommit": strings.Repeat("a", 40)},
					}
					switch req["method"] {
					case "initialize":
					case "tools/call":
						params := req["params"].(map[string]interface{})
						args := params["arguments"].(map[string]interface{})
						text := "self-upgrade-accepted job=doops-self-upgrade-aaaaaaaa namespace=ops release=agent"
						if args["image"] != nil && args["preflight"] != true && args["dry_run"] != true {
							applies.Add(1)
						}
						if args["observe_job"] != nil {
							observed.Add(1)
							text = tc.observation
						}
						result = map[string]interface{}{
							"content": []map[string]string{{"type": "text", "text": text}},
						}
					default:
						continue
					}
					_ = conn.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "id": req["id"], "result": result})
				}
			}))
			defer gateway.Close()
			auth := AuthStore{Tokens: map[string]string{}}
			auth.SetGateway(gateway.URL, "synthetic-upgrade-token")
			if err := auth.Save(); err != nil {
				t.Fatal(err)
			}
			err := UpgradeAgents(Server{
				Name: "test", Gateway: gateway.URL, Cluster: "test", Instance: "node",
			}, UpgradeOptions{
				Image:   "registry.example/doops@sha256:" + strings.Repeat("a", 64),
				Session: "upgrade-preflight-failure",
			}, false)
			if err == nil {
				t.Fatal("a failed preflight must fail the upgrade")
			}
			if applies.Load() != 0 {
				t.Fatalf("real upgrade requests = %d, want 0 (a failed preflight must not start one)", applies.Load())
			}
			if observed.Load() == 0 {
				t.Fatalf("the preflight Job was never observed: %v", err)
			}
			if tc.wantCategory != "" && !strings.Contains(err.Error(), tc.wantCategory) {
				t.Fatalf("the failure did not name %s: %v", tc.wantCategory, err)
			}
		})
	}
}

// The override is a parameter, so it reaches the Agent as one and defaults to
// off.
func TestUpgradeArgumentsCarryTheRollbackBaselineOverride(t *testing.T) {
	args := upgradeArguments(UpgradeOptions{Image: "registry.example/doops:v1"})
	if args["allow_missing_rollback_baseline"] != false {
		t.Fatalf("the override must be off by default: %#v", args["allow_missing_rollback_baseline"])
	}
	args = upgradeArguments(UpgradeOptions{
		Image:                        "registry.example/doops:v1",
		AllowMissingRollbackBaseline: true,
	})
	if args["allow_missing_rollback_baseline"] != true {
		t.Fatalf("the override did not reach the Agent: %#v", args["allow_missing_rollback_baseline"])
	}
}
