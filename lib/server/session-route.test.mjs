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
const ME = `${gateway}/v1/auth/me`
const identity = {
  user_id: "usr_synthetic", username: "ops-zhang", is_admin: false,
  token_id: "tok_synthetic", name: "synthetic-session", issued_at: "2026-11-01T00:00:00Z",
  expires_at: new Date(Date.now() + 60 * 60 * 1000).toISOString(),
}

function mockGateway(t, { metadata = identity, status = 200 } = {}) {
  const calls = []
  t.mock.method(globalThis, "fetch", async (url, init = {}) => {
    calls.push({ url: String(url), init })
    if (url === `${gateway}/v1/auth/login`) return Response.json({ token: TOKEN })
    assert.equal(url, ME, "identity must not be inferred from targets or an admin-only API")
    return Response.json(metadata, { status })
  })
  return calls
}

function jsonRequest(path, body) {
  return new Request(`http://console.test${path}`, {
    method: "POST", headers: { "Content-Type": "application/json", Origin: "http://console.test" }, body: JSON.stringify(body),
  })
}

function cookieRequest() {
  return new Request("http://console.test/api/session", {
    headers: { Cookie: `doops_session_gateway=${encodeURIComponent(gateway)}; doops_session_token=${TOKEN}; doops_session_username=untrusted-cookie-user` },
  })
}

const ENTRY_POINTS = [
  { name: "GET /api/session", call: () => sessionRoute.GET(cookieRequest()), session: (body) => body.session },
  { name: "POST /api/session", call: () => sessionRoute.POST(jsonRequest("/api/session", { gateway, token: TOKEN, username: "untrusted-body-user" })), session: (body) => body },
  { name: "POST /api/login", call: () => loginRoute.POST(jsonRequest("/api/login", { gateway, username: "request-user", password: "synthetic-password" })), session: (body) => body },
]

for (const entry of ENTRY_POINTS) {
  for (const isAdmin of [false, true]) {
    test(`${entry.name}: current identity is authoritative for username, role, token and expiry (admin=${isAdmin})`, async (t) => {
      const calls = mockGateway(t, { metadata: { ...identity, is_admin: isAdmin } })
      const response = await entry.call()
      assert.equal(response.status, 200)
      const text = await response.text()
      const session = entry.session(JSON.parse(text))
      assert.equal(session.username, identity.username)
      assert.equal(session.isAdmin, isAdmin)
      assert.equal(session.tokenId, identity.token_id)
      assert.equal(session.issuedAt, identity.issued_at)
      assert.equal(session.expiresAt, identity.expires_at)
      assert.ok(!text.includes(TOKEN), "the token must stay in the HttpOnly cookie")
      const checks = calls.filter((call) => call.url === ME)
      assert.equal(checks.length, 1)
      assert.equal(checks[0].init.headers.Authorization, `Bearer ${TOKEN}`)
      assert.equal(checks[0].init.redirect, "manual")
      assert.equal(checks[0].init.cache, "no-store")
      for (const cookie of response.headers.getSetCookie()) {
        const maxAge = Number(cookie.match(/Max-Age=(\d+)/)?.[1])
        assert.ok(maxAge > 0 && maxAge <= 3600, cookie)
        assert.match(cookie, /HttpOnly/)
      }
    })
  }
}

test("no stored session means no gateway call", async (t) => {
  const calls = mockGateway(t)
  const response = await sessionRoute.GET(new Request("http://console.test/api/session"))
  assert.deepEqual(await response.json(), { session: null })
  assert.equal(calls.length, 0)
})

test("an authentication 401 clears stored cookies and returns a logged-out session", async (t) => {
  mockGateway(t, { metadata: { code: "gateway_auth_expired" }, status: 401 })
  const response = await sessionRoute.GET(cookieRequest())
  assert.equal(response.status, 200)
  assert.deepEqual(await response.json(), { session: null })
  const cookies = response.headers.getSetCookie()
  assert.equal(cookies.length, 3)
  for (const cookie of cookies) assert.match(cookie, /Max-Age=0/)
})

test("forbidden and outages preserve stored cookies", async (t) => {
  for (const status of [403, 503]) {
    const code = "gateway_auth_unavailable"
    mockGateway(t, { metadata: { code }, status })
    const response = await sessionRoute.GET(cookieRequest())
    assert.equal(response.status, status)
    const body = await response.json()
    assert.equal(body.code, code)
    assert.equal(body.session, undefined)
    assert.equal(response.headers.getSetCookie().length, 0)
    t.mock.restoreAll()
  }
})

test("no expiry is distinct from invalid expiry and keeps only the local ninety-day cookie cap", async (t) => {
  const metadata = { ...identity }
  delete metadata.expires_at
  mockGateway(t, { metadata })
  const response = await ENTRY_POINTS[1].call()
  assert.equal(response.status, 200)
  assert.equal((await response.json()).expiresAt, undefined)
  for (const cookie of response.headers.getSetCookie()) assert.match(cookie, /Max-Age=7776000/)
})

for (const entry of ENTRY_POINTS) {
  test(`${entry.name}: incomplete or malformed identity fails before saving cookies`, async (t) => {
    for (const overrides of [
      { user_id: undefined }, { username: undefined }, { is_admin: "true" },
      { token_id: "" }, { issued_at: "invalid" }, { expires_at: "invalid" }, { expires_at: null },
    ]) {
      mockGateway(t, { metadata: { ...identity, ...overrides } })
      const response = await entry.call()
      assert.equal(response.status, 502)
      assert.equal(response.headers.get("Set-Cookie"), null)
      t.mock.restoreAll()
    }
  })
}

test("an already expired identity cannot create a browser session", async (t) => {
  mockGateway(t, { metadata: { ...identity, expires_at: "2020-01-01T00:00:00Z" } })
  const response = await ENTRY_POINTS[1].call()
  assert.equal(response.status, 401)
  assert.equal((await response.json()).code, "gateway_auth_expired")
  assert.equal(response.headers.get("Set-Cookie"), null)
})

test("logout clears only the local browser session and makes no revocation request", async (t) => {
  const calls = mockGateway(t)
  const response = await sessionRoute.DELETE(new Request("http://console.test/api/session", {
    method: "DELETE", headers: { Origin: "http://console.test" },
  }))
  assert.equal(response.status, 200)
  assert.equal(calls.length, 0)
  for (const cookie of response.headers.getSetCookie()) assert.match(cookie, /Max-Age=0/)
})

const GATEWAY_ERRORS = [
  [401, "gateway_login_invalid_credentials", "用户名或密码错误"],
  [401, "gateway_auth_missing", "缺少 Gateway token，请登录"],
  [401, "gateway_auth_invalid", "Gateway token 无效，请核对令牌或重新登录"],
  [401, "gateway_auth_expired", "Gateway token 已过期，请重新登录"],
  [401, "gateway_auth_revoked", "Gateway token 已撤销，请使用有效令牌登录"],
  [401, "gateway_auth_user_disabled", "Gateway 用户已停用，请联系管理员"],
  [401, "gateway_auth_invalid_expiry", "Gateway token 到期数据异常，请联系管理员"],
  [503, "gateway_auth_unavailable", "Gateway 认证服务暂时不可用，请稍后重试"],
]

for (const entry of ENTRY_POINTS.slice(1)) {
  for (const [status, code, error] of GATEWAY_ERRORS) {
    test(`${entry.name}: ${code} keeps its precise cause without upstream text or credentials`, async (t) => {
      t.mock.method(globalThis, "fetch", async () => Response.json({ code, message: "synthetic upstream internals", next_action: "synthetic upstream next", token: TOKEN }, { status }))
      const response = await entry.call()
      assert.equal(response.status, status)
      assert.equal(response.headers.get("Set-Cookie"), null)
      assert.deepEqual(await response.json(), { code, error })
    })
  }
}

test("an identity endpoint that is absent or unavailable is an explicit failure without other probe paths", async (t) => {
  for (const status of [404, 503]) {
    const calls = mockGateway(t, { status, metadata: { code: "gateway_auth_unavailable" } })
    const response = await ENTRY_POINTS[1].call()
    assert.equal(response.status, status)
    assert.equal(calls.length, 1)
    assert.equal(response.headers.get("Set-Cookie"), null)
    t.mock.restoreAll()
  }
})

for (const entry of ENTRY_POINTS) {
  test(`${entry.name}: a network error preserves stored credentials and redacts internal addresses`, async (t) => {
    t.mock.method(globalThis, "fetch", async () => { throw new TypeError("fetch failed: connect ECONNREFUSED 10.0.0.5:42222") })
    const response = await entry.call()
    assert.equal(response.status, 502)
    assert.equal(response.headers.get("Set-Cookie"), null)
    const text = await response.text()
    assert.doesNotMatch(text, /ECONNREFUSED|10\.0\.0\.5|synthetic-password|doops-synthetic-session-token/)
  })
}
