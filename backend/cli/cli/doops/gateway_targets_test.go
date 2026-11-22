package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestGatewayTargetsURLUsesVersionedEndpoint(t *testing.T) {
	got, err := gatewayTargetsURL("https://gateway.example.com")
	if err != nil {
		t.Fatalf("build targets URL: %v", err)
	}
	want := "https://gateway.example.com/v1/targets"
	if got != want {
		t.Fatalf("targets URL mismatch\nwant: %s\n got: %s", want, got)
	}
}

func TestGatewayTargetsRejectsInsecureHTTPGatewayByDefault(t *testing.T) {
	t.Setenv("DOOPS_ALLOW_INSECURE_GATEWAY", "")
	if _, err := gatewayTargetsURL("http://203.0.113.10:42222"); err == nil {
		t.Fatal("expected insecure non-loopback gateway targets URL to be rejected by default")
	}
}

func TestGatewayTargetsAllowsInsecureHTTPGatewayWithOptIn(t *testing.T) {
	t.Setenv("DOOPS_ALLOW_INSECURE_GATEWAY", "1")
	got, err := gatewayTargetsURL("http://203.0.113.10:42222")
	if err != nil {
		t.Fatalf("opt-in should allow insecure gateway targets URL: %v", err)
	}
	want := "http://203.0.113.10:42222/v1/targets"
	if got != want {
		t.Fatalf("targets URL mismatch\nwant: %s\n got: %s", want, got)
	}
}

func TestGatewayTargetsAllowsInsecureLocalhostForDevelopment(t *testing.T) {
	got, err := gatewayTargetsURL("http://localhost:42222")
	if err != nil {
		t.Fatalf("local insecure gateway should be allowed: %v", err)
	}
	want := "http://localhost:42222/v1/targets"
	if got != want {
		t.Fatalf("targets URL mismatch\nwant: %s\n got: %s", want, got)
	}
}


func TestFilterGatewayTargetsMatchesConfiguredRoute(t *testing.T) {
	targets := []GatewayTarget{
		{Cluster: "prod", Instance: "node-1"},
		{Cluster: "prod", Instance: "node-2"},
		{Cluster: "dev", Instance: "node-1"},
	}

	got := filterGatewayTargets(targets, " prod ", " node-2 ")
	if len(got) != 1 {
		t.Fatalf("expected one exact target, got %#v", got)
	}
	if got[0].Cluster != "prod" || got[0].Instance != "node-2" {
		t.Fatalf("unexpected target: %#v", got[0])
	}
}

func TestFilterGatewayTargetsDoesNotReturnOtherOnlineTargets(t *testing.T) {
	targets := []GatewayTarget{
		{Cluster: "prod", Instance: "node-1"},
		{Cluster: "dev", Instance: "node-2"},
	}

	if got := filterGatewayTargets(targets, "legacy", "offline"); len(got) != 0 {
		t.Fatalf("expected no route match, got %#v", got)
	}
}

func TestGatewayTargetUnavailableMessageDoesNotClaimOffline(t *testing.T) {
	message := gatewayTargetUnavailableMessage("doops-114-k8s", "master-1")
	if strings.Contains(message, "target offline") {
		t.Fatalf("message must not claim offline: %s", message)
	}
	for _, want := range []string{"target unavailable", "doops-114-k8s/master-1", "explicit target grant"} {
		if !strings.Contains(message, want) {
			t.Fatalf("message missing %q: %s", want, message)
		}
	}
}

func TestPrintTargetsMarksUnenrolledTargets(t *testing.T) {
	var out strings.Builder
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	printTargets(&out, []GatewayTarget{
		{Cluster: "dev", Instance: "local", Key: "dev/local", Status: "idle", Enrolled: true, LastSeen: now},
		{Cluster: "dev", Instance: "other", Key: "dev/other", Status: "idle", Enrolled: false, LastSeen: now},
	})
	rendered := out.String()
	if !strings.Contains(rendered, "ENROLLED") {
		t.Fatalf("target table has no enrollment column:\n%s", rendered)
	}
	if !strings.Contains(rendered, "  NOT ENROLLED: dev/other connected without a target enrollment token") {
		t.Fatalf("unenrolled target has no marker:\n%s", rendered)
	}
	if strings.Contains(rendered, "NOT ENROLLED: dev/local") {
		t.Fatalf("enrolled target was marked unenrolled:\n%s", rendered)
	}
}

func TestGatewayTargetEnrollmentDecodesAsBooleanOnly(t *testing.T) {
	var target GatewayTarget
	if err := json.Unmarshal([]byte(`{"cluster":"dev","instance":"local","enrolled":false}`), &target); err != nil {
		t.Fatal(err)
	}
	if target.Enrolled || target.enrollmentLabel() != "NO (not enrolled)" {
		t.Fatalf("enrolled=%v label=%q", target.Enrolled, target.enrollmentLabel())
	}
	if err := json.Unmarshal([]byte(`{"cluster":"dev","instance":"local","enrolled":"dga_aen_x_canary"}`), &target); err == nil {
		t.Fatal("a non-boolean enrollment value must not decode")
	}
}
