package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestUpgradeAgentsRejectsApplyBeforeGatewayCall(t *testing.T) {
	err := UpgradeAgents(Server{}, UpgradeOptions{
		Image: "registry.example/doops@sha256:abc",
	}, false)
	if err == nil || !strings.Contains(err.Error(), "direct agent self-upgrade is prohibited") {
		t.Fatalf("expected direct self-upgrade rejection, got %v", err)
	}
}

func TestUpgradeAgentsUsesConfiguredGatewayLogin(t *testing.T) {
	var gotAuth string
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"targets":[]}`))
	}))
	defer gateway.Close()
	home := t.TempDir()
	t.Setenv("HOME", home)
	auth := AuthStore{Tokens: map[string]string{}}
	auth.SetGateway(gateway.URL, "cached-token")
	data, err := json.Marshal(&auth)
	if err != nil {
		t.Fatalf("marshal auth: %v", err)
	}
	authPath := filepath.Join(home, ".agent", "skills", "doops", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o755); err != nil {
		t.Fatalf("create auth dir: %v", err)
	}
	if err := os.WriteFile(authPath, data, 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	err = UpgradeAgents(Server{
		Name:     "configured",
		Gateway:  gateway.URL,
		Cluster:  "prod",
		Instance: "node",
	}, UpgradeOptions{
		Image:  "registry.example/doops@sha256:abc",
		DryRun: true,
	}, false)
	if err == nil || !strings.Contains(err.Error(), "configured target is offline") {
		t.Fatalf("expected empty target result, got %v", err)
	}
	if gotAuth != "Bearer cached-token" {
		t.Fatalf("cached Gateway login was not used: %q", gotAuth)
	}
}

func TestUpgradeAgentsRequiresConfiguredTarget(t *testing.T) {
	err := UpgradeAgents(Server{}, UpgradeOptions{
		Image:  "registry.example/doops@sha256:abc",
		DryRun: true,
	}, false)
	if err == nil || !strings.Contains(err.Error(), "configured Gateway target is required") {
		t.Fatalf("expected configured target requirement, got %v", err)
	}
}
