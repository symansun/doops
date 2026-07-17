package main

import (
	"bytes"
	"encoding/json"
	"flag"
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

func TestPrintServerListRejectsRetiredTargetState(t *testing.T) {
	tests := []struct {
		name   string
		server Server
		want   string
	}{
		{
			name:   "direct route",
			server: Server{Name: "legacy", IP: "192.0.2.10", Port: "42222"},
			want:   "retired direct route",
		},
		{
			name: "per-target token",
			server: Server{
				Name:     "stale-token",
				Gateway:  "https://doops.example.com",
				Cluster:  "prod",
				Instance: "node-1",
				Token:    "secret",
			},
			want: "retired per-target token",
		},
		{
			name:   "incomplete gateway route",
			server: Server{Name: "incomplete", Gateway: "https://doops.example.com"},
			want:   "incomplete Gateway route",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := printServerList(&out, []Server{tt.server})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
			if out.Len() != 0 {
				t.Fatalf("invalid config produced partial list output: %q", out.String())
			}
		})
	}
}

func TestValidateGatewayAddFlagsRejectsExplicitEmptyLegacyFlags(t *testing.T) {
	for _, name := range []string{"ip", "port", "token"} {
		t.Run(name, func(t *testing.T) {
			flags := flag.NewFlagSet("add", flag.ContinueOnError)
			flags.String(name, "", "")
			if err := flags.Parse([]string{"--" + name + "="}); err != nil {
				t.Fatalf("parse flag: %v", err)
			}
			if err := validateGatewayAddFlags(flags); err == nil {
				t.Fatalf("expected explicit --%s= to be rejected", name)
			}
		})
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

func TestLoadConfigRejectsDirectTargetInCanonicalPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DOOPS_CONFIG", "")
	configDir := filepath.Join(home, ".agent", "skills", "doops")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(configDir, "config.json"),
		[]byte(`{"servers":[{"name":"legacy","ip":"192.0.2.10","port":"42222"}]}`),
		0o600,
	); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "invalid canonical config") {
		t.Fatalf("expected canonical direct target rejection, got %v", err)
	}
}

func TestExplicitConfigOverrideCanLoadLegacyTargetForDiagnostics(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "legacy.json")
	t.Setenv("DOOPS_CONFIG", configPath)
	if err := os.WriteFile(
		configPath,
		[]byte(`{"servers":[{"name":"legacy","ip":"192.0.2.10","port":"42222"}]}`),
		0o600,
	); err != nil {
		t.Fatalf("write config: %v", err)
	}

	servers, _, err := LoadConfig()
	if err != nil {
		t.Fatalf("load explicit diagnostic config: %v", err)
	}
	if len(servers) != 1 || servers[0].Name != "legacy" {
		t.Fatalf("unexpected explicit config: %#v", servers)
	}
}

func TestLegacyDirectServerJSONKeepsNonEmptyRouteFields(t *testing.T) {
	data, err := json.Marshal(Server{Name: "legacy", IP: "192.0.2.10", Port: "42222"})
	if err != nil {
		t.Fatalf("marshal server: %v", err)
	}
	got := string(data)
	for _, want := range []string{`"ip":"192.0.2.10"`, `"port":"42222"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("legacy JSON missing %s: %s", want, got)
		}
	}
}
