package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The third upgrade guarantee is "restore the backup when verification fails".
// It can only hold when the image a rollback would restore is still in its
// registry, so the pre-change checks read that image's manifest before the
// release changes.
//
// Given/When/Then matrix:
//
//	Given the rollback baseline manifest is in the registry, When the
//	pre-change checks run, Then the checks pass and the release may change.
//	Given the rollback baseline image is gone from the registry, When the
//	pre-change checks run, Then they fail with
//	rollback_baseline_image_unavailable and nothing changes.
//	Given the registry refuses the pull credentials, is unreachable, or the
//	Agent's pull secret cannot be read, When the pre-change checks run, Then
//	they fail with rollback_baseline_probe_failed, never as a missing image.
//	Given the operator allowed a missing rollback baseline on purpose, When the
//	pre-change checks run, Then they pass with a visible warning.
//	Given the target image is not pullable, When the pre-change checks run,
//	Then they fail and nothing changes.
//	Given the rollback revision does not contain exactly one doops-agent image,
//	When the pre-change checks run, Then they fail with
//	rollback_baseline_image_unresolved and nothing changes.
//	Given the release does not have exactly one deployed revision, When the
//	pre-change checks run, Then they fail with
//	rollback_baseline_image_unresolved and nothing changes.
//	Given the release is not unique in the namespace, When the release is
//	resolved, Then the upgrade is refused before any Job is created.

func TestSelfUpgradePreflightRefusesAnUnpullableRollbackBaseline(t *testing.T) {
	for _, tc := range []struct {
		name         string
		pullFails    bool
		allowMissing bool
		wantCategory string
		wantRefused  bool
	}{
		{
			name:        "the baseline image is pullable",
			pullFails:   false,
			wantRefused: false,
		},
		{
			name:         "the baseline image is gone from the registry",
			pullFails:    true,
			wantCategory: selfUpgradeCategoryRollbackBaselineUnavailable,
			wantRefused:  true,
		},
		{
			name:         "the operator allowed a missing rollback baseline",
			pullFails:    true,
			allowMissing: true,
			wantRefused:  false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			callLog := filepath.Join(t.TempDir(), "calls.log")
			env := selfUpgradeScriptEnv(callLog)
			if tc.pullFails {
				env["SKOPEO_RESULT"] = "missing"
			}
			if tc.allowMissing {
				env["DOOPS_UPGRADE_ALLOW_MISSING_ROLLBACK_BASELINE"] = "1"
			}
			out, err := runSelfUpgradeScript(t, selfUpgradePreflightScript, selfUpgradeFakeCommands(), env)

			logBytes, readErr := os.ReadFile(callLog)
			if readErr != nil {
				t.Fatal(readErr)
			}
			log := string(logBytes)
			mutated := false
			for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
				if strings.Contains(line, "helm upgrade") && !strings.Contains(line, "--dry-run=server") {
					mutated = true
				}
			}

			if tc.wantRefused {
				if err == nil {
					t.Fatalf("preflight accepted an unusable rollback baseline:\n%s", out)
				}
				if !strings.Contains(out, tc.wantCategory) {
					t.Fatalf("preflight did not report %s:\n%s", tc.wantCategory, out)
				}
				if mutated {
					t.Fatalf("the release changed despite a refused rollback baseline:\n%s", log)
				}
				return
			}

			if err != nil {
				t.Fatalf("preflight refused a usable rollback baseline: %v\n%s", err, out)
			}
			if !strings.Contains(out, "preflight-passed image="+testPinnedImage()) {
				t.Fatalf("preflight did not pass: %s", out)
			}
			if tc.allowMissing && !strings.Contains(out, "warning:") {
				t.Fatalf("the operator override was not visible:\n%s", out)
			}
			if mutated {
				t.Fatalf("the preflight Job must not mutate the release:\n%s", log)
			}
		})
	}
}

// The preflight Job and the apply Job run the same pre-change checks, so a
// release the preflight accepted can never be refused later for a reason the
// preflight would have caught.
func TestSelfUpgradeApplyRunsTheSameRollbackBaselineProbe(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "calls.log")
	env := selfUpgradeScriptEnv(callLog)
	env["SKOPEO_RESULT"] = "missing"
	out, err := runSelfUpgradeScript(t, selfUpgradeApplyScript, selfUpgradeFakeCommands(), env)
	if err == nil {
		t.Fatalf("the apply Job accepted an unusable rollback baseline:\n%s", out)
	}
	if !strings.Contains(out, selfUpgradeCategoryRollbackBaselineUnavailable) {
		t.Fatalf("the apply Job did not report the stable category:\n%s", out)
	}
	logBytes, readErr := os.ReadFile(callLog)
	if readErr != nil {
		t.Fatal(readErr)
	}
	log := string(logBytes)
	if strings.Contains(log, "helm rollback") {
		t.Fatalf("a refused pre-change check entered the rollback path:\n%s", log)
	}
}

// A rollback revision that does not name exactly one doops-agent image leaves
// the rollback target undefined, so the upgrade must not start.
func TestSelfUpgradePreflightRefusesAnUnresolvedRollbackBaseline(t *testing.T) {
	for _, tc := range []struct {
		name         string
		manifest     string
		history      string
		wantCategory string
	}{
		{
			name: "the revision has no doops-agent container",
			manifest: `{"kind":"Deployment","metadata":{"name":"doops-agent"},"spec":{"template":{"spec":{"containers":[` +
				`{"name":"sidecar","image":"registry.example/sidecar:old"}]}}}}`,
			wantCategory: selfUpgradeCategoryRollbackBaselineUnresolved,
		},
		{
			name: "the revision has two doops-agent containers",
			manifest: `{"kind":"Deployment","metadata":{"name":"doops-agent"},"spec":{"template":{"spec":{"containers":[` +
				`{"name":"doops-agent","image":"registry.example/doops:old"},` +
				`{"name":"doops-agent","image":"registry.example/doops:old"}]}}}}`,
			wantCategory: selfUpgradeCategoryRollbackBaselineUnresolved,
		},
		{
			name: "the revision names another workload",
			manifest: `{"kind":"Deployment","metadata":{"name":"other"},"spec":{"template":{"spec":{"containers":[` +
				`{"name":"doops-agent","image":"registry.example/doops:old"}]}}}}`,
			wantCategory: selfUpgradeCategoryRollbackBaselineUnresolved,
		},
		{
			name:         "the release has no single deployed revision",
			history:      `[{"revision":4,"status":"superseded"},{"revision":5,"status":"failed"}]`,
			wantCategory: selfUpgradeCategoryRollbackBaselineUnresolved,
		},
		{
			name:         "the release has two deployed revisions",
			history:      `[{"revision":4,"status":"deployed"},{"revision":5,"status":"deployed"}]`,
			wantCategory: selfUpgradeCategoryRollbackBaselineUnresolved,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			callLog := filepath.Join(t.TempDir(), "calls.log")
			env := selfUpgradeScriptEnv(callLog)
			env["REVISION_MANIFEST"] = tc.manifest
			if tc.history != "" {
				env["HELM_HISTORY"] = tc.history
			}
			out, err := runSelfUpgradeScript(t, selfUpgradePreflightScript, selfUpgradeFakeCommands(), env)
			if err == nil {
				t.Fatalf("preflight accepted an unresolved rollback baseline:\n%s", out)
			}
			if !strings.Contains(out, tc.wantCategory) {
				t.Fatalf("preflight did not report %s:\n%s", tc.wantCategory, out)
			}
			logBytes, readErr := os.ReadFile(callLog)
			if readErr != nil {
				t.Fatal(readErr)
			}
			log := string(logBytes)
			if strings.Contains(log, "helm upgrade") && !strings.Contains(log, "--dry-run=server") {
				t.Fatalf("the release changed despite an unresolved rollback baseline:\n%s", log)
			}
		})
	}
}

// The probe reads the baseline manifest from the registry, with the
// credentials the kubelet would use for the rollback Pod: the Agent Pod's own
// imagePullSecrets, or none. Each registry answer maps to one category, and a
// refused credential or an unreachable registry is never reported as a
// missing image. The pull secret payload reaches a 0600 file only, never an
// argument or the Job log, and the file is gone when the probe returns.
func TestSelfUpgradeRollbackBaselineProbeClassifiesTheRegistryAnswer(t *testing.T) {
	const canary = "c3ludGhldGljLXB1bGwtc2VjcmV0LWNhbmFyeQ=="
	for _, tc := range []struct {
		name         string
		skopeo       string
		pullSecret   string
		wantCategory string
		wantReason   string
		wantAuthfile bool
	}{
		{name: "the manifest is there, anonymous", skopeo: "ok"},
		{name: "the manifest is there, with the Agent pull secret", skopeo: "ok", pullSecret: "dockerconfig", wantAuthfile: true},
		{name: "the registry has no artifact for the digest", skopeo: "missing",
			wantCategory: selfUpgradeCategoryRollbackBaselineUnavailable, wantReason: "the registry has no manifest for this image"},
		{name: "the registry reports manifest unknown", skopeo: "manifest-unknown",
			wantCategory: selfUpgradeCategoryRollbackBaselineUnavailable, wantReason: "the registry has no manifest for this image"},
		{name: "the registry refuses the credentials", skopeo: "refused", pullSecret: "dockerconfig", wantAuthfile: true,
			wantCategory: selfUpgradeCategoryRollbackBaselineProbeFailed, wantReason: "the registry refused the pull credentials"},
		{name: "a refusal worded as not found is still a refusal", skopeo: "refused-not-found",
			wantCategory: selfUpgradeCategoryRollbackBaselineProbeFailed, wantReason: "the registry refused the pull credentials"},
		{name: "the registry is unreachable", skopeo: "unreachable",
			wantCategory: selfUpgradeCategoryRollbackBaselineProbeFailed, wantReason: "the registry manifest check failed"},
		{name: "the pull secret cannot be read", skopeo: "ok", pullSecret: "missing",
			wantCategory: selfUpgradeCategoryRollbackBaselineProbeFailed, wantReason: "the pull secret registry-pull could not be read"},
		{name: "the pull secret is not a registry credential", skopeo: "ok", pullSecret: "opaque",
			wantCategory: selfUpgradeCategoryRollbackBaselineProbeFailed, wantReason: "the pull secret registry-pull is not a registry credential"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			callLog := filepath.Join(t.TempDir(), "calls.log")
			env := selfUpgradeScriptEnv(callLog)
			env["SKOPEO_RESULT"] = tc.skopeo
			env["PULL_SECRET"] = tc.pullSecret
			env["PULL_SECRET_CANARY"] = canary
			out, err := runSelfUpgradeScript(t, selfUpgradePreflightScript, selfUpgradeFakeCommands(), env)
			logBytes, readErr := os.ReadFile(callLog)
			if readErr != nil {
				t.Fatal(readErr)
			}
			log := string(logBytes)

			if tc.wantCategory == "" {
				if err != nil {
					t.Fatalf("preflight refused a baseline whose manifest is there: %v\n%s", err, out)
				}
				if !strings.Contains(out, "preflight-passed image="+testPinnedImage()) {
					t.Fatalf("preflight did not pass:\n%s", out)
				}
			} else {
				if err == nil {
					t.Fatalf("preflight accepted %s:\n%s", tc.name, out)
				}
				if !strings.Contains(out, tc.wantCategory+": image=registry.example/doops:old reason="+tc.wantReason) {
					t.Fatalf("want %s with reason %q:\n%s", tc.wantCategory, tc.wantReason, out)
				}
				if tc.wantCategory != selfUpgradeCategoryRollbackBaselineUnavailable &&
					strings.Contains(out, selfUpgradeCategoryRollbackBaselineUnavailable) {
					t.Fatalf("a probe failure was reported as a missing image:\n%s", out)
				}
			}

			if tc.pullSecret == "missing" || tc.pullSecret == "opaque" {
				if strings.Contains(log, "skopeo ") {
					t.Fatalf("skopeo ran without the Agent's pull credentials:\n%s", log)
				}
			} else if !strings.Contains(log, "skopeo inspect --raw") || !strings.Contains(log, "docker://registry.example/doops:old") {
				t.Fatalf("the baseline manifest was not read:\n%s", log)
			}
			if tc.pullSecret != "" && !strings.Contains(log, "kubectl -n kz-ops get secret registry-pull -o json") {
				t.Fatalf("the Agent pull secret was not read from the Agent namespace:\n%s", log)
			}
			if tc.wantAuthfile {
				mode, _ := os.ReadFile(callLog + ".authmode")
				if strings.TrimSpace(string(mode)) != "600" {
					t.Fatalf("authfile mode = %q, want 600", mode)
				}
				content, _ := os.ReadFile(callLog + ".authfile")
				if !strings.Contains(string(content), canary) || !strings.Contains(string(content), `"registry.example"`) {
					t.Fatalf("authfile does not carry the Agent pull secret: %s", content)
				}
				path, _ := os.ReadFile(callLog + ".authpath")
				if _, statErr := os.Stat(strings.TrimSpace(string(path))); !os.IsNotExist(statErr) {
					t.Fatalf("authfile %s was left behind: %v", path, statErr)
				}
			} else {
				if strings.Contains(log, "--authfile") {
					t.Fatalf("an authfile was used without a pull secret:\n%s", log)
				}
				if tc.pullSecret == "" && !strings.Contains(log, "--no-creds") {
					t.Fatalf("a Pod without pull secrets must be checked anonymously:\n%s", log)
				}
			}
			if strings.Contains(out, canary) || strings.Contains(log, canary) {
				t.Fatalf("the pull secret payload reached the output or an argument:\n%s\n%s", out, log)
			}
			if strings.Contains(log, "create -f -") || strings.Contains(log, "job doops-rollback-baseline") {
				t.Fatalf("the probe must not start a Job:\n%s", log)
			}
		})
	}
}

// A target image that cannot be pulled never reaches the release: the preflight
// Job itself starts from that image, so the kubelet leaves the Job Pod pending
// and no pre-change check can run at all. The baseline probe must not paper
// over that, and the Job manifest must keep starting from the target image.
func TestSelfUpgradePreflightJobStartsFromTheTargetImage(t *testing.T) {
	pod := selfUpgradePod{
		image:              "registry.example/doops:old",
		nodeName:           "node-a",
		serviceAccountName: "doops-agent",
	}
	manifest, err := selfUpgradeJobManifest("doops-self-upgrade-aaaaaaaa", "kz-ops", pod, selfUpgradePlan{
		image: testPinnedImage(), repository: "registry.example/doops", digest: "sha256:" + testPinnedDigest,
		release: "doops-agent", workload: "doops-agent", script: selfUpgradePreflightScript, preflight: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(manifest)
	if !strings.Contains(text, testPinnedImage()) {
		t.Fatalf("the preflight Job must start from the target image:\n%s", text)
	}
	if strings.Contains(text, "registry.example/doops:old") {
		t.Fatalf("the preflight Job must not start from the old image:\n%s", text)
	}
}

// A Helm release that is not unique in the namespace leaves the upgrade target
// ambiguous, so the Agent refuses before it creates a Job.
func TestSelfUpgradeRefusesAnAmbiguousHelmRelease(t *testing.T) {
	proc := upgradeProcess{
		namespace: "kz-ops",
		hostname:  "doops-agent-abc",
		cluster:   "doops-edu",
		instance:  "edu-coder",
		run: func(ctx context.Context, stdin string, name string, args ...string) ([]byte, error) {
			joined := strings.Join(args, " ")
			switch {
			case strings.Contains(joined, "get jobs"):
				return []byte(`{"items":[]}`), nil
			case name == "helm" && strings.Contains(joined, "list"):
				return []byte(`[{"name":"doops-agent-live","chart":"doops-agent-0.1.0"},{"name":"doops-agent-canary","chart":"doops-agent-0.1.0"}]`), nil
			default:
				t.Fatalf("an ambiguous release continued to %s %s", name, joined)
				return nil, nil
			}
		},
	}
	_, err := proc.startSelfUpgradeJob(context.Background(), agentUpgradeParams{Image: testPinnedImage(), Mode: "k8s"})
	if err == nil || !strings.Contains(err.Error(), "exactly one managed doops-agent Helm release") {
		t.Fatalf("ambiguous release error = %v", err)
	}
}

// The operator override travels as a parameter, never as an environment
// variable an operator could set on the Agent by accident, and it is off by
// default.
func TestSelfUpgradeAllowMissingRollbackBaselineIsOptIn(t *testing.T) {
	pod := selfUpgradePod{
		image:              "registry.example/doops:old",
		nodeName:           "node-a",
		serviceAccountName: "doops-agent",
	}
	envNames := func(manifest []byte) []string {
		var decoded struct {
			Spec struct {
				Template struct {
					Spec struct {
						Containers []struct {
							Env []map[string]string `json:"env"`
						} `json:"containers"`
					} `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
		}
		if err := json.Unmarshal(manifest, &decoded); err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, container := range decoded.Spec.Template.Spec.Containers {
			for _, entry := range container.Env {
				names = append(names, entry["name"])
			}
		}
		return names
	}

	manifest, err := selfUpgradeJobManifest("doops-self-upgrade-aaaaaaaa", "kz-ops", pod, selfUpgradePlan{
		image: testPinnedImage(), repository: "registry.example/doops", digest: "sha256:" + testPinnedDigest,
		release: "doops-agent", workload: "doops-agent", script: selfUpgradePreflightScript,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range envNames(manifest) {
		if name == "DOOPS_UPGRADE_ALLOW_MISSING_ROLLBACK_BASELINE" {
			t.Fatalf("the override must be off by default: %v", envNames(manifest))
		}
	}

	manifest, err = selfUpgradeJobManifest("doops-self-upgrade-aaaaaaaa", "kz-ops", pod, selfUpgradePlan{
		image: testPinnedImage(), repository: "registry.example/doops", digest: "sha256:" + testPinnedDigest,
		release: "doops-agent", workload: "doops-agent", script: selfUpgradePreflightScript,
		allowMissingRollbackBaseline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, name := range envNames(manifest) {
		if name == "DOOPS_UPGRADE_ALLOW_MISSING_ROLLBACK_BASELINE" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the override did not reach the Job: %v", envNames(manifest))
	}
}

// The stable category has to survive the Job log, so the CLI can name the
// reason an upgrade was refused.
func TestSelfUpgradeFailureCategoryIsReadFromTheJobLog(t *testing.T) {
	for _, tc := range []struct {
		name string
		logs string
		want string
	}{
		{"unavailable", "rollback_baseline_image_unavailable: image=... reason=...", selfUpgradeCategoryRollbackBaselineUnavailable},
		{"unresolved", "rollback_baseline_image_unresolved: ...", selfUpgradeCategoryRollbackBaselineUnresolved},
		{"probe failed", "rollback_baseline_probe_failed: ...", selfUpgradeCategoryRollbackBaselineProbeFailed},
		{"stale mounts unresolved", "stale_volume_mounts_unresolved: the new chart must render exactly one doops-agent Deployment", selfUpgradeCategoryStaleVolumeMountsUnresolved},
		{"unrelated", "helm upgrade failed", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := selfUpgradeFailureCategory(tc.logs); got != tc.want {
				t.Fatalf("category = %q, want %q", got, tc.want)
			}
		})
	}
}

// The probe and the rollback script must read the rollback target through one
// helper, or the pre-change check could verify a different image than the one
// the rollback restores.
func TestSelfUpgradeSharesTheRollbackRevisionImageHelper(t *testing.T) {
	for name, script := range map[string]string{
		"pre-change checks": selfUpgradeApplyChecks,
		"rollback script":   selfUpgradeRollbackScript,
	} {
		if !strings.Contains(script, `revision_image() {`) {
			t.Fatalf("the %s does not define the shared revision image helper", name)
		}
		if !strings.Contains(script, `revision_image "$previous"`) {
			t.Fatalf("the %s does not read the rollback image through the shared helper", name)
		}
	}
}
