import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"
import { fileURLToPath } from "node:url"
import TypeScript from "typescript"
import { sourceLoader } from "./test-source.mjs"

const root = fileURLToPath(new URL("../", import.meta.url))
const load = sourceLoader(root)
const model = await load("lib/model-configs.ts")
const { readAllowedAuth, setGatewaySession, gatewayPolicyResponse } = await load("lib/gateway-policy.ts")
const { SessionChangedError } = await load("lib/auth-session.ts")
const key = "doops.modelConfigs.v1"
const template = {
  id: "review", name: "review", kind: "file", path: "review.conf",
  content: "version-A", updated_at: "2026-11-06T00:00:00.000Z",
}
const tree = TypeScript.createSourceFile("dispatch.tsx",
  readFileSync(new URL("../components/admin-config-dispatch.tsx", import.meta.url), "utf8"),
  TypeScript.ScriptTarget.ESNext, true, TypeScript.ScriptKind.TSX)
let publishSource
function visit(node) {
  if (TypeScript.isFunctionDeclaration(node) && node.name?.text === "publish") publishSource = node.getText(tree)
  TypeScript.forEachChild(node, visit)
}
visit(tree)
assert.ok(publishSource)
const publishJS = TypeScript.transpile(publishSource, { target: TypeScript.ScriptTarget.ES2022 })

function storage(t, templates = [template]) {
  const values = new Map([[key, JSON.stringify(templates)]])
  const previous = globalThis.window
  globalThis.window = { localStorage: {
    getItem: (name) => values.get(name) ?? null,
    setItem: (name, value) => values.set(name, value),
  } }
  t.after(() => { globalThis.window = previous })
}

function publisher(session, cfg = template) {
  const state = {}
  const targets = ["one", "two", "three"].map((instance) => ({ cluster: "review", instance }))
  const dependencies = {
    ...model, SessionChangedError, configs: [cfg], dispatchCfg: cfg.id,
    activeInstances: targets,
    selected: Object.fromEntries(targets.map((target) => [`review/${target.instance}`, true])),
    session, sessionRef: { current: "review-config" },
    setDispatchError: (value) => { state.error = value },
    setDispatching: (value) => { state.dispatching = value },
    setRows: (value) => { state.rows = typeof value === "function" ? value(state.rows) : value },
    setConfigs: (value) => { state.configs = value },
  }
  return {
    state,
    publish: new Function(...Object.keys(dependencies), `${publishJS}; return publish`)(...Object.values(dependencies)),
  }
}

function browserSession(gateway, token = "synthetic-A") {
  const headers = new Headers()
  const sessionGeneration = setGatewaySession(headers, { gateway, token })
  return {
    session: { gateway, sessionGeneration },
    cookie: headers.getSetCookie().map((cookie) => cookie.split(";")[0]).join("; "),
  }
}

test("batch stops at a server-rejected generation and retains all target outcomes", async (t) => {
  storage(t)
  const original = browserSession("http://127.0.0.1:8111")
  let current = original
  let calls = 0
  const writes = []
  t.mock.method(globalThis, "fetch", async (url, init) => {
    calls++
    const request = new Request(`http://console.test${url}`, {
      ...init, headers: { ...init.headers, cookie: current.cookie },
    })
    try {
      const auth = readAllowedAuth(request, { requireGeneration: true })
      const body = await request.json()
      writes.push({ gateway: auth.gateway, instance: body.instance, ...body.arguments })
      current = browserSession("http://127.0.0.1:8222", "synthetic-B")
      return new Response(JSON.stringify({ type: "result", result: { content: [{ text: "written" }] } }) + "\n")
    } catch (error) {
      const response = gatewayPolicyResponse(error)
      assert.ok(response)
      return response
    }
  })
  const batch = publisher(original.session)
  await batch.publish()
  assert.equal(calls, 2, "third target must not be attempted")
  assert.deepEqual(writes.map(({ gateway, instance, path, content }) => ({ gateway, instance, path, content })), [
    { gateway: original.session.gateway, instance: "one", path: "review.conf", content: "version-A" },
  ])
  assert.equal(batch.state.rows["review/one"].state, "ok")
  assert.equal(batch.state.rows["review/two"].state, "err")
  assert.match(batch.state.rows["review/three"].message, /未继续写入/)
  assert.match(batch.state.error, /登录会话已变更/)
  assert.equal(batch.state.dispatching, false)
})

test("delayed completion marks only the exact template that was written", async (t) => {
  storage(t)
  const saved = model.upsertConfig({ ...template, content: "version-B" })[0]
  assert.equal(model.markPublished(template)[0].published_at, undefined)
  assert.equal(model.loadConfigs()[0].content, "version-B")
  assert.equal(model.hasUnpublishedChanges(model.loadConfigs()[0]), true)
  assert.ok(model.markPublished(saved)[0].published_at)
  const sameClockEdit = { ...saved, content: "version-C" }
  window.localStorage.setItem(key, JSON.stringify([sameClockEdit]))
  assert.equal(model.markPublished(saved)[0].published_at, undefined, "content matters even with equal timestamps")
  model.removeConfig(template.id)
  assert.deepEqual(model.markPublished(template), [], "completion cannot resurrect a removed template")
})

test("optional empty fields survive storage serialization without preventing exact-version marking", (t) => {
  storage(t)
  const saved = model.upsertConfig({ ...template, max_tokens: undefined })[0]
  assert.ok(model.markPublished(saved)[0].published_at)
})

test("defaults are workspace files; invalid old paths remain visible and never dispatch", async (t) => {
  storage(t)
  window.localStorage.setItem(key, "")
  const defaults = model.loadConfigs()
  assert.equal(model.SETTINGS_PATH, ".agent/settings.json")
  assert.equal(defaults.find((cfg) => cfg.id === "cfg_nginx").path, "nginx/conf.d/doops.conf")
  for (const cfg of defaults) assert.equal(model.requireConfigWorkspacePath(model.configTargetPath(cfg), "review"), model.configTargetPath(cfg))
  let requests = 0
  t.mock.method(globalThis, "fetch", async () => { requests++; throw new Error("must not dispatch") })
  for (const path of ["/etc/review.conf", "/root/.agent/settings.json", "../review.conf", "a/../../review.conf", "", ".", "a//b", "a/./b", " x", "a\0b"]) {
    const invalid = { ...template, path }
    window.localStorage.setItem(key, JSON.stringify([invalid]))
    assert.equal(model.loadConfigs()[0].path, path)
    await assert.rejects(model.applyConfigToNode({}, { cluster: "review", instance: "one" }, "review", invalid), /请编辑模板/)
  }
  assert.equal(requests, 0)
})
