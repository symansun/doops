package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFilterUpgradeTargets(t *testing.T) {
	targets := []GatewayTarget{
		{Cluster: "doops-jm", Instance: "jm-228"},
		{Cluster: "doops-89", Instance: "master-node"},
		{Cluster: "doops-89", Instance: "worker"},
	}
	got := filterUpgradeTargets(targets, "doops-89", "*")
	if len(got) != 2 {
		t.Fatalf("expected two doops-89 targets, got %#v", got)
	}
	got = filterUpgradeTargets(targets, "*", "jm-228")
	if len(got) != 1 || got[0].Cluster != "doops-jm" {
		t.Fatalf("expected jm target, got %#v", got)
	}
	got = filterUpgradeTargets(targets, "missing", "*")
	if len(got) != 0 {
		t.Fatalf("expected no targets, got %#v", got)
	}
}

func TestResolveUpgradeScopeDefaultsToConfiguredTarget(t *testing.T) {
	base := Server{Cluster: "doops-edu", Instance: "edu-coder"}

	cluster, instance, err := resolveUpgradeScope(base, "", "")
	if err != nil {
		t.Fatalf("resolve configured target scope: %v", err)
	}
	if cluster != "doops-edu" || instance != "edu-coder" {
		t.Fatalf("expected configured target scope, got %q/%q", cluster, instance)
	}
}

func TestResolveUpgradeScopeRequiresCompleteExplicitScope(t *testing.T) {
	base := Server{Cluster: "doops-edu", Instance: "edu-coder"}

	_, _, err := resolveUpgradeScope(base, "*", "")
	if err == nil || !strings.Contains(err.Error(), "together") {
		t.Fatalf("expected incomplete explicit scope error, got %v", err)
	}

	cluster, instance, err := resolveUpgradeScope(base, "*", "*")
	if err != nil {
		t.Fatalf("resolve explicit broadcast scope: %v", err)
	}
	if cluster != "*" || instance != "*" {
		t.Fatalf("expected explicit broadcast scope, got %q/%q", cluster, instance)
	}
}

func TestResolveUpgradeScopeRejectsUnscopedGateway(t *testing.T) {
	_, _, err := resolveUpgradeScope(Server{}, "", "")
	if err == nil || !strings.Contains(err.Error(), "configured target") {
		t.Fatalf("expected unscoped gateway error, got %v", err)
	}
}

func TestUpgradeAgentsRejectsApplyBeforeGatewayCall(t *testing.T) {
	err := UpgradeAgents(Server{}, UpgradeOptions{
		Image: "registry.example/doops@sha256:abc",
	}, false)
	if err == nil || !strings.Contains(err.Error(), "direct agent self-upgrade is prohibited") {
		t.Fatalf("expected direct self-upgrade rejection, got %v", err)
	}
}

func TestUpgradeAgentsUsesExplicitTokenWithoutResolvingTargetLogin(t *testing.T) {
	var gotAuth string
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"targets":[]}`))
	}))
	defer gateway.Close()

	err := UpgradeAgents(Server{}, UpgradeOptions{
		Gateway:  gateway.URL,
		Token:    "explicit-token",
		Cluster:  "*",
		Instance: "*",
		Image:    "registry.example/doops@sha256:abc",
		DryRun:   true,
	}, false)
	if err == nil || !strings.Contains(err.Error(), "no online targets matched") {
		t.Fatalf("expected empty target result, got %v", err)
	}
	if gotAuth != "Bearer explicit-token" {
		t.Fatalf("explicit token was not used: %q", gotAuth)
	}
}

func TestUpgradeAgentsRejectsGatewayMismatchBeforeRequest(t *testing.T) {
	err := UpgradeAgents(Server{Gateway: "https://trusted.example.com"}, UpgradeOptions{
		Gateway: "https://other.example.com",
		Token:   "explicit-token",
		Image:   "registry.example/doops@sha256:abc",
		DryRun:  true,
	}, false)
	if err == nil || !strings.Contains(err.Error(), "configured target belongs to gateway") {
		t.Fatalf("expected gateway mismatch, got %v", err)
	}
}
