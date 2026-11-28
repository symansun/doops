import assert from "node:assert/strict"
import { mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join, resolve } from "node:path"
import test, { after } from "node:test"
import { fileURLToPath, pathToFileURL } from "node:url"
import TypeScript from "typescript"

const root = fileURLToPath(new URL("../../", import.meta.url))
const fixture = mkdtempSync(join(tmpdir(), "doops-terminal-ai-route-test-"))
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

const { POST } = await import(sourceModule(join(root, "app/api/terminal-ai/route.ts")))
const gateway = "http://127.0.0.1:18080"

function makeReq(body, { cookie = true, origin = "http://console.test" } = {}) {
  return new Request("http://console.test/api/terminal-ai", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      ...(origin ? { Origin: origin } : {}),
      ...(cookie ? { Cookie: `doops_session_gateway=${encodeURIComponent(gateway)}; doops_session_token=synthetic-token` } : {}),
    },
    body: typeof body === "string" ? body : JSON.stringify(body),
  })
}

test("POST /api/terminal-ai rejects cross-origin, unauthenticated, and invalid requests", async () => {
  const cross = await POST(makeReq({ cluster: "prod", instance: "n1", session_id: "s1", question: "cpu" }, { origin: "https://evil.invalid" }))
  assert.equal(cross.status, 403)

  const noAuth = await POST(makeReq({ cluster: "prod", instance: "n1", session_id: "s1", question: "cpu" }, { cookie: false }))
  assert.equal(noAuth.status, 401)

  const badTarget = await POST(makeReq({ cluster: "../etc", instance: "n1", session_id: "s1", question: "cpu" }))
  assert.equal(badTarget.status, 400)

  const emptyQ = await POST(makeReq({ cluster: "prod", instance: "n1", session_id: "s1", question: "   " }))
  assert.equal(emptyQ.status, 400)

  const badAction = await POST(makeReq({ cluster: "prod", instance: "n1", session_id: "s1", question: "cpu", action: "dropdb" }))
  assert.equal(badAction.status, 400)
})

test("POST /api/terminal-ai forwards single-shot prompt to Gateway /v1/terminal/ai with security headers", async (t) => {
  const calls = []
  t.mock.method(globalThis, "fetch", async (url, init) => {
    calls.push({ url, init })
    return new Response(
      JSON.stringify({
        ok: true,
        model: "deepseek/deepseek-flash",
        provider: "deepseek",
        explanation: "查看主机 CPU 信息与实时占用。",
        commands: [{ command: "lscpu", description: "查看 CPU 架构与核心数" }],
      }),
      { status: 200, headers: { "Content-Type": "application/json" } },
    )
  })

  const res = await POST(makeReq({
    cluster: "prod",
    instance: "node-1",
    session_id: "console-s1",
    question: "我想查一下系统的cpu",
    action: "ask",
    cwd: "/workspace",
  }))

  assert.equal(res.status, 200)
  assert.equal(res.headers.get("cache-control"), "no-store")
  assert.equal(res.headers.get("x-content-type-options"), "nosniff")
  assert.equal(res.headers.get("cross-origin-resource-policy"), "same-origin")
  assert.equal(calls.length, 1)
  assert.equal(calls[0].url, `${gateway}/v1/terminal/ai`)
  assert.equal(calls[0].init.headers.Authorization, "Bearer synthetic-token")
  assert.equal(calls[0].init.redirect, "manual")
  const payload = await res.json()
  assert.equal(payload.ok, true)
  assert.equal(payload.commands[0].command, "lscpu")
})

test("POST /api/terminal-ai maps Gateway 404 to clear 501 upgrade notice and passes through 412", async (t) => {
  t.mock.method(globalThis, "fetch", async () => new Response("404 page not found", { status: 404 }))
  const res404 = await POST(makeReq({ cluster: "prod", instance: "n1", session_id: "s1", question: "查cpu" }))
  assert.equal(res404.status, 501)
  const body404 = await res404.json()
  assert.equal(body404.code, "terminal_ai_gateway_unsupported")

  t.mock.method(globalThis, "fetch", async () =>
    new Response(JSON.stringify({ code: "model_provider_not_configured", message: "未绑定模型凭据" }), { status: 412 }),
  )
  const res412 = await POST(makeReq({ cluster: "prod", instance: "n1", session_id: "s1", question: "查cpu" }))
  assert.equal(res412.status, 412)
  const body412 = await res412.json()
  assert.equal(body412.code, "model_provider_not_configured")
  assert.equal(body412.error, "未绑定模型凭据")
})
