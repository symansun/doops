package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrintServerListShowsGatewayRoutesOnly(t *testing.T) {
	var out bytes.Buffer
	err := printServerList(&out, []Server{{
		Name:     "gw-scu",
		Gateway:  "https://doops.example.com",
		Cluster:  "doops-chuanda",
		Instance: "ecs-phjs-0005",
		Use:      "SCU via gateway",
	}})
	if err != nil {
		t.Fatalf("print gateway list: %v", err)
	}

	got := out.String()
	for _, want := range []string{"NAME", "GATEWAY", "CLUSTER/INSTANCE", "gw-scu", "doops-chuanda/ecs-phjs-0005"} {
		if !strings.Contains(got, want) {
			t.Fatalf("list output missing %q:\n%s", want, got)
		}
	}
	for _, obsolete := range []string{"IP", "PORT", "42222"} {
		if strings.Contains(got, obsolete) {
			t.Fatalf("gateway list output contains obsolete direct field %q:\n%s", obsolete, got)
		}
	}
}

func TestPrintServerListRejectsIncompleteGatewayRoute(t *testing.T) {
	var out bytes.Buffer
	err := printServerList(&out, []Server{{Name: "incomplete", Gateway: "https://doops.example.com"}})
	if err == nil || !strings.Contains(err.Error(), "incomplete Gateway route") {
		t.Fatalf("expected incomplete route rejection, got %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("invalid config produced partial list output: %q", out.String())
	}
}

func TestValidateGatewayServerRequiresExplicitRouteFields(t *testing.T) {
	tests := []Server{
		{Name: " ", Gateway: "https://doops.example.com", Cluster: "prod", Instance: "node-1"},
		{Name: "target", Gateway: " ", Cluster: "prod", Instance: "node-1"},
		{Name: "target", Gateway: "https://doops.example.com", Cluster: " ", Instance: "node-1"},
		{Name: "target", Gateway: "https://doops.example.com", Cluster: "prod", Instance: " "},
	}
	for _, server := range tests {
		if err := validateGatewayServers([]Server{server}); err == nil {
			t.Fatalf("expected incomplete route rejection for %#v", server)
		}
	}
}

func TestGatewayServerJSONOmitsDirectAndTokenFields(t *testing.T) {
	data, err := json.Marshal(Server{
		Name:     "gw-scu",
		Gateway:  "https://doops.example.com",
		Cluster:  "doops-chuanda",
		Instance: "ecs-phjs-0005",
	})
	if err != nil {
		t.Fatalf("marshal server: %v", err)
	}

	got := string(data)
	for _, obsolete := range []string{`"ip"`, `"port"`, `"token"`} {
		if strings.Contains(got, obsolete) {
			t.Fatalf("gateway target JSON contains obsolete field %s: %s", obsolete, got)
		}
	}
}

func TestSaveServersOmitsEmptyDirectAndTokenFields(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("DOOPS_CONFIG", configPath)

	err := saveServers([]Server{{
		Name:     "gw-scu",
		Gateway:  "https://doops.example.com",
		Cluster:  "doops-chuanda",
		Instance: "ecs-phjs-0005",
	}})
	if err != nil {
		t.Fatalf("save servers: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	got := string(data)
	for _, obsolete := range []string{`"ip"`, `"port"`, `"token"`} {
		if strings.Contains(got, obsolete) {
			t.Fatalf("saved Gateway config contains obsolete field %s: %s", obsolete, got)
		}
	}
}

func TestSaveServersPreservesLLMConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("DOOPS_CONFIG", configPath)
	original := `{
  "servers": [{"name":"old","gateway":"https://doops.example.com","cluster":"old","instance":"node-1"}],
  "llm": {"api_key":"key","model":"model","base_url":"https://api.example.com"}
}`
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := saveServers([]Server{{
		Name:     "new",
		Gateway:  "https://doops.example.com",
		Cluster:  "new",
		Instance: "node-2",
	}}); err != nil {
		t.Fatalf("save servers: %v", err)
	}
	_, llm, err := loadPath(configPath)
	if err != nil {
		t.Fatalf("load saved config: %v", err)
	}
	if llm.APIKey != "key" || llm.Model != "model" || llm.BaseURL != "https://api.example.com" {
		t.Fatalf("LLM config changed: %#v", llm)
	}
}

func TestSaveServersDoesNotOverwriteInvalidExistingConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("DOOPS_CONFIG", configPath)
	original := []byte(`{
  "servers": [
    {"name":"legacy","ip":"192.0.2.10","port":"42222"},
    {"name":"valid","gateway":"https://doops.example.com","cluster":"prod","instance":"node-1"}
  ],
  "llm": {"api_key":"keep","model":"keep","base_url":"https://api.example.com"}
}`)
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	err := saveServers([]Server{{
		Name:     "new",
		Gateway:  "https://doops.example.com",
		Cluster:  "new",
		Instance: "node-2",
	}})
	if err == nil {
		t.Fatal("expected invalid existing config to block save")
	}
	after, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("read config after failed save: %v", readErr)
	}
	if !bytes.Equal(after, original) {
		t.Fatalf("invalid existing config was overwritten:\n%s", after)
	}
}

func TestEveryConfigSourceRejectsRetiredTargetFields(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("DOOPS_CONFIG", configPath)
	if err := os.WriteFile(
		configPath,
		[]byte(`{"servers":[{"name":"legacy","ip":"192.0.2.10","port":"42222"}]}`),
		0o600,
	); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), `unknown field "ip"`) {
		t.Fatalf("expected strict override config rejection, got %v", err)
	}
}

func TestLoadConfigRejectsLegacyArrayFormat(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("DOOPS_CONFIG", configPath)
	if err := os.WriteFile(configPath, []byte(`[{"name":"legacy"}]`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, _, err := LoadConfig(); err == nil {
		t.Fatal("expected legacy array config to be rejected")
	}
}
