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

test("the AI assistant panel only occupies the content area on its own tab or terminal AI split with an online target", () => {
  const panel = shell.match(/hidden=\{view !== "console" \|\| \(tab !== "ask" && !\(tab === "terminal" && terminalAiOpen\)\) \|\| !onlineSelected\}/)
  assert.ok(panel, "assistant panel wrapper keeps its hidden condition")
})

test("online-only deploy actions without an online target are refused with a visible reason while platform views stay reachable", () => {
  assert.match(shell, /function blockMissingOnlineTarget\(destination: ConsoleNavEntry\["destination"\]\): boolean/)
  assert.match(shell, /destination\.view === "deploy"/)
  assert.match(shell, /destination\.section === "workload" \|\| destination\.section === "upgrade"/)
  assert.match(shell, /setNeedsTarget\(true\)/)
  assert.match(shell, /if \(blockMissingOnlineTarget\(destination\)\) return/)
  // 平台级台账、态势感知、资源监控、智能巡检、Agent 运行监控与插件中心均读取 Gateway 持久化数据，无在线目标时不得被拦截。
  assert.doesNotMatch(shell, /if \(next !== "assets" \|\| onlineSelected\) return false/)
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

test("the target switcher dropdown renders above the AI workspace and keeps outside-click guards on the portaled menu", () => {
  // 顶栏带 backdrop-blur 会形成层叠上下文，必须显式抬高 z-index，避免被下方工作区盖住。
  const header = shell.match(/<header\s+className="([^"]+)"/)
  assert.ok(header, "console shell renders a top header")
  assert.match(header[1], /\brelative\b/)
  assert.match(header[1], /\bz-(?:[3-9]0|\[[0-9]+\])\b/)

  // 目标选择器下拉菜单通过 Portal 挂到 document.body 并使用不透明 bg-popover，
  // 且 mousedown 监听同时排除触发器与菜单自身，避免点击选项时直接被外部点击逻辑关掉导致无法切换。
  assert.match(switcher, /createPortal\(/)
  assert.match(switcher, /document\.body/)
  assert.match(switcher, /role="listbox"[\s\S]{0,300}bg-popover/)
  assert.match(switcher, /ref\.current\?\.contains\(target\)\s*\|\|\s*menuRef\.current\?\.contains\(target\)/)
})

test("ConsoleShell never calls React hooks after early auth/session returns", () => {
  const earlyReturnAt = shell.indexOf("if (authLoading || disconnecting)")
  assert.ok(earlyReturnAt > 0, "ConsoleShell keeps early auth loading return")
  const afterEarlyReturn = shell.slice(earlyReturnAt)
  assert.doesNotMatch(
    afterEarlyReturn,
    /\buse(?:State|Effect|Memo|Callback|Ref|SyncExternalStore|LayoutEffect|Reducer|Context)\s*\(/,
    "all React hooks in ConsoleShell must run before early auth/session returns (prevents React #310)",
  )
})

test("?view=admin deep links redirect platform admins to /console/admin and show an explicit warning for non-admins", () => {
  assert.match(shell, /view === "admin"/)
  assert.match(shell, /window\.location\.replace\(buildAdminRedirectUrl\(window\.location\.search, adminTab\)\)/)
  assert.match(shell, /当前账号不是平台管理员，无法访问「平台管理」页面。/)
})
