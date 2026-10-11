package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const testPinnedDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func testPinnedImage() string {
	return "registry.example/doops@" + "sha256:" + testPinnedDigest
}

func TestParsePinnedImage(t *testing.T) {
	repository, digest, err := parsePinnedImage("docker.cnb.cool/l8ai/ai/doops.sh@sha256:" + testPinnedDigest)
	if err != nil {
		t.Fatal(err)
	}
	if repository != "docker.cnb.cool/l8ai/ai/doops.sh" || digest != "sha256:"+testPinnedDigest {
		t.Fatalf("parsed repository=%s digest=%s", repository, digest)
	}
	for _, image := range []string{
		"registry.example/doops:latest",
		"registry.example/doops@sha256:abc",
		"Registry.Example/doops@sha256:" + testPinnedDigest,
		"registry.example/doops@sha256:" + strings.Repeat("A", 64),
	} {
		if _, _, err := parsePinnedImage(image); err == nil {
			t.Fatalf("accepted unpinned image %s", image)
		}
	}
}

func TestSelfUpgradeScriptStaysOutsideTheAgentProcess(t *testing.T) {
	if strings.Contains(selfUpgradeApplyScript, "set image") || strings.Contains(selfUpgradeApplyScript, "upgrade --install") {
		t.Fatal("apply script must helm-upgrade the existing release without kubectl set image")
	}
	for _, part := range []string{
		"maxUnavailable must be 0",
		"helm upgrade",
		"helm rollback",
		selfUpgradeChartPath,
		"/app/configure_kubeconfig.py",
		"selectorLabels",
		"observedGeneration",
		"availableReplicas",
		"unavailableReplicas",
		"--dry-run=server",
	} {
		if !strings.Contains(selfUpgradeApplyScript, part) {
			t.Fatalf("apply script missing %s", part)
		}
	}
	if strings.Contains(selfUpgradeApplyScript, "jsonpath=") {
		t.Fatal("apply script must verify one complete Deployment JSON")
	}
	if strings.Contains(selfUpgradeRollbackScript, `image["digest"]`) {
		t.Fatal("rollback script must not publish a new image")
	}
	if !strings.Contains(selfUpgradeRollbackScript, "helm rollback") {
		t.Fatal("rollback script missing helm rollback")
	}
	if !strings.Contains(selfUpgradeRollbackScript, `row["status"] == "superseded"`) ||
		!strings.Contains(selfUpgradeRollbackScript, "DOOPS_UPGRADE_REVISION") {
		t.Fatal("rollback script must recheck the admitted superseded revision")
	}
}

func TestSelfUpgradeJobManifestPreservesReleaseTag(t *testing.T) {
	pod := selfUpgradePod{
		image:              "registry.example/doops:old",
		nodeName:           "node-a",
		serviceAccountName: "default",
		mount:              jobVolumeMount{Name: "kube", MountPath: "/root/.kube", ReadOnly: true},
		volume:             json.RawMessage(`{"name":"kube","secret":{"secretName":"cluster-admin"}}`),
	}
	manifest, err := selfUpgradeJobManifest("doops-self-upgrade-aaaaaaaa", "kz-ops", pod, selfUpgradePlan{
		image:      "registry.example/doops:v1.3.40",
		repository: "registry.example/doops",
		tag:        "v1.3.40",
		release:    "doops-agent-live",
		workload:   "doops-agent",
		script:     selfUpgradeApplyScript,
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Spec struct {
			Template struct {
				Spec struct {
					Containers []struct {
						Image           string              `json:"image"`
						ImagePullPolicy string              `json:"imagePullPolicy"`
						Env             []map[string]string `json:"env"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(manifest, &decoded); err != nil {
		t.Fatal(err)
	}
	container := decoded.Spec.Template.Spec.Containers[0]
	if container.Image != "registry.example/doops:v1.3.40" || container.ImagePullPolicy != "Always" {
		t.Fatalf("tagged upgrade container = %#v", container)
	}
	for _, env := range container.Env {
		if env["name"] == "DOOPS_UPGRADE_TAG" && env["value"] == "v1.3.40" {
			return
		}
	}
	t.Fatal("tagged upgrade Job did not preserve DOOPS_UPGRADE_TAG")
}

func TestSelfUpgradeApplyScriptVerifiesCompleteDeploymentTwice(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "calls.log")
	env := selfUpgradeScriptEnv(callLog)
	out, err := runSelfUpgradeScript(t, selfUpgradeApplyScript, selfUpgradeFakeCommands(), env)
	if err != nil {
		t.Fatalf("self-upgrade script failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "self-upgrade complete image="+testPinnedImage()) {
		t.Fatalf("success evidence = %s", out)
	}
	logBytes, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logBytes)
	lines := strings.Split(strings.TrimSpace(log), "\n")
	dryRunLine := -1
	mutationLine := -1
	deploymentGets := 0
	for index, line := range lines {
		if strings.Contains(line, "get deployment") {
			deploymentGets++
		}
		if strings.Contains(line, "helm upgrade") && strings.Contains(line, "--dry-run=server") {
			dryRunLine = index
		}
		if strings.Contains(line, "helm upgrade") && !strings.Contains(line, "--dry-run=server") {
			mutationLine = index
		}
	}
	if dryRunLine < 0 || mutationLine < 0 || dryRunLine >= mutationLine {
		t.Fatalf("expected server-side dry-run before mutation, calls:\n%s", log)
	}
	if deploymentGets != 5 {
		t.Fatalf("expected strategy, selector, baseline, and two complete verification reads, got %d:\n%s", deploymentGets, log)
	}
	if strings.Contains(log, "jsonpath=") {
		t.Fatalf("script used partial jsonpath evidence:\n%s", log)
	}
}

func TestSelfUpgradeApplyScriptRejectsIncompleteDeploymentEvidence(t *testing.T) {
	for _, field := range []string{
		"observedGeneration",
		"replicas",
		"desiredReplicas",
		"statusReplicas",
		"updatedReplicas",
		"readyReplicas",
		"availableReplicas",
		"unavailableReplicas",
		"image",
	} {
		t.Run(field, func(t *testing.T) {
			callLog := filepath.Join(t.TempDir(), "calls.log")
			env := selfUpgradeScriptEnv(callLog)
			env["BAD_FIELD"] = field
			env["DATE_CALLS"] = filepath.Join(t.TempDir(), "date-calls")
			out, err := runSelfUpgradeScript(t, selfUpgradeApplyScript, selfUpgradeFakeCommands(), env)
			if err == nil {
				t.Fatalf("incomplete %s evidence was accepted:\n%s", field, out)
			}
			if !strings.Contains(out, "rolling back doops-agent-live to 5") ||
				!strings.Contains(out, "automatic rollback complete revision=5") {
				t.Fatalf("failed evidence did not complete a visible rollback:\n%s", out)
			}
			logBytes, readErr := os.ReadFile(callLog)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !strings.Contains(string(logBytes), "helm rollback doops-agent-live 5") {
				t.Fatalf("rollback revision was not the previous deployed revision:\n%s", logBytes)
			}
		})
	}
}

func TestSelfUpgradeVerifiesRuntimeIdentityFromPinnedController(t *testing.T) {
	env := selfUpgradeScriptEnv(filepath.Join(t.TempDir(), "calls.log"))
	env["RUNTIME_DIGEST"] = "sha256:" + strings.Repeat("c", 64)
	out, err := runSelfUpgradeScript(t, selfUpgradeApplyScript, selfUpgradeFakeCommands(), env)
	if err != nil || !strings.Contains(out, "self-upgrade complete image="+testPinnedImage()) {
		t.Fatalf("a shared runtime imageID must be proven by the independently pinned controller: %v\n%s", err, out)
	}
	if !strings.Contains(out, env["RUNTIME_DIGEST"]) {
		t.Fatalf("runtime imageID evidence is missing: %s", out)
	}
}

func TestSelfUpgradeRejectsUnverifiedControllerIdentity(t *testing.T) {
	for _, field := range []string{"image", "imageID", "node", "ready"} {
		t.Run(field, func(t *testing.T) {
			env := selfUpgradeScriptEnv(filepath.Join(t.TempDir(), "calls.log"))
			env["BAD_CONTROLLER_FIELD"] = field
			out, err := runSelfUpgradeScript(t, selfUpgradeApplyScript, selfUpgradeFakeCommands(), env)
			if err == nil || strings.Contains(out, "self-upgrade complete") {
				t.Fatalf("invalid controller %s was accepted: %v\n%s", field, err, out)
			}
			if !strings.Contains(out, "automatic rollback complete") {
				t.Fatalf("invalid controller must retain verified rollback: %s", out)
			}
		})
	}
}

func TestSelfUpgradeRejectsDifferentNodeRuntime(t *testing.T) {
	env := selfUpgradeScriptEnv(filepath.Join(t.TempDir(), "calls.log"))
	env["AGENT_NODE"] = "node-b"
	out, err := runSelfUpgradeScript(t, selfUpgradeApplyScript, selfUpgradeFakeCommands(), env)
	if err == nil || !strings.Contains(out, "same node runtime") || strings.Contains(out, "self-upgrade complete") {
		t.Fatalf("cross-node runtime identity was not rejected: %v\n%s", err, out)
	}
}

func TestSelfUpgradeAutomaticRollbackUsesCapturedRuntimeIdentity(t *testing.T) {
	env := selfUpgradeScriptEnv(filepath.Join(t.TempDir(), "calls.log"))
	env["OLD_IMAGE"] = "registry.example/doops@sha256:" + strings.Repeat("b", 64)
	env["OLD_RUNTIME_DIGEST"] = "sha256:" + strings.Repeat("d", 64)
	env["RUNTIME_DIGEST"] = "sha256:" + strings.Repeat("c", 64)
	env["BAD_FIELD"] = "readyReplicas"
	out, err := runSelfUpgradeScript(t, selfUpgradeApplyScript, selfUpgradeFakeCommands(), env)
	if err == nil || !strings.Contains(out, "automatic rollback complete revision=5") {
		t.Fatalf("failed update must restore the captured artifact identity: %v\n%s", err, out)
	}
	if !strings.Contains(out, env["OLD_RUNTIME_DIGEST"]) {
		t.Fatalf("captured rollback runtime identity was not verified: %s", out)
	}
}

func TestSelfUpgradeManualRollbackUsesHistoricalControllerIdentity(t *testing.T) {
	env := selfUpgradeScriptEnv(filepath.Join(t.TempDir(), "calls.log"))
	env["OLD_IMAGE"] = "registry.example/doops@sha256:" + strings.Repeat("b", 64)
	env["DOOPS_UPGRADE_IMAGE"] = env["OLD_IMAGE"]
	env["OLD_RUNTIME_DIGEST"] = "sha256:" + strings.Repeat("c", 64)
	env["RUNTIME_DIGEST"] = env["OLD_RUNTIME_DIGEST"]
	out, err := runSelfUpgradeScript(t, selfUpgradeRollbackScript, selfUpgradeFakeCommands(), env)
	if err != nil || !strings.Contains(out, "self-upgrade rolled back to 4") {
		t.Fatalf("historical manifest/index identity must remain verifiable: %v\n%s", err, out)
	}
}

func TestSelfUpgradeRollbackImageIsPinnedToSuccessfulHistory(t *testing.T) {
	wanted := "registry.example/doops@sha256:" + strings.Repeat("b", 64)
	manifest := `kind: Deployment
metadata: {name: doops-agent, namespace: kz-ops}
spec:
  template:
    spec:
      containers:
        - {name: doops-agent, image: "` + wanted + `"}
`
	for _, tc := range []struct {
		name     string
		manifest string
		wantErr  bool
	}{
		{"pinned", manifest, false},
		{"mutable", strings.ReplaceAll(manifest, wanted, "registry.example/doops:old"), true},
		{"wrong namespace", strings.ReplaceAll(manifest, "namespace: kz-ops", "namespace: other"), true},
		{"duplicate workload", manifest + "---\n" + manifest, true},
		{"missing evidence", manifest, true},
		{"evidence image mismatch", manifest, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := upgradeProcess{run: func(ctx context.Context, stdin, name string, args ...string) ([]byte, error) {
				joined := strings.Join(args, " ")
				if name == "kubectl" && joined == "-n kz-ops get configmap doops-upgrade-verified-doops-agent-live --ignore-not-found -o json" {
					if tc.name == "missing evidence" {
						return nil, nil
					}
					image := wanted
					if tc.name == "evidence image mismatch" {
						image = testPinnedImage()
					}
					evidence, _ := json.Marshal(map[string]interface{}{
						"schemaVersion": 1, "release": "doops-agent-live", "workload": "doops-agent",
						"revisions": map[string]interface{}{"7": map[string]string{"image": image}},
					})
					raw, _ := json.Marshal(map[string]interface{}{"data": map[string]string{"evidence.json": string(evidence)}})
					return raw, nil
				}
				if name != "helm" {
					t.Fatalf("unexpected mutation or command: %s %s", name, joined)
				}
				if strings.HasPrefix(joined, "history ") {
					return []byte(`[{"revision":7,"status":"superseded"},{"revision":8,"status":"deployed"},{"revision":9,"status":"failed"}]`), nil
				}
				if joined != "get manifest doops-agent-live -n kz-ops --revision 7" {
					t.Fatalf("rollback selected the wrong revision: %s", joined)
				}
				return []byte(tc.manifest), nil
			}}
			got, revision, err := p.rollbackImage(t.Context(), "kz-ops", "doops-agent-live", "doops-agent")
			if (err != nil) != tc.wantErr || (!tc.wantErr && (got != wanted || revision != 7)) {
				t.Fatalf("rollback artifact = %q, %v", got, err)
			}
		})
	}
}

func TestSelfUpgradeReceiptFailurePreventsMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.log")
	env := selfUpgradeScriptEnv(path)
	env["RECEIPT_FAIL"] = "1"
	out, err := runSelfUpgradeScript(t, selfUpgradeApplyScript, selfUpgradeFakeCommands(), env)
	if err == nil || !strings.Contains(out, "verification receipt persistence failed") {
		t.Fatalf("missing durable baseline must fail: %v\n%s", err, out)
	}
	log, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(log), "helm upgrade") || strings.Contains(string(log), "helm rollback") {
		t.Fatalf("mutation dispatched without baseline receipt: %s", log)
	}
}

func TestSelfUpgradeApplyScriptReportsRollbackFailure(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "calls.log")
	env := selfUpgradeScriptEnv(callLog)
	env["BAD_FIELD"] = "availableReplicas"
	env["DATE_CALLS"] = filepath.Join(t.TempDir(), "date-calls")
	env["ROLLBACK_FAIL"] = "1"
	out, err := runSelfUpgradeScript(t, selfUpgradeApplyScript, selfUpgradeFakeCommands(), env)
	if err == nil {
		t.Fatalf("script reported success after rollback failure:\n%s", out)
	}
	for _, part := range []string{
		"helm rollback failed",
		"Error: automatic rollback failed: Helm rollback command failed",
		"Error: automatic rollback failed; manual intervention is required",
	} {
		if !strings.Contains(out, part) {
			t.Fatalf("rollback failure evidence missing %q:\n%s", part, out)
		}
	}
	if strings.Contains(out, "self-upgrade complete") {
		t.Fatalf("failed rollback was reported as upgrade success:\n%s", out)
	}
}

func TestSelfUpgradeApplyScriptReportsRollbackVerificationFailure(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "calls.log")
	env := selfUpgradeScriptEnv(callLog)
	env["BAD_FIELD"] = "availableReplicas"
	env["DATE_CALLS"] = filepath.Join(t.TempDir(), "date-calls")
	env["ROLLBACK_VERIFY_FAIL"] = "1"
	out, err := runSelfUpgradeScript(t, selfUpgradeApplyScript, selfUpgradeFakeCommands(), env)
	if err == nil {
		t.Fatalf("script reported success after rollback verification failure:\n%s", out)
	}
	if !strings.Contains(out, "Error: automatic rollback failed: rolled-back Deployment final state is not verified") ||
		!strings.Contains(out, "Error: automatic rollback failed; manual intervention is required") {
		t.Fatalf("rollback verification failure was not visible:\n%s", out)
	}
	if strings.Contains(out, "automatic rollback complete") {
		t.Fatalf("rollback verification failure was reported as complete:\n%s", out)
	}
}

func TestSelfUpgradeApplyScriptRejectsStableWindowDrift(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "calls.log")
	env := selfUpgradeScriptEnv(callLog)
	env["DATE_CALLS"] = filepath.Join(t.TempDir(), "date-calls")
	env["DRIFT_ON_SECOND_VERIFY"] = "1"
	out, err := runSelfUpgradeScript(t, selfUpgradeApplyScript, selfUpgradeFakeCommands(), env)
	if err == nil {
		t.Fatalf("stable-window drift was accepted:\n%s", out)
	}
	if strings.Contains(out, "self-upgrade complete") ||
		!strings.Contains(out, "automatic rollback complete revision=5") {
		t.Fatalf("stable-window drift did not fail and roll back:\n%s", out)
	}
}

func TestSelfUpgradeApplyScriptDryRunFailureDoesNotMutate(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "calls.log")
	env := selfUpgradeScriptEnv(callLog)
	env["DRY_RUN_FAIL"] = "1"
	out, err := runSelfUpgradeScript(t, selfUpgradeApplyScript, selfUpgradeFakeCommands(), env)
	if err == nil {
		t.Fatalf("dry-run failure was accepted:\n%s", out)
	}
	logBytes, readErr := os.ReadFile(callLog)
	if readErr != nil {
		t.Fatal(readErr)
	}
	log := string(logBytes)
	if !strings.Contains(log, "--dry-run=server") {
		t.Fatalf("dry-run command was not attempted:\n%s", log)
	}
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		if strings.Contains(line, "helm upgrade") && !strings.Contains(line, "--dry-run=server") {
			t.Fatalf("mutation ran after dry-run failure:\n%s", log)
		}
	}
	if strings.Contains(log, "helm rollback") || strings.Contains(log, "rollout status") {
		t.Fatalf("dry-run failure unexpectedly entered rollback:\n%s", log)
	}
}

func TestSelfUpgradeRejectsInvalidPodEvidence(t *testing.T) {
	for _, field := range []string{"missing", "uid", "ready", "image", "imageID", "digest"} {
		t.Run(field, func(t *testing.T) {
			env := selfUpgradeScriptEnv(filepath.Join(t.TempDir(), "calls.log"))
			env["BAD_POD_FIELD"] = field
			out, err := runSelfUpgradeScript(t, selfUpgradeApplyScript, selfUpgradeFakeCommands(), env)
			if err == nil || strings.Contains(out, "self-upgrade complete") {
				t.Fatalf("invalid Pod %s accepted: %v\n%s", field, err, out)
			}
			if !strings.Contains(out, "automatic rollback complete revision=5") {
				t.Fatalf("invalid Pod did not restore the old release:\n%s", out)
			}
		})
	}
}

func TestSelfUpgradeRejectsNonstandardSurgeBeforeMutation(t *testing.T) {
	for _, surge := range []string{"0", "2", "true", `"100%"`} {
		t.Run(surge, func(t *testing.T) {
			logPath := filepath.Join(t.TempDir(), "calls.log")
			env := selfUpgradeScriptEnv(logPath)
			env["MAX_SURGE"] = surge
			out, err := runSelfUpgradeScript(t, selfUpgradeApplyScript, selfUpgradeFakeCommands(), env)
			if err == nil || !strings.Contains(out, "maxSurge must be 1") {
				t.Fatalf("nonstandard surge accepted: %v\n%s", err, out)
			}
			log, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(log), "helm upgrade") || strings.Contains(string(log), "helm rollback") {
				t.Fatalf("nonstandard surge reached mutation:\n%s", log)
			}
		})
	}
}

func TestSelfUpgradeRollbackRejectsDriftAfterHelmSuccess(t *testing.T) {
	env := selfUpgradeScriptEnv(filepath.Join(t.TempDir(), "calls.log"))
	env["DOOPS_UPGRADE_IMAGE"] = "registry.example/doops:old"
	env["ROLLBACK_VERIFY_FAIL"] = "1"
	out, err := runSelfUpgradeScript(t, selfUpgradeRollbackScript, selfUpgradeFakeCommands(), env)
	if err == nil || strings.Contains(out, "self-upgrade rolled back to") {
		t.Fatalf("rollback accepted a different live image: %v\n%s", err, out)
	}
	if !strings.Contains(out, "image mismatch") {
		t.Fatalf("rollback did not report final image mismatch:\n%s", out)
	}
}

func TestSelfUpgradeRollbackUsesSuccessfulSupersededRevision(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "calls.log")
	env := selfUpgradeScriptEnv(callLog)
	env["DOOPS_UPGRADE_IMAGE"] = "registry.example/doops:old"
	env["DOOPS_UPGRADE_REVISION"] = "7"
	commands := selfUpgradeFakeCommands()
	commands["helm"] = `#!/bin/sh
set -eu
printf 'helm %s\n' "$*" >> "$CALL_LOG"
	case "${1:-}" in
  history)
    printf '%s\n' '[{"revision":6,"status":"failed"},{"revision":7,"status":"superseded"},{"revision":8,"status":"deployed"}]'
    ;;
  get)
    printf '%s\n' '{"kind":"Deployment","metadata":{"name":"doops-agent"},"spec":{"template":{"spec":{"containers":[{"name":"doops-agent","image":"registry.example/doops:old"}]}}}}'
    ;;
  rollback)
    printf 'rolled back\n' >"$ROLLBACK_MARKER"
    ;;
  *)
    echo "unexpected helm command: $*" >&2
    exit 2
    ;;
esac
`
	out, err := runSelfUpgradeScript(t, selfUpgradeRollbackScript, commands, env)
	if err != nil {
		t.Fatalf("manual rollback failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "self-upgrade rolled back to 7") {
		t.Fatalf("manual rollback evidence = %s", out)
	}
	logBytes, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "helm rollback doops-agent-live 7") {
		t.Fatalf("manual rollback did not use the prior superseded revision:\n%s", log)
	}
	if strings.Contains(log, "helm rollback doops-agent-live 9") {
		t.Fatalf("manual rollback selected a failed revision:\n%s", log)
	}
}

func selfUpgradeScriptEnv(callLog string) map[string]string {
	return map[string]string{
		"DOOPS_UPGRADE_REVISION":   "4",
		"RECEIPT_FILE":             callLog + ".receipt",
		"HOSTNAME":                 "upgrade-controller",
		"CALL_LOG":                 callLog,
		"KUBECTL_CALLS":            callLog + ".kubectl",
		"DATE_CALLS":               callLog + ".date",
		"ROLLBACK_MARKER":          callLog + ".rollback",
		"DOOPS_UPGRADE_NAMESPACE":  "kz-ops",
		"DOOPS_UPGRADE_RELEASE":    "doops-agent-live",
		"DOOPS_UPGRADE_WORKLOAD":   "doops-agent",
		"DOOPS_UPGRADE_REPOSITORY": "registry.example/doops",
		"DOOPS_UPGRADE_DIGEST":     "sha256:" + testPinnedDigest,
		"DOOPS_UPGRADE_IMAGE":      testPinnedImage(),
	}
}

func runSelfUpgradeScript(t *testing.T, script string, commands map[string]string, extraEnv map[string]string) (string, error) {
	t.Helper()
	binDir := t.TempDir()
	for name, contents := range commands {
		path := filepath.Join(binDir, name)
		if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	body := strings.TrimPrefix(script, selfUpgradeKubeconfigPrelude)
	if body == script {
		t.Fatal("script does not contain the kubeconfig prelude")
	}
	command := `set -eu
ns="$DOOPS_UPGRADE_NAMESPACE"
release="$DOOPS_UPGRADE_RELEASE"
workload="$DOOPS_UPGRADE_WORKLOAD"
` + body
	cmd := exec.Command("sh", "-c", command)
	env := append([]string{}, os.Environ()...)
	env = append(env, "PATH="+binDir+":"+os.Getenv("PATH"))
	env = append(env, "TMPDIR="+t.TempDir())
	for key, value := range extraEnv {
		env = append(env, key+"="+value)
	}
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func selfUpgradeFakeCommands() map[string]string {
	return map[string]string{
		"sleep": `#!/bin/sh
exit 0
`,
		"date": `#!/bin/sh
set -eu
calls=0
if [ -f "$DATE_CALLS" ]; then
  calls="$(cat "$DATE_CALLS")"
fi
calls=$((calls + 1))
printf '%s\n' "$calls" >"$DATE_CALLS"
if [ "$calls" -ge 3 ]; then
  printf '601\n'
else
  printf '0\n'
fi
`,
		"helm": `#!/bin/sh
set -eu
printf 'helm %s\n' "$*" >> "$CALL_LOG"
case "${1:-}" in
  history)
    printf '%s\n' '[{"revision":4,"status":"superseded"},{"revision":5,"status":"deployed"},{"revision":6,"status":"failed"}]'
    ;;
  get)
    if [ "${2:-}" = "manifest" ]; then
      printf '{"kind":"Deployment","metadata":{"name":"doops-agent"},"spec":{"template":{"spec":{"containers":[{"name":"doops-agent","image":"%s"}]}}}}\n' "${OLD_IMAGE:-registry.example/doops:old}"
    else
      printf '%s\n' '{"image":{"repository":"registry.example/doops","tag":"old"},"workload":{"name":"doops-agent"}}'
    fi
    ;;
  upgrade)
    case "$*" in
      *"--dry-run=server"*)
        if [ "${DRY_RUN_FAIL:-0}" = "1" ]; then
          echo "helm dry-run failed" >&2
          exit 19
        fi
        ;;
    esac
    exit 0
    ;;
  rollback)
    if [ "${ROLLBACK_FAIL:-0}" = "1" ]; then
      echo "helm rollback failed" >&2
      exit 17
    fi
    printf 'rolled back\n' >"$ROLLBACK_MARKER"
    ;;
  *)
    echo "unexpected helm command: $*" >&2
    exit 2
    ;;
esac
`,
		"kubectl": `#!/bin/sh
set -eu
printf 'kubectl %s\n' "$*" >> "$CALL_LOG"
case "$*" in
  *"get configmap"*)
    if [ -f "$RECEIPT_FILE" ]; then cat "$RECEIPT_FILE"; fi
    ;;
  *"apply -f -"*)
    if [ "${RECEIPT_FAIL:-0}" = "1" ]; then echo "verification receipt persistence failed" >&2; exit 17; fi
    cat >"$RECEIPT_FILE"
    ;;
  *"-o jsonpath="*)
    echo "jsonpath is forbidden in this test" >&2
    exit 91
    ;;
  *"rollout status"*)
    exit 0
    ;;
  *"get pod upgrade-controller"*)
    image="$DOOPS_UPGRADE_IMAGE"
    image_id="registry.example/doops@${RUNTIME_DIGEST:-$DOOPS_UPGRADE_DIGEST}"
    node="node-a"
    ready=true
    case "${BAD_CONTROLLER_FIELD:-}" in
      image) image="registry.example/doops:old" ;;
      imageID) image_id="" ;;
      node) node="" ;;
      ready) ready=false ;;
    esac
    printf '{"metadata":{"name":"upgrade-controller","uid":"controller-uid"},"spec":{"nodeName":"%s","containers":[{"name":"controller","image":"%s"}]},"status":{"phase":"Running","containerStatuses":[{"name":"controller","ready":%s,"imageID":"%s"}]}}\n' "$node" "$image" "$ready" "$image_id"
    ;;
  *"get pods"*)
    image="$DOOPS_UPGRADE_IMAGE"
    image_id="registry.example/doops@${RUNTIME_DIGEST:-$DOOPS_UPGRADE_DIGEST}"
    uid="candidate-pod"
    ready=true
    calls=0
    if [ -f "$KUBECTL_CALLS" ]; then calls="$(cat "$KUBECTL_CALLS")"; fi
    if [ -f "$ROLLBACK_MARKER" ] || [ "$calls" -lt 4 ]; then
      image="${OLD_IMAGE:-registry.example/doops:old}"
      image_id="registry.example/doops@${OLD_RUNTIME_DIGEST:-$DOOPS_UPGRADE_DIGEST}"
      uid="restored-pod"
    else
      case "${BAD_POD_FIELD:-}" in
        missing) printf '{"items":[]}\n'; exit 0 ;;
        uid) uid="" ;;
        ready) ready=false ;;
        image) image="registry.example/doops:old" ;;
        imageID) image_id="" ;;
        digest) image_id="registry.example/doops@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" ;;
      esac
    fi
    printf '{"items":[{"metadata":{"name":"agent-pod","uid":"%s"},"spec":{"nodeName":"%s","containers":[{"name":"doops-agent","image":"%s"}]},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}],"containerStatuses":[{"name":"doops-agent","ready":%s,"imageID":"%s"}]}}]}\n' "$uid" "${AGENT_NODE:-node-a}" "$image" "$ready" "$image_id"
    ;;
  *"get deployment"*)
    calls=0
    if [ -f "$KUBECTL_CALLS" ]; then
      calls="$(cat "$KUBECTL_CALLS")"
    fi
    calls=$((calls + 1))
    printf '%s\n' "$calls" >"$KUBECTL_CALLS"
    image="${OLD_IMAGE:-registry.example/doops:old}"
    if [ ! -f "$ROLLBACK_MARKER" ] && [ "$calls" -ge 4 ]; then
      image="$DOOPS_UPGRADE_IMAGE"
    fi
    if [ ! -f "$ROLLBACK_MARKER" ] && [ "${BAD_FIELD:-}" = "image" ] && [ "$calls" -ge 4 ]; then
      image="registry.example/doops@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
    fi
    if [ ! -f "$ROLLBACK_MARKER" ] && [ "${DRIFT_ON_SECOND_VERIFY:-0}" = "1" ] && [ "$calls" -ge 5 ]; then
      image="registry.example/doops@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
    fi
    generation=2
    observed=2
    replicas=1
    status_replicas=1
    updated=1
    ready=1
    available=1
    unavailable=0
    if [ ! -f "$ROLLBACK_MARKER" ] && [ "$calls" -ge 4 ]; then
      case "${BAD_FIELD:-}" in
        observedGeneration) observed=1 ;;
        replicas) replicas=2 ;;
        desiredReplicas) replicas=0 ;;
        statusReplicas) status_replicas=0 ;;
        updatedReplicas) updated=0 ;;
        readyReplicas) ready=0 ;;
        availableReplicas) available=0 ;;
        unavailableReplicas) unavailable=1 ;;
      esac
    fi
    if [ -f "$ROLLBACK_MARKER" ]; then
      status_replicas=1
      updated=1
      ready=1
      available=1
      unavailable=0
    fi
    if [ -f "$ROLLBACK_MARKER" ] && [ "${ROLLBACK_VERIFY_FAIL:-0}" = "1" ]; then
      image="registry.example/doops@sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
    fi
    printf '{"metadata":{"name":"doops-agent","generation":%s},"spec":{"replicas":%s,"strategy":{"type":"RollingUpdate","rollingUpdate":{"maxUnavailable":0,"maxSurge":%s}},"selector":{"matchLabels":{"app":"doops"}},"template":{"spec":{"containers":[{"name":"sidecar","image":"registry.example/sidecar:old"},{"name":"doops-agent","image":"%s"}]}}},"status":{"observedGeneration":%s,"replicas":%s,"updatedReplicas":%s,"readyReplicas":%s,"availableReplicas":%s,"unavailableReplicas":%s}}\n' "$generation" "$replicas" "${MAX_SURGE:-1}" "$image" "$observed" "$status_replicas" "$updated" "$ready" "$available" "$unavailable"
    ;;
  *)
    echo "unexpected kubectl command: $*" >&2
    exit 2
    ;;
esac
`,
	}
}

func TestSelfUpgradeJobUsesSharedKubeconfigVolume(t *testing.T) {
	pod := selfUpgradePod{
		image:              "registry.example/doops:old",
		nodeName:           "node-a",
		serviceAccountName: "default",
		runtimeClassName:   "verified-runtime",
		imagePullSecrets:   []string{"cnb"},
		mount:              jobVolumeMount{Name: "kube", MountPath: "/root/.kube", ReadOnly: true},
		volume:             json.RawMessage(`{"name":"kube","secret":{"secretName":"cluster-admin"}}`),
	}
	manifest, err := selfUpgradeJobManifest("doops-self-upgrade-aaaaaaaa", "kz-ops", pod, selfUpgradePlan{
		image:      testPinnedImage(),
		repository: "registry.example/doops",
		digest:     "sha256:" + testPinnedDigest,
		release:    "doops-agent-live",
		workload:   "doops-agent",
		script:     selfUpgradeApplyScript,
	})
	if err != nil {
		t.Fatal(err)
	}
	var job map[string]interface{}
	if err := json.Unmarshal(manifest, &job); err != nil {
		t.Fatal(err)
	}
	text := string(manifest)
	if strings.Contains(text, "hostPath") || strings.Contains(text, "hostNetwork") || strings.Contains(text, "hostPID") {
		t.Fatalf("job copied node privileges: %s", text)
	}
	if !strings.Contains(text, `"privileged":false`) {
		t.Fatalf("controller must not be privileged: %s", text)
	}
	if !strings.Contains(text, testPinnedImage()) || strings.Contains(text, "registry.example/doops:old") {
		t.Fatalf("job image should be the pinned target: %s", text)
	}
	spec := job["spec"].(map[string]interface{})
	if spec["backoffLimit"].(float64) != 0 {
		t.Fatalf("backoffLimit = %v", spec["backoffLimit"])
	}
	podSpec := spec["template"].(map[string]interface{})["spec"].(map[string]interface{})
	if podSpec["restartPolicy"] != "Never" {
		t.Fatalf("restartPolicy = %v", podSpec["restartPolicy"])
	}
	if podSpec["nodeName"] != "node-a" {
		t.Fatal("the controller must use the Agent node runtime even with a Secret kubeconfig")
	}
	if podSpec["runtimeClassName"] != "verified-runtime" {
		t.Fatal("the controller must retain the Agent runtime class")
	}
	volumes := podSpec["volumes"].([]interface{})
	if len(volumes) != 1 {
		t.Fatalf("volumes = %#v", volumes)
	}
}

func TestSelfUpgradePinsHostPathJobToTheAgentNode(t *testing.T) {
	_, err := nodeNameForVolume(json.RawMessage(`{"name":"kube","emptyDir":{}}`), "node-a")
	if err == nil || !strings.Contains(err.Error(), "cannot be shared") {
		t.Fatalf("emptyDir error = %v", err)
	}
	node, err := nodeNameForVolume(json.RawMessage(`{"name":"kube","hostPath":{"path":"/root/.kube"}}`), "node-a")
	if err != nil || node != "node-a" {
		t.Fatalf("hostPath node = %q err=%v", node, err)
	}
}

func TestStartSelfUpgradeCreatesJobWithoutHelmMutation(t *testing.T) {
	var applied string
	proc := upgradeProcess{
		namespace: "kz-ops",
		hostname:  "doops-agent-abc",
		cluster:   "doops-edu",
		instance:  "edu-coder",
		run: func(ctx context.Context, stdin string, name string, args ...string) ([]byte, error) {
			joined := strings.Join(args, " ")
			if name == "helm" && strings.Contains(joined, "upgrade") {
				t.Fatalf("agent process ran helm upgrade: %s", joined)
			}
			switch {
			case strings.Contains(joined, "get jobs"):
				return []byte(`{"items":[]}`), nil
			case strings.Contains(joined, "get job "):
				return nil, fmt.Errorf("Error from server (NotFound): jobs.batch not found")
			case strings.Contains(joined, "get pod"):
				return []byte(selfUpgradePodFixture), nil
			case name == "helm" && strings.Contains(joined, "list"):
				return []byte(`[{"name":"doops-agent-live","chart":"doops-agent-0.1.0"}]`), nil
			case name == "helm" && strings.Contains(joined, "get values"):
				return []byte(`{"gateway":{"cluster":"doops-edu","instance":"edu-coder"},"workload":{"name":"doops-agent"}}`), nil
			case strings.Contains(joined, "apply"):
				applied = stdin
				return []byte("job.batch/doops-self-upgrade-aaaaaaaa created\n"), nil
			default:
				t.Fatalf("unexpected command %s %s", name, joined)
				return nil, nil
			}
		},
	}
	out, err := proc.startSelfUpgradeJob(context.Background(), agentUpgradeParams{
		Image: testPinnedImage(),
		Mode:  "k8s",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "self-upgrade-accepted job=doops-self-upgrade-aaaaaaaa namespace=kz-ops release=doops-agent-live") {
		t.Fatalf("acceptance = %s", out)
	}
	if !strings.Contains(applied, "helm rollback") || strings.Contains(applied, "set image") {
		t.Fatalf("job manifest lost the rollback controller: %s", applied)
	}
	if strings.Contains(applied, "emptyDir") || strings.Contains(applied, `"privileged":true`) {
		t.Fatalf("job copied the agent pod privileges or data volume: %s", applied)
	}
}

func TestStartSelfUpgradeRefusesForeignIdentity(t *testing.T) {
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
				return []byte(`[{"name":"doops-agent-live","chart":"doops-agent-0.1.0"}]`), nil
			case name == "helm" && strings.Contains(joined, "get values"):
				return []byte(`{"gateway":{"cluster":"other","instance":"node"},"workload":{"name":"doops-agent"}}`), nil
			default:
				t.Fatalf("identity mismatch continued to %s %s", name, joined)
				return nil, nil
			}
		},
	}
	_, err := proc.startSelfUpgradeJob(context.Background(), agentUpgradeParams{Image: testPinnedImage(), Mode: "k8s"})
	if err == nil || !strings.Contains(err.Error(), "does not match this agent") {
		t.Fatalf("identity error = %v", err)
	}
}

func TestObserveSelfUpgradeReportsTerminalPhase(t *testing.T) {
	proc := upgradeProcess{
		namespace: "kz-ops",
		hostname:  "doops-agent-abc",
		run: func(ctx context.Context, stdin string, name string, args ...string) ([]byte, error) {
			joined := strings.Join(args, " ")
			switch {
			case strings.Contains(joined, "get job"):
				return []byte(`{"status":{"succeeded":1}}`), nil
			case strings.Contains(joined, "logs"):
				return []byte("self-upgrade complete image=registry.example/doops@sha256:" + testPinnedDigest + "\n"), nil
			default:
				t.Fatalf("unexpected observe command %s", joined)
				return nil, nil
			}
		},
	}
	out, err := proc.observeSelfUpgrade(context.Background(), agentUpgradeParams{
		ObserveJob: "doops-self-upgrade-" + testPinnedDigest[:8],
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "self-upgrade-succeeded") || !strings.Contains(out, "self-upgrade complete") {
		t.Fatalf("observe = %s", out)
	}
}

const selfUpgradePodFixture = `{
  "spec": {
    "nodeName": "node-a",
    "serviceAccountName": "default",
    "imagePullSecrets": [{"name": "cnb"}],
    "containers": [{
      "name": "doops-agent",
      "image": "registry.example/doops:old",
      "securityContext": {"privileged": true},
      "volumeMounts": [
        {"name": "kube", "mountPath": "/root/.kube", "readOnly": true},
        {"name": "data", "mountPath": "/var/lib/data"}
      ]
    }],
    "volumes": [
      {"name": "kube", "secret": {"secretName": "cluster-admin"}},
      {"name": "data", "emptyDir": {}}
    ]
  }
}`
