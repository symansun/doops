package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleAgentUpgradeRequiresImage(t *testing.T) {
	if _, err := handleAgentUpgrade(json.RawMessage(`{"mode":"k8s"}`)); err == nil {
		t.Fatal("expected image to be required")
	}
}

func TestHandleAgentUpgradeDryRunK8s(t *testing.T) {
	out, err := handleAgentUpgrade(json.RawMessage(`{
		"mode":"k8s",
		"image":"repo.example/doops-agent@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"namespace":"ai",
		"workload":"deployment/doops-agent",
		"container":"doops-agent",
		"dry_run":true
	}`))
	if err != nil {
		t.Fatalf("dry-run k8s upgrade: %v", err)
	}
	if strings.Contains(out, "kubectl") || strings.Contains(out, "set image") {
		t.Fatalf("dry-run must not emit an executable mutation command: %s", out)
	}
	if !strings.Contains(out, "image=repo.example/doops-agent@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") ||
		!strings.Contains(out, "namespace=ai") ||
		!strings.Contains(out, "workload=deployment/doops-agent") {
		t.Fatalf("unexpected dry-run output: %s", out)
	}
	if !strings.Contains(out, "in-cluster Job") || !strings.Contains(out, "roll back") {
		t.Fatalf("dry-run should describe the in-cluster Job: %s", out)
	}
}

func TestHandleAgentUpgradeDryRunRejectsUnpinnedImage(t *testing.T) {
	_, err := handleAgentUpgrade(json.RawMessage(`{
		"mode":"k8s",
		"image":"repo.example/doops-agent:latest",
		"dry_run":true
	}`))
	if err == nil || !strings.Contains(err.Error(), "repository@sha256") {
		t.Fatalf("expected dry-run digest pin rejection, got %v", err)
	}
}

func TestHandleAgentUpgradeDryRunRejectsLegacyDaemonSet(t *testing.T) {
	_, err := handleAgentUpgrade(json.RawMessage(`{
		"mode":"daemonset",
		"image":"repo.example/doops-agent@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"dry_run":true
	}`))
	if err == nil ||
		!strings.Contains(err.Error(), "explicit Helm migration") ||
		!strings.Contains(err.Error(), "not automatic adoption") {
		t.Fatalf("expected legacy Agent guidance, got %v", err)
	}
}

func TestHandleAgentUpgradeRejectsUnpinnedApply(t *testing.T) {
	_, err := handleAgentUpgrade(json.RawMessage(`{
		"mode":"k8s",
		"image":"repo.example/doops-agent:latest",
		"namespace":"ai",
		"workload":"deployment/doops-agent",
		"container":"doops-agent"
	}`))
	if err == nil || !strings.Contains(err.Error(), "repository@sha256") {
		t.Fatalf("expected digest pin rejection, got %v", err)
	}
}

func TestAgentDockerfileInstallsAndVerifiesKubectl(t *testing.T) {
	dockerfile, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Fatalf("read agent Dockerfile: %v", err)
	}

	out := string(dockerfile)
	for _, requirement := range []string{
		"dl.k8s.io/release/",
		"sha256sum",
		"kubectl version --client >/dev/null",
	} {
		if !strings.Contains(out, requirement) {
			t.Fatalf("agent Dockerfile must install and verify kubectl; missing %q", requirement)
		}
	}
}
