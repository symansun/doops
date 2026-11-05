import assert from "node:assert/strict"
import test from "node:test"
import { consoleViewEntries } from "./console-views.ts"

test("Given the account role, When the header lists views, Then admin appears only for admins and the order is fixed", () => {
  for (const [name, isAdmin, want] of [
    ["admin", true, ["console", "deploy", "cicd", "admin"]],
    ["ordinary user", false, ["console", "deploy", "cicd"]],
  ]) {
    assert.deepEqual(consoleViewEntries(isAdmin).map((entry) => entry.id), want, name)
  }
  assert.deepEqual(consoleViewEntries(true).map((entry) => entry.label), ["控制台", "部署", "CI/CD", "管理"])
})
