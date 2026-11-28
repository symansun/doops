import assert from "node:assert/strict"
import { mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join, resolve } from "node:path"
import test, { after } from "node:test"
import { fileURLToPath, pathToFileURL } from "node:url"
import TypeScript from "typescript"

const root = fileURLToPath(new URL("../../", import.meta.url))
const fixture = mkdtempSync(join(tmpdir(), "doops-bgtasks-route-test-"))
symlinkSync(join(root, "node_modules"), join(fixture, "node_modules"), "dir")
const compiled = new Map()
after(() => rmSync(fixture, { recursive: true, force: true }))

function sourceModule(filename) {
  filename = resolve(filename)
  if (compiled.has(filename)) return compiled.get(filename)
  const output = join(fixture, `${compiled.size}.mjs`)
  const url = pathToFileURL(output).href
  compiled.set(filename, url)
  let javascript = TypeScript.transpile(readFileSync(filename, "utf8"), {
    module: TypeScript.ModuleKind.ESNext,
    target: TypeScript.ScriptTarget.ES2022,
  }, filename)
  const parsed = TypeScript.createSourceFile(filename, javascript, TypeScript.ScriptTarget.ES2022, true, TypeScript.ScriptKind.JS)
  for (const statement of parsed.statements.filter((statement) =>
    (TypeScript.isImportDeclaration(statement) || TypeScript.isExportDeclaration(statement)) && statement.moduleSpecifier,
  ).reverse()) {
    const specifier = statement.moduleSpecifier
    const name = specifier.text
    if (!name.startsWith(".") && !name.startsWith("@/")) continue
    const source = name.startsWith("@/") ? join(root, name.slice(2)) : name
    const resolved = TypeScript.resolveModuleName(source, filename, {
      moduleResolution: TypeScript.ModuleResolutionKind.Bundler,
      module: TypeScript.ModuleKind.ESNext,
    }, TypeScript.sys).resolvedModule
    assert.ok(resolved && resolved.resolvedFileName.startsWith(root) && resolved.resolvedFileName.endsWith(".ts"),
      `Missing DoOps TypeScript source: ${name} from ${filename}`)
    javascript = javascript.slice(0, specifier.getStart(parsed)) +
      JSON.stringify(sourceModule(resolved.resolvedFileName)) +
      javascript.slice(specifier.end)
  }
  writeFileSync(output, javascript)
  return url
}

const { GET, POST } = await import(sourceModule(join(root, "app/api/background-tasks/[[...path]]/route.ts")))
const gateway = "http://127.0.0.1:18080"

function request(method, path = "", { body, cookie = true, origin = "http://console.test", search = "" } = {}) {
  return new Request(`http://console.test/api/background-tasks${path}${search}`, {
    method,
    headers: {
      "Content-Type": "application/json",
      ...(origin ? { Origin: origin } : {}),
      ...(cookie ? { Cookie: `doops_session_gateway=${encodeURIComponent(gateway)}; doops_session_token=synthetic-token` } : {}),
    },
    ...(body === undefined ? {} : { body }),
  })
}

const params = (path) => Promise.resolve({ path })

test("Given a gateway session, when tasks are listed, then the target filter reaches the gateway", async (t) => {
  const calls = []
  t.mock.method(globalThis, "fetch", async (url, init) => {
    calls.push({ url, init })
    return new Response(JSON.stringify({ tasks: [{ audit_id: 42, status: "running" }] }), { status: 200 })
  })
  const response = await GET(request("GET", "", { search: "?cluster=prod&instance=node" }), { params: params(undefined) })
  assert.equal(response.status, 200)
  assert.equal(calls.length, 1)
  assert.equal(calls[0].url, `${gateway}/v1/background-tasks?cluster=prod&instance=node`)
  assert.equal(calls[0].init.method, "GET")
  assert.equal(calls[0].init.headers.Authorization, "Bearer synthetic-token")
  assert.deepEqual(await response.json(), { tasks: [{ audit_id: 42, status: "running" }] })
})

test("Given a task id, when its detail is fetched, then the path is forwarded", async (t) => {
  const calls = []
  t.mock.method(globalThis, "fetch", async (url, init) => {
    calls.push({ url, init })
    return new Response(JSON.stringify({ audit_id: 42, status: "success", output: "done" }), { status: 200 })
  })
  const response = await GET(request("GET", "/42"), { params: params(["42"]) })
  assert.equal(response.status, 200)
  assert.equal(calls[0].url, `${gateway}/v1/background-tasks/42`)
})

test("Given a cancel request, when posted same-origin, then it reaches the gateway and 202 passes through", async (t) => {
  const calls = []
  t.mock.method(globalThis, "fetch", async (url, init) => {
    calls.push({ url, init })
    return new Response(JSON.stringify({ audit_id: 42, cancel_requested: true }), { status: 202 })
  })
  const response = await POST(request("POST", "/42/cancel", { body: "" }), { params: params(["42", "cancel"]) })
  assert.equal(response.status, 202)
  assert.equal(calls[0].url, `${gateway}/v1/background-tasks/42/cancel`)
  assert.equal(calls[0].init.method, "POST")
})

test("Given a finished task, when cancel is posted, then the gateway 409 passes through", async (t) => {
  t.mock.method(globalThis, "fetch", async () => {
    return new Response(JSON.stringify({ error: "background_task_finished" }), { status: 409 })
  })
  const response = await POST(request("POST", "/42/cancel", { body: "" }), { params: params(["42", "cancel"]) })
  assert.equal(response.status, 409)
})

test("Given a cross-origin cancel post, when checked, then it is rejected before reaching the gateway", async (t) => {
  const calls = []
  t.mock.method(globalThis, "fetch", async (url, init) => {
    calls.push({ url, init })
    return new Response("{}", { status: 200 })
  })
  const response = await POST(request("POST", "/42/cancel", { body: "", origin: "http://evil.test" }), { params: params(["42", "cancel"]) })
  assert.equal(response.status, 403)
  assert.equal(calls.length, 0)
})

test("Given no session cookie, when tasks are listed, then the request is rejected", async (t) => {
  const calls = []
  t.mock.method(globalThis, "fetch", async (url, init) => {
    calls.push({ url, init })
    return new Response("{}", { status: 200 })
  })
  const response = await GET(request("GET", "", { cookie: false }), { params: params(undefined) })
  assert.equal(response.status, 401)
  assert.equal(calls.length, 0)
})
