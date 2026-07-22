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

func TestBuildK8SCommandsBuildsNamedReadOnlyGet(t *testing.T) {
	commands, err := buildK8SCommands(api.K8SParams{
		SessionID: "read-one",
		Operation: "get",
		Kind:      "deployment",
		Name:      "doops-agent",
		Namespace: "kz-ops",
		Output:    "yaml",
	})
	if err != nil {
		t.Fatalf("build named get: %v", err)
	}
	if len(commands) != 1 ||
		strings.Join(commands[0].args, " ") != "get deployment --namespace kz-ops doops-agent --output yaml" {
		t.Fatalf("unexpected command: %#v", commands)
	}
}

func TestBuildK8SCommandsRejectsCredentialBearingKinds(t *testing.T) {
	for _, params := range []api.K8SParams{
		{SessionID: "read-only", Operation: "get", Kind: "secrets", Output: "yaml"},
		{SessionID: "read-only", Operation: "get", Kind: "serviceaccounts", Output: "json"},
		{SessionID: "read-only", Operation: "describe", Kind: "secret", Name: "registry-auth"},
	} {
		if _, err := buildK8SCommands(params); err == nil ||
			!strings.Contains(err.Error(), "not available through the read tool") {
			t.Fatalf("credential-bearing read accepted for %#v: %v", params, err)
		}
	}
	for _, params := range []api.K8SParams{
		{SessionID: "read-only", Operation: "logs", Resource: "secret/registry-auth"},
		{SessionID: "read-only", Operation: "events", Resource: "serviceaccount/builder"},
		{SessionID: "read-only", Operation: "rollout-status", Resource: "secret/registry-auth"},
	} {
		if _, err := buildK8SCommands(params); err == nil {
			t.Fatalf("credential-bearing resource accepted for %#v", params)
		}
	}
}

func TestBuildK8SCommandsRejectsMutationWithoutConfirm(t *testing.T) {
	_, err := buildK8SCommands(api.K8SParams{
		SessionID: "mutation",
		Operation: "rollout-restart",
		Resource:  "deployment/example",
		Namespace: "kz-ops",
	})
	if err == nil || !strings.Contains(err.Error(), "confirm=true") {
		t.Fatalf("expected confirmation error, got %v", err)
	}
}

func TestBuildK8SCommandsRejectsUnsupportedMutationResourceKinds(t *testing.T) {
	replicas := 2
	for _, params := range []api.K8SParams{
		{SessionID: "mutation", Operation: "rollout-restart", Resource: "service/api", Confirm: true},
		{SessionID: "mutation", Operation: "scale", Resource: "secret/api", Replicas: &replicas, Confirm: true},
	} {
		if _, err := buildK8SCommands(params); err == nil {
			t.Fatalf("unsupported mutation resource accepted for %#v", params)
		}
	}
}

func TestBuildK8SGetIgnoresRedundantResourceField(t *testing.T) {
	commands, err := buildK8SCommands(api.K8SParams{
		SessionID: "read-session",
		Operation: "get",
		Kind:      "deployment",
		Name:      "app",
		Resource:  "deployment",
		Namespace: "apps",
	})
	if err != nil {
		t.Fatalf("redundant resource rejected for get: %v", err)
	}
	if got := strings.Join(commands[0].args, " "); !strings.Contains(got, "get deployment") {
		t.Fatalf("unexpected get command: %s", got)
	}
}

func TestBuildK8SCommandsRejectsRemovedSetImageOperation(t *testing.T) {
	_, err := buildK8SCommands(api.K8SParams{
		SessionID: "set-image",
		Operation: "set-image",
		Resource:  "deployment/example",
		Confirm:   true,
	})
	if err == nil || !strings.Contains(err.Error(), `unsupported k8s operation "set-image"`) {
		t.Fatalf("removed set-image operation accepted: %v", err)
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
