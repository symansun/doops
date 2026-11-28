package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveLoginGatewayRequiresTargetOrGateway(t *testing.T) {
	_, _, err := resolveLoginGateway("", "", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "--gateway is required") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveLoginGatewayAllowsFirstLoginWithoutConfig(t *testing.T) {
	gateway, server, err := resolveLoginGateway("", " https://gateway.example.com ", nil, os.ErrNotExist)
	if err != nil {
		t.Fatalf("gateway-first login rejected: %v", err)
	}
	if server != nil || gateway != "https://gateway.example.com" {
		t.Fatalf("gateway = %q server = %#v", gateway, server)
	}
}

func TestResolveLoginGatewayRejectsTargetWithoutConfig(t *testing.T) {
	_, _, err := resolveLoginGateway("demo", "", nil, os.ErrNotExist)
	if err == nil || !strings.Contains(err.Error(), "--gateway") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveLoginGatewayKeepsTargetBehaviour(t *testing.T) {
	servers := []Server{{Name: "demo", Aliases: []string{"jy"}, Gateway: "https://gateway.example.com", Cluster: "doops-demo", Instance: "demo-228"}}
	gateway, server, err := resolveLoginGateway("jy", "", servers, nil)
	if err != nil || server == nil || server.Name != "demo" || gateway != "https://gateway.example.com" {
		t.Fatalf("gateway = %q server = %#v err = %v", gateway, server, err)
	}
	if _, _, err := resolveLoginGateway("demo", "https://other.example.com", servers, nil); err == nil {
		t.Fatal("a gateway that does not match the target was accepted")
	}
}

func TestResolveLoginGatewayRejectsInvalidGatewayURL(t *testing.T) {
	if _, _, err := resolveLoginGateway("", "ftp://gateway.example.com", nil, nil); err == nil {
		t.Fatal("non-HTTP gateway URL was accepted")
	}
}

func TestPersistVisibleGatewayRoutesCreatesConfigOnFirstLogin(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("DOOPS_CONFIG", configPath)
	t.Setenv("HOME", t.TempDir())

	updated, err := persistVisibleGatewayRoutes(nil, "https://gateway.example.com", []GatewayTarget{
		{Cluster: "doops-zyj", Instance: "zyj-test"},
		{Cluster: "doops-zyj", Instance: "zyj-test"},
	})
	if err != nil {
		t.Fatalf("persist routes: %v", err)
	}
	if len(updated) != 1 || updated[0].Name != "zyj-test" || updated[0].Gateway != "https://gateway.example.com" {
		t.Fatalf("updated = %#v", updated)
	}
	disk, _, err := LoadConfig()
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if len(disk) != 1 || findSameRoute(disk, "https://gateway.example.com", "doops-zyj", "zyj-test") != "zyj-test" {
		t.Fatalf("saved routes = %#v", disk)
	}

	again, err := persistVisibleGatewayRoutes(disk, "https://gateway.example.com", []GatewayTarget{
		{Cluster: "doops-zyj", Instance: "zyj-test"},
	})
	if err != nil || len(again) != 1 {
		t.Fatalf("second persist = %#v, %v", again, err)
	}
}

func TestFirstLoginDiscoveryPersistsVisibleTargets(t *testing.T) {
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/targets" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"targets":[{"cluster":"doops-zyj","instance":"zyj-test"}]}`))
	}))
	defer live.Close()

	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("DOOPS_CONFIG", configPath)
	t.Setenv("HOME", t.TempDir())

	targets, err := fetchGatewayTargets(live.URL, "test-token")
	if err != nil {
		t.Fatalf("fetch targets: %v", err)
	}
	routes, err := persistVisibleGatewayRoutes(nil, live.URL, targets)
	if err != nil {
		t.Fatalf("persist routes: %v", err)
	}
	if len(routes) != 1 || routes[0].Name != "zyj-test" || routes[0].Cluster != "doops-zyj" || routes[0].Instance != "zyj-test" {
		t.Fatalf("routes = %#v", routes)
	}
	disk, _, err := LoadConfig()
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if len(disk) != 1 || findSameRoute(disk, live.URL, "doops-zyj", "zyj-test") != "zyj-test" {
		t.Fatalf("saved routes = %#v", disk)
	}
}

func TestResolveLoginGatewayAcceptsConfiguredName(t *testing.T) {
	servers := []Server{{Name: "gw-test", Aliases: []string{"demo"}, Gateway: "https://gateway.example.com", Cluster: "doops-demo", Instance: "demo-228"}}
	gateway, server, err := resolveLoginGateway("", "gw-test", servers, nil)
	if err != nil || server != nil || gateway != "https://gateway.example.com" {
		t.Fatalf("name as --gateway should resolve to its Gateway and still run discovery: gateway=%q server=%#v err=%v", gateway, server, err)
	}
	gateway, server, err = resolveLoginGateway("", "demo", servers, nil)
	if err != nil || server != nil || gateway != "https://gateway.example.com" {
		t.Fatalf("alias as --gateway: gateway=%q server=%#v err=%v", gateway, server, err)
	}
	if _, _, err := resolveLoginGateway("", "unknown-gw", servers, nil); err == nil || !strings.Contains(err.Error(), "not a Gateway URL or a configured target name") {
		t.Fatalf("unknown name: %v", err)
	}
	if _, _, err := resolveLoginGateway("", "gw-test", nil, os.ErrNotExist); err == nil || !strings.Contains(err.Error(), "--gateway <url>") {
		t.Fatalf("name without config must ask for a URL: %v", err)
	}
}
