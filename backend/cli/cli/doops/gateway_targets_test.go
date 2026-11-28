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

func TestPrintTargetsAppendsVersionDriftSummaryWhenMultipleTargetsDiverge(t *testing.T) {
	var out strings.Builder
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	const (
		commitA = "74fe24910123456789abcdef0123456789abcdef"
		commitB = "574d168a0123456789abcdef0123456789abcdef"
		commitC = "dc8899b20123456789abcdef0123456789abcdef"
	)
	targets := []GatewayTarget{
		{Cluster: "prod", Instance: "n1", Key: "prod/n1", Status: "idle", Enrolled: true, ReleaseCommit: commitA, LastSeen: now},
		{Cluster: "prod", Instance: "n2", Key: "prod/n2", Status: "idle", Enrolled: true, ReleaseCommit: commitA, LastSeen: now},
		{Cluster: "prod", Instance: "n3", Key: "prod/n3", Status: "idle", Enrolled: true, ReleaseCommit: commitA, LastSeen: now},
		{Cluster: "prod", Instance: "n4", Key: "prod/n4", Status: "idle", Enrolled: true, ReleaseCommit: commitA, LastSeen: now},
		{Cluster: "prod", Instance: "n5", Key: "prod/n5", Status: "idle", Enrolled: true, ReleaseCommit: commitA, LastSeen: now},
		{Cluster: "prod", Instance: "n6", Key: "prod/n6", Status: "idle", Enrolled: true, ReleaseCommit: commitA, LastSeen: now},
		{Cluster: "prod", Instance: "n7", Key: "prod/n7", Status: "idle", Enrolled: true, ReleaseCommit: commitB, LastSeen: now},
		{Cluster: "prod", Instance: "n8", Key: "prod/n8", Status: "idle", Enrolled: true, ReleaseCommit: commitB, LastSeen: now},
		{Cluster: "prod", Instance: "n9", Key: "prod/n9", Status: "idle", Enrolled: true, ReleaseCommit: commitC, LastSeen: now},
		{Cluster: "prod", Instance: "n10", Key: "prod/n10", Status: "idle", Enrolled: true, ReleaseCommit: "", LastSeen: now},
	}

	printTargets(&out, targets)
	rendered := out.String()
	want := "VERSION DRIFT: 10 online targets span 4 release commits (74fe2491: 6, 574d168a: 2, dc8899b2: 1, <unknown>: 1). Align via doops upgrade / release-host-agent.sh."
	if !strings.Contains(rendered, want) {
		t.Fatalf("missing expected version drift line\nwant: %s\ngot:\n%s", want, rendered)
	}
}

func TestPrintTargetsOmitsVersionDriftForSingleTargetOrConsistentFleet(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	const commitA = "74fe24910123456789abcdef0123456789abcdef"

	t.Run("single target keeps deploy-gateway.sh exact 3-line contract", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			target GatewayTarget
		}{
			{
				name:   "enrolled with commit",
				target: GatewayTarget{Cluster: "prod", Instance: "n1", Key: "prod/n1", Status: "idle", Enrolled: true, ReleaseCommit: commitA, LastSeen: now},
			},
			{
				name:   "enrolled unknown commit",
				target: GatewayTarget{Cluster: "prod", Instance: "n1", Key: "prod/n1", Status: "idle", Enrolled: true, ReleaseCommit: "", LastSeen: now},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var out strings.Builder
				printTargets(&out, []GatewayTarget{tc.target})
				rendered := out.String()
				if strings.Contains(rendered, "VERSION DRIFT:") {
					t.Fatalf("single target output must never emit VERSION DRIFT:\n%s", rendered)
				}
				lines := strings.Split(strings.TrimSuffix(rendered, "\n"), "\n")
				if len(lines) != 3 {
					t.Fatalf("single enrolled target must emit exact 3 lines (header, divider, row), got %d:\n%s", len(lines), rendered)
				}
			})
		}

		// Also verify single unenrolled target emits exact 4 lines (header, divider, row, NOT ENROLLED) with no drift line.
		var out strings.Builder
		printTargets(&out, []GatewayTarget{
			{Cluster: "prod", Instance: "n1", Key: "prod/n1", Status: "idle", Enrolled: false, ReleaseCommit: "", LastSeen: now},
		})
		rendered := out.String()
		if strings.Contains(rendered, "VERSION DRIFT:") {
			t.Fatalf("single unenrolled target must never emit VERSION DRIFT:\n%s", rendered)
		}
		lines := strings.Split(strings.TrimSuffix(rendered, "\n"), "\n")
		if len(lines) != 4 || !strings.HasPrefix(lines[3], "  NOT ENROLLED: prod/n1 ") {
			t.Fatalf("single unenrolled target output mismatch (%d lines):\n%s", len(lines), rendered)
		}
	})

	t.Run("multiple targets on same commit emit no drift line", func(t *testing.T) {
		var out strings.Builder
		printTargets(&out, []GatewayTarget{
			{Cluster: "prod", Instance: "n1", Key: "prod/n1", Status: "idle", Enrolled: true, ReleaseCommit: commitA, LastSeen: now},
			{Cluster: "prod", Instance: "n2", Key: "prod/n2", Status: "idle", Enrolled: true, ReleaseCommit: commitA, LastSeen: now},
		})
		if strings.Contains(out.String(), "VERSION DRIFT:") {
			t.Fatalf("consistent fleet must not emit VERSION DRIFT:\n%s", out.String())
		}
	})

	t.Run("empty target list emits no drift line", func(t *testing.T) {
		var out strings.Builder
		printTargets(&out, nil)
		if strings.Contains(out.String(), "VERSION DRIFT:") {
			t.Fatalf("empty targets must not emit VERSION DRIFT:\n%s", out.String())
		}
	})
}
