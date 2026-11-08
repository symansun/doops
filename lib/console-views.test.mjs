import assert from "node:assert/strict"
import test from "node:test"
import { consoleViewEntries, credentialsDestination } from "./console-views.ts"

test("Given the account role, When the header lists views, Then admin appears only for admins and the order is fixed", () => {
  for (const [name, isAdmin, want] of [
    ["admin", true, ["console", "deploy", "cicd", "admin"]],
    ["ordinary user", false, ["console", "deploy", "cicd"]],
  ]) {
    assert.deepEqual(consoleViewEntries(isAdmin).map((entry) => entry.id), want, name)
  }
  assert.deepEqual(consoleViewEntries(true).map((entry) => entry.label), ["控制台", "部署", "CI/CD", "管理"])
})

// The AI assistant's and the release page's "打开凭据" both lead here. DoAgent
// models come only from modelProvider credentials, so it never leads to the
// admin config center.
test("Given the account role, When a page asks to open credentials, Then admins land on the console credentials tab", () => {
  for (const [name, isAdmin, want] of [
    ["admin", true, { view: "console", tab: "credentials" }],
    ["ordinary user", false, null],
  ]) {
    assert.deepEqual(credentialsDestination(isAdmin), want, name)
  }
  assert.notEqual(credentialsDestination(true)?.view, "admin")
})
