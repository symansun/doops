package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

const (
	defaultPluginScenarioID = "all-in-one"
	pluginConfigRowID       = "default"
)

var (
	builtinPluginIDPattern = regexp.MustCompile(`^doops\.[a-z][a-z0-9-]{1,30}$`)
	skillPluginIDPattern   = regexp.MustCompile(`^skill\.[a-z0-9][a-z0-9-]{0,62}$`)
	pluginScenarioPattern  = regexp.MustCompile(`^[a-z][a-z0-9-]{1,40}$`)
	pluginActionPattern    = regexp.MustCompile(`^(apply_scenario|set_plugin|clear_override|reset)$`)
)

// pluginCoreEntryIDs lists the fixed Core Shell entries that cannot be toggled
// off or stored in platform_plugin_overrides.
var pluginCoreEntryIDs = []string{
	"home",
	"overview",
	"ask",
	"terminal",
	"kb",
	"credentials",
	"audit",
	"plugin-center",
	"deploy-onboard",
	"deploy-upgrade",
	"platform-admin",
}

// PluginScenarioDefinition is one built-in scenario preset.
type PluginScenarioDefinition struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Defaults    []string `json:"defaults"`
}

// BuiltinPluginDefinition is one first-party L1 frontend plugin.
type BuiltinPluginDefinition struct {
	ID                 string   `json:"id"`
	Tier               string   `json:"tier"` // always "builtin"
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	Category           string   `json:"category"` // observability | infrastructure | delivery | data | ops | security
	NavIDs             []string `json:"nav_ids"`
	TargetTabs         []string `json:"target_tabs"`
	RequiredSkills     []string `json:"required_skills"`
	RequiredMCP        []string `json:"required_mcp"`
	RequiresKubernetes bool     `json:"requires_kubernetes,omitempty"`
}

// PluginCatalogExport is the single source of truth exported to
// lib/plugins/builtin-catalog.json and verified by TestPluginCatalogJSONInSync.
type PluginCatalogExport struct {
	Version   int                        `json:"version"`
	CoreIDs   []string                   `json:"core_ids"`
	Scenarios []PluginScenarioDefinition `json:"scenarios"`
	Plugins   []BuiltinPluginDefinition  `json:"plugins"`
}

// PluginDependencyState reports whether a referenced Skill or MCP is available
// and enabled. It is advisory only and never blocks enabling a UI plugin.
type PluginDependencyState struct {
	Slug  string `json:"slug"`
	State string `json:"state"` // ready | disabled | missing
}

type PluginDependenciesView struct {
	Skills []PluginDependencyState `json:"skills"`
	MCP    []PluginDependencyState `json:"mcp"`
}

// PluginView is the resolved per-plugin state returned by GET /v1/plugins.
type PluginView struct {
	ID                 string                 `json:"id"`
	Tier               string                 `json:"tier"` // builtin | declarative
	SkillSlug          string                 `json:"skill_slug,omitempty"`
	Scope              string                 `json:"scope,omitempty"`       // system | custom (declarative only)
	Builtin            bool                   `json:"builtin,omitempty"`     // true for Core built-in L1/L2 plugins
	CanDelete          bool                   `json:"can_delete,omitempty"`  // true when caller may uninstall this external/custom plugin
	OwnerIsMe          bool                   `json:"owner_is_me,omitempty"` // declarative custom only
	SkillEnabled       *bool                  `json:"skill_enabled,omitempty"`
	Name               string                 `json:"name"`
	Description        string                 `json:"description"`
	Category           string                 `json:"category"`
	NavIDs             []string               `json:"nav_ids,omitempty"`
	TargetTabs         []string               `json:"target_tabs,omitempty"`
	RequiresKubernetes bool                   `json:"requires_kubernetes,omitempty"`
	Effective          bool                   `json:"effective"`
	ScenarioDefault    bool                   `json:"scenario_default"`
	Override           *bool                  `json:"override"`
	Dependencies       PluginDependenciesView `json:"dependencies"`
	Manifest           *SkillPluginManifest   `json:"manifest,omitempty"`
	ManifestError      string                 `json:"manifest_error,omitempty"`
}

// PluginStateResponse is returned by GET /v1/plugins.
type PluginStateResponse struct {
	Supported      bool                       `json:"supported"`
	Configured     bool                       `json:"configured"`
	ScenarioID     string                     `json:"scenario_id"`
	Revision       int64                      `json:"revision"`
	Customized     bool                       `json:"customized"`
	UpdatedBy      string                     `json:"updated_by"`
	UpdatedAtMs    int64                      `json:"updated_at_ms"`
	Scenarios      []PluginScenarioDefinition `json:"scenarios"`
	Plugins        []PluginView               `json:"plugins"`
	StaleOverrides []string                   `json:"stale_overrides"`
}

// PluginAffectedSchedule summarizes an active schedule whose skills overlap
// with a hidden plugin. Schedules keep running; this notice is informational.
type PluginAffectedSchedule struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Skills []string `json:"skills"`
	Note   string   `json:"note"`
}

// PluginMutationDiff summarizes what changes a scenario switch or toggle makes.
type PluginMutationDiff struct {
	Show              []string                 `json:"show"`
	Hide              []string                 `json:"hide"`
	DroppedOverrides  []string                 `json:"dropped_overrides"`
	AffectedSchedules []PluginAffectedSchedule `json:"affected_schedules"`
}

// PluginMutationResponse is returned by POST /v1/admin/plugins.
type PluginMutationResponse struct {
	DryRun     bool               `json:"dry_run"`
	Revision   int64              `json:"revision"`
	ScenarioID string             `json:"scenario_id"`
	Customized bool               `json:"customized"`
	Diff       PluginMutationDiff `json:"diff"`
}

func builtinPluginCatalog() PluginCatalogExport {
	return PluginCatalogExport{
		Version: 1,
		CoreIDs: append([]string(nil), pluginCoreEntryIDs...),
		Scenarios: []PluginScenarioDefinition{
			{
				ID:          "all-in-one",
				Name:        "全功能模式",
				Description: "启用全部内置功能插件，适合综合运维团队与存量实例平滑升级。",
				Defaults: []string{
					"doops.inspection",
					"doops.k8s",
					"doops.delivery",
					"doops.database",
					"doops.website",
					"doops.image",
					"doops.cmdb",
					"doops.automation",
				},
			},
			{
				ID:          "ai-compute-cluster",
				Name:        "AI 算力 / 高校集群",
				Description: "聚焦 GPU 与 K8s 算力集群巡检、态势感知、机器台账与安全治理，隐藏低频业务交付入口。",
				Defaults: []string{
					"doops.inspection",
					"doops.k8s",
					"doops.cmdb",
					"doops.automation",
				},
			},
			{
				ID:          "app-delivery-web",
				Name:        "应用交付与 Web 运维",
				Description: "覆盖 Kubernetes、工作负载发布、数据库、网站、镜像与自动化巡检的全链路业务运维视图。",
				Defaults: []string{
					"doops.inspection",
					"doops.k8s",
					"doops.delivery",
					"doops.database",
					"doops.website",
					"doops.image",
					"doops.automation",
				},
			},
			{
				ID:          "host-security-ops",
				Name:        "主机与安全合规运维",
				Description: "面向纯物理机/虚机机群的资源监控、补丁合规与安全巡检，彻底隐藏 K8s 与镜像模块。",
				Defaults: []string{
					"doops.inspection",
					"doops.cmdb",
					"doops.automation",
				},
			},
			{
				ID:          "minimal-workbench",
				Name:        "极简 AI + 终端工作台",
				Description: "仅保留内核能力（首页、AI 助手、终端、知识库、凭据、审计与插件中心），零冗余菜单。",
				Defaults:    []string{},
			},
		},
		Plugins: []BuiltinPluginDefinition{
			{
				ID:             "doops.inspection",
				Tier:           "builtin",
				Name:           "态势感知与监控",
				Description:    "态势感知大盘、全网与单目标资源及阈值告警监控与 Agent 运行监控。",
				Category:       "observability",
				NavIDs:         []string{"situational-awareness", "resource-monitor", "platform-monitor"},
				TargetTabs:     []string{"monitor"},
				RequiredSkills: []string{"inspect-vm-host"},
				RequiredMCP:    []string{},
			},
			{
				ID:                 "doops.k8s",
				Tier:               "builtin",
				Name:               "Kubernetes 治理",
				Description:        "集群资产台账与目标 Kubernetes 工作负载、Pod、事件和命名空间操作。",
				Category:           "infrastructure",
				NavIDs:             []string{"k8s"},
				TargetTabs:         []string{"k8s"},
				RequiredSkills:     []string{"inspect-k8s-cluster"},
				RequiredMCP:        []string{"doops"},
				RequiresKubernetes: true,
			},
			{
				ID:             "doops.delivery",
				Tier:           "builtin",
				Name:           "应用发布与交付",
				Description:    "工作负载发布、目标部署包管理与 CI/CD 接入指引。",
				Category:       "delivery",
				NavIDs:         []string{"deploy-workload", "files", "deploy-cicd"},
				TargetTabs:     []string{"files"},
				RequiredSkills: []string{},
				RequiredMCP:    []string{"doops"},
			},
			{
				ID:             "doops.database",
				Tier:           "builtin",
				Name:           "数据库治理",
				Description:    "受管数据库资产目录与 DoSql 查询、Schema 对比、迁移工作台。",
				Category:       "data",
				NavIDs:         []string{"database"},
				TargetTabs:     []string{"database"},
				RequiredSkills: []string{"inspect-database"},
				RequiredMCP:    []string{"dosql"},
			},
			{
				ID:             "doops.website",
				Tier:           "builtin",
				Name:           "网站与服务",
				Description:    "Web 站点与域名资产目录、DoWeb 服务诊断与站点健康检查。",
				Category:       "ops",
				NavIDs:         []string{"website"},
				TargetTabs:     []string{"website"},
				RequiredSkills: []string{"inspect-app-web"},
				RequiredMCP:    []string{"doops"},
			},
			{
				ID:             "doops.image",
				Tier:           "builtin",
				Name:           "镜像构建与仓库",
				Description:    "容器镜像目录、镜像构建任务与容器运行时巡检。",
				Category:       "delivery",
				NavIDs:         []string{"image"},
				TargetTabs:     []string{"image"},
				RequiredSkills: []string{"inspect-container-runtime"},
				RequiredMCP:    []string{},
			},
			{
				ID:             "doops.cmdb",
				Tier:           "builtin",
				Name:           "资产台账（CMDB）",
				Description:    "机器台账、业务系统拓扑台账及 Agent 概览中的台账分配范围。",
				Category:       "infrastructure",
				NavIDs:         []string{"ledger-machines", "ledger-systems"},
				TargetTabs:     []string{},
				RequiredSkills: []string{"inspect-vm-host"},
				RequiredMCP:    []string{},
			},
			{
				ID:             "doops.automation",
				Tier:           "builtin",
				Name:           "自动化与巡检",
				Description:    "统一管理定时任务与 Skill 智能巡检、执行记录与报告，以及一次性后台执行（exec --bg）。",
				Category:       "ops",
				NavIDs:         []string{"tasks"},
				TargetTabs:     []string{"tasks"},
				RequiredSkills: []string{},
				RequiredMCP:    []string{},
			},
		},
	}
}

func marshalBuiltinPluginCatalogJSON() ([]byte, error) {
	raw, err := json.MarshalIndent(builtinPluginCatalog(), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func findPluginScenario(scenarioID string) (PluginScenarioDefinition, bool) {
	for _, sc := range builtinPluginCatalog().Scenarios {
		if sc.ID == scenarioID {
			return sc, true
		}
	}
	return PluginScenarioDefinition{}, false
}

func findBuiltinPlugin(pluginID string) (BuiltinPluginDefinition, bool) {
	for _, p := range builtinPluginCatalog().Plugins {
		if p.ID == pluginID {
			return p, true
		}
	}
	return BuiltinPluginDefinition{}, false
}

func isPluginCoreEntryID(id string) bool {
	for _, core := range pluginCoreEntryIDs {
		if core == id {
			return true
		}
	}
	return false
}

func scenarioDefaultSet(scenarioID string) map[string]bool {
	sc, ok := findPluginScenario(scenarioID)
	if !ok {
		sc, _ = findPluginScenario(defaultPluginScenarioID)
	}
	out := make(map[string]bool, len(sc.Defaults))
	for _, id := range sc.Defaults {
		out[id] = true
	}
	return out
}

func resolveBuiltinPluginEffective(scenarioID string, overrides map[string]bool) map[string]bool {
	defaults := scenarioDefaultSet(scenarioID)
	out := make(map[string]bool, len(builtinPluginCatalog().Plugins))
	for _, p := range builtinPluginCatalog().Plugins {
		if v, ok := overrides[p.ID]; ok {
			out[p.ID] = v
		} else {
			out[p.ID] = defaults[p.ID]
		}
	}
	return out
}

func buildPluginStateResponse(
	cfg pluginConfigRecord,
	overrides map[string]bool,
	skills []platformSkillRecord,
	mcps []platformMCPServerRecord,
	callerUserID string,
	isAdmin bool,
) PluginStateResponse {
	catalog := builtinPluginCatalog()
	scenarioID := cfg.ScenarioID
	if _, ok := findPluginScenario(scenarioID); !ok {
		scenarioID = defaultPluginScenarioID
	}
	defaults := scenarioDefaultSet(scenarioID)

	skillState := make(map[string]string, len(skills))
	for _, sk := range skills {
		if sk.Enabled {
			skillState[sk.Slug] = "ready"
		} else {
			skillState[sk.Slug] = "disabled"
		}
	}
	mcpState := make(map[string]string, len(mcps))
	for _, m := range mcps {
		if m.Enabled {
			mcpState[m.Slug] = "ready"
		} else {
			mcpState[m.Slug] = "disabled"
		}
	}

	knownPlugins := make(map[string]bool, len(catalog.Plugins)+len(skills))
	plugins := make([]PluginView, 0, len(catalog.Plugins)+len(skills))
	activeOverrideCount := 0
	for _, p := range catalog.Plugins {
		knownPlugins[p.ID] = true
		def := defaults[p.ID]
		effective := def
		var ov *bool
		if v, ok := overrides[p.ID]; ok {
			effective = v
			val := v
			ov = &val
			if v != def {
				activeOverrideCount++
			}
		}

		skillDeps := make([]PluginDependencyState, 0, len(p.RequiredSkills))
		for _, slug := range p.RequiredSkills {
			st := skillState[slug]
			if st == "" {
				st = "missing"
			}
			skillDeps = append(skillDeps, PluginDependencyState{Slug: slug, State: st})
		}
		mcpDeps := make([]PluginDependencyState, 0, len(p.RequiredMCP))
		for _, slug := range p.RequiredMCP {
			st := mcpState[slug]
			if st == "" {
				st = "missing"
			}
			mcpDeps = append(mcpDeps, PluginDependencyState{Slug: slug, State: st})
		}

		plugins = append(plugins, PluginView{
			ID:                 p.ID,
			Tier:               p.Tier,
			Builtin:            true,
			Name:               p.Name,
			Description:        p.Description,
			Category:           p.Category,
			NavIDs:             append([]string(nil), p.NavIDs...),
			TargetTabs:         append([]string(nil), p.TargetTabs...),
			RequiresKubernetes: p.RequiresKubernetes,
			Effective:          effective,
			ScenarioDefault:    def,
			Override:           ov,
			Dependencies: PluginDependenciesView{
				Skills: skillDeps,
				MCP:    mcpDeps,
			},
		})
	}

	// Phase 2: append declarative L2 Skill plugins (plugin.yaml).
	// System L2 plugins participate in scenario defaults & platform_plugin_overrides.
	// Custom L2 plugins are scoped strictly to owner_is_me so other users' custom
	// plugins never pollute an admin's sidebar or /v1/plugins response.
	for _, sk := range skills {
		if strings.TrimSpace(sk.UIManifestJSON) == "" && strings.TrimSpace(sk.UIManifestError) == "" {
			continue
		}
		ownerIsMe := callerUserID != "" && sk.OwnerID == callerUserID
		if sk.Scope != "system" && !ownerIsMe {
			continue
		}
		pluginID := "skill." + sk.Slug
		manifest := decodeStoredSkillUIManifest(sk.UIManifestJSON)
		manifestErr := strings.TrimSpace(sk.UIManifestError)
		if manifest == nil && manifestErr == "" {
			manifestErr = "ui_manifest_json 解析失败"
		}
		skillEnabled := sk.Enabled

		var def, effective bool
		var ov *bool
		if sk.Scope == "system" {
			if manifest != nil {
				knownPlugins[pluginID] = true
			}
			def = skillPluginScenarioDefault(manifest, scenarioID)
			slotEnabled := def
			if v, ok := overrides[pluginID]; ok && manifest != nil {
				slotEnabled = v
				val := v
				ov = &val
				if v != def {
					activeOverrideCount++
				}
			}
			effective = sk.Enabled && manifest != nil && slotEnabled
		} else {
			def = true
			effective = sk.Enabled && manifest != nil
		}

		name := sk.Name
		desc := sk.Description
		cat := sk.Category
		skillDeps := []PluginDependencyState{{Slug: sk.Slug, State: skillState[sk.Slug]}}
		mcpDeps := []PluginDependencyState{}
		if manifest != nil {
			name = manifest.Plugin.Name
			if manifest.Plugin.Description != "" {
				desc = manifest.Plugin.Description
			}
			if manifest.Plugin.Category != "" {
				cat = manifest.Plugin.Category
			}
			seenDepSkill := map[string]bool{sk.Slug: true}
			for _, depSlug := range manifest.Capabilities.RequiresSkills {
				if seenDepSkill[depSlug] {
					continue
				}
				seenDepSkill[depSlug] = true
				st := skillState[depSlug]
				if st == "" {
					st = "missing"
				}
				skillDeps = append(skillDeps, PluginDependencyState{Slug: depSlug, State: st})
			}
			seenDepMCP := map[string]bool{}
			for _, depMCP := range manifest.Capabilities.RequiresMCP {
				if seenDepMCP[depMCP] {
					continue
				}
				seenDepMCP[depMCP] = true
				st := mcpState[depMCP]
				if st == "" {
					st = "missing"
				}
				mcpDeps = append(mcpDeps, PluginDependencyState{Slug: depMCP, State: st})
			}
			if manifest.MCPServer != nil && manifest.MCPServer.Slug != "" && !seenDepMCP[manifest.MCPServer.Slug] {
				st := mcpState[manifest.MCPServer.Slug]
				if st == "" {
					st = "missing"
				}
				mcpDeps = append(mcpDeps, PluginDependencyState{Slug: manifest.MCPServer.Slug, State: st})
			}
		}

		isBuiltin := sk.Scope == "system" && isBuiltinSystemSkill(sk.Slug)
		canDelete := (sk.Scope == "custom" && (ownerIsMe || isAdmin)) || (sk.Scope == "system" && isAdmin && !isBuiltin)

		plugins = append(plugins, PluginView{
			ID:              pluginID,
			Tier:            "declarative",
			SkillSlug:       sk.Slug,
			Scope:           sk.Scope,
			Builtin:         isBuiltin,
			CanDelete:       canDelete,
			OwnerIsMe:       ownerIsMe,
			SkillEnabled:    &skillEnabled,
			Name:            name,
			Description:     desc,
			Category:        cat,
			Effective:       effective,
			ScenarioDefault: def,
			Override:        ov,
			Dependencies: PluginDependenciesView{
				Skills: skillDeps,
				MCP:    mcpDeps,
			},
			Manifest:      manifest,
			ManifestError: manifestErr,
		})
	}

	stale := make([]string, 0)
	if isAdmin {
		for id := range overrides {
			if !knownPlugins[id] {
				stale = append(stale, id)
			}
		}
		sort.Strings(stale)
	}

	return PluginStateResponse{
		Supported:      true,
		Configured:     cfg.Configured,
		ScenarioID:     scenarioID,
		Revision:       cfg.Revision,
		Customized:     activeOverrideCount > 0,
		UpdatedBy:      cfg.UpdatedBy,
		UpdatedAtMs:    cfg.UpdatedAtMs,
		Scenarios:      catalog.Scenarios,
		Plugins:        plugins,
		StaleOverrides: stale,
	}
}

func computePluginStateETag(resp PluginStateResponse, isAdmin bool) string {
	h := sha256.New()
	if isAdmin {
		_, _ = h.Write([]byte("admin|"))
	} else {
		_, _ = h.Write([]byte("user|"))
	}
	raw, _ := json.Marshal(resp)
	_, _ = h.Write(raw)
	sum := hex.EncodeToString(h.Sum(nil))[:16]
	return `"plugins-r` + formatInt64(resp.Revision) + `-u` + sum + `"`
}

func formatInt64(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [24]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func computePluginDiff(
	beforeScenario string,
	beforeOverrides map[string]bool,
	afterScenario string,
	afterOverrides map[string]bool,
	schedules []monitorScheduleRecord,
	systemL2 map[string]*SkillPluginManifest,
) PluginMutationDiff {
	beforeEff := resolveBuiltinPluginEffective(beforeScenario, beforeOverrides)
	afterEff := resolveBuiltinPluginEffective(afterScenario, afterOverrides)

	show := make([]string, 0)
	hide := make([]string, 0)
	for _, p := range builtinPluginCatalog().Plugins {
		if !beforeEff[p.ID] && afterEff[p.ID] {
			show = append(show, p.ID)
		} else if beforeEff[p.ID] && !afterEff[p.ID] {
			hide = append(hide, p.ID)
		}
	}

	l2IDs := make([]string, 0, len(systemL2))
	for id := range systemL2 {
		l2IDs = append(l2IDs, id)
	}
	sort.Strings(l2IDs)
	for _, id := range l2IDs {
		m := systemL2[id]
		beforeVal := skillPluginScenarioDefault(m, beforeScenario)
		if v, ok := beforeOverrides[id]; ok {
			beforeVal = v
		}
		afterVal := skillPluginScenarioDefault(m, afterScenario)
		if v, ok := afterOverrides[id]; ok {
			afterVal = v
		}
		if !beforeVal && afterVal {
			show = append(show, id)
		} else if beforeVal && !afterVal {
			hide = append(hide, id)
		}
	}

	dropped := make([]string, 0)
	for id := range beforeOverrides {
		if _, still := afterOverrides[id]; !still {
			dropped = append(dropped, id)
		}
	}
	sort.Strings(dropped)

	// Identify active schedules whose skills belong to a plugin being hidden.
	// Schedules are never auto-disabled; this is strictly an informational hint.
	hiddenSkills := make(map[string]bool)
	for _, id := range hide {
		if p, ok := findBuiltinPlugin(id); ok {
			for _, slug := range p.RequiredSkills {
				hiddenSkills[slug] = true
			}
			// Also cover all schedules when hiding doops.inspection or doops.automation.
			if id == "doops.inspection" || id == "doops.automation" {
				hiddenSkills["*"] = true
			}
		} else if slug, ok := strings.CutPrefix(id, "skill."); ok && slug != "" {
			hiddenSkills[slug] = true
		}
	}

	affected := make([]PluginAffectedSchedule, 0)
	if len(hiddenSkills) > 0 {
		for _, sched := range schedules {
			if !sched.Enabled {
				continue
			}
			matched := make([]string, 0)
			if hiddenSkills["*"] {
				matched = append(matched, sched.Skills...)
			} else {
				for _, sk := range sched.Skills {
					if hiddenSkills[strings.TrimSpace(sk)] {
						matched = append(matched, sk)
					}
				}
			}
			if len(matched) > 0 || (hiddenSkills["*"] && len(sched.Skills) == 0) {
				affected = append(affected, PluginAffectedSchedule{
					ID:     sched.ID,
					Name:   sched.Name,
					Skills: matched,
					Note:   "定时计划将继续按原周期运行，不会被自动停用或删除",
				})
			}
		}
	}

	return PluginMutationDiff{
		Show:              show,
		Hide:              hide,
		DroppedOverrides:  dropped,
		AffectedSchedules: affected,
	}
}
