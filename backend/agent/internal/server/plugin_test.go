package server

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updatePluginCatalogJSON = flag.Bool("update-plugin-catalog", false, "rewrite lib/plugins/builtin-catalog.json from plugin_catalog.go")

func newPluginTestServer(t *testing.T) (*GatewayStore, *GatewayHub, *http.ServeMux, string, string) {
	t.Helper()
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("open gateway store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	adminUser, err := store.CreateUser("admin-plg")
	if err != nil {
		t.Fatalf("create admin user: %v", err)
	}
	if err := store.SetUserAdmin(adminUser.ID, true); err != nil {
		t.Fatalf("set admin: %v", err)
	}
	adminTok, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: adminUser.ID, Name: "admin-tok"})
	if err != nil {
		t.Fatalf("create admin token: %v", err)
	}

	normalUser, err := store.CreateUser("ops-plg")
	if err != nil {
		t.Fatalf("create normal user: %v", err)
	}
	userTok, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: normalUser.ID, Name: "user-tok"})
	if err != nil {
		t.Fatalf("create user token: %v", err)
	}

	hub := NewGatewayHub(store, GatewayHubOptions{})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	return store, hub, mux, adminTok.Plaintext, userTok.Plaintext
}

func doPluginRequest(t *testing.T, mux *http.ServeMux, method, path, token string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestPluginCatalogJSONInSync guards the single source of truth between
// backend/agent/internal/server/plugin_catalog.go and lib/plugins/builtin-catalog.json.
func TestPluginCatalogJSONInSync(t *testing.T) {
	want, err := marshalBuiltinPluginCatalogJSON()
	if err != nil {
		t.Fatalf("marshal catalog: %v", err)
	}
	jsonPath := filepath.Join("..", "..", "..", "..", "lib", "plugins", "builtin-catalog.json")
	if *updatePluginCatalogJSON {
		if err := os.MkdirAll(filepath.Dir(jsonPath), 0o755); err != nil {
			t.Fatalf("mkdir lib/plugins: %v", err)
		}
		if err := os.WriteFile(jsonPath, want, 0o644); err != nil {
			t.Fatalf("write %s: %v", jsonPath, err)
		}
	}
	got, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read %s: %v (run go test -run TestPluginCatalogJSONInSync -update-plugin-catalog)", jsonPath, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is out of sync with plugin_catalog.go; run `go test -run TestPluginCatalogJSONInSync -update-plugin-catalog`", jsonPath)
	}
}

// TestPluginTablesOnlyReadByPluginFiles enforces AGENTS.md openness:
// platform_plugin_config and platform_plugin_overrides must never be read or
// consulted by authorization, RPC, monitor execution, or skill dispatch code.
func TestPluginTablesOnlyReadByPluginFiles(t *testing.T) {
	allowed := map[string]bool{
		"plugin_store.go":                true,
		"plugin_catalog.go":              true,
		"plugin_http.go":                 true,
		"gateway_store_schema.go":        true,
		"gateway_mysql_schema.go":        true,
		"gateway_store_mysql_runtime.go": true,
		"gateway_mysql_copy.go":          true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read server dir: %v", err)
	}
	scanned := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		scanned++
		if allowed[name] {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(raw), "platform_plugin_") {
			t.Errorf("%s references platform_plugin_* table; plugin UI state must stay strictly isolated from execution, authorization, and scheduler paths", name)
		}
	}
	if scanned < 80 {
		t.Fatalf("scanned only %d Go production files, want >= 80", scanned)
	}
}

func TestPluginReadETagAndUnconfiguredDefault(t *testing.T) {
	_, _, mux, adminTok, userTok := newPluginTestServer(t)

	// Unauthenticated must be rejected.
	unauth := doPluginRequest(t, mux, http.MethodGet, "/v1/plugins", "", nil, nil)
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET /v1/plugins = %d, want 401", unauth.Code)
	}

	// Ordinary user can read default all-in-one unconfigured state.
	rec := doPluginRequest(t, mux, http.MethodGet, "/v1/plugins", userTok, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/plugins = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("GET /v1/plugins missing ETag header")
	}
	var state PluginStateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !state.Supported || state.Configured || state.ScenarioID != "all-in-one" || state.Revision != 0 || state.Customized {
		t.Fatalf("unexpected default state: %+v", state)
	}
	if len(state.Scenarios) != 5 || len(state.Plugins) != 20 {
		t.Fatalf("got %d scenarios, %d plugins, want 5 and 20 (8 L1 + 12 Core builtin L2+)", len(state.Scenarios), len(state.Plugins))
	}
	for _, p := range state.Plugins {
		if p.Manifest != nil && p.Manifest.Plugin.OptIn {
			if p.Effective || p.ScenarioDefault || p.Override != nil {
				t.Fatalf("opt-in plugin %s expected effective=false, default=false, override=nil; got %+v", p.ID, p)
			}
			continue
		}
		if !p.Effective || !p.ScenarioDefault || p.Override != nil {
			t.Fatalf("all-in-one plugin %s expected effective=true, default=true, override=nil; got %+v", p.ID, p)
		}
	}

	// Matching If-None-Match returns 304 Not Modified.
	notMod := doPluginRequest(t, mux, http.MethodGet, "/v1/plugins", userTok, nil, map[string]string{
		"If-None-Match": etag,
	})
	if notMod.Code != http.StatusNotModified {
		t.Fatalf("conditional GET /v1/plugins = %d, want 304", notMod.Code)
	}
	if notMod.Header().Get("ETag") != etag {
		t.Fatalf("304 ETag = %q, want %q", notMod.Header().Get("ETag"), etag)
	}

	// Admin also gets 200 cleanly.
	adminRec := doPluginRequest(t, mux, http.MethodGet, "/v1/plugins", adminTok, nil, nil)
	if adminRec.Code != http.StatusOK {
		t.Fatalf("admin GET /v1/plugins = %d, want 200", adminRec.Code)
	}
}

func TestPluginAdminWriteRequiresAdminAndValidatesInputs(t *testing.T) {
	store, _, mux, adminTok, userTok := newPluginTestServer(t)

	// Non-admin must receive 403 Forbidden and DB must stay untouched.
	forbid := doPluginRequest(t, mux, http.MethodPost, "/v1/admin/plugins", userTok, map[string]any{
		"action":            "apply_scenario",
		"scenario_id":       "host-security-ops",
		"expected_revision": 0,
	}, nil)
	if forbid.Code != http.StatusForbidden {
		t.Fatalf("non-admin POST /v1/admin/plugins = %d, want 403", forbid.Code)
	}
	cfg, _, err := store.GetPluginConfig(context.Background())
	if err != nil || cfg.Configured || cfg.Revision != 0 {
		t.Fatalf("non-admin mutated plugin config: cfg=%+v err=%v", cfg, err)
	}

	// Unknown field rejected (DisallowUnknownFields).
	badField := doPluginRequest(t, mux, http.MethodPost, "/v1/admin/plugins", adminTok, map[string]any{
		"action":            "reset",
		"expected_revision": 0,
		"evil_extra":        "nope",
	}, nil)
	if badField.Code != http.StatusBadRequest {
		t.Fatalf("unknown field POST = %d, want 400", badField.Code)
	}

	// Unknown scenario rejected.
	badScenario := doPluginRequest(t, mux, http.MethodPost, "/v1/admin/plugins", adminTok, map[string]any{
		"action":            "apply_scenario",
		"scenario_id":       "nonexistent-pack",
		"expected_revision": 0,
	}, nil)
	if badScenario.Code != http.StatusBadRequest || !strings.Contains(badScenario.Body.String(), "unknown_scenario") {
		t.Fatalf("unknown scenario POST = %d (%s), want 400 unknown_scenario", badScenario.Code, badScenario.Body.String())
	}

	// Attempting to disable Core entries must fail closed.
	for _, coreID := range []string{"home", "ask", "terminal", "kb", "credentials", "audit", "plugin-center", "deploy-upgrade"} {
		badCore := doPluginRequest(t, mux, http.MethodPost, "/v1/admin/plugins", adminTok, map[string]any{
			"action":            "set_plugin",
			"plugin_id":         coreID,
			"enabled":           false,
			"expected_revision": 0,
		}, nil)
		if badCore.Code != http.StatusBadRequest || !strings.Contains(badCore.Body.String(), "invalid_plugin_id") {
			t.Fatalf("core entry %s disable = %d (%s), want 400 invalid_plugin_id", coreID, badCore.Code, badCore.Body.String())
		}
	}
}

func TestPluginScenarioLifecycleDryRunOverridesConflictAuditAndSideEffectFree(t *testing.T) {
	store, _, mux, adminTok, userTok := newPluginTestServer(t)
	ctx := context.Background()

	// Seed an enabled monitor schedule referencing inspect-k8s-cluster so dry_run reports affected_schedules.
	sched, err := store.SaveMonitorSchedule(ctx, monitorScheduleRecord{
		Name:            "K8s 定时巡检",
		Enabled:         true,
		ScopeMode:       "all_online",
		Skills:          []string{"inspect-k8s-cluster"},
		IntervalSeconds: 600,
		CreatedBy:       "admin-plg",
	}, false)
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}

	// Capture snapshots of skills, MCPs, and schedules before any plugin mutation.
	snapshotRuntime := func() (string, string, string) {
		sk, _ := store.ListPlatformSkills(ctx, TokenAuth{UserID: "system"}, "", "", false)
		mcp, _ := store.ListPlatformMCPServers(ctx, TokenAuth{UserID: "system"})
		sc, _ := store.ListMonitorSchedules(ctx)
		b1, _ := json.Marshal(sk)
		b2, _ := json.Marshal(mcp)
		b3, _ := json.Marshal(sc)
		return string(b1), string(b2), string(b3)
	}
	skBefore, mcpBefore, schedBefore := snapshotRuntime()

	// 1. Dry-run switching to host-security-ops must NOT increment revision or mark configured.
	dryRec := doPluginRequest(t, mux, http.MethodPost, "/v1/admin/plugins", adminTok, map[string]any{
		"action":            "apply_scenario",
		"scenario_id":       "host-security-ops",
		"expected_revision": 0,
		"dry_run":           true,
	}, nil)
	if dryRec.Code != http.StatusOK {
		t.Fatalf("dry_run apply_scenario = %d (%s), want 200", dryRec.Code, dryRec.Body.String())
	}
	var dryResp PluginMutationResponse
	if err := json.Unmarshal(dryRec.Body.Bytes(), &dryResp); err != nil {
		t.Fatalf("unmarshal dryResp: %v", err)
	}
	// host-security-ops hides 5 L1 plugins + 4 Core L2+ plugins (gpu-governance, inspect-k8s-cluster,
	// inspect-app-web, inspect-database) = 9 total, keeping the 8 Core host/security L2+ plugins active.
	if !dryResp.DryRun || dryResp.Revision != 0 || len(dryResp.Diff.Hide) != 9 {
		t.Fatalf("unexpected dryResp: %+v", dryResp)
	}
	if len(dryResp.Diff.AffectedSchedules) != 1 || dryResp.Diff.AffectedSchedules[0].ID != sched.ID {
		t.Fatalf("expected affected schedule %s in dry_run diff, got %+v", sched.ID, dryResp.Diff.AffectedSchedules)
	}
	cfgAfterDry, _, _ := store.GetPluginConfig(ctx)
	if cfgAfterDry.Configured || cfgAfterDry.Revision != 0 {
		t.Fatalf("dry_run mutated DB: %+v", cfgAfterDry)
	}

	// 2. Apply ai-compute-cluster (revision 0 -> 1).
	applyRec := doPluginRequest(t, mux, http.MethodPost, "/v1/admin/plugins", adminTok, map[string]any{
		"action":            "apply_scenario",
		"scenario_id":       "ai-compute-cluster",
		"expected_revision": 0,
	}, nil)
	if applyRec.Code != http.StatusOK {
		t.Fatalf("apply_scenario = %d (%s), want 200", applyRec.Code, applyRec.Body.String())
	}
	var applyResp PluginMutationResponse
	_ = json.Unmarshal(applyRec.Body.Bytes(), &applyResp)
	if applyResp.Revision != 1 || applyResp.ScenarioID != "ai-compute-cluster" || applyResp.Customized {
		t.Fatalf("unexpected applyResp: %+v", applyResp)
	}

	// 3. Stale expected_revision=0 must return 409 plugin_revision_conflict with current_revision=1.
	conflictRec := doPluginRequest(t, mux, http.MethodPost, "/v1/admin/plugins", adminTok, map[string]any{
		"action":            "set_plugin",
		"plugin_id":         "doops.database",
		"enabled":           true,
		"expected_revision": 0,
	}, nil)
	if conflictRec.Code != http.StatusConflict {
		t.Fatalf("stale revision POST = %d (%s), want 409", conflictRec.Code, conflictRec.Body.String())
	}
	var conflictBody map[string]any
	_ = json.Unmarshal(conflictRec.Body.Bytes(), &conflictBody)
	if conflictBody["code"] != "plugin_revision_conflict" || int64(conflictBody["current_revision"].(float64)) != 1 {
		t.Fatalf("unexpected conflict body: %v", conflictBody)
	}

	// 4. Enable doops.database as an override on ai-compute-cluster (revision 1 -> 2, customized=true).
	ovRec := doPluginRequest(t, mux, http.MethodPost, "/v1/admin/plugins", adminTok, map[string]any{
		"action":            "set_plugin",
		"plugin_id":         "doops.database",
		"enabled":           true,
		"expected_revision": 1,
	}, nil)
	if ovRec.Code != http.StatusOK {
		t.Fatalf("set_plugin enable override = %d (%s), want 200", ovRec.Code, ovRec.Body.String())
	}
	var ovResp PluginMutationResponse
	_ = json.Unmarshal(ovRec.Body.Bytes(), &ovResp)
	if ovResp.Revision != 2 || !ovResp.Customized {
		t.Fatalf("expected revision=2 customized=true, got %+v", ovResp)
	}

	// 5. Setting doops.database back to scenario default (false) deletes override row -> customized=false (revision 2 -> 3).
	revertRec := doPluginRequest(t, mux, http.MethodPost, "/v1/admin/plugins", adminTok, map[string]any{
		"action":            "set_plugin",
		"plugin_id":         "doops.database",
		"enabled":           false,
		"expected_revision": 2,
	}, nil)
	if revertRec.Code != http.StatusOK {
		t.Fatalf("set_plugin back to default = %d (%s), want 200", revertRec.Code, revertRec.Body.String())
	}
	var revertResp PluginMutationResponse
	_ = json.Unmarshal(revertRec.Body.Bytes(), &revertResp)
	if revertResp.Revision != 3 || revertResp.Customized {
		t.Fatalf("expected revision=3 customized=false when matching scenario default, got %+v", revertResp)
	}
	_, overridesNow, _ := store.GetPluginConfig(ctx)
	if len(overridesNow) != 0 {
		t.Fatalf("override row should be deleted when matching default, got %v", overridesNow)
	}

	// 6. Seed a stale override row directly, verify admin sees stale_overrides while user does not, then clear_override.
	if _, err := store.db.ExecContext(ctx, `INSERT INTO platform_plugin_overrides (plugin_id, enabled, updated_by, updated_at_ms) VALUES ('skill.legacy-removed', 1, 'admin-plg', 1)`); err != nil {
		t.Fatalf("seed stale override: %v", err)
	}
	userViewRec := doPluginRequest(t, mux, http.MethodGet, "/v1/plugins", userTok, nil, nil)
	var userView PluginStateResponse
	_ = json.Unmarshal(userViewRec.Body.Bytes(), &userView)
	if len(userView.StaleOverrides) != 0 {
		t.Fatalf("non-admin must not see stale_overrides, got %v", userView.StaleOverrides)
	}
	adminViewRec := doPluginRequest(t, mux, http.MethodGet, "/v1/plugins", adminTok, nil, nil)
	var adminView PluginStateResponse
	_ = json.Unmarshal(adminViewRec.Body.Bytes(), &adminView)
	if len(adminView.StaleOverrides) != 1 || adminView.StaleOverrides[0] != "skill.legacy-removed" {
		t.Fatalf("admin should see stale_overrides=[skill.legacy-removed], got %v", adminView.StaleOverrides)
	}
	clearRec := doPluginRequest(t, mux, http.MethodPost, "/v1/admin/plugins", adminTok, map[string]any{
		"action":            "clear_override",
		"plugin_id":         "skill.legacy-removed",
		"expected_revision": 3,
	}, nil)
	if clearRec.Code != http.StatusOK {
		t.Fatalf("clear_override = %d (%s), want 200", clearRec.Code, clearRec.Body.String())
	}

	// 7. Reset back to all-in-one (revision 4 -> 5).
	resetRec := doPluginRequest(t, mux, http.MethodPost, "/v1/admin/plugins", adminTok, map[string]any{
		"action":            "reset",
		"expected_revision": 4,
	}, nil)
	if resetRec.Code != http.StatusOK {
		t.Fatalf("reset = %d (%s), want 200", resetRec.Code, resetRec.Body.String())
	}

	// 8. Verify runtime snapshots of platform_skills, platform_mcp_servers, and monitor_schedules are byte-identical!
	skAfter, mcpAfter, schedAfter := snapshotRuntime()
	if skBefore != skAfter || mcpBefore != mcpAfter || schedBefore != schedAfter {
		t.Fatalf("plugin mutations altered runtime skills/MCPs/schedules!\nskills changed=%v mcps changed=%v schedules changed=%v",
			skBefore != skAfter, mcpBefore != mcpAfter, schedBefore != schedAfter)
	}

	// 9. Verify admin audit recorded typed action/scenario_id/plugin_id metadata in audit_events.
	events, err := store.ListAuditFiltered(AuditFilter{Action: ActionAdmin, Limit: 20})
	if err != nil {
		t.Fatalf("list admin audit: %v", err)
	}
	foundScenarioAudit := false
	foundToggleAudit := false
	for _, ev := range events {
		if ev.Session != "admin-plugins" {
			continue
		}
		if strings.Contains(ev.CommandSummary, `"action":"apply_scenario"`) && strings.Contains(ev.CommandSummary, `"scenario_id":"ai-compute-cluster"`) {
			foundScenarioAudit = true
		}
		if strings.Contains(ev.CommandSummary, `"action":"set_plugin"`) && strings.Contains(ev.CommandSummary, `"plugin_id":"doops.database"`) {
			foundToggleAudit = true
		}
	}
	if !foundScenarioAudit || !foundToggleAudit {
		t.Fatalf("expected structured admin-plugins audit entries (scenario=%v, toggle=%v); events=%+v", foundScenarioAudit, foundToggleAudit, events)
	}
}

func TestPluginWriteFailsClosedWhenAuditUnavailable(t *testing.T) {
	store, _, mux, adminTok, _ := newPluginTestServer(t)

	// Drop audit_events to simulate audit persistence outage; mutation must fail closed with 503.
	if _, err := store.db.Exec(`DROP TABLE audit_events`); err != nil {
		t.Fatalf("drop audit_events: %v", err)
	}
	rec := doPluginRequest(t, mux, http.MethodPost, "/v1/admin/plugins", adminTok, map[string]any{
		"action":            "apply_scenario",
		"scenario_id":       "minimal-workbench",
		"expected_revision": 0,
	}, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("mutation with unavailable audit = %d (%s), want 503", rec.Code, rec.Body.String())
	}
	cfg, _, err := store.GetPluginConfig(context.Background())
	if err != nil || cfg.Configured || cfg.Revision != 0 {
		t.Fatalf("mutation must not persist when audit is unavailable: cfg=%+v err=%v", cfg, err)
	}
}
