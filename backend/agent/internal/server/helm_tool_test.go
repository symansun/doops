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

func TestBuildHelmUpgradeInstallUsesBoundWorkspaceAssets(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "release")
	chart := filepath.Join(workspace, "deploy", "helm", "app")
	values := filepath.Join(workspace, "deploy", "values", "production.yaml")
	if err := os.MkdirAll(chart, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(values), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chart, "Chart.yaml"), []byte("apiVersion: v2\nname: app\nversion: 1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(values, []byte("replicas: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)

	command, err := buildHelmCommand(api.HelmParams{
		SessionID:  "release",
		Operation:  "upgrade-install",
		Release:    "app",
		Chart:      "deploy/helm/app",
		Namespace:  "apps",
		ValueFiles: []string{"deploy/values/production.yaml"},
		SetStrings: map[string]string{
			"image.digest":     "sha256:" + strings.Repeat("a", 64),
			"image.repository": "docker.cnb.cool/team/app",
		},
		Timeout: "180s",
		Confirm: true,
	})
	if err != nil {
		t.Fatalf("build helm command: %v", err)
	}
	expectedWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if command.cwd != expectedWorkspace {
		t.Fatalf("helm cwd = %q, want %q", command.cwd, expectedWorkspace)
	}
	got := strings.Join(command.args, " ")
	for _, expected := range []string{
		"upgrade app deploy/helm/app --install --atomic",
		"--values deploy/values/production.yaml",
		"--set-string image.digest=sha256:" + strings.Repeat("a", 64),
		"--set-string image.repository=docker.cnb.cool/team/app",
		"--namespace apps --wait --timeout 180s",
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("helm command missing %q: %s", expected, got)
		}
	}
}

func TestBuildHelmCommandRejectsLegacyValuesField(t *testing.T) {
	var params api.HelmParams
	decoder := json.NewDecoder(strings.NewReader(`{
		"session_id":"release",
		"operation":"upgrade-install",
		"release":"app",
		"chart":"deploy/helm/app",
		"namespace":"apps",
		"values":["image.repository=registry.example/app"],
		"confirm":true
	}`))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&params); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("legacy values field accepted: %v", err)
	}
}

func TestBuildHelmCommandRejectsUnsafeInputs(t *testing.T) {
	root := t.TempDir()
	chart := filepath.Join(root, "release", "deploy", "helm", "app")
	if err := os.MkdirAll(chart, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)

	base := api.HelmParams{
		SessionID: "release",
		Operation: "upgrade-install",
		Release:   "app",
		Chart:     "../chart",
		Namespace: "apps",
		SetStrings: map[string]string{
			"image.digest":     "sha256:" + strings.Repeat("a", 64),
			"image.repository": "registry.example/app",
		},
		Confirm: true,
	}
	if _, err := buildHelmCommand(base); err == nil || !strings.Contains(err.Error(), "deploy/helm") {
		t.Fatalf("workspace escape accepted: %v", err)
	}

	base.Chart = "deploy/helm/app"
	base.SetStrings["registry.password"] = "secret-value"
	if _, err := buildHelmCommand(base); err == nil ||
		!strings.Contains(err.Error(), "forbidden credential material") {
		t.Fatalf("credential material accepted: %v", err)
	}

	base.SetStrings = nil
	base.Confirm = false
	if _, err := buildHelmCommand(base); err == nil || !strings.Contains(err.Error(), "confirm=true") {
		t.Fatalf("missing confirmation accepted: %v", err)
	}
}

func TestBuildHelmCommandRequiresImmutableImageOverrides(t *testing.T) {
	root := t.TempDir()
	chart := filepath.Join(root, "release", "deploy", "helm", "app")
	if err := os.MkdirAll(chart, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)

	base := api.HelmParams{
		SessionID: "release",
		Operation: "upgrade-install",
		Release:   "app",
		Chart:     "deploy/helm/app",
		Namespace: "apps",
		Confirm:   true,
	}
	if _, err := buildHelmCommand(base); err == nil ||
		!strings.Contains(err.Error(), "image.repository and image.digest") {
		t.Fatalf("missing image overrides accepted: %v", err)
	}

	base.SetStrings = map[string]string{
		"image.repository": "registry.example/app@sha256:" + strings.Repeat("a", 64),
		"image.digest":     "sha256:" + strings.Repeat("a", 64),
	}
	if _, err := buildHelmCommand(base); err == nil ||
		!strings.Contains(err.Error(), "image.repository") {
		t.Fatalf("digest-bearing repository accepted: %v", err)
	}

	base.SetStrings["image.repository"] = "registry.example/app"
	base.SetStrings["image.digest"] = "latest"
	if _, err := buildHelmCommand(base); err == nil ||
		!strings.Contains(err.Error(), "image.digest") {
		t.Fatalf("mutable image digest accepted: %v", err)
	}

	for _, invalidRepository := range []string{
		"/app",
		"app/",
		"https://registry.example/app",
		"registry.example/app/",
		"registry.example:bad/app",
		"registry.example/app//api",
	} {
		base.SetStrings["image.repository"] = invalidRepository
		base.SetStrings["image.digest"] = "sha256:" + strings.Repeat("a", 64)
		if _, err := buildHelmCommand(base); err == nil ||
			!strings.Contains(err.Error(), "image.repository") {
			t.Fatalf("invalid repository %q accepted: %v", invalidRepository, err)
		}
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

func TestRunHelmCommandStreamsExistingValuesBeforeExplicitFiles(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "release")
	chart := filepath.Join(workspace, "deploy", "helm", "app")
	explicitValues := filepath.Join(workspace, "deploy", "values", "production.yaml")
	if err := os.MkdirAll(chart, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(explicitValues), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(explicitValues, []byte("replicas: 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(root, "args")
	stdinFile := filepath.Join(root, "stdin")
	helm := filepath.Join(root, "helm")
	if err := os.WriteFile(helm, []byte(`#!/bin/sh
set -eu
if [ "$1" = "status" ]; then
  exit 0
fi
if [ "$1" = "get" ] && [ "$2" = "values" ]; then
  echo 'warning: kubeconfig permissions are too open' >&2
  printf '%s\n' '{"large":9007199254740993,"gateway":{"cluster":"prod"},"image":{"tag":"legacy","pullPolicy":"Always"}}'
  exit 0
fi
printf '%s\n' "$@" >"${ARGS_FILE}"
cat >"${STDIN_FILE}"
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ARGS_FILE", argsFile)
	t.Setenv("STDIN_FILE", stdinFile)
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)

	command, err := buildHelmCommand(api.HelmParams{
		SessionID:  "release",
		Operation:  "upgrade-install",
		Release:    "app",
		Chart:      "deploy/helm/app",
		Namespace:  "apps",
		ValueFiles: []string{"deploy/values/production.yaml"},
		SetStrings: map[string]string{
			"image.repository": "registry.example/app",
			"image.digest":     "sha256:" + strings.Repeat("a", 64),
		},
		Confirm: true,
	})
	if err != nil {
		t.Fatalf("build helm command: %v", err)
	}
	if err := runHelmCommand(context.Background(), command, func(notificationEvent) {}); err != nil {
		t.Fatalf("run helm command: %v", err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	argLines := strings.Split(strings.TrimSpace(string(args)), "\n")
	stdinIndex := slices.Index(argLines, "-")
	explicitIndex := slices.Index(argLines, "deploy/values/production.yaml")
	if stdinIndex < 0 || explicitIndex < 0 || stdinIndex >= explicitIndex {
		t.Fatalf("existing values do not precede explicit values: %q", argLines)
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

func TestBuildHelmCommandRejectsSymlinkOutsideRequiredAssetPrefix(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "release")
	outsideChart := filepath.Join(workspace, "other-chart")
	if err := os.MkdirAll(outsideChart, 0o755); err != nil {
		t.Fatal(err)
	}
	linkParent := filepath.Join(workspace, "deploy", "helm")
	if err := os.MkdirAll(linkParent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../other-chart", filepath.Join(linkParent, "app")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)

	_, err := buildHelmCommand(api.HelmParams{
		SessionID: "release",
		Operation: "upgrade-install",
		Release:   "app",
		Chart:     "deploy/helm/app",
		Namespace: "apps",
		Confirm:   true,
	})
	if err == nil || !strings.Contains(err.Error(), "must remain under deploy/helm/") {
		t.Fatalf("symlinked chart outside prefix accepted: %v", err)
	}
}
