package server

import (
	"strings"
	"testing"

	"github.com/user/doops/agent/api"
)

func TestBuildK8SCommandsBuildsReadOnlyGet(t *testing.T) {
	commands, err := buildK8SCommands(api.K8SParams{
		SessionID: "read-only",
		Operation: "get",
		Kind:      "pods",
		Namespace: "kz-ops",
		Output:    "wide",
	})
	if err != nil {
		t.Fatalf("build get: %v", err)
	}
	if len(commands) != 1 || strings.Join(commands[0].args, " ") != "get pods --namespace kz-ops --output wide" {
		t.Fatalf("unexpected command: %#v", commands)
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
	commands, err := buildK8SCommands(api.K8SParams{
		SessionID: "set-image",
		Operation: "set-image",
		Resource:  "deployment/doops-release-runner",
		Namespace: "kz-ops",
		Container: "doops-agent",
		Image:     image,
		Timeout:   "180s",
		Confirm:   true,
	})
	if err != nil {
		t.Fatalf("build set-image: %v", err)
	}
	if len(commands) != 2 {
		t.Fatalf("expected set-image and rollout status, got %#v", commands)
	}
	if got := strings.Join(commands[0].args, " "); !strings.Contains(got, "doops-agent="+image) {
		t.Fatalf("immutable image missing from command: %s", got)
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
