import assert from "node:assert/strict"
import { mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join, resolve } from "node:path"
import test, { after } from "node:test"
import { fileURLToPath, pathToFileURL } from "node:url"
import TypeScript from "typescript"

const root = fileURLToPath(new URL("../../", import.meta.url))
const fixture = mkdtempSync(join(tmpdir(), "doops-session-route-test-"))
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

const sessionRoute = await import(sourceModule(join(root, "app/api/session/route.ts")))
const loginRoute = await import(sourceModule(join(root, "app/api/login/route.ts")))
const gateway = "http://127.0.0.1:18080"
const TOKEN = "doops-synthetic-session-token-5c1d"
const ADMIN_PROBE = `${gateway}/v1/admin/instances`

function mockGateway(t, { targets = 200, admin = 403 } = {}) {
  const calls = []
  t.mock.method(globalThis, "fetch", async (url, init = {}) => {
    calls.push({ url: String(url), init })
    if (url === `${gateway}/v1/auth/login`) return Response.json({ token: TOKEN, username: "ops-zhang" })
    if (url === `${gateway}/v1/targets`) {
      return targets === 200 ? Response.json({ targets: [] }) : new Response("Unauthorized", { status: targets })
    }
    if (url === ADMIN_PROBE) {
      if (admin === "network") throw new TypeError("fetch failed")
      return new Response(admin === 200 ? JSON.stringify({ instances: [] }) : "Forbidden", { status: admin })
    }
    throw new Error(`unexpected gateway call: ${url}`)
  })
  return calls
}

function jsonRequest(path, body) {
  return new Request(`http://console.test${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })
}

function cookieRequest() {
  return new Request("http://console.test/api/session", {
    headers: {
      Cookie: `doops_session_gateway=${encodeURIComponent(gateway)}; doops_session_token=${TOKEN}; doops_session_username=ops-zhang`,
    },
  })
}

const ENTRY_POINTS = [
  { name: "GET /api/session", call: () => sessionRoute.GET(cookieRequest()), isAdmin: (body) => body.session.isAdmin },
  { name: "POST /api/session", call: () => sessionRoute.POST(jsonRequest("/api/session", { gateway, token: TOKEN })), isAdmin: (body) => body.isAdmin },
  {
    name: "POST /api/login",
    call: () => loginRoute.POST(jsonRequest("/api/login", { gateway, username: "ops-zhang", password: "synthetic-password" })),
    isAdmin: (body) => body.isAdmin,
  },
]

const ADMIN_PROBES = [
  { name: "200 means platform admin", admin: 200, want: true },
  { name: "403 means ordinary user", admin: 403, want: false },
  { name: "401 is not guessed as admin", admin: 401, want: false },
  { name: "404 from a gateway without the route is not guessed as admin", admin: 404, want: false },
  { name: "5xx is not guessed as admin", admin: 502, want: false },
  { name: "a redirect is not guessed as admin", admin: 302, want: false },
  { name: "a network error is not guessed as admin", admin: "network", want: false },
]

for (const entry of ENTRY_POINTS) {
  for (const probe of ADMIN_PROBES) {
    test(`${entry.name}: ${probe.name}`, async (t) => {
      const calls = mockGateway(t, { admin: probe.admin })
      const response = await entry.call()
      assert.equal(response.status, 200)
      const text = await response.text()
      assert.equal(entry.isAdmin(JSON.parse(text)), probe.want)
      assert.ok(!text.includes(TOKEN), "the token must stay in the HttpOnly cookie")
      const probes = calls.filter((call) => call.url === ADMIN_PROBE)
      assert.equal(probes.length, 1)
      assert.equal(probes[0].init.method, undefined)
      assert.equal(probes[0].init.headers.Authorization, `Bearer ${TOKEN}`)
      assert.equal(probes[0].init.redirect, "manual")
    })
  }
}

test("a rejected session is not probed for admin access", async (t) => {
  const cases = [
    { name: "GET /api/session", call: () => sessionRoute.GET(cookieRequest()) },
    { name: "POST /api/session", call: () => sessionRoute.POST(jsonRequest("/api/session", { gateway, token: TOKEN })) },
  ]
  for (const { name, call } of cases) {
    const calls = mockGateway(t, { targets: 401, admin: 200 })
    const response = await call()
    assert.equal(response.status, 401, name)
    assert.equal(calls.some((item) => item.url === ADMIN_PROBE), false, name)
    t.mock.restoreAll()
  }
})

test("no stored session means no gateway call", async (t) => {
  const calls = mockGateway(t)
  const response = await sessionRoute.GET(new Request("http://console.test/api/session"))
  assert.deepEqual(await response.json(), { session: null })
  assert.equal(calls.length, 0)
})
