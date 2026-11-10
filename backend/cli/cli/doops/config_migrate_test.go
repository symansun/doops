package main

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigRejectsAbandonedLegacyDotConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DOOPS_CONFIG", "")

	legacyDir := filepath.Join(home, ".config", "doops")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatalf("mkdir legacy config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "config.json"), []byte(`{"servers":[{"name":"gw-oilan","ip":"106.54.197.139","port":"42222"}]}`), 0o600); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	_, _, err := LoadConfig()
	if err == nil {
		t.Fatal("expected abandoned ~/.config/doops/config.json to fail instead of loading")
	}
	msg := err.Error()
	for _, want := range []string{".config/doops/config.json", "gateway", "cluster", "instance", "ip", "port"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("legacy path error missing %q: %v", want, err)
		}
	}
}

func TestLoadConfigWrapsRetiredIPPortFields(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("DOOPS_CONFIG", configPath)
	if err := os.WriteFile(
		configPath,
		[]byte(`{"servers":[{"name":"legacy","ip":"192.0.2.10","port":"42222"}]}`),
		0o600,
	); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, _, err := LoadConfig()
	if err == nil {
		t.Fatal("expected retired ip/port fields to fail")
	}
	msg := err.Error()
	if !strings.Contains(msg, `unknown field "ip"`) && !strings.Contains(msg, `unknown field "port"`) {
		t.Fatalf("expected unknown field error, got %v", err)
	}
	for _, want := range []string{"direct", "gateway", "cluster", "instance"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("retired field error missing %q: %v", want, err)
		}
	}
}

func TestEnforceSecureGatewayURLTellsOperatorToUseHTTPS(t *testing.T) {
	t.Setenv("DOOPS_ALLOW_INSECURE_GATEWAY", "")
	u, err := url.Parse("http://106.54.197.139:42222")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	err = enforceSecureGatewayURL("http://106.54.197.139:42222", u)
	if err == nil {
		t.Fatal("expected insecure non-loopback gateway to be rejected")
	}
	msg := err.Error()
	for _, want := range []string{"https://", "wss://", "https://doops.aiedulab.cn"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("insecure gateway error missing %q: %v", want, err)
		}
	}
}
