import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"
import { agentErrorMessage, stripConnectCode } from "./agent-error.ts"

// 源码级守卫：这些重复一旦被重新写回来，行为会悄悄分叉（时间格式不一致、弹窗丢 Escape/焦点、
// 错误码前缀漏到界面），所以在这里拦住，而不是等人工复核。

function source(path) {
  return readFileSync(new URL(`../${path}`, import.meta.url), "utf8")
}

test("Given any component, Then date rendering goes through lib/format", () => {
  const files = [
    "components/audit-panel.tsx",
    "components/asset-coverage-panel.tsx",
    "components/asset-inspection-panel.tsx",
    "components/asset-monitor-panel.tsx",
    "components/asset-platform-panel.tsx",
    "components/asset-topology-panel.tsx",
    "components/asset-weekly-summary.tsx",
    "components/admin-console.tsx",
    "components/admin-config-dispatch.tsx",
    "components/business-assets-panel.tsx",
    "components/cluster-assets-panel.tsx",
    "components/credential-panel.tsx",
    "components/machine-diagnostic-panel.tsx",
    "components/proxy-routes-panel.tsx",
    "components/website-inventory.tsx",
    "lib/console-persistence.ts",
  ]
  for (const file of files) {
    const text = source(file)
    assert.doesNotMatch(text, /\.toLocaleString\(/, `${file} 应改用 @/lib/format`)
  }
})

test("Given a panel that shows time, Then it declares the time unit explicitly", () => {
  // 秒级时间戳来自 inspections / 周报接口；把它当毫秒读会显示 1970 年附近的时间。
  assert.match(source("components/asset-inspection-panel.tsx"), /formatDateTime\(v, \{ unit: "s" \}\)/)
  assert.match(source("components/asset-weekly-summary.tsx"), /unit: "s"/)
  assert.match(source("components/asset-monitor-panel.tsx"), /unit: "ms"/)
})

test("Given a modal, Then it renders through the shared Modal with Escape and focus restore", () => {
  const modal = source("components/ui/modal.tsx")
  assert.match(modal, /createPortal/)
  assert.match(modal, /event\.key !== "Escape"/)
  assert.match(modal, /previous\?\.focus\(\)/)
  assert.match(modal, /aria-modal="true"/)
  for (const file of [
    "components/credential-panel.tsx",
    "components/credential-bundle-dialog.tsx",
    "components/credential-secret-import.tsx",
  ]) {
    const text = source(file)
    assert.doesNotMatch(text, /fixed inset-0 z-50/, `${file} 不应再手写弹窗外壳`)
  }
})

test("Given stacked connect codes, Then none of them reach the user", () => {
  assert.equal(stripConnectCode("[doops_operation_failed] [aborted] 当前会话仍有运行中的任务"), "当前会话仍有运行中的任务")
  assert.equal(agentErrorMessage("[doops_operation_failed] [aborted] 当前会话仍有运行中的任务"), "当前会话仍有运行中的任务")
  // deployment.ts 的 agentErrorText 和 agentErrorMessage 共用同一条规则，不再各写一份正则。
  assert.match(source("lib/deployment.ts"), /stripConnectCode/)
  assert.doesNotMatch(source("lib/deployment.ts"), /replace\(\/\^\(\?:\\\[/)
})
