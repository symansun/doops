import assert from "node:assert/strict"
import { execFileSync, spawn } from "node:child_process"
import { once } from "node:events"
import { existsSync, mkdtempSync, readFileSync, readdirSync, rmSync, symlinkSync, writeFileSync } from "node:fs"
import { createServer } from "node:http"
import { tmpdir } from "node:os"
import { join, resolve } from "node:path"
import { Readable } from "node:stream"
import test, { after, before } from "node:test"
import { fileURLToPath, pathToFileURL } from "node:url"
import TypeScript from "typescript"
import WebSocket from "ws"

const root = fileURLToPath(new URL("../../", import.meta.url))
const fixture = mkdtempSync(join(tmpdir(), "doops-attachment-auth-test-"))
symlinkSync(join(root, "node_modules"), join(fixture, "node_modules"), "dir")
const compiled = new Map()
function sourceModule(filename) {
  filename = resolve(filename)
  if (compiled.has(filename)) return compiled.get(filename)
  const output = join(fixture, `${compiled.size}.mjs`)
  const url = pathToFileURL(output).href
  compiled.set(filename, url)
  let javascript = TypeScript.transpile(readFileSync(filename, "utf8"), {
    module: TypeScript.ModuleKind.ESNext, target: TypeScript.ScriptTarget.ES2022,
  }, filename)
  const parsed = TypeScript.createSourceFile(filename, javascript, TypeScript.ScriptTarget.ES2022, true, TypeScript.ScriptKind.JS)
  for (const statement of parsed.statements.filter((statement) =>
    (TypeScript.isImportDeclaration(statement) || TypeScript.isExportDeclaration(statement)) && statement.moduleSpecifier).reverse()) {
    const specifier = statement.moduleSpecifier, name = specifier.text
    if (!name.startsWith(".") && !name.startsWith("@/")) continue
    const source = name.startsWith("@/") ? join(root, name.slice(2)) : name
    const resolved = TypeScript.resolveModuleName(source, filename, {
      moduleResolution: TypeScript.ModuleResolutionKind.Bundler, module: TypeScript.ModuleKind.ESNext,
    }, TypeScript.sys).resolvedModule
    assert.ok(resolved && resolved.resolvedFileName.startsWith(root) && resolved.resolvedFileName.endsWith(".ts"))
    const replacement = sourceModule(resolved.resolvedFileName)
    javascript = javascript.slice(0, specifier.getStart(parsed)) + JSON.stringify(replacement) + javascript.slice(specifier.end)
  }
  writeFileSync(output, javascript)
  return url
}

// Isolate the existing tmpdir-based attachment store without changing its configuration contract.
const previousTmpdir = process.env.TMPDIR
process.env.TMPDIR = fixture
const { POST: upload } = await import(sourceModule(join(root, "app/api/agent-attachments/route.ts")))
const { POST: login } = await import(sourceModule(join(root, "app/api/login/route.ts")))
const { GET: snapshot } = await import(sourceModule(join(root, "app/api/agent-sessions/route.ts")))
const { consumeAgentAttachment, MAX_AGENT_ATTACHMENT_BYTES } = await import(sourceModule(join(root, "lib/server/agent-attachments.ts")))
if (previousTmpdir === undefined) delete process.env.TMPDIR
else process.env.TMPDIR = previousTmpdir

const attachmentDir = join(fixture, "doops-agent-attachments")
const cookies = new Map(), metadataCalls = [], agents = []
const scope = { cluster: "attachment-test", instance: "node-a", session_id: "attachment-session" }
let gatewayProcess, gatewayURL, web, webURL
let gatewayLog = ""
let metadata = { status: "connected", model: "synthetic-model", event_schema: "doops.sh/acp-events/v1", event_formats: ["acp"] }

async function listen(server) {
  server.listen(0, "127.0.0.1")
  await once(server, "listening")
  return `http://127.0.0.1:${server.address().port}`
}

async function waitUntil(check) {
  const deadline = Date.now() + 5_000
  while (Date.now() < deadline) {
    if (await check()) return
    await new Promise((resolve) => setTimeout(resolve, 20))
  }
  throw new Error(`Gateway fixture did not become ready: ${gatewayLog}`)
}

async function connectAgent(cluster, instance) {
  const socket = new WebSocket(`${gatewayURL.replace("http:", "ws:")}/v1/agent/connect?cluster=${cluster}&instance=${instance}`)
  agents.push(socket)
  socket.on("message", (raw) => {
    const request = JSON.parse(raw.toString())
    if (request.method === "initialize") {
      socket.send(JSON.stringify({ jsonrpc: "2.0", id: request.id, result: {
        runtimeId: `synthetic-${cluster}-${instance}`, protocolVersion: "2024-11-05", capabilities: { tools: {} },
      } }))
    } else if (request.method === "tools/call") {
      assert.equal(request.params.name, "doops_agent_prompt")
      assert.equal(request.params.arguments.mode, "metadata")
      metadataCalls.push({ cluster, instance, arguments: request.params.arguments })
      socket.send(JSON.stringify({ jsonrpc: "2.0", id: request.id, result: {
        content: [{ type: "text", text: JSON.stringify(metadata) }], isError: false,
      } }))
    }
  })
  await once(socket, "open")
}

before(async () => {
  const binary = join(fixture, "doops-gateway"), database = join(fixture, "gateway.db")
  execFileSync("go", ["build", "-o", binary, "."], { cwd: join(root, "backend/gateway"), timeout: 60_000 })
  for (const [username, cluster, instance, actions] of [
    ["authorized", scope.cluster, scope.instance, "targets:list,ask"],
    ["read-only", scope.cluster, scope.instance, "targets:list,read"],
    ["ask-hidden", scope.cluster, scope.instance, "ask"],
    ["wrong-cluster", "other-cluster", scope.instance, "targets:list,ask"],
    ["wrong-instance", scope.cluster, "node-b", "targets:list,ask"],
  ]) {
    execFileSync(binary, ["user", "create", "-name", username, "-password", "synthetic-local-password", "-db", database])
    execFileSync(binary, ["grant", "-user", username, "-cluster", cluster, "-instance", instance, "-actions", actions, "-db", database])
  }
  const reservation = createServer()
  gatewayURL = await listen(reservation)
  const port = String(reservation.address().port)
  await new Promise((resolve) => reservation.close(resolve))
  gatewayProcess = spawn(binary, ["serve", "-port", port, "-db", database, "-target-queue-timeout", "2s"])
  gatewayProcess.stdout.on("data", (chunk) => { gatewayLog += chunk })
  gatewayProcess.stderr.on("data", (chunk) => { gatewayLog += chunk })
  await waitUntil(async () => {
    try { return (await fetch(`${gatewayURL}/health`)).ok } catch { return false }
  })
  for (const [cluster, instance] of [[scope.cluster, scope.instance], [scope.cluster, "node-b"], ["other-cluster", scope.instance]]) {
    await connectAgent(cluster, instance)
  }
  web = createServer(async (incoming, outgoing) => {
    try {
      const url = new URL(incoming.url, "http://localhost")
      const request = new Request(url, {
        method: incoming.method, headers: incoming.headers,
        ...(incoming.method === "POST" ? { body: Readable.toWeb(incoming), duplex: "half" } : {}),
      })
      const response = url.pathname === "/api/login" ? await login(request)
        : url.pathname === "/api/agent-sessions" ? await snapshot(request) : await upload(request)
      const headers = Object.fromEntries(response.headers)
      if (response.headers.getSetCookie().length) headers["set-cookie"] = response.headers.getSetCookie()
      outgoing.writeHead(response.status, headers)
      outgoing.end(Buffer.from(await response.arrayBuffer()))
    } catch (error) { outgoing.statusCode = 500; outgoing.end(String(error)) }
  })
  webURL = await listen(web)
  for (const username of ["authorized", "read-only", "ask-hidden", "wrong-cluster", "wrong-instance"]) {
    const response = await fetch(`${webURL}/api/login`, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ gateway: gatewayURL, username, password: "synthetic-local-password" }),
    })
    assert.equal(response.status, 200, await response.text())
    cookies.set(username, response.headers.getSetCookie().map((cookie) => cookie.split(";")[0]).join("; "))
  }
  await waitUntil(async () => {
    const response = await fetch(`${gatewayURL}/v1/targets`, { headers: { Authorization: `Bearer ${cookieValue(cookies.get("authorized"), "doops_session_token")}` } })
    return response.ok && (await response.json()).targets.some((target) => target.cluster === scope.cluster && target.instance === scope.instance)
  })
}, { timeout: 90_000 })

after(async () => {
  for (const agent of agents) agent.terminate()
  if (web) { web.closeAllConnections(); await new Promise((resolve) => web.close(resolve)) }
  if (gatewayProcess && gatewayProcess.exitCode === null) {
    const exited = once(gatewayProcess, "exit")
    gatewayProcess.kill("SIGTERM")
    await exited
  }
  rmSync(fixture, { recursive: true, force: true })
})

function cookieValue(cookie, name) {
  return decodeURIComponent(cookie.split("; ").find((value) => value.startsWith(`${name}=`)).slice(name.length + 1))
}

function diskState() {
  return existsSync(attachmentDir) ? readdirSync(attachmentDir).sort().map((name) => [name, readFileSync(join(attachmentDir, name)).toString("base64")]) : []
}

async function sendUpload(cookie, fields = {}, bytes = new TextEncoder().encode("synthetic attachment content")) {
  const form = new FormData()
  for (const [key, value] of Object.entries({ ...scope, ...fields })) form.set(key, value)
  form.set("file", new File([bytes], "note.txt", { type: "text/plain" }))
  return fetch(`${webURL}/api/agent-attachments`, { method: "POST", headers: cookie ? { Cookie: cookie } : {}, body: form })
}

test("anonymous upload is 401 before multipart parsing and creates no attachment directory", async () => {
  const before = diskState()
  assert.equal(existsSync(attachmentDir), false)
  const response = await sendUpload()
  assert.equal(response.status, 401)
  assert.deepEqual(diskState(), before)
  assert.equal(existsSync(attachmentDir), false)
  const malformed = await fetch(`${webURL}/api/agent-attachments`, { method: "POST", body: "not multipart" })
  assert.equal(malformed.status, 401)
})

test("a cookie is not authority: invalid Gateway token is 401 and writes nothing", async () => {
  const before = diskState()
  const cookie = cookies.get("authorized").replace(/doops_session_token=[^;]+/, "doops_session_token=synthetic-invalid-token")
  const response = await sendUpload(cookie)
  assert.equal(response.status, 401)
  assert.deepEqual(diskState(), before)
})

for (const username of ["read-only", "ask-hidden", "wrong-cluster", "wrong-instance"]) {
  test(`${username} cannot upload to the same session on an unauthorized or invisible exact target`, async () => {
    const before = diskState(), calls = metadataCalls.length
    const response = await sendUpload(cookies.get(username))
    assert.equal(response.status, 403)
    assert.deepEqual(diskState(), before)
    assert.equal(metadataCalls.length, calls, "Denied upload must not reach the Agent")
  })
}

test("Gateway connection failure returns 502 and does not store an attachment", async () => {
  const unavailable = createServer(), unavailableURL = await listen(unavailable)
  await new Promise((resolve) => unavailable.close(resolve))
  const cookie = cookies.get("authorized").replace(/doops_session_gateway=[^;]+/, `doops_session_gateway=${encodeURIComponent(unavailableURL)}`)
  const before = diskState(), response = await sendUpload(cookie)
  assert.equal(response.status, 502)
  assert.deepEqual(diskState(), before)
})

test("authorized live session uploads metadata only and consumes the file exactly once", async () => {
  const cookie = cookies.get("authorized")
  const session = await fetch(`${webURL}/api/agent-sessions?${new URLSearchParams({ ...scope, view: "snapshot" })}`, { headers: { Cookie: cookie } })
  assert.equal(session.status, 200, await session.text())
  const before = diskState(), calls = metadataCalls.length
  const response = await sendUpload(cookie)
  assert.equal(response.status, 200, await response.clone().text())
  const reference = await response.json()
  assert.deepEqual(Object.keys(reference).sort(), ["bytes", "id", "mediaType", "name"])
  assert.equal(reference.name, "note.txt")
  assert.equal(reference.mediaType, "text/plain")
  assert.equal(response.headers.get("cache-control"), "no-store")
  assert.equal(metadataCalls.length, calls + 1, "Upload must revalidate ActionAsk even for an existing session")
  const call = metadataCalls.at(-1)
  assert.equal(call.cluster, scope.cluster)
  assert.equal(call.instance, scope.instance)
  assert.equal(call.arguments.session_id, scope.session_id)
  assert.equal(diskState().length, before.length + 1)
  const consumed = await consumeAgentAttachment({ gateway: gatewayURL, token: cookieValue(cookie, "doops_session_token"), cluster: scope.cluster, instance: scope.instance, sessionId: scope.session_id }, reference.id)
  assert.equal(consumed.base64Data, Buffer.from("synthetic attachment content").toString("base64"))
  assert.deepEqual(diskState(), before)
  await assert.rejects(consumeAgentAttachment({ gateway: gatewayURL, token: cookieValue(cookie, "doops_session_token"), cluster: scope.cluster, instance: scope.instance, sessionId: scope.session_id }, reference.id), /附件不存在或已过期/)
})

test("target and ActionAsk denial happen before reading File bytes", async (t) => {
  for (const username of ["read-only", "ask-hidden", "wrong-cluster", "wrong-instance"]) {
    const form = new FormData()
    for (const [key, value] of Object.entries(scope)) form.set(key, value)
    form.set("file", new File(["synthetic unread content"], "unread.txt", { type: "text/plain" }))
    const read = t.mock.method(form.get("file"), "arrayBuffer", () => { throw new Error("unauthorized file read") })
    const request = new Request(`${webURL}/api/agent-attachments`, { method: "POST", headers: { Cookie: cookies.get(username) } })
    t.mock.method(request, "formData", async () => form)
    const before = diskState(), response = await upload(request)
    assert.equal(response.status, 403)
    assert.equal(read.mock.callCount(), 0)
    assert.deepEqual(diskState(), before)
  }
})

test("authorized upload retains empty-file and 25 MiB rejection without disk writes", async () => {
  for (const [bytes, status] of [[new Uint8Array(), 400], [new Uint8Array(MAX_AGENT_ATTACHMENT_BYTES + 1), 413]]) {
    const before = diskState(), response = await sendUpload(cookies.get("authorized"), {}, bytes)
    assert.equal(response.status, status)
    assert.deepEqual(diskState(), before)
  }
})

test("DoAgent metadata failure blocks storage and surfaces its existing status", async () => {
  const previous = metadata
  metadata = { ...metadata, status: "unconfigured", model: "" }
  try {
    const before = diskState(), response = await sendUpload(cookies.get("authorized"))
    assert.equal(response.status, 503)
    assert.deepEqual(diskState(), before)
  } finally { metadata = previous }
})
