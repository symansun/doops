import assert from "node:assert/strict"
import test from "node:test"
import { buildAdminSearch, buildConsoleSearch, DEFAULT_ADMIN_TAB, DEFAULT_CONSOLE_ROUTE, parseAdminTab, parseConsoleSearch } from "./console-url.ts"

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
      state: { ...DEFAULT_CONSOLE_ROUTE, view: "catalog", catalogKind: "website" },
      expectedSearch: "?view=catalog&kind=website",
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
  }
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
