import assert from "node:assert/strict"
import test from "node:test"
import { readFileSync } from "node:fs"
import {
  manifestStatus,
  resourceKindLabel,
  showsAppliedRevision,
} from "./target-manifest.ts"

const status = (overrides = {}) => ({
  online: true,
  supported: true,
  manifest: { revision: 4, resources: [] },
  applied_revision: 4,
  ...overrides,
})

// 后端 Agent 观测（agent_resource_probe.go / agent_resource_inventory.go）与台账（declaredAgentResourcesSQL）
// 实际会产生的 kind；新增 kind 时这里和标签表一起改。
const backendKinds = ["machine", "cluster", "namespace", "container", "site", "database", "system", "credential", "image"]

test("Given backend resource kinds, When labelled, Then every kind renders Chinese instead of the raw token", () => {
  for (const kind of backendKinds) {
    const label = resourceKindLabel(kind)
    assert.notEqual(label, kind, `${kind} 必须有中文标签`)
    assert.match(label, /[\u4e00-\u9fa5]/, `${kind} 的标签应是中文`)
  }
  // docker 观测的 kind 就是 container（probeAgentDocker），这是 SF-24 修的线上缺陷。
  assert.equal(resourceKindLabel("container"), "容器")
  // 历史写法保留，避免老快照回退成英文。
  assert.equal(resourceKindLabel("container_engine"), "容器引擎")
})

test("Given an unknown kind, When labelled, Then it is returned as-is without throwing", () => {
  assert.equal(resourceKindLabel("widget"), "widget")
  assert.equal(resourceKindLabel(""), "")
})

test("Given manifest payloads, When resolved, Then the four states keep offline > unsupported > pending > synced order", () => {
  // 离线优先：即使 supported=false 也必须先说离线。
  assert.deepEqual(manifestStatus(status({ online: false, supported: false })), { label: "Agent 离线", variant: "warning" })
  // 不支持同步优先于等待同步：applied_revision 落后时也不能报「等待同步」。
  assert.deepEqual(manifestStatus(status({ supported: false, applied_revision: 0 })), { label: "Agent 不支持同步", variant: "warning" })
  assert.deepEqual(manifestStatus(status({ applied_revision: 3 })), { label: "等待同步", variant: "warning" })
  assert.deepEqual(manifestStatus(status({ manifest: null, applied_revision: 4 })), { label: "等待同步", variant: "warning" })
  assert.deepEqual(manifestStatus(status({ applied_revision: 4 })), { label: "已同步", variant: "success" })
  // 老响应可能完全没有 applied_revision 字段。
  assert.deepEqual(manifestStatus(status({ manifest: { revision: 0, resources: [] }, applied_revision: undefined })), { label: "已同步", variant: "success" })
})

test("Given unsupported sync, When deciding the applied-version row, Then it is hidden", () => {
  assert.equal(showsAppliedRevision(status({ supported: true })), true)
  assert.equal(showsAppliedRevision(status({ supported: false })), false)
})

test("Wiring lock: the manifest card renders the shared model instead of its own copies", () => {
  const source = readFileSync(new URL("../components/target-manifest-card.tsx", import.meta.url), "utf8")
  assert.match(source, /from "@\/lib\/target-manifest"/, "组件必须引用 lib/target-manifest")
  assert.equal((source.match(/resourceKindLabel\(/g) ?? []).length, 1, "资源 kind 必须经 resourceKindLabel 渲染")
  assert.match(source, /manifestStatus\(/, "徽章必须用 manifestStatus 判定")
  assert.match(source, /showsAppliedRevision\(/, "已应用版本行必须按 showsAppliedRevision 隐藏")
  assert.ok(source.includes("最后观测时间"), "标签必须叫最后观测时间（值取自 last_observed_at）")
  assert.ok(!source.includes("最后同步时间"), "不得再出现「最后同步时间」这个错标签")
  assert.ok(!source.includes("const resourceKinds"), "组件内不得再维护一份 kind 标签表")
})
