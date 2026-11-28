import assert from "node:assert/strict"
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs"
import { join } from "node:path"
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

test("public portal routes share the unified PortalContainer width contract without drifting wider or narrower than header/footer", () => {
  const portalShell = readFileSync(new URL("../components/ui/portal-shell.tsx", import.meta.url), "utf8")
  const chrome = readFileSync(new URL("../components/site-chrome.tsx", import.meta.url), "utf8")
  const landing = readFileSync(new URL("../components/landing-page.tsx", import.meta.url), "utf8")
  const workbench = readFileSync(new URL("../components/landing-workbench.tsx", import.meta.url), "utf8")
  const cases = readFileSync(new URL("../components/cases-page.tsx", import.meta.url), "utf8")
  const docsShell = readFileSync(new URL("../components/docs-shell.tsx", import.meta.url), "utf8")
  const blogLayout = readFileSync(new URL("../app/blog/layout.tsx", import.meta.url), "utf8")

  assert.match(portalShell, /max-w-7xl/)
  assert.match(chrome, /<PortalContainer\b/)
  assert.match(docsShell, /<PortalContainer\b/)
  assert.match(blogLayout, /<PortalContainer\b/)
  assert.doesNotMatch(docsShell, /max-w-\[90rem\]/, "DocsShell must not exceed the header/footer portal width")
  assert.doesNotMatch(workbench, /max-w-5xl/, "LandingWorkbench must align flush with the portal rail")
  assert.doesNotMatch(landing, /mx-auto max-w-(?:3xl|5xl|7xl) px-4/, "Landing sections must use PortalContainer instead of mixed ad-hoc max-w widths")
  assert.doesNotMatch(cases, /mx-auto max-w-(?:3xl|4xl|5xl|7xl) px-4/, "Cases sections must use PortalContainer instead of mixed ad-hoc max-w widths")
})

test("whole-site frontend enforces shadcn/ui framework tokens: zero arbitrary px classes, zero bare/hex Tailwind colors, zero legacy CSS primitives, and zero raw tables or form tags", () => {
  const repoRoot = new URL("..", import.meta.url).pathname
  assert.ok(existsSync(join(repoRoot, "components.json")), "shadcn/ui components.json must exist")
  assert.ok(!existsSync(join(repoRoot, "components/ui/modal.tsx")), "duplicate legacy modal.tsx must remain removed")

  const globalsCss = readFileSync(join(repoRoot, "app/globals.css"), "utf8")
  const consoleCss = readFileSync(join(repoRoot, "app/console/console.css"), "utf8")
  assert.doesNotMatch(globalsCss, /--text-(?:xs|sm|base|lg|xl):/, "app/globals.css must preserve Tailwind v4 standard rem typography scale")
  assert.match(globalsCss, /--sidebar-primary:/, "app/globals.css must define OKLCH shadcn sidebar tokens")
  assert.doesNotMatch(consoleCss, /#[0-9a-fA-F]{3,8}\b/, "app/console/console.css must not hardcode hex colors")
  assert.doesNotMatch(consoleCss, /\.console-sidebar\b/, "sidebar styling belongs to shadcn Sidebar component, not console.css")

  function walkTsx(dir) {
    const out = []
    for (const entry of readdirSync(dir)) {
      const full = join(dir, entry)
      if (statSync(full).isDirectory()) out.push(...walkTsx(full))
      else if (entry.endsWith(".tsx")) out.push(full)
    }
    return out
  }

  const files = [...walkTsx(join(repoRoot, "app")), ...walkTsx(join(repoRoot, "components"))]
  assert.ok(files.length > 100, `expected >100 tsx files, got ${files.length}`)

  const pxRe = /(?:^|[\s"'`])((?:[a-z0-9-]+:)*[a-z-]+-\[[^\]]*px[^\]]*\])/g
  const bareColorRe = /\b(?:bg|text|border|ring|accent|from|to|via)-(?:slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose)-\d{2,3}(?:\/\d+)?\b|(?:bg|text|border|ring|accent)-\[#[0-9a-fA-F]{3,8}\]/g
  const legacyCssRe = /\b(?:btn-primary|btn-danger|btn-ghost|btn-sm|btn-md|btn-lg|btn-icon|btn-file|status-error|status-warning|status-success|status-info|console-sidebar|console-nav-item|console-login)\b/g
  const rawTableRe = /<(?:table|tr)\b/g
  const rawSelectRe = /<select\b/g
  const rawTextareaRe = /<textarea\b/g
  const rawInputRe = /<input\b[^>]*>/g
  const rawSwitchRe = /role="switch"/g

  const violations = []
  for (const file of files) {
    const rel = file.slice(repoRoot.length)
    const content = readFileSync(file, "utf8")
    const isUiPrimitive = rel.startsWith("components/ui/")
    const pxHits = [...content.matchAll(pxRe)].map((m) => m[1])
    if (pxHits.length) violations.push(`${rel}: arbitrary px ${pxHits.slice(0, 3).join(", ")}`)
    const colorHits = [...content.matchAll(bareColorRe)].map((m) => m[0])
    if (colorHits.length) violations.push(`${rel}: bare/hex color ${colorHits.slice(0, 3).join(", ")}`)
    const legacyHits = [...content.matchAll(legacyCssRe)].map((m) => m[0])
    if (legacyHits.length) violations.push(`${rel}: legacy CSS ${legacyHits.slice(0, 3).join(", ")}`)
    if (!isUiPrimitive) {
      if (rel !== "components/docs-markdown.tsx" && rawTableRe.test(content)) {
        violations.push(`${rel}: raw <table>/<tr> instead of @/components/ui Table/TableRow`)
      }
      if (rawSelectRe.test(content)) violations.push(`${rel}: raw <select> instead of @/components/ui Select`)
      if (rawTextareaRe.test(content)) violations.push(`${rel}: raw <textarea> instead of @/components/ui Textarea`)
      for (const m of content.matchAll(rawInputRe)) {
        if (!/type=["']file["']/.test(m[0])) {
          violations.push(`${rel}: raw <input> instead of @/components/ui Input/Checkbox`)
          break
        }
      }
      if (rawSwitchRe.test(content)) violations.push(`${rel}: hand-rolled role="switch" instead of @/components/ui Switch`)
    }
  }
  assert.deepEqual(violations, [], `frontend framework guard violations:\n${violations.join("\n")}`)
})
