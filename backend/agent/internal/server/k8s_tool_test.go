package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/doops/agent/api"
)

func TestRunK8SCommandReturnsKubectlOutput(t *testing.T) {
	binDir := t.TempDir()
	kubectlPath := filepath.Join(binDir, "kubectl")
	if err := os.WriteFile(kubectlPath, []byte("#!/bin/sh\nprintf 'deployment.apps/api\\n'\n"), 0o755); err != nil {
		t.Fatalf("write fake kubectl: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := runK8SCommand(context.Background(), []string{"get", "deployment", "api"}, func(notificationEvent) {})
	if err != nil {
		t.Fatalf("run kubectl: %v", err)
	}
	if output != "deployment.apps/api\n" {
		t.Fatalf("unexpected kubectl output: %q", output)
	}
}

func TestBuildK8SCommandsBuildsReadOnlyGet(t *testing.T) {
	commands, err := buildK8SCommands(api.K8SParams{
		SessionID: "read-only",
		Operation: "get",
		Kind:      "deployment",
		Name:      "api",
		Namespace: "kz-ops",
		Output:    "yaml",
	})
	if err != nil {
		t.Fatalf("build get: %v", err)
	}
	if len(commands) != 1 || strings.Join(commands[0].args, " ") != "get deployment --namespace kz-ops api --output yaml" {
		t.Fatalf("unexpected command: %#v", commands)
	}
}

func TestBuildK8SCommandsAddsImagePullSecret(t *testing.T) {
	commands, err := buildK8SCommands(api.K8SParams{
		SessionID:  "pull-secret",
		Operation:  "add-image-pull-secret",
		Resource:   "deployment/flow",
		Namespace:  "kz-ops",
		PullSecret: "cnb-regcred",
		Confirm:    true,
	})
	if err != nil {
		t.Fatalf("build add-image-pull-secret: %v", err)
	}
	if len(commands) != 1 {
		t.Fatalf("expected one patch command, got %#v", commands)
	}
	got := strings.Join(commands[0].args, " ")
	if !strings.Contains(got, "patch deployment/flow --type strategic") ||
		!strings.Contains(got, `"imagePullSecrets":[{"name":"cnb-regcred"}]`) {
		t.Fatalf("image pull secret patch missing from command: %s", got)
	}
}

func TestBuildK8SCommandsRejectsMutationWithoutConfirm(t *testing.T) {
	_, err := buildK8SCommands(api.K8SParams{
		SessionID: "mutation",
		Operation: "rollout-restart",
		Resource:  "deployment/doops-release-runner",
		Namespace: "kz-ops",
	})
	if err == nil || !strings.Contains(err.Error(), "confirm=true") {
		t.Fatalf("expected confirmation error, got %v", err)
	}
}

func TestBuildK8SCommandsRequiresImmutableImageAndRollout(t *testing.T) {
	image := "docker.cnb.cool/l8ai/ai/doops.sh@sha256:fe2905a926e27283fdb5a5147b60b80725441d2b88a1e0cccf76616aac88df0a"
	revision := "64e542ad01e3bfd8938463b685dc0583731f400e"
	commands, err := buildK8SCommands(api.K8SParams{
		SessionID: "set-image",
		Operation: "set-image",
		Resource:  "deployment/doops-release-runner",
		Namespace: "kz-ops",
		Container: "doops-agent",
		Image:     image,
		Revision:  revision,
		Timeout:   "180s",
		Confirm:   true,
	})
	if err != nil {
		t.Fatalf("build set-image: %v", err)
	}
	if len(commands) != 2 {
		t.Fatalf("expected set-image and rollout status, got %#v", commands)
	}
	if got := strings.Join(commands[0].args, " "); !strings.Contains(got, "patch deployment/doops-release-runner --type strategic") ||
		!strings.Contains(got, `"name":"doops-agent"`) ||
		!strings.Contains(got, `"image":"`+image+`"`) ||
		!strings.Contains(got, `"doops.sh/revision":"`+revision+`"`) {
		t.Fatalf("safe strategic image patch missing from command: %s", got)
	}
	if got := strings.Join(commands[1].args, " "); got != "rollout status deployment/doops-release-runner --namespace kz-ops --timeout 180s" {
		t.Fatalf("unexpected rollout command: %s", got)
	}

	_, err = buildK8SCommands(api.K8SParams{
		SessionID: "set-image",
		Operation: "set-image",
		Resource:  "deployment/doops-release-runner",
		Container: "doops-agent",
		Image:     "docker.cnb.cool/l8ai/ai/doops.sh:latest",
		Confirm:   true,
	})
	if err == nil || !strings.Contains(err.Error(), "immutable sha256") {
		t.Fatalf("expected mutable image rejection, got %v", err)
	}

	_, err = buildK8SCommands(api.K8SParams{
		SessionID: "set-image",
		Operation: "set-image",
		Resource:  "deployment/doops-release-runner",
		Container: "doops-agent",
		Image:     image,
		Revision:  "64e542a",
		Confirm:   true,
	})
	if err == nil || !strings.Contains(err.Error(), "full 40-character commit sha") {
		t.Fatalf("expected short revision rejection, got %v", err)
	}
}

func TestBuildK8SCommandsRejectsArgumentInjection(t *testing.T) {
	_, err := buildK8SCommands(api.K8SParams{
		SessionID: "injection",
		Operation: "logs",
		Resource:  "deployment/app;touch-pwned",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid kubernetes resource") {
		t.Fatalf("expected resource rejection, got %v", err)
	}
}

func TestBuildK8SCommandsRequiresReplicasAndSession(t *testing.T) {
	_, err := buildK8SCommands(api.K8SParams{
		SessionID: "scale",
		Operation: "scale",
		Resource:  "deployment/api",
		Confirm:   true,
	})
	if err == nil || !strings.Contains(err.Error(), "replicas is required") {
		t.Fatalf("expected replicas error, got %v", err)
	}

	_, err = buildK8SCommands(api.K8SParams{
		Operation: "get",
		Kind:      "pods",
	})
	if err == nil {
		t.Fatal("expected session validation error")
	}
}
