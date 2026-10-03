package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/user/doops/agent/api"
)

var testHelmDigest = "sha256:" + "a" + strings.Repeat("a", 63)

func testHelmParams() api.HelmParams {
	return api.HelmParams{
		SessionID:  "release",
		Operation:  "upgrade-install",
		Release:    "app",
		Chart:      "https://charts.example.test/app-1.0.0.tgz",
		Namespace:  "apps",
		ValueFiles: []string{"https://config.example.test/app-production.yaml"},
		SetStrings: map[string]string{
			"image.repository": "registry.example/app",
			"image.digest":     testHelmDigest,
		},
		Timeout: "180s",
		Confirm: true,
	}
}

func TestBuildHelmUpgradeInstallUsesRemoteAssetsAndImmutableImage(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)

	command, err := buildHelmCommand(testHelmParams())
	if err != nil {
		t.Fatalf("build helm command: %v", err)
	}
	if command.cwd != root {
		t.Fatalf("helm cwd = %q, want %q", command.cwd, root)
	}
	if command.expectedImage != "registry.example/app@"+testHelmDigest {
		t.Fatalf("expected image = %q", command.expectedImage)
	}
	got := strings.Join(command.args, " ")
	for _, expected := range []string{
		"upgrade app https://charts.example.test/app-1.0.0.tgz --install --atomic",
		"--values https://config.example.test/app-production.yaml",
		"--set-string image.digest=" + testHelmDigest,
		"--set-string image.repository=registry.example/app",
		"--namespace apps --wait --timeout 180s",
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("helm command missing %q: %s", expected, got)
		}
	}
}

func TestBuildHelmUpgradeInstallNormalizesOperationName(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	params := testHelmParams()
	params.Operation = "UPGRADE-INSTALL"

	command, err := buildHelmCommand(params)
	if err != nil {
		t.Fatalf("build normalized helm command: %v", err)
	}
	if !slices.Contains(command.args, "upgrade") {
		t.Fatalf("normalized operation did not build an upgrade command: %q", command.args)
	}
}

func TestBuildHelmUpgradeInstallSupportsReleaseTagImage(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	params := testHelmParams()
	params.SetStrings = map[string]string{
		"image.repository": "registry.example/app",
		"image.tag":        "release-20260927",
	}

	command, err := buildHelmCommand(params)
	if err != nil {
		t.Fatalf("build tag helm command: %v", err)
	}
	if command.expectedImage != "registry.example/app:release-20260927" {
		t.Fatalf("expected tag image = %q", command.expectedImage)
	}
	got := strings.Join(command.args, " ")
	if !strings.Contains(got, "--set-string image.tag=release-20260927") {
		t.Fatalf("helm command missing release tag: %s", got)
	}
}

func TestBuildHelmCommandRejectsLocalAssetsAndUncontrolledImages(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)

	tests := []struct {
		name      string
		mutate    func(*api.HelmParams)
		wantError string
	}{
		{
			name: "local chart",
			mutate: func(params *api.HelmParams) {
				params.Chart = "deploy/helm/app"
			},
			wantError: "explicit https:// or oci://",
		},
		{
			name: "unencrypted chart",
			mutate: func(params *api.HelmParams) {
				params.Chart = "http://charts.example.test/app-1.0.0.tgz"
			},
			wantError: "explicit https:// or oci://",
		},
		{
			name: "local values file",
			mutate: func(params *api.HelmParams) {
				params.ValueFiles = []string{"deploy/values/production.yaml"}
			},
			wantError: "explicit http(s)://",
		},
		{
			name: "missing immutable image",
			mutate: func(params *api.HelmParams) {
				params.SetStrings = nil
			},
			wantError: "requires image.repository plus either image.digest or image.tag",
		},
		{
			name: "mutable digest",
			mutate: func(params *api.HelmParams) {
				params.SetStrings["image.digest"] = "latest"
			},
			wantError: "invalid image.digest",
		},
		{
			name: "extra runtime override",
			mutate: func(params *api.HelmParams) {
				params.SetStrings["replicaCount"] = "2"
			},
			wantError: "only allows image.repository plus one controlled image selector",
		},
		{
			name: "credential material",
			mutate: func(params *api.HelmParams) {
				params.SetStrings["registry.password"] = "secret-value"
			},
			wantError: "only allows image.repository plus one controlled image selector",
		},
		{
			name: "mutable release tag",
			mutate: func(params *api.HelmParams) {
				params.SetStrings = map[string]string{
					"image.repository": "registry.example/app",
					"image.tag":        "latest",
				}
			},
			wantError: "expected release-YYYYMMDD",
		},
		{
			name: "mixed image selectors",
			mutate: func(params *api.HelmParams) {
				params.SetStrings["image.tag"] = "release-20260927"
			},
			wantError: "exactly one of image.digest or image.tag",
		},
		{
			name: "missing confirmation",
			mutate: func(params *api.HelmParams) {
				params.Confirm = false
			},
			wantError: "confirm=true",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params := testHelmParams()
			test.mutate(&params)
			if _, err := buildHelmCommand(params); err == nil ||
				!strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestHelmRemoteReferenceSchemes(t *testing.T) {
	for _, chart := range []string{"https://charts.example.test/app.tgz", "oci://registry.example.test/charts/app"} {
		if _, err := resolveRemoteHelmChartReference(chart); err != nil {
			t.Fatalf("allowed chart %s: %v", chart, err)
		}
	}
	if _, err := resolveRemoteHelmValueReference("http://config.example.test/values.yaml"); err != nil {
		t.Fatalf("values must retain their separate HTTP(S) contract: %v", err)
	}
}

func TestBuildHelmCommandRejectsLegacyValuesField(t *testing.T) {
	var params api.HelmParams
	decoder := json.NewDecoder(strings.NewReader(`{
		"session_id":"release",
		"operation":"upgrade-install",
		"release":"app",
		"chart":"https://charts.example.test/app.tgz",
		"namespace":"apps",
		"values":["image.repository=registry.example/app"],
		"confirm":true
	}`))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&params); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("legacy values field accepted: %v", err)
	}
}

func TestPrepareExistingHelmValuesRemovesLegacyImageSelectors(t *testing.T) {
	root := t.TempDir()
	helm := filepath.Join(root, "helm")
	if err := os.WriteFile(helm, []byte(`#!/bin/sh
set -eu
if [ "$1" = "status" ]; then
  exit 0
fi
if [ "$1" = "get" ] && [ "$2" = "values" ]; then
  printf '%s\n' '{"gateway":{"cluster":"prod"},"image":{"repository":"old.example/app","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","tag":"legacy","pullPolicy":"Always"}}'
  exit 0
fi
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))

	sanitized, found, err := prepareExistingHelmValues(context.Background(), helmCommand{
		cwd:       root,
		release:   "app",
		namespace: "apps",
	})
	if err != nil {
		t.Fatalf("prepare existing values: %v", err)
	}
	if !found {
		t.Fatal("existing release values were not found")
	}
	var values map[string]interface{}
	if err := json.Unmarshal(sanitized, &values); err != nil {
		t.Fatal(err)
	}
	image, _ := values["image"].(map[string]interface{})
	if len(image) != 1 || image["pullPolicy"] != "Always" {
		t.Fatalf("legacy image selectors were not removed: %#v", image)
	}
	if _, ok := values["gateway"]; !ok {
		t.Fatalf("non-image values were not preserved: %#v", values)
	}
}

func TestPrepareExistingHelmValuesAllowsFirstInstall(t *testing.T) {
	root := t.TempDir()
	helm := filepath.Join(root, "helm")
	if err := os.WriteFile(helm, []byte(`#!/bin/sh
echo 'Error: release: not found' >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))

	sanitized, found, err := prepareExistingHelmValues(context.Background(), helmCommand{
		cwd:       root,
		release:   "app",
		namespace: "apps",
	})
	if err != nil {
		t.Fatalf("first install rejected: %v", err)
	}
	if found || sanitized != nil {
		t.Fatalf("first install unexpectedly produced existing values: %s", sanitized)
	}
}

func TestRunHelmCommandVerifiesFinalDeploymentState(t *testing.T) {
	root := t.TempDir()
	argsFile := filepath.Join(root, "args")
	preflightArgsFile := filepath.Join(root, "preflight-args")
	stdinFile := filepath.Join(root, "stdin")
	helm := filepath.Join(root, "helm")
	kubectl := filepath.Join(root, "kubectl")
	if err := os.WriteFile(helm, []byte(`#!/bin/sh
set -eu
if [ "$1" = "status" ]; then
  exit 0
fi
if [ "$1" = "get" ] && [ "$2" = "values" ]; then
  printf '%s\n' '{"large":9007199254740993,"gateway":{"cluster":"prod"},"image":{"tag":"legacy","pullPolicy":"Always"}}'
  exit 0
fi
if [ "$1" = "get" ] && [ "$2" = "manifest" ]; then
  cat <<'YAML'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: app
spec:
  template:
    spec:
      containers:
        - name: app
          image: registry.example/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
YAML
  exit 0
fi
for arg in "$@"; do
  if [ "$arg" = "--dry-run=server" ]; then
    printf '%s\n' "$@" >"${PREFLIGHT_ARGS_FILE}"
    cat >/dev/null
    exit 0
  fi
done
printf '%s\n' "$@" >"${ARGS_FILE}"
cat >"${STDIN_FILE}"
`), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kubectl, []byte(`#!/bin/sh
set -eu
cat <<'JSON'
{"metadata":{"generation":7},"spec":{"replicas":1,"template":{"spec":{"containers":[{"image":"registry.example/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}}},"status":{"observedGeneration":7,"updatedReplicas":1,"readyReplicas":1,"availableReplicas":1,"unavailableReplicas":0}}
JSON
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ARGS_FILE", argsFile)
	t.Setenv("PREFLIGHT_ARGS_FILE", preflightArgsFile)
	t.Setenv("STDIN_FILE", stdinFile)
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)

	command, err := buildHelmCommand(testHelmParams())
	if err != nil {
		t.Fatalf("build helm command: %v", err)
	}
	verification, err := runHelmCommand(context.Background(), command, func(notificationEvent) {})
	if err != nil {
		t.Fatalf("run helm command: %v", err)
	}
	if !strings.Contains(verification, "final Deployment spec verified") {
		t.Fatalf("final Deployment verification missing: %q", verification)
	}
	preflightArgs, err := os.ReadFile(preflightArgsFile)
	if err != nil {
		t.Fatal(err)
	}
	preflightArgLines := strings.Split(strings.TrimSpace(string(preflightArgs)), "\n")
	if !slices.Contains(preflightArgLines, "--dry-run=server") ||
		slices.Contains(preflightArgLines, "--dry-run") {
		t.Fatalf("helm preflight must be server-side: %q", preflightArgLines)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	argLines := strings.Split(strings.TrimSpace(string(args)), "\n")
	if !slices.Contains(argLines, "https://charts.example.test/app-1.0.0.tgz") ||
		!slices.Contains(argLines, "--set-string") {
		t.Fatalf("remote chart or immutable image was not used: %q", argLines)
	}
	stdin, err := os.ReadFile(stdinFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stdin), `"tag"`) ||
		!strings.Contains(string(stdin), `"large":9007199254740993`) ||
		!strings.Contains(string(stdin), `"pullPolicy":"Always"`) {
		t.Fatalf("sanitized values stream is incorrect: %s", stdin)
	}
}

func TestRunHelmCommandPreflightBlocksMutation(t *testing.T) {
	root := t.TempDir()
	mutationMarker := filepath.Join(root, "mutation")
	helm := filepath.Join(root, "helm")
	if err := os.WriteFile(helm, []byte(`#!/bin/sh
set -eu
if [ "$1" = "status" ]; then
  echo 'Error: release: not found' >&2
  exit 1
fi
for arg in "$@"; do
  if [ "$arg" = "--dry-run=server" ]; then
    echo 'cannot overwrite table with non table' >&2
    exit 1
  fi
done
touch "${MUTATION_MARKER}"
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MUTATION_MARKER", mutationMarker)
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)

	command, err := buildHelmCommand(testHelmParams())
	if err != nil {
		t.Fatalf("build helm command: %v", err)
	}
	_, err = runHelmCommand(context.Background(), command, func(notificationEvent) {})
	if err == nil || err.Error() != "helm_preflight_failed" {
		t.Fatalf("preflight failure not returned: %v", err)
	}
	if _, err := os.Stat(mutationMarker); !os.IsNotExist(err) {
		t.Fatalf("helm mutation ran after failed preflight")
	}
}

func TestHelmSingleServiceReleaseVerifiesUnchangedServicesAgainstFinalSpec(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "helm"), []byte(`#!/bin/sh
cat <<'YAML'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
spec:
  template:
    spec:
      containers:
        - name: api
          image: registry.example/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: frontend
spec:
  template:
    spec:
      containers:
        - name: frontend
          image: registry.example/frontend:release-20260925
YAML
`), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "kubectl"), []byte(`#!/bin/sh
set -eu
if [ "$3" = "api" ]; then
  image=registry.example/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
else
  image="${FRONTEND_IMAGE}"
fi
printf '{"metadata":{"generation":7},"spec":{"replicas":1,"template":{"spec":{"containers":[{"image":"%s"}]}}},"status":{"observedGeneration":7,"updatedReplicas":1,"readyReplicas":1,"availableReplicas":1}}\n' "$image"
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FRONTEND_IMAGE", "registry.example/frontend:release-20260925")
	command := helmCommand{cwd: root, release: "app", namespace: "apps", expectedImage: "registry.example/app@" + testHelmDigest}
	if _, err := verifyHelmDeploymentState(t.Context(), command); err != nil {
		t.Fatalf("valid single-service update rejected: %v", err)
	}
	t.Setenv("FRONTEND_IMAGE", "registry.example/frontend:release-20260924")
	if _, err := verifyHelmDeploymentState(t.Context(), command); err == nil || !strings.Contains(err.Error(), "spec images") {
		t.Fatalf("unrelated service spec drift accepted: %v", err)
	}
	command.expectedImage = "registry.example/missing@" + testHelmDigest
	if _, err := verifyHelmDeploymentState(t.Context(), command); err == nil || !strings.Contains(err.Error(), "does not contain expected image") {
		t.Fatalf("missing intended image accepted: %v", err)
	}
}
