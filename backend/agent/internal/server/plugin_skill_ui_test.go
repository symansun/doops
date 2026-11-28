package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPluginPhase2BuiltinL2ManifestsAndSyncExclusion(t *testing.T) {
	store, _, mux, adminTok, userTok := newPluginTestServer(t)
	ctx := context.Background()

	// 1. All 12 Core builtin system Skills must seed with valid L2+ plugin.yaml manifests and zero errors.
	// DNS (dodns) and Email (doemail) are decoupled external installable plugins (plugins/dodns, plugins/doemail).
	builtinL2Slugs := []string{
		"inspect-vm-host",
		"machine-workload-discovery",
		"gpu-governance",
		"inspect-k8s-cluster",
		"host-security",
		"inspect-container-runtime",
		"inspect-app-web",
		"attack-surface-scan",
		"inspect-database",
		"inspect-ssh-host",
		"inspect-package-vuln",
		"inspect-proxy-evasion",
	}
	for _, slug := range builtinL2Slugs {
		sk, err := store.GetPlatformSkill(ctx, TokenAuth{UserID: "system"}, slug)
		if err != nil {
			t.Fatalf("GetPlatformSkill(%s): %v", slug, err)
		}
		if sk.UIManifestError != "" || sk.UIManifestJSON == "" || !sk.HasUIPlugin {
			t.Fatalf("builtin L2+ %s expected valid manifest, got has=%v err=%q", slug, sk.HasUIPlugin, sk.UIManifestError)
		}
		// filterAgentSyncSkillFiles must exclude plugin.yaml while keeping scripts/inspect.sh (when script-backed).
		synced := filterAgentSyncSkillFiles(sk.Files)
		hasInspect := false
		for _, f := range synced {
			if strings.EqualFold(f.Path, "plugin.yaml") {
				t.Fatalf("filterAgentSyncSkillFiles failed to strip plugin.yaml for %s", slug)
			}
			if f.Path == "scripts/inspect.sh" {
				hasInspect = true
			}
		}
		if sk.Entrypoint != "" && !hasInspect {
			t.Fatalf("filterAgentSyncSkillFiles dropped scripts/inspect.sh for %s", slug)
		}
	}

	// 2. GET /v1/plugins exposes all 12 Core builtin declarative L2+ plugins with manifests and builtin=true.
	rec := doPluginRequest(t, mux, http.MethodGet, "/v1/plugins", userTok, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/plugins = %d (%s)", rec.Code, rec.Body.String())
	}
	var state PluginStateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatalf("unmarshal plugins: %v", err)
	}
	byID := map[string]PluginView{}
	for _, p := range state.Plugins {
		byID[p.ID] = p
	}
	for _, slug := range builtinL2Slugs {
		id := "skill." + slug
		p, ok := byID[id]
		if !ok {
			t.Fatalf("missing L2+ plugin %s in GET /v1/plugins", id)
		}
		if p.Tier != "declarative" || p.Scope != "system" || !p.Builtin || p.CanDelete || !p.Effective || p.Manifest == nil {
			t.Fatalf("unexpected Core builtin L2+ plugin view %s: %+v", id, p)
		}
	}
	for _, decoupled := range []string{"skill.dns-governance", "skill.mail-governance", "skill.dodns", "skill.doemail"} {
		if _, exists := byID[decoupled]; exists {
			t.Fatalf("decoupled external plugin %s must NOT be pre-installed in Core", decoupled)
		}
	}

	// 3. Switching to host-security-ops hides 4 non-host L2+ plugins while keeping the 8 host/security L2+ plugins active.
	sw := doPluginRequest(t, mux, http.MethodPost, "/v1/admin/plugins", adminTok, map[string]any{
		"action":            "apply_scenario",
		"scenario_id":       "host-security-ops",
		"expected_revision": 0,
	}, nil)
	if sw.Code != http.StatusOK {
		t.Fatalf("apply host-security-ops = %d (%s)", sw.Code, sw.Body.String())
	}
	rec2 := doPluginRequest(t, mux, http.MethodGet, "/v1/plugins", userTok, nil, nil)
	var state2 PluginStateResponse
	_ = json.Unmarshal(rec2.Body.Bytes(), &state2)
	byID2 := map[string]PluginView{}
	for _, p := range state2.Plugins {
		byID2[p.ID] = p
	}
	wantEnabledHostSec := []string{
		"skill.inspect-vm-host",
		"skill.machine-workload-discovery",
		"skill.host-security",
		"skill.inspect-container-runtime",
		"skill.attack-surface-scan",
		"skill.inspect-ssh-host",
		"skill.inspect-package-vuln",
		"skill.inspect-proxy-evasion",
	}
	wantHiddenHostSec := []string{
		"skill.gpu-governance",
		"skill.inspect-k8s-cluster",
		"skill.inspect-app-web",
		"skill.inspect-database",
	}
	for _, id := range wantEnabledHostSec {
		if !byID2[id].Effective {
			t.Fatalf("host-security-ops expected %s effective=true, got false", id)
		}
	}
	for _, id := range wantHiddenHostSec {
		if byID2[id].Effective {
			t.Fatalf("host-security-ops expected %s effective=false, got true", id)
		}
	}

	// 4. Admin can override system L2 skill.gpu-governance ON under host-security-ops.
	ov := doPluginRequest(t, mux, http.MethodPost, "/v1/admin/plugins", adminTok, map[string]any{
		"action":            "set_plugin",
		"plugin_id":         "skill.gpu-governance",
		"enabled":           true,
		"expected_revision": 1,
	}, nil)
	if ov.Code != http.StatusOK {
		t.Fatalf("override system L2 = %d (%s)", ov.Code, ov.Body.String())
	}
	rec3 := doPluginRequest(t, mux, http.MethodGet, "/v1/plugins", userTok, nil, nil)
	var state3 PluginStateResponse
	_ = json.Unmarshal(rec3.Body.Bytes(), &state3)
	for _, p := range state3.Plugins {
		if p.ID == "skill.gpu-governance" && (!p.Effective || p.Override == nil || !*p.Override) {
			t.Fatalf("expected %s overridden to effective=true, got %+v", p.ID, p)
		}
	}
}

func TestPluginManifestValidationMatrixAndDiskReindex(t *testing.T) {
	store, _, mux, adminTok, userTok := newPluginTestServer(t)

	badCases := []struct {
		name string
		yaml string
	}{
		{
			name: "reject_text_input_type_Q3",
			yaml: "apiVersion: doops.sh/v1alpha1\nkind: SkillPlugin\nplugin:\n  name: Bad Text Input\nui:\n  inputs:\n    - key: FREE_CMD\n      label: Command\n      type: text\n",
		},
		{
			name: "reject_unknown_field_KnownFields",
			yaml: "apiVersion: doops.sh/v1alpha1\nkind: SkillPlugin\nplugin:\n  name: Unknown Field\n  evil_script: alert(1)\nui: {}\n",
		},
		{
			name: "reject_bad_apiVersion",
			yaml: "apiVersion: doops.sh/v99\nkind: SkillPlugin\nplugin:\n  name: Bad Ver\nui: {}\n",
		},
		{
			name: "reject_mismatched_plugin_id",
			yaml: "apiVersion: doops.sh/v1alpha1\nkind: SkillPlugin\nplugin:\n  id: skill.other\n  name: Mismatch\nui: {}\n",
		},
		{
			name: "reject_unknown_scenario",
			yaml: "apiVersion: doops.sh/v1alpha1\nkind: SkillPlugin\nplugin:\n  name: Bad Scenario\n  scenarios: [does-not-exist]\nui: {}\n",
		},
		{
			name: "reject_lowercase_input_key",
			yaml: "apiVersion: doops.sh/v1alpha1\nkind: SkillPlugin\nplugin:\n  name: Bad Key\nui:\n  inputs:\n    - key: warn_temp\n      label: Temp\n      type: number\n",
		},
		{
			name: "reject_preset_undeclared_input",
			yaml: "apiVersion: doops.sh/v1alpha1\nkind: SkillPlugin\nplugin:\n  name: Bad Preset\nui:\n  presets:\n    - id: p1\n      title: Run\n      mode: script\n      inputs:\n        UNDECLARED: 1\n",
		},
		{
			name: "reject_oversized_32kib",
			yaml: "apiVersion: doops.sh/v1alpha1\nkind: SkillPlugin\nplugin:\n  name: Oversized\n  description: \"" + strings.Repeat("a", 33*1024) + "\"\nui: {}\n",
		},
		{
			name: "reject_control_char_in_name",
			yaml: "apiVersion: doops.sh/v1alpha1\nkind: SkillPlugin\nplugin:\n  name: \"Bad\\x07Name\"\nui: {}\n",
		},
		{
			name: "reject_opt_in_with_scenarios",
			yaml: "apiVersion: doops.sh/v1alpha1\nkind: SkillPlugin\nplugin:\n  name: Bad OptIn\n  opt_in: true\n  scenarios: [all-in-one]\nui: {}\n",
		},
	}

	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doPluginRequest(t, mux, http.MethodPost, "/v1/skills", userTok, map[string]any{
				"action":   "upsert",
				"slug":     "bad-" + strings.ReplaceAll(strings.ToLower(tc.name), "_", "-"),
				"skill_md": "---\nname: bad\ndescription: bad\n---\n# body\n",
				"files": []map[string]string{
					{"path": "plugin.yaml", "content": tc.yaml},
				},
			}, nil)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_plugin_manifest") {
				t.Fatalf("%s: status=%d body=%s, want 400 invalid_plugin_manifest", tc.name, rec.Code, rec.Body.String())
			}
			// Atomic refusal: nothing should have been saved.
			slug := "bad-" + strings.ReplaceAll(strings.ToLower(tc.name), "_", "-")
			if _, err := store.GetPlatformSkill(context.Background(), TokenAuth{UserID: "system"}, slug); err == nil {
				t.Fatalf("%s: invalid manifest skill should NOT have been persisted", tc.name)
			}
		})
	}

	// Valid custom L2 plugin with out-of-whitelist icon normalizes icon to "layers".
	validYAML := `apiVersion: doops.sh/v1alpha1
kind: SkillPlugin
plugin:
  name: 磁盘自检插件
  description: 验证图标回退与磁盘热重解析
  category: ops
  icon: unknown-custom-icon
ui:
  slots:
    sidebar:
      enabled: true
      label: 磁盘自检
  inputs:
    - key: WARN_PCT
      label: 告警阈值
      type: number
      default: 80
      min: 10
      max: 99
`
	okRec := doPluginRequest(t, mux, http.MethodPost, "/v1/skills", userTok, map[string]any{
		"action":   "upsert",
		"slug":     "custom-disk-check",
		"skill_md": "---\nname: disk\ndescription: disk\n---\n# body\n",
		"files":    []map[string]string{{"path": "plugin.yaml", "content": validYAML}},
	}, nil)
	if okRec.Code != http.StatusOK {
		t.Fatalf("valid custom L2 upsert = %d (%s)", okRec.Code, okRec.Body.String())
	}
	sk, err := store.GetPlatformSkill(context.Background(), TokenAuth{UserID: "system"}, "custom-disk-check")
	if err != nil {
		t.Fatalf("load custom-disk-check: %v", err)
	}
	m := decodeStoredSkillUIManifest(sk.UIManifestJSON)
	if m == nil || m.Plugin.Icon != defaultPluginIconName {
		t.Fatalf("expected fallback icon %q, got %+v", defaultPluginIconName, m)
	}

	// Admin cannot override custom L2 plugins via /v1/admin/plugins.
	customOv := doPluginRequest(t, mux, http.MethodPost, "/v1/admin/plugins", adminTok, map[string]any{
		"action":            "set_plugin",
		"plugin_id":         "skill.custom-disk-check",
		"enabled":           false,
		"expected_revision": 0,
	}, nil)
	if customOv.Code != http.StatusBadRequest || !strings.Contains(customOv.Body.String(), "invalid_plugin_id") {
		t.Fatalf("override custom L2 = %d (%s), want 400 invalid_plugin_id", customOv.Code, customOv.Body.String())
	}

	// Manual disk edit to plugin.yaml must re-parse on the next read via SHA drift.
	manifestPath := filepath.Join(store.dbDir, "skills", "custom-disk-check", "plugin.yaml")
	updatedYAML := strings.Replace(validYAML, "磁盘自检插件", "磁盘热更新名", 1)
	if err := os.WriteFile(manifestPath, []byte(updatedYAML), 0600); err != nil {
		t.Fatalf("rewrite disk plugin.yaml: %v", err)
	}
	listRec := doPluginRequest(t, mux, http.MethodGet, "/v1/plugins", userTok, nil, nil)
	if !strings.Contains(listRec.Body.String(), "磁盘热更新名") {
		t.Fatalf("disk plugin.yaml edit was not reflected in GET /v1/plugins: %s", listRec.Body.String())
	}
}

func TestPluginCustomL2CrossUserIsolation(t *testing.T) {
	store, _, mux, adminTok, userATok := newPluginTestServer(t)

	userB, err := store.CreateUser("ops-other")
	if err != nil {
		t.Fatalf("create userB: %v", err)
	}
	userBTok, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: userB.ID, Name: "userB-tok"})
	if err != nil {
		t.Fatalf("create userB token: %v", err)
	}

	customYAML := `apiVersion: doops.sh/v1alpha1
kind: SkillPlugin
plugin:
  name: 个人私有插件
  category: ops
ui:
  slots:
    sidebar:
      enabled: true
`
	up := doPluginRequest(t, mux, http.MethodPost, "/v1/skills", userATok, map[string]any{
		"action":   "upsert",
		"slug":     "user-a-private",
		"skill_md": "---\nname: private\ndescription: private\n---\n# private\n",
		"files":    []map[string]string{{"path": "plugin.yaml", "content": customYAML}},
	}, nil)
	if up.Code != http.StatusOK {
		t.Fatalf("userA upsert custom L2 = %d (%s)", up.Code, up.Body.String())
	}

	hasPlugin := func(token, id string) (PluginView, bool) {
		rec := doPluginRequest(t, mux, http.MethodGet, "/v1/plugins", token, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /v1/plugins = %d", rec.Code)
		}
		var state PluginStateResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &state)
		for _, p := range state.Plugins {
			if p.ID == id {
				return p, true
			}
		}
		return PluginView{}, false
	}

	// User A sees own custom L2 with owner_is_me=true.
	if p, ok := hasPlugin(userATok, "skill.user-a-private"); !ok || !p.OwnerIsMe || !p.Effective {
		t.Fatalf("owner User A must see own custom L2, got ok=%v p=%+v", ok, p)
	}
	// User B must NOT see User A's custom L2.
	if _, ok := hasPlugin(userBTok.Plaintext, "skill.user-a-private"); ok {
		t.Fatal("User B must not see User A's custom L2 plugin")
	}
	// Admin's /v1/plugins must NOT be flooded with User A's custom L2 plugin (§3.2 #4).
	if _, ok := hasPlugin(adminTok, "skill.user-a-private"); ok {
		t.Fatal("Admin /v1/plugins must not include another user's custom L2 plugin")
	}
	// Admin can still view User A's custom Skill in GET /v1/skills.
	skillsRec := doPluginRequest(t, mux, http.MethodGet, "/v1/skills", adminTok, nil, nil)
	if !strings.Contains(skillsRec.Body.String(), "user-a-private") {
		t.Fatalf("Admin GET /v1/skills should still see user-a-private")
	}
}

func TestPluginRunInputsValidationAndCredentialRefFailClosed(t *testing.T) {
	store, _, mux, adminTok, userTok := newPluginTestServer(t)
	ctx := context.Background()

	// Locate created users.
	var adminID, userID string
	_ = store.readDB().QueryRow(`SELECT id FROM users WHERE name='admin-plg'`).Scan(&adminID)
	_ = store.readDB().QueryRow(`SELECT id FROM users WHERE name='ops-plg'`).Scan(&userID)

	// Grant ops-plg access to prod/node-a ONLY (not prod/node-b).
	if err := store.GrantUser(userID, ScopeGrant{Cluster: "prod", Instance: "node-a"}); err != nil {
		t.Fatalf("grant prod/node-a: %v", err)
	}

	// Register a system L2 skill that accepts number + select + credential_ref.
	credSkillYAML := `apiVersion: doops.sh/v1alpha1
kind: SkillPlugin
plugin:
  name: 数据库连接检查
  category: data
ui:
  inputs:
    - key: TIMEOUT_SEC
      label: 超时秒数
      type: number
      default: 5
      min: 1
      max: 60
    - key: DB_CRED
      label: 数据库凭据
      type: credential_ref
      required: true
      credential_use: database
`
	up := doPluginRequest(t, mux, http.MethodPost, "/v1/skills", adminTok, map[string]any{
		"action":   "upsert",
		"slug":     "db-conn-check",
		"scope":    "system",
		"skill_md": "---\nname: db-conn-check\ndescription: db check\nentrypoint: scripts/inspect.sh\n---\n# db check\n",
		"files": []map[string]string{
			{"path": "plugin.yaml", "content": credSkillYAML},
			{"path": "scripts/inspect.sh", "content": "#!/usr/bin/env sh\necho '{\"status\":\"pass\"}'\n"},
		},
	}, nil)
	if up.Code != http.StatusOK {
		t.Fatalf("upsert db-conn-check = %d (%s)", up.Code, up.Body.String())
	}

	// 1. Multi-skill + inputs must return 400 inputs_require_single_skill.
	multi := doPluginRequest(t, mux, http.MethodPost, "/v1/monitor/runs", adminTok, map[string]any{
		"skills": []string{"gpu-governance", "host-security"},
		"inputs": map[string]any{"WARN_TEMP_C": 80},
	}, nil)
	if multi.Code != http.StatusBadRequest || !strings.Contains(multi.Body.String(), "inputs_require_single_skill") {
		t.Fatalf("multi-skill inputs = %d (%s), want 400 inputs_require_single_skill", multi.Code, multi.Body.String())
	}

	// 2. Unknown input key returns 400 unknown_plugin_input.
	unk := doPluginRequest(t, mux, http.MethodPost, "/v1/monitor/runs", adminTok, map[string]any{
		"skills": []string{"gpu-governance"},
		"inputs": map[string]any{"UNDECLARED_KEY": 123},
	}, nil)
	if unk.Code != http.StatusBadRequest || !strings.Contains(unk.Body.String(), "unknown_plugin_input") {
		t.Fatalf("unknown input = %d (%s), want 400 unknown_plugin_input", unk.Code, unk.Body.String())
	}

	// 3. Out-of-range number returns 400 invalid_plugin_input.
	oor := doPluginRequest(t, mux, http.MethodPost, "/v1/monitor/runs", adminTok, map[string]any{
		"skills": []string{"gpu-governance"},
		"inputs": map[string]any{"WARN_TEMP_C": 999},
	}, nil)
	if oor.Code != http.StatusBadRequest || !strings.Contains(oor.Body.String(), "invalid_plugin_input") {
		t.Fatalf("out-of-range input = %d (%s), want 400 invalid_plugin_input", oor.Code, oor.Body.String())
	}

	// 4. Inline secret / uppercase / URL in credential_ref rejected as plugin_credential_ref_invalid.
	badRef := doPluginRequest(t, mux, http.MethodPost, "/v1/monitor/runs", adminTok, map[string]any{
		"scope_mode": "targets",
		"targets":    []map[string]string{{"cluster": "prod", "instance": "node-a"}},
		"skills":     []string{"db-conn-check"},
		"inputs":     map[string]any{"DB_CRED": "postgres://user:secret@host/db"},
	}, nil)
	if badRef.Code != http.StatusBadRequest || !strings.Contains(badRef.Body.String(), "plugin_credential_ref_invalid") {
		t.Fatalf("URL credential_ref = %d (%s), want 400 plugin_credential_ref_invalid", badRef.Code, badRef.Body.String())
	}

	// 5. Missing credential_ref fails closed with 400 plugin_credential_ref_unbound.
	missingRef := doPluginRequest(t, mux, http.MethodPost, "/v1/monitor/runs", adminTok, map[string]any{
		"scope_mode": "targets",
		"targets":    []map[string]string{{"cluster": "prod", "instance": "node-a"}},
		"skills":     []string{"db-conn-check"},
		"inputs":     map[string]any{"DB_CRED": "canary-db-cred"},
	}, nil)
	if missingRef.Code != http.StatusBadRequest || !strings.Contains(missingRef.Body.String(), "plugin_credential_ref_unbound") {
		t.Fatalf("missing credential_ref = %d (%s), want 400 plugin_credential_ref_unbound", missingRef.Code, missingRef.Body.String())
	}

	// 6. Seed synthetic canary credential, promote on prod/node-a, verify target binding + zero payload leak + revocation.
	const canarySecret = "CANARY_SECRET_TOKEN_PLG02_NEVER_LEAK"
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", "plg02-synthetic-test-master-key-32b!")
	cred, err := store.CreateCredential(CredentialCreateRequest{
		Name:      "canary-db-cred",
		Scope:     CredentialScopePlatform,
		Type:      CredentialTypeOpaque,
		CreatedBy: adminID,
	})
	if err != nil {
		t.Fatalf("create canary credential: %v", err)
	}
	ver, err := store.PutCredentialVersion(CredentialVersionPutRequest{
		CredentialID: cred.ID,
		Payload:      []byte(`{"data":{"password":"` + canarySecret + `"}}`),
		CreatedBy:    adminID,
	})
	if err != nil {
		t.Fatalf("put staged version: %v", err)
	}
	grant, err := store.CreateCredentialGrant(CredentialGrantCreateRequest{
		CredentialID: cred.ID,
		Scope: CredentialGrantScope{
			Cluster:   "prod",
			Instance:  "node-a",
			Namespace: "default",
			Use:       CredentialUseDatabase,
		},
		CreatedBy: adminID,
	})
	if err != nil {
		t.Fatalf("grant credential: %v", err)
	}

	// Still staged (unpromoted) -> MUST fail closed!
	unpromoted := doPluginRequest(t, mux, http.MethodPost, "/v1/monitor/runs", userTok, map[string]any{
		"scope_mode": "targets",
		"targets":    []map[string]string{{"cluster": "prod", "instance": "node-a"}},
		"skills":     []string{"db-conn-check"},
		"inputs":     map[string]any{"DB_CRED": "canary-db-cred"},
	}, nil)
	if unpromoted.Code != http.StatusBadRequest || !strings.Contains(unpromoted.Body.String(), "plugin_credential_ref_unbound") {
		t.Fatalf("unpromoted credential_ref = %d (%s), want 400 plugin_credential_ref_unbound", unpromoted.Code, unpromoted.Body.String())
	}

	// Mark verified and promote version to active.
	if err := store.RecordCredentialVerification(CredentialVerification{
		CredentialID: cred.ID,
		VersionID:    ver.ID,
		GrantID:      grant.ID,
		Use:          CredentialUseDatabase,
		Status:       "verified",
		Request:      CredentialMaterializeRequest{Scope: grant.Scope},
		Evidence:     CredentialMaterialization{Status: "verified"},
		VerifiedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("record verification: %v", err)
	}
	if err := store.PromoteCredentialVersion(cred.ID, ver.ID, adminID); err != nil {
		t.Fatalf("promote credential: %v", err)
	}

	// Wrong target prod/node-b (no grant on node-b) -> MUST fail closed for admin too!
	wrongTarget := doPluginRequest(t, mux, http.MethodPost, "/v1/monitor/runs", adminTok, map[string]any{
		"scope_mode": "targets",
		"targets":    []map[string]string{{"cluster": "prod", "instance": "node-b"}},
		"skills":     []string{"db-conn-check"},
		"inputs":     map[string]any{"DB_CRED": "canary-db-cred"},
	}, nil)
	if wrongTarget.Code != http.StatusBadRequest || !strings.Contains(wrongTarget.Body.String(), "plugin_credential_ref_unbound") {
		t.Fatalf("wrong target credential_ref = %d (%s), want 400 plugin_credential_ref_unbound", wrongTarget.Code, wrongTarget.Body.String())
	}

	// Non-admin targeting unauthorized prod/node-b -> 403 monitor_target_forbidden.
	forbidTarget := doPluginRequest(t, mux, http.MethodPost, "/v1/monitor/runs", userTok, map[string]any{
		"scope_mode": "targets",
		"targets":    []map[string]string{{"cluster": "prod", "instance": "node-b"}},
		"skills":     []string{"db-conn-check"},
		"inputs":     map[string]any{"DB_CRED": "canary-db-cred"},
	}, nil)
	if forbidTarget.Code != http.StatusForbidden {
		t.Fatalf("unauthorized target = %d (%s), want 403", forbidTarget.Code, forbidTarget.Body.String())
	}

	// Authorized user targeting prod/node-a -> passes validation (offline target records offline item cleanly), zero canary payload anywhere!
	okRun := doPluginRequest(t, mux, http.MethodPost, "/v1/monitor/runs", userTok, map[string]any{
		"scope_mode": "targets",
		"targets":    []map[string]string{{"cluster": "prod", "instance": "node-a"}},
		"skills":     []string{"db-conn-check"},
		"inputs":     map[string]any{"DB_CRED": "canary-db-cred", "TIMEOUT_SEC": 12},
	}, nil)
	if okRun.Code != http.StatusOK {
		t.Fatalf("authorized credential_ref run = %d (%s), want 200", okRun.Code, okRun.Body.String())
	}
	if strings.Contains(okRun.Body.String(), canarySecret) {
		t.Fatal("CANARY SECRET LEAKED IN RUN RESPONSE!")
	}

	// Verify skillScriptCommand quoting & AI prompt block formatting pass name only, never payload.
	cmd := skillScriptCommand("db-conn-check", "scripts/inspect.sh", monitorTargetRef{Cluster: "prod", Instance: "node-a"}, "run_1", map[string]string{
		"DB_CRED":     "canary-db-cred",
		"TIMEOUT_SEC": "12",
	})
	if !strings.Contains(cmd, "export DOOPS_INPUT_DB_CRED='canary-db-cred'") || !strings.Contains(cmd, "export DOOPS_INPUT_TIMEOUT_SEC='12'") {
		t.Fatalf("skillScriptCommand missing DOOPS_INPUT_* exports: %s", cmd)
	}
	prompt := buildSkillPromptInstruction(&platformSkillRecord{Slug: "db-conn-check"}, "检查连接", map[string]string{
		"DB_CRED":     "canary-db-cred",
		"TIMEOUT_SEC": "12",
	})
	if !strings.Contains(prompt, "<<DOOPS_INPUTS>>\nDB_CRED=canary-db-cred\nTIMEOUT_SEC=12\n<<END_DOOPS_INPUTS>>") {
		t.Fatalf("buildSkillPromptInstruction missing structured block: %s", prompt)
	}

	// Revoke grant -> immediately fails closed!
	if _, err := store.RevokeCredentialGrant(cred.ID, grant.ID, adminID); err != nil {
		t.Fatalf("revoke grant: %v", err)
	}
	afterRevoke := doPluginRequest(t, mux, http.MethodPost, "/v1/monitor/runs", userTok, map[string]any{
		"scope_mode": "targets",
		"targets":    []map[string]string{{"cluster": "prod", "instance": "node-a"}},
		"skills":     []string{"db-conn-check"},
		"inputs":     map[string]any{"DB_CRED": "canary-db-cred"},
	}, nil)
	if afterRevoke.Code != http.StatusBadRequest || !strings.Contains(afterRevoke.Body.String(), "plugin_credential_ref_unbound") {
		t.Fatalf("revoked grant credential_ref = %d (%s), want 400 plugin_credential_ref_unbound", afterRevoke.Code, afterRevoke.Body.String())
	}

	// 7. Schedule inputs_json persistence & validation.
	schedCreate := doPluginRequest(t, mux, http.MethodPost, "/v1/monitor/schedules", adminTok, map[string]any{
		"action":           "create",
		"name":             "GPU 定时巡检",
		"scope_mode":       "all_online",
		"skills":           []string{"gpu-governance"},
		"interval_seconds": 900,
		"inputs":           map[string]any{"WARN_TEMP_C": 82, "INCLUDE_PROCESSES": false},
	}, nil)
	if schedCreate.Code != http.StatusOK {
		t.Fatalf("create schedule with inputs = %d (%s)", schedCreate.Code, schedCreate.Body.String())
	}
	var schedResp struct {
		Schedule monitorScheduleRecord `json:"schedule"`
	}
	_ = json.Unmarshal(schedCreate.Body.Bytes(), &schedResp)
	reloaded, err := store.GetMonitorSchedule(ctx, schedResp.Schedule.ID)
	if err != nil {
		t.Fatalf("reload schedule: %v", err)
	}
	if reloaded.Inputs["WARN_TEMP_C"] != float64(82) || reloaded.Inputs["INCLUDE_PROCESSES"] != false {
		t.Fatalf("schedule inputs round-trip mismatch: %+v", reloaded.Inputs)
	}
}

func TestMonitorMetricsLatestAggregationAndAuthScoping(t *testing.T) {
	store, _, mux, adminTok, userTok := newPluginTestServer(t)
	ctx := context.Background()

	var userID string
	_ = store.readDB().QueryRow(`SELECT id FROM users WHERE name='ops-plg'`).Scan(&userID)
	if err := store.GrantUser(userID, ScopeGrant{Cluster: "prod", Instance: "gpu-1"}); err != nil {
		t.Fatalf("grant prod/gpu-1: %v", err)
	}

	now := time.Now().UnixMilli()
	insertPoint := func(atMs int64, cluster, instance, skill, key, unit string, val float64, status string) {
		_, err := store.db.ExecContext(ctx, `INSERT INTO inspection_metric_points(
			collected_at_ms, run_id, schedule_id, cluster, instance, skill_slug, metric_key, metric_label, value, unit, status
		) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, atMs, "run_x", "", cluster, instance, skill, key, key, val, unit, status)
		if err != nil {
			t.Fatalf("insert metric point: %v", err)
		}
	}

	// Older point on prod/gpu-1 must be superseded by newer point.
	insertPoint(now-60_000, "prod", "gpu-1", "gpu-governance", "gpu_util_max_pct", "%", 99.0, "critical")
	insertPoint(now-10_000, "prod", "gpu-1", "gpu-governance", "gpu_util_max_pct", "%", 40.0, "pass")
	// Second target prod/gpu-2 (unauthorized for userTok).
	insertPoint(now-5_000, "prod", "gpu-2", "gpu-governance", "gpu_util_max_pct", "%", 80.0, "warning")
	// Third target secret/gpu-3 (unauthorized for userTok).
	insertPoint(now-2_000, "secret", "gpu-3", "host-security", "sec_risk_total", "项", 5.0, "critical")

	// Admin sees aggregated max=80, avg=60 across 2 targets for gpu_util_max_pct, plus sec_risk_total=5.
	adminRec := doPluginRequest(t, mux, http.MethodGet, "/v1/monitor/metrics/latest?skills=gpu-governance,host-security&keys=gpu_util_max_pct,sec_risk_total", adminTok, nil, nil)
	if adminRec.Code != http.StatusOK {
		t.Fatalf("admin latest metrics = %d (%s)", adminRec.Code, adminRec.Body.String())
	}
	var adminResp monitorLatestMetricsResponse
	_ = json.Unmarshal(adminRec.Body.Bytes(), &adminResp)
	if len(adminResp.Items) != 2 {
		t.Fatalf("admin expected 2 aggregated items, got %+v", adminResp.Items)
	}
	if adminResp.Items[0].Skill != "gpu-governance" || adminResp.Items[0].Max != 80 || adminResp.Items[0].Avg != 60 || adminResp.Items[0].ReportingTargets != 2 {
		t.Fatalf("unexpected admin gpu aggregation: %+v", adminResp.Items[0])
	}

	// Non-admin sees ONLY prod/gpu-1 (max=40, avg=40, reporting=1) and ZERO secret/gpu-3 data!
	userRec := doPluginRequest(t, mux, http.MethodGet, "/v1/monitor/metrics/latest?skills=gpu-governance,host-security&keys=gpu_util_max_pct,sec_risk_total", userTok, nil, nil)
	if userRec.Code != http.StatusOK {
		t.Fatalf("user latest metrics = %d (%s)", userRec.Code, userRec.Body.String())
	}
	var userResp monitorLatestMetricsResponse
	_ = json.Unmarshal(userRec.Body.Bytes(), &userResp)
	if len(userResp.Items) != 1 || userResp.Items[0].Max != 40 || userResp.Items[0].ReportingTargets != 1 {
		t.Fatalf("user must only see authorized prod/gpu-1 metric, got %+v", userResp.Items)
	}

	// Pair-scoped batch query (?pairs=skill:key,...) with zero cross-skill starvation:
	// flood gpu-governance with 600 points and verify low-frequency attack-surface-scan still resolves cleanly.
	insertPoint(now-3_600_000, "prod", "gpu-1", "attack-surface-scan", "surface_risk_total", "项", 2.0, "warning")
	for i := 0; i < 600; i++ {
		insertPoint(now-int64(i)*100, "prod", "gpu-1", "gpu-governance", "gpu_util_max_pct", "%", 42.0, "pass")
	}
	pairRec := doPluginRequest(t, mux, http.MethodGet,
		"/v1/monitor/metrics/latest?pairs=gpu-governance:gpu_util_max_pct,attack-surface-scan:surface_risk_total",
		adminTok, nil, nil)
	if pairRec.Code != http.StatusOK {
		t.Fatalf("pairs query = %d (%s)", pairRec.Code, pairRec.Body.String())
	}
	var pairResp monitorLatestMetricsResponse
	_ = json.Unmarshal(pairRec.Body.Bytes(), &pairResp)
	if len(pairResp.Items) != 2 || pairResp.Batch == nil || pairResp.Batch.RequestedPairs != 2 {
		t.Fatalf("pairs query must preserve low-frequency skill without starvation: %+v", pairResp)
	}

	// Exceeding 24 skills, 96 keys, or 64 pairs returns 400.
	tooManySkillsList := make([]string, 25)
	for i := range tooManySkillsList {
		tooManySkillsList[i] = fmt.Sprintf("s-%02d", i)
	}
	tooManySkills := doPluginRequest(t, mux, http.MethodGet, "/v1/monitor/metrics/latest?skills="+strings.Join(tooManySkillsList, ",")+"&keys=gpu_count", adminTok, nil, nil)
	if tooManySkills.Code != http.StatusBadRequest {
		t.Fatalf(">24 skills = %d, want 400", tooManySkills.Code)
	}
	tooManyKeysList := make([]string, 97)
	for i := range tooManyKeysList {
		tooManyKeysList[i] = fmt.Sprintf("k_%03d", i)
	}
	tooManyKeys := doPluginRequest(t, mux, http.MethodGet, "/v1/monitor/metrics/latest?skills=gpu-governance&keys="+strings.Join(tooManyKeysList, ","), adminTok, nil, nil)
	if tooManyKeys.Code != http.StatusBadRequest {
		t.Fatalf(">96 keys = %d, want 400", tooManyKeys.Code)
	}
	tooManyPairsList := make([]string, 65)
	for i := range tooManyPairsList {
		tooManyPairsList[i] = fmt.Sprintf("gpu-governance:k_%03d", i)
	}
	tooManyPairs := doPluginRequest(t, mux, http.MethodGet, "/v1/monitor/metrics/latest?pairs="+strings.Join(tooManyPairsList, ","), adminTok, nil, nil)
	if tooManyPairs.Code != http.StatusBadRequest {
		t.Fatalf(">64 pairs = %d, want 400", tooManyPairs.Code)
	}
}

func TestPluginL2PlusManifestValidationAndCatalogInjection(t *testing.T) {
	store, hub, mux, adminTok, _ := newPluginTestServer(t)
	ctx := context.Background()

	// 1. Reject invalid L2+ manifest fields cleanly with 400 invalid_plugin_manifest.
	badL2Plus := []struct {
		name string
		yaml string
	}{
		{
			name: "reject_unknown_catalog_input",
			yaml: "apiVersion: doops.sh/v1alpha1\nkind: SkillPlugin\nplugin:\n  name: Bad Catalog\ncapabilities:\n  catalog_inputs:\n    - arbitrary_secrets\nui: {}\n",
		},
		{
			name: "reject_resource_table_unknown_row_action",
			yaml: "apiVersion: doops.sh/v1alpha1\nkind: SkillPlugin\nplugin:\n  name: Bad Row Action\nui:\n  views:\n    - type: resource_table\n      title: 资源\n      source_key: resources\n      row_action_id: nonexistent\n      columns:\n        - key: name\n          label: 名称\n",
		},
		{
			name: "reject_columns_on_non_resource_table",
			yaml: "apiVersion: doops.sh/v1alpha1\nkind: SkillPlugin\nplugin:\n  name: Bad Columns\nui:\n  views:\n    - type: findings_list\n      title: 风险\n      columns:\n        - key: id\n          label: ID\n",
		},
		{
			name: "reject_removed_core_dns_zones_input",
			yaml: "apiVersion: doops.sh/v1alpha1\nkind: SkillPlugin\nplugin:\n  name: Removed Core DNS\ncapabilities:\n  catalog_inputs:\n    - dns_zones\nui: {}\n",
		},
		{
			name: "reject_removed_core_mail_domains_input",
			yaml: "apiVersion: doops.sh/v1alpha1\nkind: SkillPlugin\nplugin:\n  name: Removed Core Mail\ncapabilities:\n  catalog_inputs:\n    - mail_domains\nui: {}\n",
		},
		{
			name: "reject_invalid_action_mode",
			yaml: "apiVersion: doops.sh/v1alpha1\nkind: SkillPlugin\nplugin:\n  name: Bad Action Mode\nui:\n  actions:\n    - id: act-1\n      label: Exec\n      mode: shell\n      prompt_template: run\n",
		},
	}
	for _, tc := range badL2Plus {
		rec := doPluginRequest(t, mux, http.MethodPost, "/v1/skills", adminTok, map[string]any{
			"action":   "upsert",
			"slug":     "bad-l2plus-case",
			"name":     tc.name,
			"skill_md": "---\nname: bad\ndescription: bad\n---\n# bad",
			"files":    []map[string]string{{"path": "plugin.yaml", "content": tc.yaml}},
		}, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d (%s)", tc.name, rec.Code, rec.Body.String())
		}
	}

	// 2. Core catalog rejects legacy hardcoded dns_zone/mail_domain and retains Core website/database/image injection.
	if _, ok := catalogIdentity(catalogRow{Kind: "dns_zone", Details: map[string]string{"zone": "corp.example.com", "server": "203.0.113.53", "view": "default"}}); ok {
		t.Fatal("Core catalogIdentity must reject decoupled legacy dns_zone kind")
	}
	if _, ok := catalogIdentity(catalogRow{Kind: "mail_domain", Details: map[string]string{"domain": "mail.example.com", "mx_host": "mx1.example.com", "dkim_selector": "default"}}); ok {
		t.Fatal("Core catalogIdentity must reject decoupled legacy mail_domain kind")
	}
	_, err := store.db.ExecContext(ctx,
		`INSERT INTO inventory_resource_catalog(id,kind,cluster,instance,created_by,name,environment,details,identity_key,revision) VALUES
		('web-1','website','prod','node-1','admin','Portal','prod','{"scheme":"https","domain":"app.example.com","path":"/"}','["https","app.example.com","/"]',1),
		('web-2','website','prod','node-1','admin','Admin','prod','{"scheme":"https","domain":"admin.example.com","path":"/login"}','["https","admin.example.com","/login"]',1),
		('db-1','database','prod','node-1','admin','Orders DB','prod','{"engine":"PostgreSQL","host":"pg.internal","port":"5432","database":"orders"}','["PostgreSQL","pg.internal","5432","orders"]',1)`)
	if err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
	tgt := monitorTargetRef{Cluster: "prod", Instance: "node-1"}

	surfSkill, err := store.GetPlatformSkill(ctx, TokenAuth{UserID: "system"}, "attack-surface-scan")
	if err != nil {
		t.Fatalf("get attack-surface-scan: %v", err)
	}
	surfEnv, uCount, dCount := hub.skillScriptExtraEnvForSkill(ctx, surfSkill, tgt)
	if uCount != 2 || dCount != 0 || !strings.Contains(surfEnv["DOOPS_INSPECT_URLS"], "https://app.example.com/") {
		t.Fatalf("attack-surface-scan catalog injection mismatch: env=%v u=%d d=%d", surfEnv, uCount, dCount)
	}

	dbEnv, dbCounts := hub.resolveCatalogInputsForTarget(ctx, "prod", "node-1", []string{"database_refs"})
	if dbCounts["catalog_databases_count"] != 1 || dbEnv["DOOPS_CATALOG_DATABASES"] != "postgresql://pg.internal:5432/orders" {
		t.Fatalf("database_refs generic catalog injection mismatch: env=%v counts=%v", dbEnv, dbCounts)
	}

	// Custom skills MUST NEVER receive automatic catalog_inputs injection even if declared in plugin.yaml.
	customSkill := surfSkill
	customSkill.Scope = "custom"
	customEnv, cu, cd := hub.skillScriptExtraEnvForSkill(ctx, customSkill, tgt)
	if len(customEnv) != 0 || cu != 0 || cd != 0 {
		t.Fatalf("custom skill must not receive catalog injection, got env=%v u=%d d=%d", customEnv, cu, cd)
	}
}
