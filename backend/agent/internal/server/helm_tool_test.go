package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
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
		ExpectedImage: "registry.example/app@" + testHelmDigest,
		Timeout:       "180s",
		Confirm:       true,
	}
}

func testWorkspaceHelmParams() api.HelmParams {
	return api.HelmParams{
		SessionID:     "release",
		Operation:     "upgrade-install",
		Release:       "app",
		AssetSource:   "workspace",
		Chart:         "deploy/helm/prod",
		Namespace:     "apps",
		ValueFiles:    []string{"deploy/helm/prod/values.yaml"},
		ExpectedImage: "registry.example/app@" + testHelmDigest,
		Timeout:       "180s",
		Confirm:       true,
	}
}

func writeWorkspaceHelmChart(t *testing.T, root string) {
	t.Helper()
	chart := filepath.Join(root, "release", "deploy", "helm", "prod")
	if err := os.MkdirAll(chart, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chart, "Chart.yaml"), []byte("apiVersion: v2\nname: app\nversion: 1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chart, "values.yaml"), []byte("image:\n  repository: registry.example/app\n  digest: "+testHelmDigest+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildHelmUpgradeInstallUsesBoundWorkspaceAssets(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	writeWorkspaceHelmChart(t, root)

	command, err := buildHelmCommand(testWorkspaceHelmParams())
	if err != nil {
		t.Fatalf("build workspace helm command: %v", err)
	}
	wantCWD := filepath.Join(root, "release")
	if command.cwd != wantCWD {
		t.Fatalf("helm cwd = %q, want %q", command.cwd, wantCWD)
	}
	if command.expectedImage != "registry.example/app@"+testHelmDigest {
		t.Fatalf("expected image = %q", command.expectedImage)
	}
	if !slices.Contains(command.args, "--reset-values") {
		t.Fatal("workspace assets must reset old release values")
	}
	for _, want := range []string{"upgrade", "app", "deploy/helm/prod", "--install", "--reset-values", "--values", "deploy/helm/prod/values.yaml", "--namespace", "apps", "--wait", "--timeout", "180s", "--description"} {
		if !slices.Contains(command.args, want) {
			t.Fatalf("helm args missing %q: %#v", want, command.args)
		}
	}
	if command.mutationID == "" {
		t.Fatal("workspace upgrade must bind a mutation identity")
	}
}

func TestBuildHelmRollbackUsesSupportedHelmFlags(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	params := testHelmParams()
	params.Operation = "rollback"
	params.Revision = 2
	params.ValueFiles = nil
	params.SetStrings = nil
	params.ExpectedImage = ""

	command, err := buildHelmCommand(params)
	if err != nil {
		t.Fatalf("build rollback command: %v", err)
	}
	want := []string{"rollback", "app", "2", "--namespace", "apps", "--wait", "--timeout", "180s"}
	if !slices.Equal(command.args, want) {
		t.Fatalf("rollback args = %#v, want %#v", command.args, want)
	}
	if slices.Contains(command.args, "--atomic") {
		t.Fatal("rollback command must not use the unsupported --atomic flag")
	}
}

func TestBuildHelmRollbackIsAcceptedByRealHelmParser(t *testing.T) {
	helmPath, err := exec.LookPath("helm")
	if err != nil {
		t.Skipf("helm is not available: %v", err)
	}
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	params := testHelmParams()
	params.Operation = "rollback"
	params.Revision = 2
	params.ValueFiles = nil
	params.SetStrings = nil
	params.ExpectedImage = ""

	command, err := buildHelmCommand(params)
	if err != nil {
		t.Fatalf("build helm command: %v", err)
	}
	args := append(append([]string(nil), command.args...), "--help")
	if output, err := exec.Command(helmPath, args...).CombinedOutput(); err != nil {
		t.Fatalf("real Helm rejected rollback command: %v\n%s", err, output)
	}
}

func TestParseHelmReleaseOutputBindsRevisionAndDescription(t *testing.T) {
	const description = "doops:session-a:123"
	revision, err := parseHelmReleaseOutput(
		[]byte(`{"version":7,"info":{"description":"doops:session-a:123","notes":"chart-output-canary"}}`),
		description,
	)
	if err != nil || revision != 7 {
		t.Fatalf("parse release output = %d, %v; want revision 7", revision, err)
	}
	for _, output := range []string{
		`{"version":0,"info":{"description":"doops:session-a:123"}}`,
		`{"version":7,"info":{"description":"doops:session-b:123"}}`,
		`not-json`,
	} {
		if _, err := parseHelmReleaseOutput([]byte(output), description); err == nil {
			t.Fatalf("invalid release output accepted: %s", output)
		}
	}
}

func TestBuildHelmCommandRejectsWorkspaceAssetContractViolations(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(api.HelmParams, string) api.HelmParams
		wantError string
	}{
		{
			name: "absolute chart path",
			mutate: func(params api.HelmParams, root string) api.HelmParams {
				params.Chart = filepath.Join(root, "release", "deploy", "helm", "prod")
				return params
			},
			wantError: "path must be relative",
		},
		{
			name: "parent traversal",
			mutate: func(params api.HelmParams, _ string) api.HelmParams {
				params.Chart = "deploy/helm/../prod"
				return params
			},
			wantError: "path traversal",
		},
		{
			name: "missing value overlay",
			mutate: func(params api.HelmParams, _ string) api.HelmParams {
				params.ValueFiles = []string{
					"deploy/helm/prod/values.yaml",
					"deploy/helm/prod/values-production.yaml",
				}
				return params
			},
			wantError: "does not exist",
		},
		{
			name: "invalid expected image",
			mutate: func(params api.HelmParams, _ string) api.HelmParams {
				params.ExpectedImage = "invalid-image"
				return params
			},
			wantError: "expected_image",
		},
		{
			name: "credential override",
			mutate: func(params api.HelmParams, _ string) api.HelmParams {
				params.SetStrings = map[string]string{"registry.password": "synthetic-secret"}
				return params
			},
			wantError: "forbidden credential material",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("DOOPS_WORKSPACE_ROOT", root)
			writeWorkspaceHelmChart(t, root)
			params := test.mutate(testWorkspaceHelmParams(), root)
			if _, err := buildHelmCommand(params); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestBuildHelmCommandRejectsWorkspaceSymlinkEscape(t *testing.T) {
	t.Run("chart", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("DOOPS_WORKSPACE_ROOT", root)
		workspace := filepath.Join(root, "release")
		outside := t.TempDir()
		if err := os.MkdirAll(filepath.Join(workspace, "deploy", "helm"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(outside, "Chart.yaml"), []byte("apiVersion: v2\nname: app\nversion: 1.0.0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(outside, "values.yaml"), []byte("image: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(workspace, "deploy", "helm", "prod")); err != nil {
			t.Fatal(err)
		}
		if _, err := buildHelmCommand(testWorkspaceHelmParams()); err == nil || !strings.Contains(err.Error(), "escapes the session workspace") {
			t.Fatalf("chart symlink escape accepted: %v", err)
		}
	})

	t.Run("values", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("DOOPS_WORKSPACE_ROOT", root)
		writeWorkspaceHelmChart(t, root)
		outside := filepath.Join(t.TempDir(), "values.yaml")
		if err := os.WriteFile(outside, []byte("image: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		values := filepath.Join(root, "release", "deploy", "helm", "prod", "values.yaml")
		if err := os.Remove(values); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, values); err != nil {
			t.Fatal(err)
		}
		if _, err := buildHelmCommand(testWorkspaceHelmParams()); err == nil || !strings.Contains(err.Error(), "escapes the session workspace") {
			t.Fatalf("values symlink escape accepted: %v", err)
		}
	})
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
		"upgrade app https://charts.example.test/app-1.0.0.tgz --install --reset-values",
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
		"image.tag":        "v2.3.1",
	}
	params.ExpectedImage = "registry.example/app:v2.3.1"

	command, err := buildHelmCommand(params)
	if err != nil {
		t.Fatalf("build tag helm command: %v", err)
	}
	if command.expectedImage != "registry.example/app:v2.3.1" {
		t.Fatalf("expected tag image = %q", command.expectedImage)
	}
	got := strings.Join(command.args, " ")
	if !strings.Contains(got, "--set-string image.tag=v2.3.1") {
		t.Fatalf("helm command missing release tag: %s", got)
	}
}

func TestHelmChartSpecificInputsRenderWithRealHelm(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	workspace := filepath.Join(root, "release")
	chart := "charts/platform"
	files := map[string]string{
		chart + "/Chart.yaml":  "apiVersion: v2\nname: platform\nversion: 1.0.0\n",
		chart + "/values.yaml": "services:\n  api:\n    image: registry.example/api:v1\n  web:\n    image: registry.example/web:v1\ncors:\n  origins: https://default.example\n",
		"config/base.yaml":     "services:\n  api:\n    image: registry.example/api@" + testHelmDigest + "\n",
		"config/prod.yaml":     "services:\n  web:\n    image: registry.example/web:git-b87ab17\n",
		chart + "/templates/deployments.yaml": `{{- range $name, $service := .Values.services }}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ $name }}
spec:
  template:
    spec:
      containers:
        - name: {{ $name }}
          image: {{ $service.image | quote }}
          env:
            - name: CORS_ORIGINS
              value: {{ $.Values.cors.origins | quote }}
{{- end }}
`,
	}
	for path, body := range files {
		fullPath := filepath.Join(workspace, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	params := testWorkspaceHelmParams()
	params.Chart = chart
	params.ValueFiles = []string{"config/base.yaml", "config/prod.yaml"}
	params.ExpectedImage = "registry.example/api@" + testHelmDigest
	params.SetStrings = map[string]string{"cors.origins": "https://hdu.example,https://unicom.example"}
	command, err := buildHelmCommand(params)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"template", command.release, command.args[2], "--namespace", command.namespace}
	for index := 3; index < len(command.args); index++ {
		if command.args[index] == "--values" || command.args[index] == "--set-string" {
			args = append(args, command.args[index], command.args[index+1])
			index++
		}
	}
	render := exec.CommandContext(t.Context(), "helm", args...)
	render.Dir = command.cwd
	manifest, err := render.CombinedOutput()
	if err != nil {
		t.Fatalf("real Helm rendering failed: %v: %s", err, manifest)
	}
	deployments, err := validateHelmRenderedImages(manifest, command.expectedImage)
	if err != nil || len(deployments) != 2 ||
		!strings.Contains(string(manifest), "registry.example/web:git-b87ab17") ||
		!strings.Contains(string(manifest), "https://hdu.example,https://unicom.example") {
		t.Fatalf("chart-specific inputs did not render faithfully: %v\n%s", err, manifest)
	}
	if _, err := validateHelmRenderedImages(manifest, "registry.example/api:v2"); err == nil {
		t.Fatal("wrong explicit image expectation accepted")
	}
	params.ExpectedImage = ""
	params.ValueFiles = nil
	params.SetStrings = nil
	if _, err := buildHelmCommand(params); err != nil {
		t.Fatalf("chart defaults require no extra business fields: %v", err)
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
			wantError: "explicit https://",
		},
		{
			name: "unencrypted chart",
			mutate: func(params *api.HelmParams) {
				params.Chart = "http://charts.example.test/app-1.0.0.tgz"
			},
			wantError: "explicit https://",
		},
		{
			name: "local values file",
			mutate: func(params *api.HelmParams) {
				params.ValueFiles = []string{"deploy/values/production.yaml"}
			},
			wantError: "explicit http(s)://",
		},
		{
			name: "invalid expected digest",
			mutate: func(params *api.HelmParams) {
				params.ExpectedImage = "registry.example/app@latest"
			},
			wantError: "invalid expected_image",
		},
		{
			name: "invalid override key",
			mutate: func(params *api.HelmParams) {
				params.SetStrings["name=argument"] = "arbitrary"
			},
			wantError: "invalid helm set_strings key",
		},
		{
			name: "credential material",
			mutate: func(params *api.HelmParams) {
				params.SetStrings["registry.password"] = "secret-value"
			},
			wantError: "forbidden credential material",
		},
		{
			name: "credential URL",
			mutate: func(params *api.HelmParams) {
				params.SetStrings = map[string]string{
					"upstream.url": "https://user:secret@example.test/api",
				}
			},
			wantError: "forbidden credential material",
		},
		{
			name: "newline override",
			mutate: func(params *api.HelmParams) {
				params.SetStrings["cors.origins"] = "https://one.example\nhttps://two.example"
			},
			wantError: "forbidden credential material",
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

func TestValidateHelmRenderedHookJob(t *testing.T) {
	manifest := []byte(`apiVersion: batch/v1
kind: Job
metadata:
  name: sync-1
  annotations:
    helm.sh/hook: pre-install,pre-upgrade
spec:
  template:
    spec:
      containers:
        - name: sync
          image: registry.example/sync:release
`)
	workloads, err := validateHelmRenderedImages(manifest, "registry.example/sync:release")
	if err != nil || len(workloads) != 1 || workloads[0].Kind != "Job" {
		t.Fatalf("hook Job was not accepted: workloads=%#v err=%v", workloads, err)
	}
	if _, err := validateHelmRenderedImages(manifest, "registry.example/sync:wrong"); err == nil {
		t.Fatal("hook Job with mismatched expected image was accepted")
	}

	nonHook := []byte(`apiVersion: batch/v1
kind: Job
metadata:
  name: ordinary
spec:
  template:
    spec:
      containers:
        - image: registry.example/sync:release
`)
	if _, err := validateHelmRenderedImages(nonHook, ""); err == nil || !errors.Is(err, errHelmNoWorkload) {
		t.Fatalf("ordinary Job was accepted as a release workload: %v", err)
	}
}

func TestHelmRemoteReferenceSchemes(t *testing.T) {
	for _, chart := range []string{"https://charts.example.test/app.tgz"} {
		if _, err := resolveRemoteHelmChartReference(chart); err != nil {
			t.Fatalf("allowed chart %s: %v", chart, err)
		}
	}
	if _, err := resolveRemoteHelmChartReference("oci://registry.example.test/charts/app"); err == nil || !strings.Contains(err.Error(), "explicit https://") {
		t.Fatalf("OCI chart was not rejected at the input boundary: %v", err)
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

func TestRunHelmCommandVerifiesFinalDeploymentState(t *testing.T) {
	root := t.TempDir()
	argsFile := filepath.Join(root, "args")
	manifestArgsFile := filepath.Join(root, "manifest-args")
	preflightArgsFile := filepath.Join(root, "preflight-args")
	stdinFile := filepath.Join(root, "stdin")
	helm := filepath.Join(root, "helm")
	kubectl := filepath.Join(root, "kubectl")
	if err := os.WriteFile(helm, []byte(`#!/bin/sh
set -eu
if [ "$1" = "get" ] && [ "$2" = "manifest" ]; then
  printf '%s\n' "$@" >"${MANIFEST_ARGS_FILE}"
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
if [ "$1" = "status" ]; then
  desc=""
  if [ -f "${DESCRIPTION_FILE}" ]; then desc="$(cat "${DESCRIPTION_FILE}")"; fi
  printf '{"version":1,"info":{"description":"%s"}}\n' "$desc"
  exit 0
fi
for arg in "$@"; do
  if [ "$arg" = "--dry-run=server" ]; then
    printf '%s\n' "$@" >"${PREFLIGHT_ARGS_FILE}"
    printf '%s\n' '{"manifest":"kind: Deployment\nmetadata:\n  name: app\nspec:\n  template:\n    spec:\n      containers:\n        - image: registry.example/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"}'
    cat >/dev/null
    exit 0
  fi
done
printf '%s\n' "$@" >"${ARGS_FILE}"
previous=""
for arg in "$@"; do
  if [ "$previous" = "--description" ]; then printf '%s' "$arg" >"${DESCRIPTION_FILE}"; fi
  previous="$arg"
done
cat >"${STDIN_FILE}"
desc="$(cat "${DESCRIPTION_FILE}")"
printf '{"version":1,"info":{"description":"%s","notes":"chart-output-canary"}}\n' "$desc"
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
	t.Setenv("MANIFEST_ARGS_FILE", manifestArgsFile)
	t.Setenv("PREFLIGHT_ARGS_FILE", preflightArgsFile)
	t.Setenv("STDIN_FILE", stdinFile)
	t.Setenv("DESCRIPTION_FILE", filepath.Join(root, "description"))
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)

	command, err := buildHelmCommand(testHelmParams())
	if err != nil {
		t.Fatalf("build helm command: %v", err)
	}
	verification, err := runHelmCommand(context.Background(), command, func(notificationEvent) {})
	if err != nil {
		t.Fatalf("run helm command: %v", err)
	}
	if !strings.Contains(verification, "final Helm workload spec verified") {
		t.Fatalf("final Deployment verification missing: %q", verification)
	}
	if strings.Contains(verification, "chart-output-canary") {
		t.Fatalf("chart output crossed the user-facing verification boundary: %q", verification)
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
		!slices.Contains(argLines, "--set-string") ||
		!slices.Contains(argLines, "--output=json") {
		t.Fatalf("remote chart or immutable image was not used: %q", argLines)
	}
	manifestArgs, err := os.ReadFile(manifestArgsFile)
	if err != nil {
		t.Fatal(err)
	}
	manifestArgLines := strings.Split(strings.TrimSpace(string(manifestArgs)), "\n")
	if !slices.Contains(manifestArgLines, "--revision") || !slices.Contains(manifestArgLines, "1") {
		t.Fatalf("manifest was not pinned to the mutation revision: %q", manifestArgLines)
	}
	stdin, err := os.ReadFile(stdinFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(stdin) != 0 || !slices.Contains(argLines, "--reset-values") || slices.Contains(argLines, "-") {
		t.Fatalf("mutation must only use explicitly supplied values, args=%q stdin=%s", argLines, stdin)
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
	if err == nil || err.Error() != "helm_preflight_failed: cannot overwrite table with non table" {
		t.Fatalf("preflight failure not returned: %v", err)
	}
	if _, err := os.Stat(mutationMarker); !os.IsNotExist(err) {
		t.Fatalf("helm mutation ran after failed preflight")
	}
}

func TestRunHelmCommandImageExpectationBlocksMutation(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "mutation")
	if err := os.WriteFile(filepath.Join(root, "helm"), []byte(`#!/bin/sh
set -eu
for arg in "$@"; do
  if [ "$arg" = "--dry-run=server" ]; then
    printf '%s\n' '{"manifest":"kind: Deployment\nmetadata:\n  name: app\nspec:\n  template:\n    spec:\n      containers:\n        - image: registry.example/app:wrong-version\n"}'
    exit 0
  fi
done
touch "${MUTATION_MARKER}"
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MUTATION_MARKER", marker)
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	command, err := buildHelmCommand(testHelmParams())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runHelmCommand(t.Context(), command, nil); err == nil || !strings.HasPrefix(err.Error(), "helm_image_mismatch: ") {
		t.Fatalf("wrong rendered image did not fail preflight: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("mutation executed after image mismatch")
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

// --atomic is the caller's choice: by default a failed upgrade stays in place
// for the operator; atomic=true asks Helm to roll it back automatically.
func TestBuildHelmUpgradeInstallAtomicIsOptIn(t *testing.T) {
	for _, tc := range []struct {
		name       string
		atomic     bool
		wantAtomic bool
	}{
		{"default keeps the failed revision", false, false},
		{"explicit atomic rolls back on failure", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("DOOPS_WORKSPACE_ROOT", root)
			params := testHelmParams()
			params.Atomic = tc.atomic
			command, err := buildHelmCommand(params)
			if err != nil {
				t.Fatalf("build helm command: %v", err)
			}
			if got := slices.Contains(command.args, "--atomic"); got != tc.wantAtomic {
				t.Fatalf("--atomic present = %v, want %v: %q", got, tc.wantAtomic, command.args)
			}
		})
	}
}
