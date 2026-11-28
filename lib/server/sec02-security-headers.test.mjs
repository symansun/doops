import assert from "node:assert/strict"
import { mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join, resolve } from "node:path"
import test, { after } from "node:test"
import { fileURLToPath, pathToFileURL } from "node:url"
import TypeScript from "typescript"

const root = fileURLToPath(new URL("../../", import.meta.url))
const fixture = mkdtempSync(join(tmpdir(), "doops-sec02-web-test-"))
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

const nextConfigMod = await import(pathToFileURL(join(root, "next.config.mjs")).href)
const sameOrigin = await import(sourceModule(join(root, "lib/server/same-origin.ts")))
const gatewayProxy = await import(sourceModule(join(root, "lib/server/gateway-proxy.ts")))
const adminRoute = await import(sourceModule(join(root, "app/api/admin/[...path]/route.ts")))
const credentialsRoute = await import(sourceModule(join(root, "app/api/credentials/[[...path]]/route.ts")))
const bgTasksRoute = await import(sourceModule(join(root, "app/api/background-tasks/[[...path]]/route.ts")))
const terminalStreamRoute = await import(sourceModule(join(root, "app/api/terminal-stream/route.ts")))
const terminalInputRoute = await import(sourceModule(join(root, "app/api/terminal-input/route.ts")))
const sessionRoute = await import(sourceModule(join(root, "app/api/session/route.ts")))
const loginRoute = await import(sourceModule(join(root, "app/api/login/route.ts")))

const gateway = "http://127.0.0.1:18080"
const TOKEN = "doops-synthetic-sec02-token-8f31"

function authedRequest(url, { method = "GET", headers = {}, body } = {}) {
  return new Request(url, {
    method,
    headers: {
      Cookie: `doops_session_gateway=${encodeURIComponent(gateway)}; doops_session_token=${TOKEN}`,
      ...(method !== "GET" ? { Origin: new URL(url).origin } : {}),
      ...headers,
    },
    ...(body !== undefined ? { body } : {}),
  })
}

test("next.config.mjs production CSP and security headers enforce defense-in-depth", async () => {
  const prodCsp = nextConfigMod.buildContentSecurityPolicy(true)
  for (const directive of [
    "default-src 'self'",
    "base-uri 'self'",
    "form-action 'self'",
    "frame-ancestors 'none'",
    "object-src 'none'",
    "connect-src 'self'",
    "script-src 'self' 'unsafe-inline'",
  ]) {
    assert.ok(prodCsp.includes(directive), `production CSP missing ${directive}: ${prodCsp}`)
  }
  assert.ok(!prodCsp.includes("'unsafe-eval'"), `production CSP must not allow unsafe-eval: ${prodCsp}`)

  const headersList = nextConfigMod.buildSecurityHeaders(true)
  const byKey = Object.fromEntries(headersList.map((h) => [h.key, h.value]))
  assert.equal(byKey["X-Frame-Options"], "DENY")
  assert.equal(byKey["X-Content-Type-Options"], "nosniff")
  assert.equal(byKey["Referrer-Policy"], "same-origin")
  assert.equal(byKey["X-DNS-Prefetch-Control"], "off")
  assert.equal(byKey["Cross-Origin-Opener-Policy"], "same-origin")

  const rules = await nextConfigMod.default.headers()
  const apiRule = rules.find((r) => r.source === "/api/:path*")
  assert.ok(apiRule, "missing /api/:path* header rule")
  assert.ok(
    apiRule.headers.some((h) => h.key === "Cross-Origin-Resource-Policy" && h.value === "same-origin"),
    "API routes must declare Cross-Origin-Resource-Policy: same-origin",
  )
})

test("catch-all BFF proxy routes reject dot-segment path traversal before upstream fetch", async (t) => {
  let fetchCalls = 0
  t.mock.method(globalThis, "fetch", async () => {
    fetchCalls++
    return Response.json({ ok: true })
  })

  const badSegments = [
    ["..", "admin", "users"],
    ["."],
    ["cred_1", "..", "admin"],
    ["a..b"],
    ["foo/bar"],
    ["bad\\seg"],
    ["bad\nseg"],
    ["a", "b", "c", "d", "e", "f", "g", "h", "i"],
  ]

  for (const segments of badSegments) {
    assert.equal(gatewayProxy.resolveSafeGatewaySubpath(segments), null, `should reject ${JSON.stringify(segments)}`)

    const adminRes = await adminRoute.GET(
      authedRequest("http://console.test/api/admin/x"),
      { params: Promise.resolve({ path: segments }) },
    )
    assert.equal(adminRes.status, 400, `admin route should reject ${JSON.stringify(segments)}`)

    const credRes = await credentialsRoute.GET(
      authedRequest("http://console.test/api/credentials/x"),
      { params: Promise.resolve({ path: segments }) },
    )
    assert.equal(credRes.status, 400, `credentials route should reject ${JSON.stringify(segments)}`)

    const bgRes = await bgTasksRoute.GET(
      authedRequest("http://console.test/api/background-tasks/x"),
      { params: Promise.resolve({ path: segments }) },
    )
    assert.equal(bgRes.status, 400, `background-tasks route should reject ${JSON.stringify(segments)}`)
  }

  // Defense-in-depth inside proxyGatewayRequest itself even if called directly with traversal.
  for (const rawPath of [
    "/v1/credentials/../admin/users",
    "/v1/credentials/%2e%2e/admin/users",
    "/v1/credentials/%2E%2E/admin/users",
    "/v1/credentials//users",
    "/admin/users",
  ]) {
    const res = await gatewayProxy.proxyGatewayRequest(authedRequest("http://console.test/api/credentials"), rawPath)
    assert.equal(res.status, 400, `proxyGatewayRequest should reject ${rawPath}`)
  }

  assert.equal(fetchCalls, 0, "no rejected traversal path may reach upstream fetch")
})

test("valid BFF proxy requests succeed with no-store and nosniff headers", async (t) => {
  let upstreamUrl = ""
  t.mock.method(globalThis, "fetch", async (url) => {
    upstreamUrl = String(url)
    return Response.json({ id: "cred_ok" })
  })
  const res = await credentialsRoute.POST(
    authedRequest("http://console.test/api/credentials/cred_123/import-secret", {
      method: "POST",
      body: JSON.stringify({}),
    }),
    { params: Promise.resolve({ path: ["cred_123", "import-secret"] }) },
  )
  assert.equal(res.status, 200)
  assert.equal(upstreamUrl, `${gateway}/v1/credentials/cred_123/import-secret`)
  assert.equal(res.headers.get("cache-control"), "no-store")
  assert.equal(res.headers.get("x-content-type-options"), "nosniff")
})

test("rejectCrossOriginRead blocks cross-site Sec-Fetch-Site and foreign Origin on streaming GET routes", async () => {
  delete process.env.DOOPS_CONSOLE_ORIGIN
  const crossSite = authedRequest("http://console.test/api/terminal-stream?cluster=c&instance=i&session_id=s1&term=t1", {
    headers: { "Sec-Fetch-Site": "cross-site" },
  })
  assert.equal((await terminalStreamRoute.GET(crossSite)).status, 403)

  const foreignOrigin = authedRequest("http://console.test/api/terminal-stream?cluster=c&instance=i&session_id=s1&term=t1", {
    headers: { Origin: "https://evil.example.test" },
  })
  assert.equal((await terminalStreamRoute.GET(foreignOrigin)).status, 403)

  // Same-origin passes the origin check and reaches identifier validation.
  const badTarget = authedRequest("http://console.test/api/terminal-stream?cluster=../bad&instance=i&session_id=s1&term=t1", {
    headers: { "Sec-Fetch-Site": "same-origin" },
  })
  assert.equal((await terminalStreamRoute.GET(badTarget)).status, 400)

  const badInputTarget = authedRequest("http://console.test/api/terminal-input", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ cluster: "../bad", instance: "i", session_id: "s1", op: "open" }),
  })
  assert.equal((await terminalInputRoute.POST(badInputTarget)).status, 400)
})

test("login and session POST/DELETE responses set Cache-Control: no-store", async (t) => {
  const identity = {
    user_id: "usr_sec02",
    username: "sec-tester",
    is_admin: false,
    token_id: "tok_sec02",
    name: "sec02",
    issued_at: "2026-11-26T00:00:00Z",
    expires_at: new Date(Date.now() + 3600_000).toISOString(),
  }
  t.mock.method(globalThis, "fetch", async (url) => {
    if (String(url).endsWith("/v1/auth/login")) return Response.json({ token: TOKEN })
    return Response.json(identity)
  })

  const loginRes = await loginRoute.POST(new Request("http://console.test/api/login", {
    method: "POST",
    headers: { "Content-Type": "application/json", Origin: "http://console.test" },
    body: JSON.stringify({ gateway, username: "sec-tester", password: "synthetic-password" }),
  }))
  assert.equal(loginRes.status, 200)
  assert.equal(loginRes.headers.get("cache-control"), "no-store")

  const postSession = await sessionRoute.POST(new Request("http://console.test/api/session", {
    method: "POST",
    headers: { "Content-Type": "application/json", Origin: "http://console.test" },
    body: JSON.stringify({ gateway, token: TOKEN }),
  }))
  assert.equal(postSession.status, 200)
  assert.equal(postSession.headers.get("cache-control"), "no-store")

  const delSession = await sessionRoute.DELETE(authedRequest("http://console.test/api/session", { method: "DELETE" }))
  assert.equal(delSession.status, 200)
  assert.equal(delSession.headers.get("cache-control"), "no-store")
})
