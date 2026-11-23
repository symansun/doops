package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlatformMCPRegistryProbeSyncAndSkillDiskStorage(t *testing.T) {
	f := monitorEngineFixture(t)
	operator := f.user(t, "mcp-op", ScopeGrant{Cluster: "dev", Instance: "local"})
	inventoryTestAgent(t, f, "local")

	// Point Agent settings.json and skills root at isolated temp directories.
	agentDir := t.TempDir()
	origSettings := doagentSettingsFile
	doagentSettingsFile = filepath.Join(agentDir, "settings.json")
	t.Cleanup(func() { doagentSettingsFile = origSettings })

	// Seed an existing model configuration in settings.json to verify mcp_sync preserves it untouched.
	if err := writeFileAtomic0600(doagentSettingsFile, []byte(`{"model":"openai/gpt-test","provider":{"openai":{"options":{"apiKey":"sk-test-preserved"}}},"mcp_servers":[],"permissions":{}}`)); err != nil {
		t.Fatalf("seed settings.json: %v", err)
	}

	skillsRoot := filepath.Join(agentDir, "skills")
	t.Setenv("DOOPS_AGENT_SKILLS_ROOT", skillsRoot)

	// 1. Built-in real system MCP servers (doops, dosql) are seeded and cannot be modified or deleted.
	listRaw := f.http(t, operator, "GET", "/v1/mcp-servers", "", 200)
	var listResp struct {
		Servers []platformMCPServerRecord `json:"servers"`
	}
	if err := json.Unmarshal(listRaw, &listResp); err != nil {
		t.Fatalf("decode mcp list: %v", err)
	}
	if len(listResp.Servers) < 2 {
		t.Fatalf("expected >= 2 builtin mcp servers, got %d", len(listResp.Servers))
	}
	f.http(t, operator, "POST", "/v1/mcp-servers", `{"action":"delete","slug":"doops"}`, 409)
	f.http(t, operator, "POST", "/v1/mcp-servers", `{"action":"toggle","slug":"dosql","enabled":false}`, 409)

	// 2. Create a REAL stdio MCP server script speaking JSON-RPC 2.0 initialize + tools/list.
	stdioScript := filepath.Join(agentDir, "mock_mcp.sh")
	scriptBody := `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*)
      printf '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"real-stdio-mcp","version":"1.2.3"}}}\n'
      ;;
    *'"method":"tools/list"'*)
      printf '{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"check_ports","description":"List open TCP ports"},{"name":"disk_health","description":"Check SMART status"}]}}\n'
      ;;
  esac
done
`
	if err := os.WriteFile(stdioScript, []byte(scriptBody), 0755); err != nil {
		t.Fatalf("write stdio script: %v", err)
	}

	// Also start a REAL HTTP MCP server speaking JSON-RPC 2.0 initialize + tools/list.
	httpMCP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"http-prom-mcp","version":"0.9.0"}}}`))
		case "tools/list":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"prom_query","description":"Run PromQL query"}]}}`))
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer httpMCP.Close()

	// Register both custom MCP servers via POST /v1/mcp-servers.
	stdioPayload, _ := json.Marshal(map[string]any{
		"action":      "upsert",
		"slug":        "net-tools",
		"name":        "网络探测 MCP",
		"description": "真实 stdio MCP 测试",
		"transport":   "stdio",
		"command":     "/bin/sh",
		"args":        []string{stdioScript},
	})
	f.http(t, operator, "POST", "/v1/mcp-servers", string(stdioPayload), 200)

	httpPayload, _ := json.Marshal(map[string]any{
		"action":      "upsert",
		"slug":        "prom-mcp",
		"name":        "Prometheus HTTP MCP",
		"description": "真实 HTTP MCP 测试",
		"transport":   "http",
		"url":         httpMCP.URL,
	})
	f.http(t, operator, "POST", "/v1/mcp-servers", string(httpPayload), 200)

	// 3. Probe the stdio MCP server via Gateway -> Agent doops_mcp_inspect and verify REAL tools persisted!
	probeRaw := f.http(t, operator, "POST", "/v1/mcp-servers", `{"action":"probe","slug":"net-tools","target":{"cluster":"dev","instance":"local"}}`, 200)
	var probeResp struct {
		ProbeResults []mcpInspectServerResult `json:"probe_results"`
	}
	if err := json.Unmarshal(probeRaw, &probeResp); err != nil {
		t.Fatalf("decode probe: %v", err)
	}
	if len(probeResp.ProbeResults) != 1 || probeResp.ProbeResults[0].Status != "ok" || len(probeResp.ProbeResults[0].Tools) != 2 {
		t.Fatalf("expected 2 real stdio tools probed, got: %+v", probeResp.ProbeResults)
	}

	// Probe the HTTP MCP server as well!
	probeHTTPRaw := f.http(t, operator, "POST", "/v1/mcp-servers", `{"action":"probe","slug":"prom-mcp","target":{"cluster":"dev","instance":"local"}}`, 200)
	if !strings.Contains(string(probeHTTPRaw), `"prom_query"`) {
		t.Fatalf("expected prom_query tool from real HTTP MCP probe, got: %s", string(probeHTTPRaw))
	}

	// 4. Sync enabled custom MCP servers to dev/local and verify settings.json merges builtin + custom while preserving model!
	syncRaw := f.http(t, operator, "POST", "/v1/mcp-servers", `{"action":"sync","targets":[{"cluster":"dev","instance":"local"}]}`, 200)
	if !strings.Contains(string(syncRaw), `"synced_count":2`) || !strings.Contains(string(syncRaw), `"total_servers":4`) {
		t.Fatalf("unexpected mcp sync output: %s", string(syncRaw))
	}
	settingsBytes, err := os.ReadFile(doagentSettingsFile)
	if err != nil {
		t.Fatalf("read updated settings.json: %v", err)
	}
	settingsText := string(settingsBytes)
	if !strings.Contains(settingsText, `"openai/gpt-test"`) || !strings.Contains(settingsText, `"net-tools"`) || !strings.Contains(settingsText, `"prom-mcp"`) || !strings.Contains(settingsText, `"mcp_net-tools_check_ports"`) {
		t.Fatalf("settings.json missing preserved model or synced custom MCPs/allowed tools: %s", settingsText)
	}
	allowed := doagentAllowedTools(false, "", false)
	foundCustomAllowed := false
	for _, tool := range allowed {
		if tool == "mcp_net-tools_check_ports" {
			foundCustomAllowed = true
		}
	}
	if !foundCustomAllowed {
		t.Fatalf("expected mcp_net-tools_check_ports in doagentAllowedTools, got %v", allowed)
	}

	// 5. Verify disk-backed Skill storage + live Agent loaded-skills inspection (GET /v1/skills?target=dev/local).
	f.http(t, operator, "POST", "/v1/skills", `{
		"action": "upsert",
		"slug": "disk-backed-skill",
		"skill_md": "---\nname: Disk Skill\ndescription: Stored on Gateway filesystem\n---\n# Body",
		"files": [{"path": "scripts/inspect.sh", "content": "#!/bin/sh\necho '{\"status\":\"ok\"}'\n"}]
	}`, 200)

	// Assert files exist on Gateway filesystem AND SQLite skill_md column stays lightweight (empty).
	diskMDPath := filepath.Join(f.store.dbDir, "skills", "disk-backed-skill", "SKILL.md")
	if data, err := os.ReadFile(diskMDPath); err != nil || !strings.Contains(string(data), "Disk Skill") {
		t.Fatalf("expected Skill stored on Gateway filesystem at %s, err=%v", diskMDPath, err)
	}
	var dbSkillMD string
	if err := f.store.db.QueryRow(`SELECT skill_md FROM platform_skills WHERE slug='disk-backed-skill'`).Scan(&dbSkillMD); err != nil || dbSkillMD != "" {
		t.Fatalf("expected empty skill_md blob in SQLite when disk-backed, got err=%v val=%q", err, dbSkillMD)
	}

	// Sync to Agent and query live loaded skills on dev/local!
	f.http(t, operator, "POST", "/v1/skills", `{"action":"sync","slug":"disk-backed-skill","targets":[{"cluster":"dev","instance":"local"}]}`, 200)
	liveSkillsRaw := f.http(t, operator, "GET", "/v1/skills?target=dev/local", "", 200)
	if !strings.Contains(string(liveSkillsRaw), `"agent_skills"`) || !strings.Contains(string(liveSkillsRaw), `"disk-backed-skill"`) {
		t.Fatalf("expected live agent_skills containing disk-backed-skill, got: %s", string(liveSkillsRaw))
	}

	// Clean up custom MCPs and Skill.
	f.http(t, operator, "POST", "/v1/mcp-servers", `{"action":"delete","slug":"net-tools"}`, 200)
	f.http(t, operator, "POST", "/v1/mcp-servers", `{"action":"delete","slug":"prom-mcp"}`, 200)
	f.http(t, operator, "POST", "/v1/skills", `{"action":"delete","slug":"disk-backed-skill"}`, 200)
	if _, err := os.Stat(diskMDPath); !os.IsNotExist(err) {
		t.Fatalf("expected disk skill directory cleaned up on delete")
	}
}
