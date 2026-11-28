package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	pluginManifestFileName   = "plugin.yaml"
	pluginManifestAPIVersion = "doops.sh/v1alpha1"
	pluginManifestKind       = "SkillPlugin"
	maxPluginManifestBytes   = 32 * 1024
	maxPluginInputs          = 16
	maxPluginSelectOptions   = 32
	maxPluginPresets         = 8
	maxPluginActions         = 6
	maxPluginViews           = 8
	maxPluginKPIItems        = 12
	maxPluginTableColumns    = 8
	maxPluginCatalogInputs   = 6
	maxPluginCatalogSchemas  = 4
	maxPluginCatalogFields   = 6
	maxPluginHomeMetrics     = 4
	maxPluginAskPresets      = 4
	maxPluginPromptRunes     = 2000
	defaultPluginIconName    = "layers"
)

var (
	pluginInputKeyPattern      = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,31}$`)
	pluginMetricKeyPattern     = regexp.MustCompile(`^[a-z0-9_.-]{1,96}$`)
	pluginPresetIDPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,47}$`)
	pluginCredentialRefPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	pluginSourceKeyPattern     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)
	pluginColumnKeyPattern     = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,48}$`)
	pluginCatalogKeyPattern    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	pluginCatalogEnvVarPattern = regexp.MustCompile(`^DOOPS_PLUGIN_[A-Z0-9_]{1,40}$`)
	pluginMCPCommandPattern    = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)
	pluginMCPToolNamePattern   = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`)
)

var pluginAllowedCatalogFieldTypes = map[string]bool{
	"domain": true,
	"host":   true,
	"ip":     true,
	"port":   true,
	"label":  true,
	"enum":   true,
}

// pluginIconAllowlist restricts L2 manifest icons to the 20 built-in Lucide icon
// tokens mapped statically on the console. Out-of-range icons fall back cleanly
// to defaultPluginIconName instead of failing parse.
var pluginIconAllowlist = map[string]bool{
	"cpu":          true,
	"shield":       true,
	"database":     true,
	"globe":        true,
	"activity":     true,
	"server":       true,
	"lock":         true,
	"layers":       true,
	"sparkles":     true,
	"terminal":     true,
	"gauge":        true,
	"radar":        true,
	"hard-drive":   true,
	"network":      true,
	"box":          true,
	"flame":        true,
	"eye":          true,
	"wrench":       true,
	"zap":          true,
	"check-circle": true,
}

var pluginAllowedCategories = map[string]bool{
	"observability":  true,
	"infrastructure": true,
	"delivery":       true,
	"data":           true,
	"ops":            true,
	"security":       true,
}

var pluginAllowedTargetModes = map[string]bool{
	"kubernetes": true,
	"docker":     true,
	"linux":      true,
}

// Phase 2 decision Q3: strictly forbid free-text input type ("text") in L2
// v1alpha1 manifests; only structured control parameters and credential_ref
// names are accepted.
var pluginAllowedInputTypes = map[string]bool{
	"number":         true,
	"boolean":        true,
	"select":         true,
	"credential_ref": true,
}

var pluginAllowedViewTypes = map[string]bool{
	"kpi_grid":        true,
	"checks_table":    true,
	"charts":          true,
	"trend_series":    true,
	"artifact_viewer": true,
	"resource_table":  true,
	"findings_list":   true,
}

var pluginAllowedColumnKinds = map[string]bool{
	"text":   true,
	"badge":  true,
	"code":   true,
	"number": true,
}

var pluginAllowedActionModes = map[string]bool{
	"check": true,
	"plan":  true,
	"apply": true,
}

func canonicalPluginCredentialUse(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if validCredentialUse(CredentialUse(trimmed)) {
		return trimmed, true
	}
	for _, candidate := range []CredentialUse{
		CredentialUseImagePull,
		CredentialUseGitCheckout,
		CredentialUseHelmPull,
		CredentialUseTLS,
		CredentialUseOpaqueSecret,
		CredentialUseDatabase,
		CredentialUseModelProvider,
	} {
		if strings.EqualFold(trimmed, string(candidate)) {
			return string(candidate), true
		}
	}
	return "", false
}

var pluginAllowedWidgetAggregations = map[string]bool{
	"max":    true,
	"avg":    true,
	"latest": true,
}

// SkillPluginManifest is the Gateway-validated declarative UI contract for an
// L2 Skill plugin (parsed strictly from plugin.yaml via yaml.v3 KnownFields).
// The console consumes only the normalized JSON projection; browsers never
// parse YAML and Agents never receive plugin.yaml.
type SkillPluginManifest struct {
	APIVersion       string                       `yaml:"apiVersion" json:"api_version"`
	Kind             string                       `yaml:"kind" json:"kind"`
	Plugin           SkillPluginMeta              `yaml:"plugin" json:"plugin"`
	Capabilities     SkillPluginCapabilities      `yaml:"capabilities,omitempty" json:"capabilities"`
	CatalogSchemas   []SkillPluginCatalogSchema   `yaml:"catalog_schemas,omitempty" json:"catalog_schemas,omitempty"`
	MCPServer        *SkillPluginMCPServer        `yaml:"mcp_server,omitempty" json:"mcp_server,omitempty"`
	UI               SkillPluginUI                `yaml:"ui" json:"ui"`
	ScheduleTemplate *SkillPluginScheduleTemplate `yaml:"schedule_template,omitempty" json:"schedule_template,omitempty"`
}

// SkillPluginCatalogSchema declares one plugin-owned asset catalog collection
// stored inside inventory_resource_catalog under kind="plugin:<slug>:<key>"
// with zero DDL changes and zero hardcoded domain logic in DoOps Core.
type SkillPluginCatalogSchema struct {
	Key          string                    `yaml:"key" json:"key"`
	Label        string                    `yaml:"label" json:"label"`
	Description  string                    `yaml:"description,omitempty" json:"description,omitempty"`
	EnvVar       string                    `yaml:"env_var" json:"env_var"`
	PrimaryField string                    `yaml:"primary_field" json:"primary_field"`
	MaxItems     int                       `yaml:"max_items,omitempty" json:"max_items,omitempty"`
	Fields       []SkillPluginCatalogField `yaml:"fields" json:"fields"`
}

type SkillPluginCatalogField struct {
	Key         string   `yaml:"key" json:"key"`
	Label       string   `yaml:"label" json:"label"`
	Placeholder string   `yaml:"placeholder,omitempty" json:"placeholder,omitempty"`
	Type        string   `yaml:"type" json:"type"` // domain | host | ip | port | label | enum
	Default     string   `yaml:"default,omitempty" json:"default,omitempty"`
	Options     []string `yaml:"options,omitempty" json:"options,omitempty"`
}

// SkillPluginMCPServer declares a plugin-owned MCP server packaged alongside
// the plugin (stdio script under ${SKILL_DIR}/... or external HTTP/SSE endpoint),
// registered into platform_mcp_servers on install and removed on uninstall.
type SkillPluginMCPServer struct {
	Slug            string   `yaml:"slug" json:"slug"`
	Name            string   `yaml:"name,omitempty" json:"name,omitempty"`
	Description     string   `yaml:"description,omitempty" json:"description,omitempty"`
	Transport       string   `yaml:"transport,omitempty" json:"transport,omitempty"` // stdio | http | sse
	Command         string   `yaml:"command,omitempty" json:"command,omitempty"`
	Args            []string `yaml:"args,omitempty" json:"args,omitempty"`
	URL             string   `yaml:"url,omitempty" json:"url,omitempty"`
	ReadTools       []string `yaml:"read_tools,omitempty" json:"read_tools,omitempty"`
	ApplyTools      []string `yaml:"apply_tools,omitempty" json:"apply_tools,omitempty"`
	ToolTimeoutSecs int      `yaml:"tool_timeout_secs,omitempty" json:"tool_timeout_secs,omitempty"`
}

type SkillPluginMeta struct {
	ID          string   `yaml:"id,omitempty" json:"id"`
	Name        string   `yaml:"name" json:"name"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Category    string   `yaml:"category,omitempty" json:"category"`
	Icon        string   `yaml:"icon,omitempty" json:"icon"`
	Scenarios   []string `yaml:"scenarios,omitempty" json:"scenarios"`
	OptIn       bool     `yaml:"opt_in,omitempty" json:"opt_in,omitempty"`
}

type SkillPluginCapabilities struct {
	TargetModes         []string `yaml:"target_modes,omitempty" json:"target_modes,omitempty"`
	RequiresSkills      []string `yaml:"requires_skills,omitempty" json:"requires_skills,omitempty"`
	RequiresMCP         []string `yaml:"requires_mcp,omitempty" json:"requires_mcp,omitempty"`
	CatalogInputs       []string `yaml:"catalog_inputs,omitempty" json:"catalog_inputs,omitempty"`
	CompanionAgentSkill string   `yaml:"companion_agent_skill,omitempty" json:"companion_agent_skill,omitempty"`
}

type SkillPluginUI struct {
	Slots   SkillPluginSlots    `yaml:"slots,omitempty" json:"slots"`
	Inputs  []SkillPluginInput  `yaml:"inputs,omitempty" json:"inputs"`
	Presets []SkillPluginPreset `yaml:"presets,omitempty" json:"presets"`
	Actions []SkillPluginAction `yaml:"actions,omitempty" json:"actions,omitempty"`
	Views   []SkillPluginView   `yaml:"views,omitempty" json:"views"`
}

type SkillPluginSlots struct {
	Sidebar    *SkillPluginSidebarSlot    `yaml:"sidebar,omitempty" json:"sidebar,omitempty"`
	TargetTab  *SkillPluginTargetTabSlot  `yaml:"target_tab,omitempty" json:"target_tab,omitempty"`
	HomeWidget *SkillPluginHomeWidgetSlot `yaml:"home_widget,omitempty" json:"home_widget,omitempty"`
	AskPresets []SkillPluginAskPreset     `yaml:"ask_presets,omitempty" json:"ask_presets,omitempty"`
}

type SkillPluginSidebarSlot struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Label   string `yaml:"label,omitempty" json:"label,omitempty"`
	Group   string `yaml:"group,omitempty" json:"group,omitempty"`
	Order   int    `yaml:"order,omitempty" json:"order,omitempty"`
}

type SkillPluginTargetTabSlot struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Label   string `yaml:"label,omitempty" json:"label,omitempty"`
	Order   int    `yaml:"order,omitempty" json:"order,omitempty"`
}

type SkillPluginHomeWidgetSlot struct {
	Enabled bool                      `yaml:"enabled" json:"enabled"`
	Title   string                    `yaml:"title,omitempty" json:"title,omitempty"`
	Span    int                       `yaml:"span,omitempty" json:"span,omitempty"`
	Metrics []SkillPluginWidgetMetric `yaml:"metrics,omitempty" json:"metrics,omitempty"`
}

type SkillPluginWidgetMetric struct {
	Key       string `yaml:"key" json:"key"`
	Label     string `yaml:"label" json:"label"`
	Unit      string `yaml:"unit,omitempty" json:"unit,omitempty"`
	Aggregate string `yaml:"aggregate,omitempty" json:"aggregate,omitempty"` // max | avg | latest
}

type SkillPluginAskPreset struct {
	Title       string `yaml:"title" json:"title"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Prompt      string `yaml:"prompt" json:"prompt"`
}

type SkillPluginInput struct {
	Key           string                    `yaml:"key" json:"key"`
	Label         string                    `yaml:"label" json:"label"`
	Description   string                    `yaml:"description,omitempty" json:"description,omitempty"`
	Type          string                    `yaml:"type" json:"type"` // number | boolean | select | credential_ref
	Required      bool                      `yaml:"required,omitempty" json:"required,omitempty"`
	Default       any                       `yaml:"default,omitempty" json:"default,omitempty"`
	Min           *float64                  `yaml:"min,omitempty" json:"min,omitempty"`
	Max           *float64                  `yaml:"max,omitempty" json:"max,omitempty"`
	Step          *float64                  `yaml:"step,omitempty" json:"step,omitempty"`
	Unit          string                    `yaml:"unit,omitempty" json:"unit,omitempty"`
	Options       []SkillPluginSelectOption `yaml:"options,omitempty" json:"options,omitempty"`
	CredentialUse string                    `yaml:"credential_use,omitempty" json:"credential_use,omitempty"`
}

type SkillPluginSelectOption struct {
	Value string `yaml:"value" json:"value"`
	Label string `yaml:"label" json:"label"`
}

type SkillPluginPreset struct {
	ID          string         `yaml:"id" json:"id"`
	Title       string         `yaml:"title" json:"title"`
	Description string         `yaml:"description,omitempty" json:"description,omitempty"`
	Mode        string         `yaml:"mode,omitempty" json:"mode"` // script | agent
	Inputs      map[string]any `yaml:"inputs,omitempty" json:"inputs,omitempty"`
	Prompt      string         `yaml:"prompt,omitempty" json:"prompt,omitempty"`
}

type SkillPluginAction struct {
	ID             string `yaml:"id" json:"id"`
	Label          string `yaml:"label" json:"label"`
	Description    string `yaml:"description,omitempty" json:"description,omitempty"`
	Mode           string `yaml:"mode,omitempty" json:"mode"` // check | plan | apply
	Skill          string `yaml:"skill,omitempty" json:"skill,omitempty"`
	RequiresMCP    string `yaml:"requires_mcp,omitempty" json:"requires_mcp,omitempty"`
	CredentialUse  string `yaml:"credential_use,omitempty" json:"credential_use,omitempty"`
	PromptTemplate string `yaml:"prompt_template" json:"prompt_template"`
}

type SkillPluginTableColumn struct {
	Key   string `yaml:"key" json:"key"`
	Label string `yaml:"label" json:"label"`
	Kind  string `yaml:"kind,omitempty" json:"kind,omitempty"` // text | badge | code | number
}

type SkillPluginView struct {
	Type        string                   `yaml:"type" json:"type"` // kpi_grid | checks_table | charts | trend_series | artifact_viewer | resource_table | findings_list
	Title       string                   `yaml:"title,omitempty" json:"title,omitempty"`
	Items       []SkillPluginKPIItem     `yaml:"items,omitempty" json:"items,omitempty"`
	MetricKeys  []string                 `yaml:"metric_keys,omitempty" json:"metric_keys,omitempty"`
	Range       string                   `yaml:"range,omitempty" json:"range,omitempty"`
	SourceKey   string                   `yaml:"source_key,omitempty" json:"source_key,omitempty"`
	Columns     []SkillPluginTableColumn `yaml:"columns,omitempty" json:"columns,omitempty"`
	RowActionID string                   `yaml:"row_action_id,omitempty" json:"row_action_id,omitempty"`
}

type SkillPluginKPIItem struct {
	Key           string   `yaml:"key" json:"key"`
	Label         string   `yaml:"label" json:"label"`
	Unit          string   `yaml:"unit,omitempty" json:"unit,omitempty"`
	WarnThreshold *float64 `yaml:"warn_threshold,omitempty" json:"warn_threshold,omitempty"`
	CritThreshold *float64 `yaml:"crit_threshold,omitempty" json:"crit_threshold,omitempty"`
}

type SkillPluginScheduleTemplate struct {
	Name            string         `yaml:"name" json:"name"`
	Description     string         `yaml:"description,omitempty" json:"description,omitempty"`
	IntervalSeconds int            `yaml:"interval_seconds" json:"interval_seconds"`
	ScopeMode       string         `yaml:"scope_mode,omitempty" json:"scope_mode"`
	Inputs          map[string]any `yaml:"inputs,omitempty" json:"inputs,omitempty"`
}

type pluginManifestError struct {
	Field  string
	Reason string
}

func (e *pluginManifestError) Error() string {
	if e.Field == "" {
		return e.Reason
	}
	return fmt.Sprintf("%s: %s", e.Field, e.Reason)
}

func isPluginManifestFilePath(p string) bool {
	return strings.EqualFold(strings.TrimSpace(p), pluginManifestFileName)
}

// filterAgentSyncSkillFiles removes plugin.yaml before dispatching files to a
// target Agent via doops_skill_sync so UI metadata never pollutes Agent Skill
// directories (/root/.agent/skills/<slug>/).
func filterAgentSyncSkillFiles(files []platformSkillFile) []platformSkillFile {
	if len(files) == 0 {
		return files
	}
	out := make([]platformSkillFile, 0, len(files))
	for _, f := range files {
		if isPluginManifestFilePath(f.Path) {
			continue
		}
		out = append(out, f)
	}
	return out
}

func extractPluginManifestFile(files []platformSkillFile) (string, bool) {
	for _, f := range files {
		if isPluginManifestFilePath(f.Path) {
			return f.Content, true
		}
	}
	return "", false
}

func hasControlChars(s string, allowNewlines bool) bool {
	for _, r := range s {
		if r == 0 {
			return true
		}
		if allowNewlines && (r == '\n' || r == '\r' || r == '\t') {
			continue
		}
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func validatePlainTextField(field, value string, minRunes, maxRunes int, allowNewlines bool) (string, error) {
	trimmed := strings.TrimSpace(value)
	if hasControlChars(trimmed, allowNewlines) {
		return "", &pluginManifestError{Field: field, Reason: "不允许包含控制字符"}
	}
	n := utf8.RuneCountInString(trimmed)
	if n < minRunes {
		return "", &pluginManifestError{Field: field, Reason: fmt.Sprintf("长度不能少于 %d 个字符", minRunes)}
	}
	if n > maxRunes {
		return "", &pluginManifestError{Field: field, Reason: fmt.Sprintf("长度不能超过 %d 个字符", maxRunes)}
	}
	return trimmed, nil
}

// parseSkillUIManifestFromFiles locates an optional plugin.yaml attachment,
// strictly decodes and validates it with yaml.v3 KnownFields(true), and returns
// both the normalized struct and its canonical JSON projection. When no
// plugin.yaml is present, it returns (nil, "", nil).
func parseSkillUIManifestFromFiles(slug string, files []platformSkillFile) (*SkillPluginManifest, string, error) {
	raw, found := extractPluginManifestFile(files)
	if !found {
		return nil, "", nil
	}
	return parseSkillUIManifestYAML(slug, raw)
}

func parseSkillUIManifestYAML(slug, raw string) (*SkillPluginManifest, string, error) {
	if len(raw) > maxPluginManifestBytes {
		return nil, "", &pluginManifestError{Field: pluginManifestFileName, Reason: "plugin.yaml 体积不能超过 32 KiB"}
	}
	if strings.TrimSpace(raw) == "" {
		return nil, "", &pluginManifestError{Field: pluginManifestFileName, Reason: "plugin.yaml 内容不能为空"}
	}
	if strings.ContainsRune(raw, 0) {
		return nil, "", &pluginManifestError{Field: pluginManifestFileName, Reason: "plugin.yaml 不能包含 NUL 字符"}
	}
	var m SkillPluginManifest
	dec := yaml.NewDecoder(bytes.NewBufferString(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, "", &pluginManifestError{Field: pluginManifestFileName, Reason: "YAML 语法错误或存在未声明字段：" + err.Error()}
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, "", &pluginManifestError{Field: pluginManifestFileName, Reason: "plugin.yaml 仅允许单个 YAML 文档"}
	}
	normalized, err := normalizeAndValidateSkillPluginManifest(slug, m)
	if err != nil {
		return nil, "", err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return nil, "", &pluginManifestError{Field: pluginManifestFileName, Reason: "序列化 plugin.yaml 失败"}
	}
	return &normalized, string(encoded), nil
}

func normalizeAndValidateSkillPluginManifest(slug string, m SkillPluginManifest) (SkillPluginManifest, error) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if !skillSlugPattern.MatchString(slug) {
		return m, &pluginManifestError{Field: "plugin.id", Reason: "关联的 Skill slug 无效"}
	}
	if strings.TrimSpace(m.APIVersion) != pluginManifestAPIVersion {
		return m, &pluginManifestError{Field: "apiVersion", Reason: fmt.Sprintf("必须为 %q", pluginManifestAPIVersion)}
	}
	m.APIVersion = pluginManifestAPIVersion
	if strings.TrimSpace(m.Kind) != pluginManifestKind {
		return m, &pluginManifestError{Field: "kind", Reason: fmt.Sprintf("必须为 %q", pluginManifestKind)}
	}
	m.Kind = pluginManifestKind

	expectedID := "skill." + slug
	if id := strings.TrimSpace(m.Plugin.ID); id != "" && id != expectedID {
		return m, &pluginManifestError{Field: "plugin.id", Reason: fmt.Sprintf("必须省略或严格等于 %q", expectedID)}
	}
	m.Plugin.ID = expectedID

	var err error
	if m.Plugin.Name, err = validatePlainTextField("plugin.name", m.Plugin.Name, 1, 24, false); err != nil {
		return m, err
	}
	if m.Plugin.Description, err = validatePlainTextField("plugin.description", m.Plugin.Description, 0, 200, false); err != nil {
		return m, err
	}

	cat := strings.ToLower(strings.TrimSpace(m.Plugin.Category))
	if cat == "" {
		cat = "ops"
	}
	if !pluginAllowedCategories[cat] {
		return m, &pluginManifestError{Field: "plugin.category", Reason: "分类仅支持 observability / infrastructure / delivery / data / ops / security"}
	}
	m.Plugin.Category = cat

	icon := strings.ToLower(strings.TrimSpace(m.Plugin.Icon))
	if hasControlChars(icon, false) {
		return m, &pluginManifestError{Field: "plugin.icon", Reason: "图标名不能包含控制字符"}
	}
	if !pluginIconAllowlist[icon] {
		icon = defaultPluginIconName
	}
	m.Plugin.Icon = icon

	// Scenarios: each must match one of the 5 built-in scenario IDs.
	cleanScenarios := make([]string, 0, len(m.Plugin.Scenarios))
	seenScenarios := make(map[string]bool, len(m.Plugin.Scenarios))
	for i, sc := range m.Plugin.Scenarios {
		sc = strings.TrimSpace(sc)
		if _, ok := findPluginScenario(sc); !ok {
			return m, &pluginManifestError{Field: fmt.Sprintf("plugin.scenarios[%d]", i), Reason: fmt.Sprintf("未知场景 ID %q", sc)}
		}
		if !seenScenarios[sc] {
			seenScenarios[sc] = true
			cleanScenarios = append(cleanScenarios, sc)
		}
	}
	m.Plugin.Scenarios = cleanScenarios
	if m.Plugin.OptIn && len(m.Plugin.Scenarios) > 0 {
		return m, &pluginManifestError{Field: "plugin.opt_in", Reason: "opt_in 为 true 的按需插件不能同时声明默认启用场景 scenarios"}
	}

	// Capabilities
	cleanModes := make([]string, 0, len(m.Capabilities.TargetModes))
	seenModes := make(map[string]bool)
	for i, mode := range m.Capabilities.TargetModes {
		mode = strings.ToLower(strings.TrimSpace(mode))
		if !pluginAllowedTargetModes[mode] {
			return m, &pluginManifestError{Field: fmt.Sprintf("capabilities.target_modes[%d]", i), Reason: "仅支持 kubernetes / docker / linux"}
		}
		if !seenModes[mode] {
			seenModes[mode] = true
			cleanModes = append(cleanModes, mode)
		}
	}
	m.Capabilities.TargetModes = cleanModes

	if len(m.Capabilities.RequiresSkills) > 8 {
		return m, &pluginManifestError{Field: "capabilities.requires_skills", Reason: "最多声明 8 个依赖 Skill"}
	}
	cleanReqSkills := make([]string, 0, len(m.Capabilities.RequiresSkills))
	for i, rs := range m.Capabilities.RequiresSkills {
		rs = strings.ToLower(strings.TrimSpace(rs))
		if !skillSlugPattern.MatchString(rs) {
			return m, &pluginManifestError{Field: fmt.Sprintf("capabilities.requires_skills[%d]", i), Reason: "Skill slug 格式无效"}
		}
		cleanReqSkills = append(cleanReqSkills, rs)
	}
	m.Capabilities.RequiresSkills = cleanReqSkills

	if len(m.Capabilities.RequiresMCP) > 8 {
		return m, &pluginManifestError{Field: "capabilities.requires_mcp", Reason: "最多声明 8 个依赖 MCP"}
	}
	cleanReqMCP := make([]string, 0, len(m.Capabilities.RequiresMCP))
	for i, rm := range m.Capabilities.RequiresMCP {
		rm = strings.ToLower(strings.TrimSpace(rm))
		if !skillSlugPattern.MatchString(rm) {
			return m, &pluginManifestError{Field: fmt.Sprintf("capabilities.requires_mcp[%d]", i), Reason: "MCP slug 格式无效"}
		}
		cleanReqMCP = append(cleanReqMCP, rm)
	}
	m.Capabilities.RequiresMCP = cleanReqMCP

	if len(m.Capabilities.CatalogInputs) > maxPluginCatalogInputs {
		return m, &pluginManifestError{Field: "capabilities.catalog_inputs", Reason: fmt.Sprintf("最多声明 %d 个资产目录注入项", maxPluginCatalogInputs)}
	}
	cleanCatalogInputs := make([]string, 0, len(m.Capabilities.CatalogInputs))
	seenCatalogInputs := make(map[string]bool, len(m.Capabilities.CatalogInputs))
	for i, ci := range m.Capabilities.CatalogInputs {
		ci = strings.ToLower(strings.TrimSpace(ci))
		if !isAllowedCatalogInput(ci) {
			return m, &pluginManifestError{Field: fmt.Sprintf("capabilities.catalog_inputs[%d]", i), Reason: fmt.Sprintf("不支持的资产目录注入项 %q（可选：%s）", ci, strings.Join(allowedCatalogInputKeys(), " / "))}
		}
		if !seenCatalogInputs[ci] {
			seenCatalogInputs[ci] = true
			cleanCatalogInputs = append(cleanCatalogInputs, ci)
		}
	}
	m.Capabilities.CatalogInputs = cleanCatalogInputs

	if comp := strings.ToLower(strings.TrimSpace(m.Capabilities.CompanionAgentSkill)); comp != "" {
		if !skillSlugPattern.MatchString(comp) {
			return m, &pluginManifestError{Field: "capabilities.companion_agent_skill", Reason: "伴生 Agent Skill slug 格式无效"}
		}
		m.Capabilities.CompanionAgentSkill = comp
	} else {
		m.Capabilities.CompanionAgentSkill = ""
	}

	// UI Slots
	if m.UI.Slots.Sidebar != nil {
		label := strings.TrimSpace(m.UI.Slots.Sidebar.Label)
		if label == "" {
			label = m.Plugin.Name
		}
		if label, err = validatePlainTextField("ui.slots.sidebar.label", label, 1, 24, false); err != nil {
			return m, err
		}
		m.UI.Slots.Sidebar.Label = label
		grp := strings.TrimSpace(m.UI.Slots.Sidebar.Group)
		if grp == "" {
			grp = "extensions"
		}
		if grp != "extensions" {
			return m, &pluginManifestError{Field: "ui.slots.sidebar.group", Reason: "L2 插件侧栏分组仅允许 extensions"}
		}
		m.UI.Slots.Sidebar.Group = grp
	}
	if m.UI.Slots.TargetTab != nil {
		label := strings.TrimSpace(m.UI.Slots.TargetTab.Label)
		if label == "" {
			label = m.Plugin.Name
		}
		if label, err = validatePlainTextField("ui.slots.target_tab.label", label, 1, 24, false); err != nil {
			return m, err
		}
		m.UI.Slots.TargetTab.Label = label
	}
	if m.UI.Slots.HomeWidget != nil {
		hw := m.UI.Slots.HomeWidget
		title := strings.TrimSpace(hw.Title)
		if title == "" {
			title = m.Plugin.Name
		}
		if title, err = validatePlainTextField("ui.slots.home_widget.title", title, 1, 24, false); err != nil {
			return m, err
		}
		hw.Title = title
		if hw.Span == 0 {
			hw.Span = 1
		}
		if hw.Span != 1 && hw.Span != 2 {
			return m, &pluginManifestError{Field: "ui.slots.home_widget.span", Reason: "span 仅支持 1 或 2"}
		}
		if len(hw.Metrics) > maxPluginHomeMetrics {
			return m, &pluginManifestError{Field: "ui.slots.home_widget.metrics", Reason: fmt.Sprintf("首页卡片最多声明 %d 项指标", maxPluginHomeMetrics)}
		}
		for i := range hw.Metrics {
			met := &hw.Metrics[i]
			met.Key = strings.TrimSpace(met.Key)
			if !pluginMetricKeyPattern.MatchString(met.Key) {
				return m, &pluginManifestError{Field: fmt.Sprintf("ui.slots.home_widget.metrics[%d].key", i), Reason: "指标键格式无效（仅支持小写字母、数字及 _ . -）"}
			}
			if met.Label, err = validatePlainTextField(fmt.Sprintf("ui.slots.home_widget.metrics[%d].label", i), met.Label, 1, 24, false); err != nil {
				return m, err
			}
			if met.Unit, err = validatePlainTextField(fmt.Sprintf("ui.slots.home_widget.metrics[%d].unit", i), met.Unit, 0, 16, false); err != nil {
				return m, err
			}
			agg := strings.ToLower(strings.TrimSpace(met.Aggregate))
			if agg == "" {
				agg = "max"
			}
			if !pluginAllowedWidgetAggregations[agg] {
				return m, &pluginManifestError{Field: fmt.Sprintf("ui.slots.home_widget.metrics[%d].aggregate", i), Reason: "聚合方式仅支持 max / avg / latest"}
			}
			met.Aggregate = agg
		}
	}
	if len(m.UI.Slots.AskPresets) > maxPluginAskPresets {
		return m, &pluginManifestError{Field: "ui.slots.ask_presets", Reason: fmt.Sprintf("最多声明 %d 个 AI 快捷指令", maxPluginAskPresets)}
	}
	for i := range m.UI.Slots.AskPresets {
		ap := &m.UI.Slots.AskPresets[i]
		if ap.Title, err = validatePlainTextField(fmt.Sprintf("ui.slots.ask_presets[%d].title", i), ap.Title, 1, 24, false); err != nil {
			return m, err
		}
		if ap.Description, err = validatePlainTextField(fmt.Sprintf("ui.slots.ask_presets[%d].description", i), ap.Description, 0, 120, false); err != nil {
			return m, err
		}
		if ap.Prompt, err = validatePlainTextField(fmt.Sprintf("ui.slots.ask_presets[%d].prompt", i), ap.Prompt, 1, maxPluginPromptRunes, true); err != nil {
			return m, err
		}
	}

	// UI Inputs
	if len(m.UI.Inputs) > maxPluginInputs {
		return m, &pluginManifestError{Field: "ui.inputs", Reason: fmt.Sprintf("最多声明 %d 个输入参数", maxPluginInputs)}
	}
	inputIndex := make(map[string]SkillPluginInput, len(m.UI.Inputs))
	cleanInputs := make([]SkillPluginInput, 0, len(m.UI.Inputs))
	for i, inp := range m.UI.Inputs {
		prefix := fmt.Sprintf("ui.inputs[%d]", i)
		inp.Key = strings.TrimSpace(inp.Key)
		if !pluginInputKeyPattern.MatchString(inp.Key) {
			return m, &pluginManifestError{Field: prefix + ".key", Reason: "参数键必须匹配 ^[A-Z][A-Z0-9_]{0,31}$"}
		}
		if _, dup := inputIndex[inp.Key]; dup {
			return m, &pluginManifestError{Field: prefix + ".key", Reason: fmt.Sprintf("参数键 %q 重复声明", inp.Key)}
		}
		if inp.Label, err = validatePlainTextField(prefix+".label", inp.Label, 1, 24, false); err != nil {
			return m, err
		}
		if inp.Description, err = validatePlainTextField(prefix+".description", inp.Description, 0, 120, false); err != nil {
			return m, err
		}
		if inp.Unit, err = validatePlainTextField(prefix+".unit", inp.Unit, 0, 16, false); err != nil {
			return m, err
		}
		inp.Type = strings.ToLower(strings.TrimSpace(inp.Type))
		if !pluginAllowedInputTypes[inp.Type] {
			return m, &pluginManifestError{Field: prefix + ".type", Reason: fmt.Sprintf("不支持的输入类型 %q（仅支持 number / boolean / select / credential_ref，禁止自由文本 text）", inp.Type)}
		}
		switch inp.Type {
		case "number":
			if len(inp.Options) > 0 {
				return m, &pluginManifestError{Field: prefix + ".options", Reason: "number 类型不允许声明 options"}
			}
			if inp.Min != nil && (math.IsNaN(*inp.Min) || math.IsInf(*inp.Min, 0)) {
				return m, &pluginManifestError{Field: prefix + ".min", Reason: "min 必须为有限数值"}
			}
			if inp.Max != nil && (math.IsNaN(*inp.Max) || math.IsInf(*inp.Max, 0)) {
				return m, &pluginManifestError{Field: prefix + ".max", Reason: "max 必须为有限数值"}
			}
			if inp.Min != nil && inp.Max != nil && *inp.Min > *inp.Max {
				return m, &pluginManifestError{Field: prefix + ".min", Reason: "min 不能大于 max"}
			}
			if inp.Step != nil && (math.IsNaN(*inp.Step) || math.IsInf(*inp.Step, 0) || *inp.Step <= 0) {
				return m, &pluginManifestError{Field: prefix + ".step", Reason: "step 必须为正数"}
			}
		case "boolean":
			if len(inp.Options) > 0 || inp.Min != nil || inp.Max != nil || inp.Step != nil {
				return m, &pluginManifestError{Field: prefix, Reason: "boolean 类型不允许声明 min/max/step/options"}
			}
		case "select":
			if inp.Min != nil || inp.Max != nil || inp.Step != nil {
				return m, &pluginManifestError{Field: prefix, Reason: "select 类型不允许声明 min/max/step"}
			}
			if len(inp.Options) == 0 || len(inp.Options) > maxPluginSelectOptions {
				return m, &pluginManifestError{Field: prefix + ".options", Reason: fmt.Sprintf("select 类型必须包含 1..%d 个选项", maxPluginSelectOptions)}
			}
			seenOpt := make(map[string]bool, len(inp.Options))
			for j := range inp.Options {
				opt := &inp.Options[j]
				if opt.Value, err = validatePlainTextField(fmt.Sprintf("%s.options[%d].value", prefix, j), opt.Value, 1, 64, false); err != nil {
					return m, err
				}
				if opt.Label, err = validatePlainTextField(fmt.Sprintf("%s.options[%d].label", prefix, j), opt.Label, 1, 24, false); err != nil {
					return m, err
				}
				if seenOpt[opt.Value] {
					return m, &pluginManifestError{Field: fmt.Sprintf("%s.options[%d].value", prefix, j), Reason: "选项值重复"}
				}
				seenOpt[opt.Value] = true
			}
		case "credential_ref":
			if len(inp.Options) > 0 || inp.Min != nil || inp.Max != nil || inp.Step != nil {
				return m, &pluginManifestError{Field: prefix, Reason: "credential_ref 类型不允许声明 min/max/step/options"}
			}
			if rawUse := strings.TrimSpace(inp.CredentialUse); rawUse != "" {
				canonical, ok := canonicalPluginCredentialUse(rawUse)
				if !ok {
					return m, &pluginManifestError{Field: prefix + ".credential_use", Reason: fmt.Sprintf("不支持的凭据用途 %q", rawUse)}
				}
				inp.CredentialUse = canonical
			} else {
				inp.CredentialUse = ""
			}
		}
		if inp.Default != nil {
			cleanDef, _, defErr := coerceAndValidatePluginInputValue(prefix+".default", inp, inp.Default)
			if defErr != nil {
				return m, defErr
			}
			inp.Default = cleanDef
		}
		inputIndex[inp.Key] = inp
		cleanInputs = append(cleanInputs, inp)
	}
	m.UI.Inputs = cleanInputs

	// UI Presets
	if len(m.UI.Presets) > maxPluginPresets {
		return m, &pluginManifestError{Field: "ui.presets", Reason: fmt.Sprintf("最多声明 %d 个快捷预设", maxPluginPresets)}
	}
	seenPreset := make(map[string]bool, len(m.UI.Presets))
	cleanPresets := make([]SkillPluginPreset, 0, len(m.UI.Presets))
	for i, p := range m.UI.Presets {
		prefix := fmt.Sprintf("ui.presets[%d]", i)
		p.ID = strings.TrimSpace(p.ID)
		if !pluginPresetIDPattern.MatchString(p.ID) {
			return m, &pluginManifestError{Field: prefix + ".id", Reason: "预设 ID 格式无效"}
		}
		if seenPreset[p.ID] {
			return m, &pluginManifestError{Field: prefix + ".id", Reason: "预设 ID 重复"}
		}
		seenPreset[p.ID] = true
		if p.Title, err = validatePlainTextField(prefix+".title", p.Title, 1, 24, false); err != nil {
			return m, err
		}
		if p.Description, err = validatePlainTextField(prefix+".description", p.Description, 0, 120, false); err != nil {
			return m, err
		}
		mode := strings.ToLower(strings.TrimSpace(p.Mode))
		if mode == "" {
			mode = "script"
		}
		if mode != "script" && mode != "agent" {
			return m, &pluginManifestError{Field: prefix + ".mode", Reason: "预设模式仅支持 script 或 agent"}
		}
		p.Mode = mode
		if p.Prompt, err = validatePlainTextField(prefix+".prompt", p.Prompt, 0, maxPluginPromptRunes, true); err != nil {
			return m, err
		}
		if p.Mode == "agent" && p.Prompt == "" {
			return m, &pluginManifestError{Field: prefix + ".prompt", Reason: "agent 模式预设必须提供非空 prompt"}
		}
		if len(p.Inputs) > 0 {
			cleanPInputs := make(map[string]any, len(p.Inputs))
			for k, rawVal := range p.Inputs {
				spec, ok := inputIndex[k]
				if !ok {
					return m, &pluginManifestError{Field: prefix + ".inputs." + k, Reason: fmt.Sprintf("引用了未在 ui.inputs 声明的参数 %q", k)}
				}
				typedVal, _, vErr := coerceAndValidatePluginInputValue(prefix+".inputs."+k, spec, rawVal)
				if vErr != nil {
					return m, vErr
				}
				cleanPInputs[k] = typedVal
			}
			p.Inputs = cleanPInputs
		}
		cleanPresets = append(cleanPresets, p)
	}
	m.UI.Presets = cleanPresets

	// UI Actions (in-place check / plan / apply workbench operations)
	if len(m.UI.Actions) > maxPluginActions {
		return m, &pluginManifestError{Field: "ui.actions", Reason: fmt.Sprintf("最多声明 %d 个交互操作项", maxPluginActions)}
	}
	actionIndex := make(map[string]bool, len(m.UI.Actions))
	cleanActions := make([]SkillPluginAction, 0, len(m.UI.Actions))
	for i, act := range m.UI.Actions {
		prefix := fmt.Sprintf("ui.actions[%d]", i)
		act.ID = strings.TrimSpace(act.ID)
		if !pluginPresetIDPattern.MatchString(act.ID) {
			return m, &pluginManifestError{Field: prefix + ".id", Reason: "操作 ID 格式无效"}
		}
		if actionIndex[act.ID] {
			return m, &pluginManifestError{Field: prefix + ".id", Reason: "操作 ID 重复"}
		}
		actionIndex[act.ID] = true
		if act.Label, err = validatePlainTextField(prefix+".label", act.Label, 1, 24, false); err != nil {
			return m, err
		}
		if act.Description, err = validatePlainTextField(prefix+".description", act.Description, 0, 160, false); err != nil {
			return m, err
		}
		mode := strings.ToLower(strings.TrimSpace(act.Mode))
		if mode == "" {
			mode = "check"
		}
		if !pluginAllowedActionModes[mode] {
			return m, &pluginManifestError{Field: prefix + ".mode", Reason: "操作模式仅支持 check / plan / apply"}
		}
		act.Mode = mode
		actSkill := strings.ToLower(strings.TrimSpace(act.Skill))
		if actSkill == "" {
			if m.Capabilities.CompanionAgentSkill != "" {
				actSkill = m.Capabilities.CompanionAgentSkill
			} else {
				actSkill = slug
			}
		}
		if !skillSlugPattern.MatchString(actSkill) {
			return m, &pluginManifestError{Field: prefix + ".skill", Reason: "关联 Skill slug 格式无效"}
		}
		act.Skill = actSkill
		if reqMCP := strings.ToLower(strings.TrimSpace(act.RequiresMCP)); reqMCP != "" {
			if !skillSlugPattern.MatchString(reqMCP) {
				return m, &pluginManifestError{Field: prefix + ".requires_mcp", Reason: "MCP slug 格式无效"}
			}
			act.RequiresMCP = reqMCP
		} else {
			act.RequiresMCP = ""
		}
		if rawUse := strings.TrimSpace(act.CredentialUse); rawUse != "" {
			canonical, ok := canonicalPluginCredentialUse(rawUse)
			if !ok {
				return m, &pluginManifestError{Field: prefix + ".credential_use", Reason: fmt.Sprintf("不支持的凭据用途 %q", rawUse)}
			}
			act.CredentialUse = canonical
		} else {
			act.CredentialUse = ""
		}
		if act.PromptTemplate, err = validatePlainTextField(prefix+".prompt_template", act.PromptTemplate, 1, maxPluginPromptRunes, true); err != nil {
			return m, err
		}
		cleanActions = append(cleanActions, act)
	}
	m.UI.Actions = cleanActions

	// UI Views
	if len(m.UI.Views) > maxPluginViews {
		return m, &pluginManifestError{Field: "ui.views", Reason: fmt.Sprintf("最多声明 %d 个视图组件", maxPluginViews)}
	}
	cleanViews := make([]SkillPluginView, 0, len(m.UI.Views))
	for i, v := range m.UI.Views {
		prefix := fmt.Sprintf("ui.views[%d]", i)
		v.Type = strings.ToLower(strings.TrimSpace(v.Type))
		if !pluginAllowedViewTypes[v.Type] {
			return m, &pluginManifestError{Field: prefix + ".type", Reason: "视图类型仅支持 kpi_grid / checks_table / charts / trend_series / artifact_viewer / resource_table / findings_list"}
		}
		if v.Title, err = validatePlainTextField(prefix+".title", v.Title, 0, 24, false); err != nil {
			return m, err
		}
		if len(v.Items) > maxPluginKPIItems {
			return m, &pluginManifestError{Field: prefix + ".items", Reason: fmt.Sprintf("kpi_grid 最多声明 %d 项指标", maxPluginKPIItems)}
		}
		for j := range v.Items {
			it := &v.Items[j]
			it.Key = strings.TrimSpace(it.Key)
			if !pluginMetricKeyPattern.MatchString(it.Key) {
				return m, &pluginManifestError{Field: fmt.Sprintf("%s.items[%d].key", prefix, j), Reason: "指标键格式无效（仅支持小写字母、数字及 _ . -）"}
			}
			if it.Label, err = validatePlainTextField(fmt.Sprintf("%s.items[%d].label", prefix, j), it.Label, 1, 24, false); err != nil {
				return m, err
			}
			if it.Unit, err = validatePlainTextField(fmt.Sprintf("%s.items[%d].unit", prefix, j), it.Unit, 0, 16, false); err != nil {
				return m, err
			}
		}
		if len(v.MetricKeys) > maxPluginKPIItems {
			return m, &pluginManifestError{Field: prefix + ".metric_keys", Reason: fmt.Sprintf("最多声明 %d 个趋势指标键", maxPluginKPIItems)}
		}
		for j, mk := range v.MetricKeys {
			mk = strings.TrimSpace(mk)
			if !pluginMetricKeyPattern.MatchString(mk) {
				return m, &pluginManifestError{Field: fmt.Sprintf("%s.metric_keys[%d]", prefix, j), Reason: "指标键格式无效"}
			}
			v.MetricKeys[j] = mk
		}
		if rng := strings.ToLower(strings.TrimSpace(v.Range)); rng != "" {
			if _, _, ok := monitorMetricRangeDuration(rng); !ok {
				return m, &pluginManifestError{Field: prefix + ".range", Reason: "趋势默认时间范围仅支持 1h / 6h / 24h / 7d / 30d"}
			}
			v.Range = rng
		}
		if v.Type == "resource_table" {
			sk := strings.TrimSpace(v.SourceKey)
			if sk == "" {
				sk = "resources"
			}
			if !pluginSourceKeyPattern.MatchString(sk) {
				return m, &pluginManifestError{Field: prefix + ".source_key", Reason: "资源表 source_key 格式无效（须匹配 ^[a-z][a-z0-9_]{0,47}$）"}
			}
			v.SourceKey = sk
			if len(v.Columns) == 0 || len(v.Columns) > maxPluginTableColumns {
				return m, &pluginManifestError{Field: prefix + ".columns", Reason: fmt.Sprintf("resource_table 须声明 1..%d 列", maxPluginTableColumns)}
			}
			seenCol := make(map[string]bool, len(v.Columns))
			for j := range v.Columns {
				col := &v.Columns[j]
				col.Key = strings.TrimSpace(col.Key)
				if !pluginColumnKeyPattern.MatchString(col.Key) {
					return m, &pluginManifestError{Field: fmt.Sprintf("%s.columns[%d].key", prefix, j), Reason: "列字段键格式无效"}
				}
				if seenCol[col.Key] {
					return m, &pluginManifestError{Field: fmt.Sprintf("%s.columns[%d].key", prefix, j), Reason: "列字段键重复"}
				}
				seenCol[col.Key] = true
				if col.Label, err = validatePlainTextField(fmt.Sprintf("%s.columns[%d].label", prefix, j), col.Label, 1, 24, false); err != nil {
					return m, err
				}
				kind := strings.ToLower(strings.TrimSpace(col.Kind))
				if kind == "" {
					kind = "text"
				}
				if !pluginAllowedColumnKinds[kind] {
					return m, &pluginManifestError{Field: fmt.Sprintf("%s.columns[%d].kind", prefix, j), Reason: "列渲染类型仅支持 text / badge / code / number"}
				}
				col.Kind = kind
			}
			if actID := strings.TrimSpace(v.RowActionID); actID != "" {
				if !actionIndex[actID] {
					return m, &pluginManifestError{Field: prefix + ".row_action_id", Reason: fmt.Sprintf("引用了未在 ui.actions 声明的操作 ID %q", actID)}
				}
				v.RowActionID = actID
			}
		} else {
			if strings.TrimSpace(v.SourceKey) != "" || len(v.Columns) > 0 || strings.TrimSpace(v.RowActionID) != "" {
				return m, &pluginManifestError{Field: prefix, Reason: "仅 resource_table 视图允许声明 source_key / columns / row_action_id"}
			}
		}
		cleanViews = append(cleanViews, v)
	}
	m.UI.Views = cleanViews

	// Optional Schedule Template
	if m.ScheduleTemplate != nil {
		st := m.ScheduleTemplate
		if st.Name, err = validatePlainTextField("schedule_template.name", st.Name, 1, 64, false); err != nil {
			return m, err
		}
		if st.Description, err = validatePlainTextField("schedule_template.description", st.Description, 0, 200, false); err != nil {
			return m, err
		}
		if st.IntervalSeconds < 60 || st.IntervalSeconds > 7*86400 {
			return m, &pluginManifestError{Field: "schedule_template.interval_seconds", Reason: "定时周期须在 60 秒至 604800 秒之间"}
		}
		sm := strings.TrimSpace(st.ScopeMode)
		if sm == "" {
			sm = "all_online"
		}
		if sm != "all_online" && sm != "targets" && sm != "tags" {
			return m, &pluginManifestError{Field: "schedule_template.scope_mode", Reason: "范围模式仅支持 all_online / targets / tags"}
		}
		st.ScopeMode = sm
		if len(st.Inputs) > 0 {
			cleanSTInputs := make(map[string]any, len(st.Inputs))
			for k, rawVal := range st.Inputs {
				spec, ok := inputIndex[k]
				if !ok {
					return m, &pluginManifestError{Field: "schedule_template.inputs." + k, Reason: fmt.Sprintf("引用了未声明参数 %q", k)}
				}
				typedVal, _, vErr := coerceAndValidatePluginInputValue("schedule_template.inputs."+k, spec, rawVal)
				if vErr != nil {
					return m, vErr
				}
				cleanSTInputs[k] = typedVal
			}
			st.Inputs = cleanSTInputs
		}
	}

	// Optional Declarative Plugin Catalog Schemas (plugin:<slug>:<key>)
	if len(m.CatalogSchemas) > maxPluginCatalogSchemas {
		return m, &pluginManifestError{Field: "catalog_schemas", Reason: fmt.Sprintf("最多声明 %d 个插件资产目录 Schema", maxPluginCatalogSchemas)}
	}
	seenSchemaKeys := make(map[string]bool, len(m.CatalogSchemas))
	seenSchemaEnvs := make(map[string]bool, len(m.CatalogSchemas))
	cleanSchemas := make([]SkillPluginCatalogSchema, 0, len(m.CatalogSchemas))
	for i, sc := range m.CatalogSchemas {
		prefix := fmt.Sprintf("catalog_schemas[%d]", i)
		sc.Key = strings.ToLower(strings.TrimSpace(sc.Key))
		if !pluginCatalogKeyPattern.MatchString(sc.Key) {
			return m, &pluginManifestError{Field: prefix + ".key", Reason: "资产目录 key 必须匹配 ^[a-z][a-z0-9_]{0,31}$"}
		}
		if seenSchemaKeys[sc.Key] {
			return m, &pluginManifestError{Field: prefix + ".key", Reason: "资产目录 key 重复"}
		}
		seenSchemaKeys[sc.Key] = true
		if sc.Label, err = validatePlainTextField(prefix+".label", sc.Label, 1, 32, false); err != nil {
			return m, err
		}
		if sc.Description, err = validatePlainTextField(prefix+".description", sc.Description, 0, 160, false); err != nil {
			return m, err
		}
		sc.EnvVar = strings.TrimSpace(sc.EnvVar)
		if !pluginCatalogEnvVarPattern.MatchString(sc.EnvVar) {
			return m, &pluginManifestError{Field: prefix + ".env_var", Reason: "环境变量名必须以 DOOPS_PLUGIN_ 开头且仅含大写字母、数字与下划线"}
		}
		if seenSchemaEnvs[sc.EnvVar] {
			return m, &pluginManifestError{Field: prefix + ".env_var", Reason: "环境变量名重复"}
		}
		seenSchemaEnvs[sc.EnvVar] = true
		if sc.MaxItems == 0 {
			sc.MaxItems = 24
		}
		if sc.MaxItems < 1 || sc.MaxItems > 64 {
			return m, &pluginManifestError{Field: prefix + ".max_items", Reason: "max_items 须在 1 到 64 之间"}
		}
		if len(sc.Fields) == 0 || len(sc.Fields) > maxPluginCatalogFields {
			return m, &pluginManifestError{Field: prefix + ".fields", Reason: fmt.Sprintf("每个资产目录须声明 1 至 %d 个字段", maxPluginCatalogFields)}
		}
		fieldKeys := make(map[string]bool, len(sc.Fields))
		cleanFields := make([]SkillPluginCatalogField, 0, len(sc.Fields))
		for j, f := range sc.Fields {
			fPrefix := fmt.Sprintf("%s.fields[%d]", prefix, j)
			f.Key = strings.ToLower(strings.TrimSpace(f.Key))
			if !pluginCatalogKeyPattern.MatchString(f.Key) {
				return m, &pluginManifestError{Field: fPrefix + ".key", Reason: "字段 key 格式无效"}
			}
			if credentialSecretLikeKey(f.Key) || strings.Contains(f.Key, "password") || strings.Contains(f.Key, "passwd") || strings.Contains(f.Key, "secret") || strings.Contains(f.Key, "token") {
				return m, &pluginManifestError{Field: fPrefix + ".key", Reason: "资产目录严禁声明密钥、口令或令牌类字段"}
			}
			if fieldKeys[f.Key] {
				return m, &pluginManifestError{Field: fPrefix + ".key", Reason: "字段 key 重复"}
			}
			fieldKeys[f.Key] = true
			if f.Label, err = validatePlainTextField(fPrefix+".label", f.Label, 1, 24, false); err != nil {
				return m, err
			}
			if f.Placeholder, err = validatePlainTextField(fPrefix+".placeholder", f.Placeholder, 0, 80, false); err != nil {
				return m, err
			}
			f.Type = strings.ToLower(strings.TrimSpace(f.Type))
			if !pluginAllowedCatalogFieldTypes[f.Type] {
				return m, &pluginManifestError{Field: fPrefix + ".type", Reason: "字段类型仅支持 domain / host / ip / port / label / enum"}
			}
			if f.Default, err = validatePlainTextField(fPrefix+".default", f.Default, 0, 128, false); err != nil {
				return m, err
			}
			if f.Type == "enum" {
				if len(f.Options) == 0 || len(f.Options) > 16 {
					return m, &pluginManifestError{Field: fPrefix + ".options", Reason: "enum 字段须声明 1 至 16 个选项"}
				}
				cleanOpts := make([]string, 0, len(f.Options))
				for k, opt := range f.Options {
					opt, oErr := validatePlainTextField(fmt.Sprintf("%s.options[%d]", fPrefix, k), opt, 1, 64, false)
					if oErr != nil {
						return m, oErr
					}
					cleanOpts = append(cleanOpts, opt)
				}
				f.Options = cleanOpts
				if f.Default != "" && !stringSliceContains(f.Options, f.Default) {
					return m, &pluginManifestError{Field: fPrefix + ".default", Reason: "enum 默认值必须在 options 内"}
				}
			} else if len(f.Options) > 0 {
				return m, &pluginManifestError{Field: fPrefix + ".options", Reason: "仅 enum 类型字段允许声明 options"}
			}
			cleanFields = append(cleanFields, f)
		}
		sc.Fields = cleanFields
		sc.PrimaryField = strings.ToLower(strings.TrimSpace(sc.PrimaryField))
		if sc.PrimaryField == "" {
			sc.PrimaryField = sc.Fields[0].Key
		}
		if !fieldKeys[sc.PrimaryField] {
			return m, &pluginManifestError{Field: prefix + ".primary_field", Reason: "primary_field 必须匹配已声明的字段 key"}
		}
		cleanSchemas = append(cleanSchemas, sc)
	}
	m.CatalogSchemas = cleanSchemas

	// Optional Plugin-Owned MCP Server Declaration
	if m.MCPServer != nil {
		ms := m.MCPServer
		ms.Slug = strings.ToLower(strings.TrimSpace(ms.Slug))
		if ms.Slug == "" {
			ms.Slug = slug
		}
		if !skillSlugPattern.MatchString(ms.Slug) {
			return m, &pluginManifestError{Field: "mcp_server.slug", Reason: "MCP server slug 格式无效"}
		}
		if ms.Slug == mcpDomainDoops || ms.Slug == mcpDomainDoSQL {
			return m, &pluginManifestError{Field: "mcp_server.slug", Reason: "禁止覆盖平台内置 MCP server（doops / dosql）"}
		}
		if ms.Name == "" {
			ms.Name = m.Plugin.Name + " MCP"
		}
		if ms.Name, err = validatePlainTextField("mcp_server.name", ms.Name, 1, 64, false); err != nil {
			return m, err
		}
		if ms.Description, err = validatePlainTextField("mcp_server.description", ms.Description, 0, 200, false); err != nil {
			return m, err
		}
		ms.Transport = strings.ToLower(strings.TrimSpace(ms.Transport))
		if ms.Transport == "" {
			ms.Transport = "stdio"
		}
		switch ms.Transport {
		case "stdio":
			ms.Command = strings.TrimSpace(ms.Command)
			if !pluginMCPCommandPattern.MatchString(ms.Command) {
				return m, &pluginManifestError{Field: "mcp_server.command", Reason: "stdio MCP command 必须为安全可执行程序名（如 python3 / node）"}
			}
			switch strings.ToLower(ms.Command) {
			case "sh", "bash", "zsh", "dash", "ash", "csh", "tcsh", "ksh", "fish", "cmd", "cmd.exe", "powershell", "pwsh", "env", "sudo", "su":
				return m, &pluginManifestError{Field: "mcp_server.command", Reason: "stdio MCP command 禁止使用 shell 解释器或提权命令（请使用 python3 / node 等具体运行时）"}
			}
			if len(ms.Args) > 16 {
				return m, &pluginManifestError{Field: "mcp_server.args", Reason: "stdio MCP 最多声明 16 个参数"}
			}
			cleanArgs := make([]string, 0, len(ms.Args))
			for i, a := range ms.Args {
				a = strings.TrimSpace(a)
				if a == "" || len(a) > 256 || strings.ContainsAny(a, "\r\n\x00") || strings.Contains(a, "..") {
					return m, &pluginManifestError{Field: fmt.Sprintf("mcp_server.args[%d]", i), Reason: "MCP 参数非法或包含路径穿越"}
				}
				if credentialScalarContainsInlineMaterial(a) {
					return m, &pluginManifestError{Field: fmt.Sprintf("mcp_server.args[%d]", i), Reason: "MCP 参数严禁包含内联密钥"}
				}
				if strings.Contains(a, "${SKILL_DIR}") {
					rel, ok := strings.CutPrefix(a, "${SKILL_DIR}/")
					if !ok {
						return m, &pluginManifestError{Field: fmt.Sprintf("mcp_server.args[%d]", i), Reason: "${SKILL_DIR} 仅允许以 ${SKILL_DIR}/<相对路径> 形式引用"}
					}
					if _, relErr := validateSkillRelPath(rel); relErr != nil {
						return m, &pluginManifestError{Field: fmt.Sprintf("mcp_server.args[%d]", i), Reason: "${SKILL_DIR} 相对路径非法"}
					}
				}
				cleanArgs = append(cleanArgs, a)
			}
			ms.Args = cleanArgs
			ms.URL = ""
		case "http", "sse":
			ms.URL = strings.TrimSpace(ms.URL)
			if (!strings.HasPrefix(ms.URL, "http://") && !strings.HasPrefix(ms.URL, "https://")) || strings.Contains(ms.URL, "@") || len(ms.URL) > 512 {
				return m, &pluginManifestError{Field: "mcp_server.url", Reason: "http/sse MCP url 须为合法无凭据 HTTP(S) 地址"}
			}
			ms.Command = ""
			ms.Args = nil
		default:
			return m, &pluginManifestError{Field: "mcp_server.transport", Reason: "MCP transport 仅支持 stdio / http / sse"}
		}
		if ms.ToolTimeoutSecs == 0 {
			ms.ToolTimeoutSecs = 120
		}
		if ms.ToolTimeoutSecs < 5 || ms.ToolTimeoutSecs > 600 {
			return m, &pluginManifestError{Field: "mcp_server.tool_timeout_secs", Reason: "tool_timeout_secs 须在 5 至 600 秒之间"}
		}
		validateToolNames := func(field string, list []string) ([]string, error) {
			if len(list) > 32 {
				return nil, &pluginManifestError{Field: field, Reason: "最多声明 32 个 MCP 工具名"}
			}
			out := make([]string, 0, len(list))
			seen := make(map[string]bool, len(list))
			for i, tn := range list {
				tn = strings.TrimSpace(tn)
				if !pluginMCPToolNamePattern.MatchString(tn) {
					return nil, &pluginManifestError{Field: fmt.Sprintf("%s[%d]", field, i), Reason: "MCP 工具名格式无效"}
				}
				if !seen[tn] {
					seen[tn] = true
					out = append(out, tn)
				}
			}
			return out, nil
		}
		if ms.ReadTools, err = validateToolNames("mcp_server.read_tools", ms.ReadTools); err != nil {
			return m, err
		}
		if ms.ApplyTools, err = validateToolNames("mcp_server.apply_tools", ms.ApplyTools); err != nil {
			return m, err
		}
	}

	return m, nil
}

func coerceAndValidatePluginInputValue(field string, spec SkillPluginInput, raw any) (any, string, error) {
	switch spec.Type {
	case "number":
		var f float64
		switch v := raw.(type) {
		case int:
			f = float64(v)
		case int64:
			f = float64(v)
		case uint64:
			f = float64(v)
		case float64:
			f = v
		case json.Number:
			parsed, err := v.Float64()
			if err != nil {
				return nil, "", &pluginManifestError{Field: field, Reason: "必须为合法数字"}
			}
			f = parsed
		default:
			return nil, "", &pluginManifestError{Field: field, Reason: "必须为数字类型"}
		}
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, "", &pluginManifestError{Field: field, Reason: "必须为有限数值"}
		}
		if spec.Min != nil && f < *spec.Min {
			return nil, "", &pluginManifestError{Field: field, Reason: fmt.Sprintf("不能小于最小值 %v", *spec.Min)}
		}
		if spec.Max != nil && f > *spec.Max {
			return nil, "", &pluginManifestError{Field: field, Reason: fmt.Sprintf("不能大于最大值 %v", *spec.Max)}
		}
		return f, strconv.FormatFloat(f, 'f', -1, 64), nil

	case "boolean":
		b, ok := raw.(bool)
		if !ok {
			return nil, "", &pluginManifestError{Field: field, Reason: "必须为布尔类型 true / false"}
		}
		if b {
			return true, "true", nil
		}
		return false, "false", nil

	case "select":
		s, ok := raw.(string)
		if !ok {
			return nil, "", &pluginManifestError{Field: field, Reason: "必须为字符串选项值"}
		}
		if hasControlChars(s, false) {
			return nil, "", &pluginManifestError{Field: field, Reason: "不允许包含控制字符或换行"}
		}
		for _, opt := range spec.Options {
			if opt.Value == s {
				return s, s, nil
			}
		}
		return nil, "", &pluginManifestError{Field: field, Reason: fmt.Sprintf("选项值 %q 不在允许的枚举范围内", s)}

	case "credential_ref":
		s, ok := raw.(string)
		if !ok {
			return nil, "", &pluginManifestError{Field: field, Reason: "凭据引用必须为小写资源名称字符串"}
		}
		s = strings.TrimSpace(s)
		if s == "" || hasControlChars(s, false) || !pluginCredentialRefPattern.MatchString(s) || credentialScalarContainsInlineMaterial(s) {
			return nil, "", &pluginManifestError{Field: field, Reason: "凭据引用必须为合法的小写资源名，严禁填入凭据明文或 URL"}
		}
		return s, s, nil

	default:
		return nil, "", &pluginManifestError{Field: field, Reason: "不支持的参数类型"}
	}
}

func decodeStoredSkillUIManifest(rawJSON string) *SkillPluginManifest {
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON == "" {
		return nil
	}
	var m SkillPluginManifest
	if err := json.Unmarshal([]byte(rawJSON), &m); err != nil {
		return nil
	}
	return &m
}

func skillPluginScenarioDefault(manifest *SkillPluginManifest, scenarioID string) bool {
	if manifest == nil || manifest.Plugin.OptIn {
		return false
	}
	if len(manifest.Plugin.Scenarios) == 0 {
		return true
	}
	for _, sc := range manifest.Plugin.Scenarios {
		if sc == scenarioID {
			return true
		}
	}
	return false
}

// validatePluginRunInputs strictly validates L2 inputs against the single
// Skill's declared ui.inputs schema. For credential_ref parameters, it verifies
// fail-closed that the named credential exists, is active (with an active
// version), and has an active grant on every target in scope. It passes ONLY
// the credential resource name — never any decrypted payload.
func validatePluginRunInputs(
	ctx context.Context,
	store *GatewayStore,
	auth TokenAuth,
	sk platformSkillRecord,
	rawInputs map[string]any,
	targets []monitorTargetRef,
	allowEmptyTargets bool,
) (map[string]string, map[string]any, string, string, int) {
	if len(rawInputs) == 0 {
		return nil, nil, "", "", 0
	}
	manifest := decodeStoredSkillUIManifest(sk.UIManifestJSON)
	if manifest == nil || len(manifest.UI.Inputs) == 0 {
		return nil, nil, "unknown_plugin_input", fmt.Sprintf("Skill %q 未声明可接受的插件输入参数", sk.Slug), 400
	}
	specByKey := make(map[string]SkillPluginInput, len(manifest.UI.Inputs))
	for _, inp := range manifest.UI.Inputs {
		specByKey[inp.Key] = inp
	}

	envMap := make(map[string]string, len(manifest.UI.Inputs))
	cleanTyped := make(map[string]any, len(manifest.UI.Inputs))

	// Check unknown keys first.
	for k := range rawInputs {
		if _, ok := specByKey[k]; !ok {
			return nil, nil, "unknown_plugin_input", fmt.Sprintf("参数 %q 未在 Skill %q 的 plugin.yaml 中声明", k, sk.Slug), 400
		}
	}

	// Validate each declared input (caller-supplied value or declared default).
	for _, spec := range manifest.UI.Inputs {
		val, provided := rawInputs[spec.Key]
		if !provided || val == nil {
			if spec.Default != nil {
				val = spec.Default
				provided = true
			} else if spec.Required {
				return nil, nil, "invalid_plugin_input", fmt.Sprintf("缺少必填参数 %q（%s）", spec.Key, spec.Label), 400
			} else {
				continue
			}
		}
		typedVal, envStr, err := coerceAndValidatePluginInputValue(spec.Key, spec, val)
		if err != nil {
			code := "invalid_plugin_input"
			if spec.Type == "credential_ref" {
				code = "plugin_credential_ref_invalid"
			}
			return nil, nil, code, err.Error(), 400
		}
		if credentialScalarContainsInlineMaterial(envStr) {
			return nil, nil, "invalid_plugin_input", fmt.Sprintf("参数 %q 疑似包含敏感凭据明文，已拒绝", spec.Key), 400
		}
		if spec.Type == "credential_ref" {
			if code, msg, status := verifyPluginCredentialRef(ctx, store, auth, envStr, spec.CredentialUse, targets, allowEmptyTargets); status != 0 {
				return nil, nil, code, msg, status
			}
		}
		envMap[spec.Key] = envStr
		cleanTyped[spec.Key] = typedVal
	}
	return envMap, cleanTyped, "", "", 0
}

func verifyPluginCredentialRef(
	ctx context.Context,
	store *GatewayStore,
	auth TokenAuth,
	credName string,
	requiredUse string,
	targets []monitorTargetRef,
	allowEmptyTargets bool,
) (string, string, int) {
	if store == nil {
		return "plugin_credential_ref_unbound", "凭据存储不可用", 503
	}
	if len(targets) == 0 {
		if !allowEmptyTargets {
			return "plugin_credential_ref_unbound", fmt.Sprintf("凭据引用 %q 无法在无目标上下文中校验绑定", credName), 400
		}
		if err := store.verifyCredentialRefActiveGlobally(ctx, credName, requiredUse); err != nil {
			return "plugin_credential_ref_unbound", fmt.Sprintf("凭据 %q 不存在、未激活或缺少有效目标授权", credName), 400
		}
		return "", "", 0
	}
	for _, t := range targets {
		if auth.UserID == "" || auth.UserID == "system" {
			if err := store.verifyCredentialRefBoundForTarget(ctx, credName, requiredUse, t.Cluster, t.Instance); err != nil {
				return "plugin_credential_ref_unbound", fmt.Sprintf("凭据 %q 未激活或未授权给目标 %s/%s", credName, t.Cluster, t.Instance), 400
			}
			continue
		}
		resolved, err := store.AuthorizeCredentialUse(CredentialUseRequest{
			ActorID:    auth.UserID,
			Credential: credName,
			Scope: CredentialGrantScope{
				Cluster:  t.Cluster,
				Instance: t.Instance,
				Use:      CredentialUse(requiredUse),
			},
		})
		if err != nil {
			if errors.Is(err, ErrCredentialForbidden) {
				return "monitor_target_forbidden", fmt.Sprintf("当前账号无权在目标 %s/%s 使用凭据 %q", t.Cluster, t.Instance, credName), 403
			}
			return "plugin_credential_ref_unbound", fmt.Sprintf("凭据 %q 未激活、未发布版本或未授权给目标 %s/%s", credName, t.Cluster, t.Instance), 400
		}
		if requiredUse != "" && resolved.Grant.Scope.Use != CredentialUse(requiredUse) {
			return "plugin_credential_ref_unbound", fmt.Sprintf("凭据 %q 在目标 %s/%s 的用途（%s）不匹配要求的 %s", credName, t.Cluster, t.Instance, resolved.Grant.Scope.Use, requiredUse), 400
		}
	}
	return "", "", 0
}

func (s *GatewayStore) verifyCredentialRefActiveGlobally(ctx context.Context, credName, requiredUse string) error {
	rows, err := s.readDB().QueryContext(ctx, `SELECT c.id, c.type, g.scope_json
		FROM credentials c JOIN credential_grants g ON g.credential_id = c.id
		WHERE c.name = ? AND c.state = ? AND g.state = ?`,
		credName, CredentialStateActive, CredentialStateActive)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var credID, credType, scopeJSON string
		if err := rows.Scan(&credID, &credType, &scopeJSON); err != nil {
			return err
		}
		var scope CredentialGrantScope
		if err := json.Unmarshal([]byte(scopeJSON), &scope); err != nil {
			continue
		}
		scope = normalizeCredentialGrantScope(scope)
		if scope.Use == "" {
			scope.Use = credentialUseForType(CredentialType(credType))
		}
		if requiredUse != "" && scope.Use != CredentialUse(requiredUse) {
			continue
		}
		if _, err := s.ActiveCredentialVersion(credID); err == nil {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return ErrCredentialGrantDenied
}

func (s *GatewayStore) verifyCredentialRefBoundForTarget(ctx context.Context, credName, requiredUse, cluster, instance string) error {
	rows, err := s.readDB().QueryContext(ctx, `SELECT c.id, c.type, g.scope_json
		FROM credentials c JOIN credential_grants g ON g.credential_id = c.id
		WHERE c.name = ? AND c.state = ? AND g.state = ?`,
		credName, CredentialStateActive, CredentialStateActive)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var credID, credType, scopeJSON string
		if err := rows.Scan(&credID, &credType, &scopeJSON); err != nil {
			return err
		}
		var scope CredentialGrantScope
		if err := json.Unmarshal([]byte(scopeJSON), &scope); err != nil {
			continue
		}
		scope = normalizeCredentialGrantScope(scope)
		if scope.Cluster != cluster || scope.Instance != instance {
			continue
		}
		if scope.Use == "" {
			scope.Use = credentialUseForType(CredentialType(credType))
		}
		if requiredUse != "" && scope.Use != CredentialUse(requiredUse) {
			continue
		}
		if _, err := s.ActiveCredentialVersion(credID); err == nil {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return ErrCredentialGrantDenied
}

// formatPluginInputsPromptBlock formats validated L2 inputs as a delimited
// structured block appended to AI-driven Skill inspection prompts.
func formatPluginInputsPromptBlock(inputs map[string]string) string {
	if len(inputs) == 0 {
		return ""
	}
	keys := make([]string, 0, len(inputs))
	for k := range inputs {
		if pluginInputKeyPattern.MatchString(k) {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString("\n<<DOOPS_INPUTS>>\n")
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(sanitizeSecretText(inputs[k]))
		sb.WriteByte('\n')
	}
	sb.WriteString("<<END_DOOPS_INPUTS>>\n")
	return sb.String()
}

func sanitizePluginInputsForEvidence(inputs map[string]string) map[string]string {
	if len(inputs) == 0 {
		return nil
	}
	out := make(map[string]string, len(inputs))
	for k, v := range inputs {
		if pluginInputKeyPattern.MatchString(k) {
			out[k] = sanitizeSecretText(v)
		}
	}
	return out
}
