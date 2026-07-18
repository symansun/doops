package server

import (
	"os"
	"path/filepath"
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
		SessionID: "release",
		Operation: "upgrade-install",
		Release:   "app",
		Chart:     "deploy/helm/app",
		Namespace: "apps",
		Values:    []string{"deploy/values/production.yaml"},
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
		Confirm:   true,
	}
	if _, err := buildHelmCommand(base); err == nil || !strings.Contains(err.Error(), "deploy/helm") {
		t.Fatalf("workspace escape accepted: %v", err)
	}

	base.Chart = "deploy/helm/app"
	base.SetStrings = map[string]string{"registry.password": "secret-value"}
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
