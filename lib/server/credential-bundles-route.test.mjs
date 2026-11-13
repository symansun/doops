import assert from "node:assert/strict"
import { mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join, resolve } from "node:path"
import test, { after } from "node:test"
import { fileURLToPath, pathToFileURL } from "node:url"
import TypeScript from "typescript"

const root = fileURLToPath(new URL("../../", import.meta.url))
const fixture = mkdtempSync(join(tmpdir(), "doops-bundle-route-test-"))
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

const { GET, POST } = await import(sourceModule(join(root, "app/api/credential-bundles/route.ts")))
const gateway = "http://127.0.0.1:18080"
const CANARY = "doops-synthetic-canary-route-41b2"

function request(method, path = "", { body, cookie = true } = {}) {
  return new Request(`http://console.test/api/credential-bundles${path}`, {
    method,
    headers: {
      "Content-Type": "application/json",
      Origin: "http://console.test",
      ...(cookie ? { Cookie: `doops_session_gateway=${encodeURIComponent(gateway)}; doops_session_token=synthetic-token` } : {}),
    },
    ...(body === undefined ? {} : { body }),
  })
}

test("Given a gateway session, when a bundle is created, then the body is forwarded once with the bearer token", async (t) => {
  const calls = []
  t.mock.method(globalThis, "fetch", async (url, init) => {
    calls.push({ url, init })
    return new Response(JSON.stringify({ id: "credbundle_1", name: "release", scope: "personal", state: "active" }), { status: 200 })
  })
  const body = JSON.stringify({ name: "release", scope: "personal", items: [{ credential_id: "cred_aaa" }] })
  const response = await POST(request("POST", "", { body }))
  assert.equal(response.status, 200)
  assert.equal(calls.length, 1)
  assert.equal(calls[0].url, `${gateway}/v1/credential-bundles`)
  assert.equal(calls[0].init.method, "POST")
  assert.equal(calls[0].init.headers.Authorization, "Bearer synthetic-token")
  assert.equal(calls[0].init.body, body)
  assert.equal(response.headers.get("Cache-Control"), "no-store")
})

test("Given a bundle name, when it is viewed, then the query string reaches the gateway unchanged", async (t) => {
  const calls = []
  t.mock.method(globalThis, "fetch", async (url, init) => {
    calls.push({ url, init })
    return new Response(JSON.stringify({ id: "credbundle_1", items: [] }), { status: 200 })
  })
  const response = await GET(request("GET", "?name=release"))
  assert.equal(response.status, 200)
  assert.equal(calls[0].url, `${gateway}/v1/credential-bundles?name=release`)
  assert.equal(calls[0].init.body, undefined)
})

const gatewayFailures = [
  { name: "forbidden", status: 403, text: "credential_forbidden" },
  { name: "audit unavailable", status: 500, text: "credential_audit_unavailable" },
  { name: "key unavailable", status: 503, text: "credential_key_unavailable" },
  { name: "validation failed", status: 400, text: "credential bundle metadata and items are required" },
]

for (const failure of gatewayFailures) {
  test(`Given the gateway answers ${failure.name}, when a bundle is created, then status and category pass through`, async (t) => {
    t.mock.method(globalThis, "fetch", async () => new Response(`${failure.text}\n`, { status: failure.status }))
    const response = await POST(request("POST", "", { body: JSON.stringify({ name: "x", scope: "personal", items: [{ credential_id: CANARY }] }) }))
    assert.equal(response.status, failure.status)
    const data = await response.json()
    assert.equal(data.error, failure.text)
    assert.ok(!JSON.stringify(data).includes(CANARY))
  })
}

test("Given the gateway is unreachable, when a bundle is created, then 502 is returned without the request body", async (t) => {
  t.mock.method(globalThis, "fetch", async () => { throw new Error("connect ECONNREFUSED") })
  const response = await POST(request("POST", "", { body: JSON.stringify({ name: CANARY, scope: "personal", items: [] }) }))
  assert.equal(response.status, 502)
  const data = await response.json()
  assert.match(data.error, /无法连接 gateway/)
  assert.ok(!JSON.stringify(data).includes(CANARY))
})

test("Given no gateway session, when a bundle is created, then nothing is forwarded", async (t) => {
  const fetchMock = t.mock.method(globalThis, "fetch", async () => new Response("{}"))
  const response = await POST(request("POST", "", { body: "{}", cookie: false }))
  assert.equal(response.status, 401)
  assert.equal(fetchMock.mock.callCount(), 0)
})
