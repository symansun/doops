import assert from "node:assert/strict"
import test from "node:test"
import { buildAdminRedirectUrl, buildAdminSearch, buildConsoleSearch, DEFAULT_ADMIN_TAB, DEFAULT_CONSOLE_ROUTE, parseAdminTab, parseConsoleSearch } from "./console-url.ts"

test("Given an empty or unknown search string, When parsed, Then it defaults to the home route cleanly", () => {
  assert.deepEqual(parseConsoleSearch(""), DEFAULT_CONSOLE_ROUTE)
  assert.deepEqual(parseConsoleSearch("?view=unknown&tab=hacked"), DEFAULT_CONSOLE_ROUTE)
})

test("Given console, catalog, deploy, and asset routes, When serialized and parsed back, Then view state and demo flags round-trip without leaking extraneous params", () => {
  const cases = [
    {
      current: "?demo=1&role=user&stale=abc",
      state: { ...DEFAULT_CONSOLE_ROUTE, view: "console", tab: "audit" },
      expectedSearch: "?demo=1&role=user&view=console&tab=audit",
    },
    {
      current: "",
      state: { ...DEFAULT_CONSOLE_ROUTE, view: "console", tab: "overview", overviewSub: "manifest" },
      expectedSearch: "?view=console&sub=manifest",
    },
    {
      current: "",
      state: { ...DEFAULT_CONSOLE_ROUTE, view: "console", tab: "overview", overviewSub: "workspace" },
      expectedSearch: "?view=console&sub=workspace",
    },
    {
      current: "",
      state: { ...DEFAULT_CONSOLE_ROUTE, view: "console", tab: "tasks", tasksSub: "runs", tasksRunId: "run_42" },
      expectedSearch: "?view=console&tab=tasks&sub=runs&run=run_42",
    },
    {
      current: "",
      state: { ...DEFAULT_CONSOLE_ROUTE, view: "console", tab: "tasks", tasksSub: "bg" },
      expectedSearch: "?view=console&tab=tasks&sub=bg",
    },
    {
      current: "",
      state: { ...DEFAULT_CONSOLE_ROUTE, view: "catalog", catalogKind: "website" },
      expectedSearch: "?view=catalog&kind=website",
    },
    {
      current: "",
      state: { ...DEFAULT_CONSOLE_ROUTE, view: "assets", assetPage: { section: "situation", clusterId: "prod-cn" } },
      expectedSearch: "?view=assets&area=situation&cluster=prod-cn",
    },
    {
      current: "",
      state: { ...DEFAULT_CONSOLE_ROUTE, view: "assets", assetPage: { section: "monitor", sub: "fleet" } },
      expectedSearch: "?view=assets&area=monitor",
    },
    {
      current: "",
      state: { ...DEFAULT_CONSOLE_ROUTE, view: "assets", assetPage: { section: "monitor", sub: "target", targetKey: "prod-cn/node-1" } },
      expectedSearch: "?view=assets&area=monitor&sub=target&target=prod-cn%2Fnode-1",
    },
    {
      current: "",
      state: { ...DEFAULT_CONSOLE_ROUTE, view: "assets", assetPage: { section: "inspection", runId: "run_1" } },
      expectedSearch: "?view=assets&area=inspection&run=run_1",
    },
    {
      current: "",
      state: { ...DEFAULT_CONSOLE_ROUTE, view: "assets", assetPage: { section: "skills", slug: "inspect-vm-host" } },
      expectedSearch: "?view=assets&area=skills&skill=inspect-vm-host",
    },
    {
      current: "",
      state: { ...DEFAULT_CONSOLE_ROUTE, view: "assets", assetPage: { section: "mcp" } },
      expectedSearch: "?view=assets&area=mcp",
    },
    {
      current: "",
      state: { ...DEFAULT_CONSOLE_ROUTE, view: "assets", assetPage: { section: "machines", machineId: "m-101" } },
      expectedSearch: "?view=assets&area=machines&machine=m-101",
    },
    {
      current: "",
      state: { ...DEFAULT_CONSOLE_ROUTE, view: "deploy", deploySection: "workload" },
      expectedSearch: "?view=deploy&section=workload",
    },
    {
      current: "?view=console&tab=audit",
      state: DEFAULT_CONSOLE_ROUTE,
      expectedSearch: "",
    },
  ]

  for (const item of cases) {
    const search = buildConsoleSearch(item.current, item.state)
    assert.equal(search, item.expectedSearch)
    const parsed = parseConsoleSearch(search)
    assert.equal(parsed.view, item.state.view)
    assert.equal(parsed.tab, item.state.tab)
    assert.equal(parsed.deploySection, item.state.deploySection)
    assert.equal(parsed.catalogKind, item.state.catalogKind)
    assert.equal(parsed.overviewSub, item.state.overviewSub)
    assert.equal(parsed.tasksSub, item.state.tasksSub)
    assert.equal(parsed.tasksRunId, item.state.tasksRunId)
    if (item.state.view === "assets") {
      assert.deepEqual(parsed.assetPage, item.state.assetPage)
    }
  }

  // Legacy ?view=console&tab=assets bookmark opens the manifest sub-tab cleanly.
  assert.equal(parseConsoleSearch("?view=console&tab=assets").overviewSub, "manifest")

  // Removed legacy scope route and invalid target keys fall back cleanly.
  assert.deepEqual(parseConsoleSearch("?view=assets&area=scope").assetPage, { section: "board" })
  assert.deepEqual(parseConsoleSearch("?view=assets&area=monitor&sub=target&target=../evil").assetPage, {
    section: "monitor",
    sub: "target",
  })
})

test("Given admin search strings, When parsing the tab, Then valid ids restore and invalid ones fall back to the default", () => {
  assert.equal(parseAdminTab(""), DEFAULT_ADMIN_TAB)
  assert.equal(parseAdminTab("?tab=users"), "users")
  assert.equal(parseAdminTab("?demo=1&tab=proxy"), "proxy")
  assert.equal(parseAdminTab("?tab=hacked"), DEFAULT_ADMIN_TAB)
})

test("Given an admin tab, When serialized, Then runtime flags are preserved and the default tab is omitted", () => {
  assert.equal(buildAdminSearch("?demo=1&role=admin&stale=x", "users"), "?demo=1&role=admin&tab=users")
  assert.equal(buildAdminSearch("", "grants"), "?tab=grants")
  assert.equal(buildAdminSearch("?tab=users&demo=1", DEFAULT_ADMIN_TAB), "?demo=1")
  assert.equal(parseAdminTab(buildAdminSearch("?demo=1", "tokens")), "tokens")
})

test("Given ?view=admin deep links, When parsed and redirected, Then view=admin is preserved instead of falling back to home", () => {
  const basic = parseConsoleSearch("?view=admin")
  assert.equal(basic.view, "admin")
  assert.equal(basic.adminTab, undefined)
  assert.equal(buildConsoleSearch("", basic), "?view=admin")
  assert.equal(buildAdminRedirectUrl("?view=admin", basic.adminTab), "/console/admin")

  const withTab = parseConsoleSearch("?demo=1&role=admin&view=admin&tab=users")
  assert.equal(withTab.view, "admin")
  assert.equal(withTab.adminTab, "users")
  assert.equal(buildConsoleSearch("?demo=1&role=admin", withTab), "?demo=1&role=admin&view=admin&tab=users")
  assert.equal(buildAdminRedirectUrl("?demo=1&role=admin&view=admin&tab=users", withTab.adminTab), "/console/admin?demo=1&role=admin&tab=users")

  const invalidTab = parseConsoleSearch("?view=admin&tab=hacked")
  assert.equal(invalidTab.view, "admin")
  assert.equal(invalidTab.adminTab, undefined)
  assert.equal(buildAdminRedirectUrl("?view=admin&tab=hacked", invalidTab.adminTab), "/console/admin")
})
