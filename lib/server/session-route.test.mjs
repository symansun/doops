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
const { setGatewaySession } = await import(sourceModule(join(root, "lib/gateway-policy.ts")))
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
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body),
  })
}

function cookieRequest() {
  const cookies = new Headers()
  setGatewaySession(cookies, { gateway, token: TOKEN, username: "untrusted-cookie-user" })
  return new Request("http://console.test/api/session", {
    headers: { Cookie: cookies.getSetCookie().map((cookie) => cookie.split(";")[0]).join("; ") },
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
      assert.match(session.sessionGeneration, /^[0-9a-f]{64}$/, "Every authenticated response keeps the browser session generation")
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

test("only an authentication 401 clears stored cookies; forbidden and outages preserve them", async (t) => {
  for (const status of [401, 403, 503]) {
    const code = status === 401 ? "gateway_auth_revoked" : "gateway_auth_unavailable"
    mockGateway(t, { metadata: { code }, status })
    const response = await sessionRoute.GET(cookieRequest())
    assert.equal(response.status, status)
    const body = await response.json()
    assert.equal(body.code, code)
    assert.equal(body.session === null, status === 401)
    const cookies = response.headers.getSetCookie()
    assert.equal(cookies.length, status === 401 ? 4 : 0)
    for (const cookie of cookies) assert.match(cookie, /Max-Age=0/)
    t.mock.restoreAll()
  }
})

test("no expiry is distinct from invalid expiry and keeps only the local seven-day cookie cap", async (t) => {
  const metadata = { ...identity }
  delete metadata.expires_at
  mockGateway(t, { metadata })
  const response = await ENTRY_POINTS[1].call()
  assert.equal(response.status, 200)
  assert.equal((await response.json()).expiresAt, undefined)
  for (const cookie of response.headers.getSetCookie()) assert.match(cookie, /Max-Age=604800/)
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
  const response = await sessionRoute.DELETE()
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

// Identity errors are terminal: they never create an ordinary-user session by
// treating a failed administrative probe as evidence of a role.
for (const entry of ENTRY_POINTS) {
  for (const status of [401, 403, 404, 502, 302]) {
    test(`${entry.name}: identity status ${status} cannot issue or restore a session`, async (t) => {
      const calls = mockGateway(t, { status, metadata: { code: "gateway_identity_rejected" } })
      const response = await entry.call()
      assert.equal(response.status, status)
      const body = await response.json()
      assert.equal(body.session === null, status === 401 && entry.name === "GET /api/session")
      assert.equal(body.authenticated, undefined)
      assert.equal(body.isAdmin, undefined)
      const cookies = response.headers.getSetCookie()
      assert.equal(cookies.length, status === 401 && entry.name === "GET /api/session" ? 4 : 0)
      for (const cookie of cookies) assert.match(cookie, /Max-Age=0/)
      assert.equal(calls.filter((call) => call.url === ME).length, 1)
      assert.equal(calls.some((call) => call.url === `${gateway}/v1/admin/instances`), false)
    })
  }
}

// 合成的 Gateway 错误响应，字段形态与 tunnel_hub.go 的 doopsErrorResponse 一致。
const PASSWORD = "synthetic-wrong-password-9f2e"
const ENGLISH_LOGIN = "Gateway username or password was rejected"
const ENGLISH_LOGIN_NEXT = "Verify the Gateway username/password or provide a valid Gateway user token"
const ENGLISH_AUTH = "Gateway user token is missing, invalid, expired, or revoked"
const ENGLISH_AUTH_NEXT = "Run doops login for this Gateway target"

function doopsError(code, message, nextAction) {
  return JSON.stringify({ error: "unauthorized", code, auth_domain: "gateway", message, next_action: nextAction })
}

const REDACTION_CASES = [
  {
    name: "wrong password",
    status: 401,
    body: doopsError("gateway_login_invalid_credentials", ENGLISH_LOGIN, ENGLISH_LOGIN_NEXT),
    want: { error: "用户名或密码错误", code: "gateway_login_invalid_credentials" },
  },
  {
    name: "gateway_auth_invalid",
    status: 401,
    body: doopsError("gateway_auth_invalid", ENGLISH_AUTH, ENGLISH_AUTH_NEXT),
    want: { error: "Gateway token 无效，请核对令牌或重新登录", code: "gateway_auth_invalid" },
  },
  {
    name: "unknown code keeps the code and uses the status message",
    status: 403,
    body: JSON.stringify({ error: "forbidden", code: "gateway_future_reason", message: "Some new English reason" }),
    want: { error: "没有访问该 Gateway 的权限", code: "gateway_future_reason" },
  },
  {
    name: "a code that is not lower_snake is dropped",
    status: 401,
    body: JSON.stringify({ code: "Not A Code <b>", message: "Raw upstream text" }),
    want: { error: "身份验证失败，请检查登录信息" },
  },
  {
    name: "non-JSON 401",
    status: 401,
    body: "Unauthorized\n",
    want: { error: "身份验证失败，请检查登录信息" },
  },
  {
    name: "non-JSON 403",
    status: 403,
    body: "Forbidden\n",
    want: { error: "没有访问该 Gateway 的权限" },
  },
  {
    name: "429",
    status: 429,
    body: "Too Many Requests\n",
    want: { error: "请求过于频繁，请稍后再试" },
  },
  {
    name: "5xx HTML page",
    status: 502,
    body: "<html><body>Bad Gateway from nginx</body></html>",
    want: { error: "Gateway 暂时不可用，请稍后再试" },
  },
  {
    name: "500 with an internal error text",
    status: 500,
    body: "database is locked\n",
    want: { error: "Gateway 暂时不可用，请稍后再试" },
  },
  {
    name: "empty body",
    status: 401,
    body: "",
    want: { error: "身份验证失败，请检查登录信息" },
  },
]

const REDACTION_ENTRY_POINTS = [
  {
    name: "POST /api/login",
    upstream: `${gateway}/v1/auth/login`,
    call: () => loginRoute.POST(jsonRequest("/api/login", { gateway, username: "synthetic-user", password: PASSWORD })),
  },
  {
    name: "POST /api/session",
    upstream: ME,
    call: () => sessionRoute.POST(jsonRequest("/api/session", { gateway, token: TOKEN })),
  },
]

function assertNoUpstreamText(text, name) {
  for (const raw of [ENGLISH_LOGIN, ENGLISH_LOGIN_NEXT, ENGLISH_AUTH, ENGLISH_AUTH_NEXT, "unauthorized",
    "auth_domain", "next_action", "Some new English reason", "Raw upstream text", "Unauthorized", "Forbidden",
    "Too Many Requests", "nginx", "database is locked", "ECONNREFUSED", "10.0.0.5", PASSWORD, TOKEN]) {
    assert.ok(!text.includes(raw), `${name}: response must not contain ${JSON.stringify(raw)}`)
  }
}

for (const entry of REDACTION_ENTRY_POINTS) {
  for (const item of REDACTION_CASES) {
    test(`${entry.name}: ${item.name} is shown in Chinese without the Gateway body`, async (t) => {
      const calls = []
      t.mock.method(globalThis, "fetch", async (url, init = {}) => {
        calls.push(String(url))
        assert.equal(String(url), entry.upstream)
        return new Response(item.body, { status: item.status })
      })
      const response = await entry.call()
      assert.equal(response.status, item.status)
      assert.equal(response.headers.get("Set-Cookie"), null)
      const text = await response.text()
      assert.deepEqual(JSON.parse(text), item.want)
      assertNoUpstreamText(text, item.name)
      assert.equal(calls.some((url) => url === `${gateway}/v1/admin/instances`), false)
    })
  }

  test(`${entry.name}: a network error is shown in Chinese without the error message`, async (t) => {
    t.mock.method(globalThis, "fetch", async () => {
      throw new TypeError("fetch failed: connect ECONNREFUSED 10.0.0.5:42222")
    })
    const response = await entry.call()
    assert.equal(response.status, 502)
    const text = await response.text()
    const body = JSON.parse(text)
    assert.deepEqual(Object.keys(body), ["error"])
    assert.match(body.error, /^(无法连接 Gateway，请检查地址或网络|无法验证 Gateway token)$/)
    assertNoUpstreamText(text, "network error")
  })
}
