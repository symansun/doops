import assert from "node:assert/strict"
import test from "node:test"
import {
  SENSITIVE_SCOPE_OPTIONS,
  describeSensitiveFilterError,
  filterSensitiveRules,
  filterSensitiveWhitelist,
  normalizeSensitiveTerm,
  parseSensitiveRules,
  parseSensitiveWhitelist,
  sensitiveScopeLabel,
  sensitiveWhitelistLabel,
} from "./sensitive-filter.ts"
import { demoAddSensitiveWhitelist, demoSetSensitiveRuleEnabled } from "./demo.ts"

test("parseSensitiveRules accepts one rule per line, folds whitespace and counts duplicates", () => {
  const parsed = parseSensitiveRules("  rm  -rf /  \nDROP   DATABASE\n\nrm -rf /\n", "command")
  assert.equal(parsed.ok, true)
  assert.deepEqual(parsed.rules, [
    { text: "rm -rf /", scope: "command" },
    { text: "DROP DATABASE", scope: "command" },
  ])
  assert.equal(parsed.duplicates, 1)
  assert.deepEqual(parseSensitiveRules("内部  机密", "prompt").rules, [{ text: "内部 机密", scope: "prompt" }])
})

test("parseSensitiveRules rejects an empty batch, long lines, control characters and an unknown scope", () => {
  const cases = [
    { name: "blank", raw: "  \n\t\n", scope: "all", error: /至少输入一条/ },
    { name: "too long", raw: "x".repeat(129), scope: "all", error: /第 1 行超过 128 个字符/ },
    { name: "control character", raw: "bad" + String.fromCharCode(7), scope: "all", error: /第 1 行包含控制字符/ },
    { name: "unknown scope", raw: "term", scope: "everything", error: /请选择适用范围/ },
  ]
  for (const c of cases) {
    const got = parseSensitiveRules(c.raw, c.scope)
    assert.equal(got.ok, false, c.name)
    assert.match(got.error, c.error, c.name)
  }
  // 128 characters is the inclusive boundary.
  assert.equal(parseSensitiveRules("x".repeat(128), "all").ok, true)
})

test("parseSensitiveWhitelist requires a user or a cluster and leaves the instance optional", () => {
  assert.deepEqual(parseSensitiveWhitelist("user", { userID: " usr_1 ", cluster: "", instance: "" }), {
    ok: true,
    value: { kind: "user", user_id: "usr_1" },
  })
  assert.deepEqual(parseSensitiveWhitelist("target", { userID: "", cluster: " prod ", instance: " web-1 " }), {
    ok: true,
    value: { kind: "target", cluster: "prod", instance: "web-1" },
  })
  assert.deepEqual(parseSensitiveWhitelist("target", { userID: "", cluster: "staging", instance: "  " }), {
    ok: true,
    value: { kind: "target", cluster: "staging" },
  })
  const cases = [
    { name: "missing user", kind: "user", form: { userID: "", cluster: "", instance: "" }, error: /请选择用户/ },
    { name: "user whitespace", kind: "user", form: { userID: "a b", cluster: "", instance: "" }, error: /用户 ID 不能包含空白/ },
    { name: "missing cluster", kind: "target", form: { userID: "", cluster: " ", instance: "web" }, error: /请填写集群/ },
    { name: "cluster whitespace", kind: "target", form: { userID: "", cluster: "a b", instance: "" }, error: /不能包含空白/ },
    { name: "long cluster", kind: "target", form: { userID: "", cluster: "x".repeat(129), instance: "" }, error: /过长/ },
  ]
  for (const c of cases) {
    const got = parseSensitiveWhitelist(c.kind, c.form)
    assert.equal(got.ok, false, c.name)
    assert.match(got.error, c.error, c.name)
  }
})

test("whitelist labels and search use the same normalized text as the Gateway", () => {
  assert.equal(normalizeSensitiveTerm("  Drop\t  Database "), "drop database")
  assert.equal(sensitiveScopeLabel("command"), "命令")
  assert.equal(SENSITIVE_SCOPE_OPTIONS.length, 3)
  assert.equal(sensitiveWhitelistLabel({ kind: "user", user_id: "usr_1" }), "用户 usr_1")
  assert.equal(sensitiveWhitelistLabel({ kind: "target", cluster: "prod", instance: "web-1" }), "目标 prod/web-1")
  assert.equal(sensitiveWhitelistLabel({ kind: "target", cluster: "staging" }), "目标 staging/*")

  const rules = [
    { id: "a", text: "RM -RF /", scope: "command", enabled: true },
    { id: "b", text: "内部 机密", scope: "prompt", enabled: true },
  ]
  assert.deepEqual(filterSensitiveRules(rules, "rm  -rf").map((rule) => rule.id), ["a"])
  assert.deepEqual(filterSensitiveRules(rules, "机密").map((rule) => rule.id), ["b"])
  assert.equal(filterSensitiveRules(rules, " ").length, 2)
  const whitelist = [
    { kind: "user", user_id: "usr_1" },
    { kind: "target", cluster: "prod", instance: "web-1" },
  ]
  assert.deepEqual(filterSensitiveWhitelist(whitelist, "PROD").map((entry) => entry.kind), ["target"])
  assert.deepEqual(filterSensitiveWhitelist(whitelist, "usr").map((entry) => entry.kind), ["user"])
})

test("describeSensitiveFilterError maps Gateway categories, auth, audit and unknown mutation outcomes", () => {
  const at = (status, category, message = category) =>
    Object.assign(new Error(message), { status, category })

  let got = describeSensitiveFilterError(at(409, "sensitive_whitelist_exists"), "addWhitelist")
  assert.equal(got.kind, "error")
  assert.equal(got.reload, true)
  assert.match(got.message, /添加白名单失败.*已存在/)

  got = describeSensitiveFilterError(at(404, "sensitive_rule_not_found"), "deleteRule")
  assert.equal(got.reload, true)
  assert.match(got.message, /已被其他管理员删除/)

  got = describeSensitiveFilterError(at(409, "sensitive_rule_limit_reached"), "addRules")
  assert.match(got.message, /500 条/)

  got = describeSensitiveFilterError(at(401, "gateway_auth_expired"), "load")
  assert.equal(got.kind, "auth")

  got = describeSensitiveFilterError(at(403, "Forbidden", "Forbidden"), "load")
  assert.equal(got.kind, "forbidden")

  got = describeSensitiveFilterError(at(404, "404 page not found", "404 page not found"), "load")
  assert.equal(got.kind, "error")
  assert.match(got.message, /请先升级 Gateway/)

  got = describeSensitiveFilterError(at(503, "audit_unavailable"), "toggleRule")
  assert.match(got.message, /审计存储不可用.*未执行/)

  got = describeSensitiveFilterError(at(502, "bad_gateway", "bad_gateway"), "toggleRule")
  assert.equal(got.reload, true)
  assert.match(got.message, /结果未知.*已刷新/)

  got = describeSensitiveFilterError(new SyntaxError("Unexpected token <"), "load")
  assert.equal(got.kind, "error")
  assert.match(got.message, /反向代理错误页/)

  got = describeSensitiveFilterError(new TypeError("fetch failed"), "addRules")
  assert.equal(got.reload, true)
  assert.match(got.message, /结果未知.*请求中断/)
})

test("demo sensitive filter failures carry the Gateway error category", () => {
  assert.throws(
    () => demoAddSensitiveWhitelist({ kind: "user", user_id: "usr_admin01" }),
    (err) => err.category === "sensitive_whitelist_exists",
  )
  assert.throws(
    () => demoSetSensitiveRuleEnabled("sw_missing", false),
    (err) => err.category === "sensitive_rule_not_found",
  )
})

test("describeSensitiveFilterError uses the category even when no HTTP status is attached", () => {
  const got = describeSensitiveFilterError(
    Object.assign(new Error("该白名单条目已存在"), { category: "sensitive_whitelist_exists" }),
    "addWhitelist",
  )
  assert.equal(got.kind, "error")
  assert.equal(got.reload, true)
  assert.match(got.message, /该白名单条目已存在/)
})
