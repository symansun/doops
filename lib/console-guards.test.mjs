import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"

// 这些行为只在浏览器里出现，源码级断言防止改动回退：
// 1. 资产台账/平台监控被 AI 助手面板盖住——面板只能在自己是当前标签时占用内容区。
// 2. 没有可用目标时点了需要目标的功能——必须给出可见提示，不能静默无响应。
// 3. 文件浏览器不能把内部会话 ID 当路径展示。
const shell = readFileSync(new URL("../components/console-shell.tsx", import.meta.url), "utf8")
const switcher = readFileSync(new URL("../components/target-switcher.tsx", import.meta.url), "utf8")
const files = readFileSync(new URL("../components/files-panel.tsx", import.meta.url), "utf8")

test("the AI assistant panel only occupies the content area on its own tab with a target", () => {
  const panel = shell.match(/hidden=\{view !== "console" \|\| tab !== "ask"[^}]*\}/)
  assert.ok(panel, "assistant panel wrapper keeps its hidden condition")
  assert.match(panel[0], /\|\| !onlineSelected/)
})

test("asset ledger navigation without an online target is refused with a visible reason", () => {
  assert.match(shell, /function blockMissingTarget\(next: ConsoleView\): boolean/)
  assert.match(shell, /if \(next !== "assets" \|\| onlineSelected\) return false/)
  assert.match(shell, /setNeedsTarget\(true\)/)
  // 守卫要接在真正阻止导航的路径上；部署向导无目标时仍可进入。
  assert.match(shell, /if \(next !== view && blockMissingTarget\(next\)\) return/)
  assert.match(shell, /if \(blockMissingTarget\(destination\.view\)\) return/)
  assert.match(shell, /if \(blockMissingTarget\("assets"\)\) return/)
  // 提示必须渲染在目标选择器里，用户在首页也能看见。
  assert.match(shell, /needsTarget=\{needsTarget\}/)
  assert.match(switcher, /role="alert"[^>]*>[\s\S]{0,40}当前没有可用的 Agent 目标/)
})

test("selecting a target clears the missing-target hint", () => {
  assert.match(shell, /function selectTarget\(t: Target\) \{[\s\S]{0,1200}setNeedsTarget\(false\)/)
})

test("the file browser shows the workspace path instead of the raw session id", () => {
  assert.doesNotMatch(files, /\/root\/ws\/\{sessionId\}/)
  assert.match(files, /workspaceRootPath\(sessionId\)/)
})
