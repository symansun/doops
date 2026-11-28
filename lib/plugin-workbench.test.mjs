import assert from "node:assert/strict"
import test from "node:test"
import { readFileSync } from "node:fs"
import {
  formatMibCompact,
  formatPluginOriginLabel,
  parseGpuArtifact,
  shouldAutoLiveProbe,
  splitPluginViewsForTabs,
} from "./plugin-workbench.ts"

function makePlugin(overrides = {}) {
  return {
    id: "skill.gpu-governance",
    tier: "declarative",
    skill_slug: "gpu-governance",
    scope: "system",
    builtin: true,
    can_delete: false,
    owner_is_me: false,
    skill_enabled: true,
    name: "GPU 算力治理",
    description: "desc",
    category: "infrastructure",
    effective: true,
    scenario_default: true,
    override: null,
    dependencies: { skills: [], mcp: [] },
    manifest: {
      api_version: "doops.sh/v1alpha1",
      kind: "SkillPlugin",
      plugin: { id: "skill.gpu-governance", name: "GPU 算力治理", category: "infrastructure", icon: "cpu", scenarios: [] },
      ui: {
        slots: {},
        inputs: [],
        presets: [{ id: "p1", title: "快速算力体检", mode: "script" }],
        views: [],
      },
    },
    ...overrides,
  }
}

test("Given gpu-governance artifact JSON, When parsing GPU devices and processes, Then idle VRAM hogs are flagged as zombie suspects and summary totals stay exact", () => {
  const parsed = parseGpuArtifact({
    status: "warning",
    summary: "检出 2 张 GPU",
    applied_inputs: { WARN_TEMP_C: 85, WARN_MEM_PCT: 90 },
    gpus: [
      { index: "0", name: "NVIDIA A100-SXM4-80GB", uuid: "GPU-AAA", util_percent: 2.0, mem_used_mib: 65536, mem_total_mib: 81920, mem_percent: 80.0, temp_c: 54, power_w: 85, ecc_uncorrected: 0 },
      { index: "1", name: "NVIDIA A100-SXM4-80GB", uuid: "GPU-BBB", util_percent: 94.0, mem_used_mib: 75776, mem_total_mib: 81920, mem_percent: 92.5, temp_c: 86, power_w: 310, ecc_uncorrected: 1 },
    ],
    gpu_processes: [
      { gpu_uuid: "GPU-BBB", pid: "4201", process: "python3", used_mib: 74000 },
      { gpu_uuid: "GPU-AAA", pid: "1999", process: "jupyter-kernel", used_mib: 62000 },
    ],
  })

  assert.ok(parsed)
  assert.equal(parsed.totalGpus, 2)
  assert.equal(parsed.avgUtilPercent, 48.0)
  assert.equal(parsed.maxMemPercent, 92.5)
  assert.equal(parsed.maxTempC, 86)
  assert.equal(parsed.eccTotal, 1)
  assert.equal(parsed.gpus[0].health, "ok")
  assert.equal(parsed.gpus[1].health, "critical")
  assert.equal(parsed.zombieCount, 1)
  // Zombie process must sort to top even when active process uses slightly more MiB.
  assert.equal(parsed.processes[0].pid, "1999")
  assert.equal(parsed.processes[0].gpuIndex, "0")
  assert.equal(parsed.processes[0].zombieSuspect, true)
  assert.equal(parsed.processes[1].zombieSuspect, false)
  assert.equal(formatMibCompact(65536), "64.0 GiB")
  assert.equal(formatMibCompact(512), "512 MiB")
})

test("Given plugin capabilities, When deciding auto live-probe, Then only zero-token script plugins without required credentials auto-probe once", () => {
  const scriptPlugin = makePlugin()
  assert.equal(shouldAutoLiveProbe({ plugin: scriptPlugin, hasCachedRun: false, hasOnlineTarget: true, alreadyProbed: false }), true)
  assert.equal(shouldAutoLiveProbe({ plugin: scriptPlugin, hasCachedRun: true, hasOnlineTarget: true, alreadyProbed: false }), false)
  assert.equal(shouldAutoLiveProbe({ plugin: scriptPlugin, hasCachedRun: false, hasOnlineTarget: false, alreadyProbed: false }), false)
  assert.equal(shouldAutoLiveProbe({ plugin: scriptPlugin, hasCachedRun: false, hasOnlineTarget: true, alreadyProbed: true }), false)

  const aiOnlyPlugin = makePlugin({
    manifest: {
      ...scriptPlugin.manifest,
      ui: { ...scriptPlugin.manifest.ui, presets: [{ id: "ai", title: "AI", mode: "agent" }] },
    },
  })
  assert.equal(shouldAutoLiveProbe({ plugin: aiOnlyPlugin, hasCachedRun: false, hasOnlineTarget: true, alreadyProbed: false }), false)

  const credRequiredPlugin = makePlugin({
    manifest: {
      ...scriptPlugin.manifest,
      ui: {
        ...scriptPlugin.manifest.ui,
        inputs: [{ key: "API_CRED", label: "凭据", type: "credential_ref", required: true }],
      },
    },
  })
  assert.equal(shouldAutoLiveProbe({ plugin: credRequiredPlugin, hasCachedRun: false, hasOnlineTarget: true, alreadyProbed: false }), false)
})

test("Given declarative plugin views, When splitting views across workbench tabs, Then live overview and historical reports separate cleanly", () => {
  const { liveViews, reportViews } = splitPluginViewsForTabs([
    { type: "kpi_grid", title: "KPI" },
    { type: "findings_list", title: "Findings" },
    { type: "resource_table", title: "Resources" },
    { type: "charts", title: "Charts" },
    { type: "trend_series", title: "Trends" },
    { type: "checks_table", title: "Checks" },
    { type: "artifact_viewer", title: "Artifact" },
  ])
  assert.deepEqual(liveViews.map((v) => v.type), ["kpi_grid", "findings_list", "resource_table", "charts"])
  assert.deepEqual(reportViews.map((v) => v.type), ["trend_series", "checks_table", "artifact_viewer"])
  assert.equal(formatPluginOriginLabel({ scope: "system", builtin: true }), "内置扩展")
  assert.equal(formatPluginOriginLabel({ scope: "system", builtin: false }), "已安装插件")
  assert.equal(formatPluginOriginLabel({ scope: "custom", builtin: false }), "个人扩展")
})

test("Given the rectified plugin workbench components, When inspecting source, Then runner shell jargon and upside-down forms are gone", () => {
  const hostSrc = readFileSync(new URL("../components/plugins/skill-plugin-host.tsx", import.meta.url), "utf8")
  assert.doesNotMatch(hostSrc, /核心内置 L2/, "internal L2 developer badge must not appear in plugin header")
  assert.doesNotMatch(hostSrc, /系统 L2 插件/, "internal L2 developer badge must not appear in plugin header")
  assert.doesNotMatch(hostSrc, /自定义 L2 插件/, "internal L2 developer badge must not appear in plugin header")
  assert.doesNotMatch(hostSrc, /DOOPS_INPUT_\*/, "raw internal env var jargon must not clutter plugin workbench")
  assert.doesNotMatch(hostSrc, /暂无该插件的历史巡检记录/, "dead runner-style empty copy must be replaced with domain workbench UX")
  assert.ok(hostSrc.includes("<PageTabs"), "plugin workbench must organize into clean PageTabs")
  assert.ok(hostSrc.includes("GpuGovernanceWorkbench"), "gpu-governance must render dedicated visual GPU workbench")
  assert.ok(hostSrc.includes("shouldAutoLiveProbe"), "plugin workbench must auto-probe fast read-only plugins when empty")
})
