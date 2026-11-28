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

	// 1. Built-in real system MCP servers (doops, dosql) are seeded and readable by all users.
	// Non-admin users cannot mutate, delete, or sync MCP servers (403); admin gets 409 on immutable builtins.
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
	f.http(t, operator, "POST", "/v1/mcp-servers", `{"action":"delete","slug":"doops"}`, 403)
	f.http(t, operator, "POST", "/v1/mcp-servers", `{"action":"toggle","slug":"dosql","enabled":false}`, 403)
	f.http(t, f.admin, "POST", "/v1/mcp-servers", `{"action":"delete","slug":"doops"}`, 409)
	f.http(t, f.admin, "POST", "/v1/mcp-servers", `{"action":"toggle","slug":"dosql","enabled":false}`, 409)

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

	// Register both custom MCP servers via POST /v1/mcp-servers (platform admin only).
	stdioPayload, _ := json.Marshal(map[string]any{
		"action":      "upsert",
		"slug":        "net-tools",
		"name":        "网络探测 MCP",
		"description": "真实 stdio MCP 测试",
		"transport":   "stdio",
		"command":     "/bin/sh",
		"args":        []string{stdioScript},
	})
	f.http(t, operator, "POST", "/v1/mcp-servers", string(stdioPayload), 403)
	f.http(t, f.admin, "POST", "/v1/mcp-servers", string(stdioPayload), 200)

	httpPayload, _ := json.Marshal(map[string]any{
		"action":      "upsert",
		"slug":        "prom-mcp",
		"name":        "Prometheus HTTP MCP",
		"description": "真实 HTTP MCP 测试",
		"transport":   "http",
		"url":         httpMCP.URL,
	})
	f.http(t, f.admin, "POST", "/v1/mcp-servers", string(httpPayload), 200)

	// 3. Probe the stdio MCP server via Gateway -> Agent doops_mcp_inspect and verify REAL tools persisted!
	f.http(t, operator, "POST", "/v1/mcp-servers", `{"action":"probe","slug":"net-tools","target":{"cluster":"dev","instance":"local"}}`, 403)
	probeRaw := f.http(t, f.admin, "POST", "/v1/mcp-servers", `{"action":"probe","slug":"net-tools","target":{"cluster":"dev","instance":"local"}}`, 200)
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
	probeHTTPRaw := f.http(t, f.admin, "POST", "/v1/mcp-servers", `{"action":"probe","slug":"prom-mcp","target":{"cluster":"dev","instance":"local"}}`, 200)
	if !strings.Contains(string(probeHTTPRaw), `"prom_query"`) {
		t.Fatalf("expected prom_query tool from real HTTP MCP probe, got: %s", string(probeHTTPRaw))
	}

	// 4. Sync enabled custom MCP servers to dev/local (admin only) and verify settings.json merges builtin + custom while preserving model!
	f.http(t, operator, "POST", "/v1/mcp-servers", `{"action":"sync","targets":[{"cluster":"dev","instance":"local"}]}`, 403)
	syncRaw := f.http(t, f.admin, "POST", "/v1/mcp-servers", `{"action":"sync","targets":[{"cluster":"dev","instance":"local"}]}`, 200)
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

	// Clean up custom MCPs (admin) and Skill (owner).
	f.http(t, operator, "POST", "/v1/mcp-servers", `{"action":"delete","slug":"net-tools"}`, 403)
	f.http(t, f.admin, "POST", "/v1/mcp-servers", `{"action":"delete","slug":"net-tools"}`, 200)
	f.http(t, f.admin, "POST", "/v1/mcp-servers", `{"action":"delete","slug":"prom-mcp"}`, 200)
	f.http(t, operator, "POST", "/v1/skills", `{"action":"delete","slug":"disk-backed-skill"}`, 200)
	if _, err := os.Stat(diskMDPath); !os.IsNotExist(err) {
		t.Fatalf("expected disk skill directory cleaned up on delete")
	}
}

func TestPlatformMCPSecurityAdminBoundaryAndSecretProtection(t *testing.T) {
	f := monitorEngineFixture(t)
	operator := f.user(t, "mcp-sec-op", ScopeGrant{Cluster: "dev", Instance: "local"})
	noGrantUser, err := f.store.CreateUser("mcp-sec-nogrant")
	if err != nil {
		t.Fatalf("create zero-grant user: %v", err)
	}
	noGrant, err := f.store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: noGrantUser.ID, Name: "mcp-sec-nogrant"})
	if err != nil {
		t.Fatalf("create zero-grant token: %v", err)
	}
	inventoryTestAgent(t, f, "local")

	agentDir := t.TempDir()
	origSettings := doagentSettingsFile
	doagentSettingsFile = filepath.Join(agentDir, "settings.json")
	t.Cleanup(func() { doagentSettingsFile = origSettings })
	if err := writeFileAtomic0600(doagentSettingsFile, []byte(`{"mcp_servers":[],"permissions":{}}`)); err != nil {
		t.Fatalf("seed settings.json: %v", err)
	}

	const envCanary = "mcp-env-canary-9a8b7c6d"
	const hdrCanary = "mcp-hdr-canary-4d5e6f1a"

	var gotAuthHeader string
	httpMCP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthHeader = r.Header.Get("Authorization")
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"sec-http-mcp","version":"1.0"}}}`))
		case "tools/list":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"sec_check","description":"Check auth"}]}}`))
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer httpMCP.Close()

	// 1. Both zero-grant user and target-granted non-admin are denied custom MCP mutations and sync.
	upsertBody, _ := json.Marshal(map[string]any{
		"action":    "upsert",
		"slug":      "sec-http",
		"name":      "Sec HTTP MCP",
		"transport": "http",
		"url":       httpMCP.URL,
		"headers":   map[string]string{"Authorization": "Bearer " + hdrCanary},
	})
	for _, tc := range []struct {
		name string
		user CreatedToken
	}{
		{"zero-grant", noGrant},
		{"target-granted-non-admin", operator},
	} {
		resp := f.http(t, tc.user, "POST", "/v1/mcp-servers", string(upsertBody), 403)
		if !strings.Contains(string(resp), "mcp_admin_required") {
			t.Fatalf("%s upsert response = %s, want mcp_admin_required", tc.name, string(resp))
		}
		syncResp := f.http(t, tc.user, "POST", "/v1/mcp-servers", `{"action":"sync"}`, 403)
		if !strings.Contains(string(syncResp), "mcp_admin_required") {
			t.Fatalf("%s sync response = %s, want mcp_admin_required", tc.name, string(syncResp))
		}
	}

	// 2. Saving MCP secrets without DOOPS_GATEWAY_SECRET_KEY fails closed (503).
	t.Setenv(secretKeyEnv, "")
	noKeyResp := f.http(t, f.admin, "POST", "/v1/mcp-servers", string(upsertBody), 503)
	if !strings.Contains(string(noKeyResp), "mcp_secret_key_unavailable") {
		t.Fatalf("upsert without secret key = %s, want mcp_secret_key_unavailable", string(noKeyResp))
	}

	// 3. With DOOPS_GATEWAY_SECRET_KEY configured, secrets are encrypted at rest with v2 envelope.
	t.Setenv(secretKeyEnv, "synthetic-mcp-master-key-for-sec01-test")
	f.http(t, f.admin, "POST", "/v1/mcp-servers", string(upsertBody), 200)

	stdioBody, _ := json.Marshal(map[string]any{
		"action":    "upsert",
		"slug":      "sec-stdio",
		"name":      "Sec Stdio MCP",
		"transport": "stdio",
		"command":   "/bin/true",
		"env":       map[string]string{"MCP_CANARY_TOKEN": envCanary},
	})
	f.http(t, f.admin, "POST", "/v1/mcp-servers", string(stdioBody), 200)

	var rawHeadersDB, rawEnvDB string
	if err := f.store.db.QueryRow(`SELECT headers_json FROM platform_mcp_servers WHERE slug='sec-http'`).Scan(&rawHeadersDB); err != nil {
		t.Fatalf("read raw headers_json: %v", err)
	}
	if err := f.store.db.QueryRow(`SELECT env_json FROM platform_mcp_servers WHERE slug='sec-stdio'`).Scan(&rawEnvDB); err != nil {
		t.Fatalf("read raw env_json: %v", err)
	}
	if !strings.HasPrefix(rawHeadersDB, secretEnvelopePrefixV2) || strings.Contains(rawHeadersDB, hdrCanary) {
		t.Fatalf("headers_json must be v2 encrypted without plaintext canary, got: %q", rawHeadersDB)
	}
	if !strings.HasPrefix(rawEnvDB, secretEnvelopePrefixV2) || strings.Contains(rawEnvDB, envCanary) {
		t.Fatalf("env_json must be v2 encrypted without plaintext canary, got: %q", rawEnvDB)
	}

	// 4. HTTP GET responses never leak secret values and only expose key names.
	for _, tc := range []struct {
		name string
		user CreatedToken
		path string
	}{
		{"admin-list", f.admin, "/v1/mcp-servers"},
		{"admin-http-detail", f.admin, "/v1/mcp-servers/sec-http"},
		{"admin-stdio-detail", f.admin, "/v1/mcp-servers/sec-stdio"},
		{"operator-list", operator, "/v1/mcp-servers"},
		{"operator-http-detail", operator, "/v1/mcp-servers/sec-http"},
		{"operator-stdio-detail", operator, "/v1/mcp-servers/sec-stdio"},
	} {
		raw := string(f.http(t, tc.user, "GET", tc.path, "", 200))
		if strings.Contains(raw, hdrCanary) || strings.Contains(raw, envCanary) {
			t.Fatalf("%s leaked plaintext MCP secret: %s", tc.name, raw)
		}
	}
	opDetailRaw := f.http(t, operator, "GET", "/v1/mcp-servers/sec-http", "", 200)
	var opDetail struct {
		Server platformMCPServerRecord `json:"server"`
	}
	if err := json.Unmarshal(opDetailRaw, &opDetail); err != nil {
		t.Fatalf("decode operator detail: %v", err)
	}
	if opDetail.Server.CanEdit || opDetail.Server.CanDelete {
		t.Fatalf("non-admin must have can_edit=false can_delete=false on custom MCP, got %+v", opDetail.Server)
	}
	if len(opDetail.Server.HeaderKeys) != 1 || opDetail.Server.HeaderKeys[0] != "Authorization" {
		t.Fatalf("expected header_keys=[Authorization], got %v", opDetail.Server.HeaderKeys)
	}

	// 5. Updating metadata with headers omitted preserves encrypted headers for live probe.
	metaUpdate, _ := json.Marshal(map[string]any{
		"action":      "upsert",
		"slug":        "sec-http",
		"name":        "Sec HTTP MCP Updated",
		"description": "updated without resending headers",
		"transport":   "http",
		"url":         httpMCP.URL,
	})
	f.http(t, f.admin, "POST", "/v1/mcp-servers", string(metaUpdate), 200)
	f.http(t, f.admin, "POST", "/v1/mcp-servers", `{"action":"probe","slug":"sec-http","target":{"cluster":"dev","instance":"local"}}`, 200)
	if gotAuthHeader != "Bearer "+hdrCanary {
		t.Fatalf("expected preserved encrypted header on live probe, got Authorization=%q", gotAuthHeader)
	}

	// 6. Non-admin GET ?probe=1 must not overwrite persisted probe metadata in platform_mcp_servers.
	if _, err := f.store.db.Exec(`UPDATE platform_mcp_servers SET last_probe_status='admin-baseline' WHERE slug='doops'`); err != nil {
		t.Fatalf("seed baseline probe status: %v", err)
	}
	f.http(t, operator, "GET", "/v1/mcp-servers?target=dev/local&probe=1", "", 200)
	var probeStatusAfter string
	if err := f.store.db.QueryRow(`SELECT last_probe_status FROM platform_mcp_servers WHERE slug='doops'`).Scan(&probeStatusAfter); err != nil {
		t.Fatalf("read probe status after non-admin probe: %v", err)
	}
	if probeStatusAfter != "admin-baseline" {
		t.Fatalf("non-admin probe mutated global platform_mcp_servers.last_probe_status to %q", probeStatusAfter)
	}
}
