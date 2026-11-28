import assert from "node:assert/strict"
import test from "node:test"
import { readFileSync } from "node:fs"
import {
  ADMIN_TABS,
  ASSET_LEDGER_SECTIONS,
  CONSOLE_HOME,
  DEPLOY_SECTIONS,
  consoleNavEntries,
  credentialsDestination,
  targetTabLabel,
} from "./console-views.ts"

const USER_LABELS = [
  "首页",
  "Agent 概览",
  "AI 助手",
  "终端",
  "自动化",
  "部署包",
  "审计记录",
  "知识库",
  "机器台账",
  "Kubernetes",
  "业务系统",
  "数据库",
  "网站",
  "镜像",
  "凭据",
  "态势感知",
  "资源监控",
  "Agent 运行监控",
  "插件中心",
  "发布工作负载",
  "升级 Agent",
  "首次接入",
  "CI/CD 接入",
]

const ADMIN_LABELS = [...USER_LABELS, "平台管理"]

test("Given the account role, When the sidebar lists navigation, Then platform admin is hidden for non-admin users", () => {
  assert.deepEqual(consoleNavEntries(false).map((entry) => entry.label), USER_LABELS)
  assert.deepEqual(consoleNavEntries(true).map((entry) => entry.label), ADMIN_LABELS)
  for (const entry of consoleNavEntries(true)) {
    assert.ok(!("children" in entry), "navigation entries stay a single flat list grouped by view")
  }
})

test("Given the console shell and sidebar navigation, When rendered, Then desktop sidebar and mobile dialog drawer are wired cleanly", () => {
  const shell = readFileSync(new URL("../components/console-shell.tsx", import.meta.url), "utf8")
  assert.ok(shell.includes("<TargetSwitcher"), "the agent switcher stays in the header")
  assert.ok(shell.indexOf("<TargetSwitcher") < shell.indexOf("<ConsoleNav"))
  assert.ok(shell.includes("useState<ConsoleView>(CONSOLE_HOME)"))
  assert.match(shell, /view === CONSOLE_HOME \? \(\s*<DashboardPanel/)
  assert.ok(shell.indexOf("<DashboardPanel") < shell.indexOf("<AgentDirectory"))

  const nav = readFileSync(new URL("../components/console-nav.tsx", import.meta.url), "utf8")
  assert.ok(nav.includes("<Sidebar>"), "desktop renders the shadcn Sidebar rail")
  assert.ok(nav.includes('id="console-nav-drawer"') && nav.includes("<Sheet"), "mobile renders the Radix Sheet navigation drawer")
})

test("Given a target tab, When labeled, Then it uses the canonical navigation label", () => {
  assert.equal(targetTabLabel("overview"), "Agent 概览")
  assert.equal(targetTabLabel("ask"), "AI 助手")
  assert.equal(targetTabLabel("kb"), "知识库")
  assert.equal(targetTabLabel("database"), "数据库")
  assert.equal(targetTabLabel("tasks"), "自动化")
  assert.equal(targetTabLabel("assets"), "资源对账清单")
  assert.equal(targetTabLabel("monitor"), "资源监控")
  assert.deepEqual(ASSET_LEDGER_SECTIONS.map((section) => section.label), ["机器台账", "集群台账", "业务系统"])
  assert.deepEqual(DEPLOY_SECTIONS.filter((section) => section.needsTarget).map((section) => section.label), ["发布工作负载", "升级 Agent"])
  assert.deepEqual(ADMIN_TABS.map((section) => section.label), ["实例", "配置中心", "用户", "权限", "令牌", "敏感词", "代理路由", "出站 Webhook", "Agent管理"])
  assert.equal(CONSOLE_HOME, "home")
})

test("Given the account role, When a page asks to open credentials, Then admins land on the console credentials tab", () => {
  for (const [name, isAdmin, want] of [
    ["admin", true, { view: "console", tab: "credentials" }],
    ["ordinary user", false, null],
  ]) {
    assert.deepEqual(credentialsDestination(isAdmin), want, name)
  }
  assert.notEqual(credentialsDestination(true)?.view, "admin")
})

test("Given ConsoleFormAssist scope rules, When inspected, Then AI form assist is restricted strictly to explicit asset-entry forms", () => {
  const domHelper = readFileSync(new URL("./form-assist-dom.ts", import.meta.url), "utf8")
  assert.ok(domHelper.includes("scopeSelector='[data-assist-scope=\"asset-entry\"]'"))
  assert.equal(domHelper.includes("form,[data-assist-scope]"), false, "must not attach globally to every form or dialog")
  for (const file of [
    "../components/machine-assets-panel.tsx",
    "../components/cluster-assets-panel.tsx",
    "../components/business-assets-panel.tsx",
    "../components/resource-catalog-panel.tsx",
    "../components/personal-asset-panel.tsx",
  ]) {
    const src = readFileSync(new URL(file, import.meta.url), "utf8")
    assert.ok(src.includes('data-assist-scope="asset-entry"'), `${file} should opt into asset-entry AI form assist`)
  }
})
