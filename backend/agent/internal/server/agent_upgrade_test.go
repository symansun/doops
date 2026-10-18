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

func TestHandleAgentUpgradeDryRunAllowsTaggedImage(t *testing.T) {
	out, err := handleAgentUpgrade(json.RawMessage(`{
		"mode":"k8s",
		"image":"repo.example/doops-agent:v1.3.40",
		"dry_run":true
	}`))
	if err != nil {
		t.Fatalf("tagged image dry-run: %v", err)
	}
	if !strings.Contains(out, "image=repo.example/doops-agent:v1.3.40") {
		t.Fatalf("tagged image was not preserved: %s", out)
	}
}

func TestHandleAgentUpgradeDryRunRejectsInvalidImage(t *testing.T) {
	_, err := handleAgentUpgrade(json.RawMessage(`{
		"mode":"k8s",
		"image":"not-an-image",
		"dry_run":true
	}`))
	if err == nil || !strings.Contains(err.Error(), "repository:tag") {
		t.Fatalf("expected invalid image rejection, got %v", err)
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

func TestHandleAgentUpgradeRejectsInvalidApplyImage(t *testing.T) {
	_, err := handleAgentUpgrade(json.RawMessage(`{
		"mode":"k8s",
		"image":"not-an-image",
		"namespace":"ai",
		"workload":"deployment/doops-agent",
		"container":"doops-agent"
	}`))
	if err == nil || !strings.Contains(err.Error(), "repository:tag") {
		t.Fatalf("expected invalid image rejection, got %v", err)
	}
}

// The shipped Agent image takes kubectl from the base image, which copies it
// from a digest-pinned upstream image and runs it once at build time.
func TestAgentBaseImagePinsAndVerifiesKubectl(t *testing.T) {
	dockerfile, err := os.ReadFile(filepath.Join("..", "..", "..", "Dockerfile.base.light"))
	if err != nil {
		t.Fatalf("read agent base Dockerfile: %v", err)
	}

	out := string(dockerfile)
	for _, requirement := range []string{
		"ARG KUBECTL_IMAGE=registry.k8s.io/kubectl:",
		"@sha256:",
		"FROM ${KUBECTL_IMAGE} AS kubectl",
		"COPY --from=kubectl /bin/kubectl /usr/local/bin/kubectl",
		"kubectl version --client=true",
	} {
		if !strings.Contains(out, requirement) {
			t.Fatalf("agent base Dockerfile must pin and verify kubectl; missing %q", requirement)
		}
	}
}
