import assert from "node:assert/strict"
import { once } from "node:events"
import { mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join, resolve } from "node:path"
import test, { after } from "node:test"
import { fileURLToPath, pathToFileURL } from "node:url"
import TypeScript from "typescript"
import WebSocket, { WebSocketServer } from "ws"

const root = fileURLToPath(new URL("../../", import.meta.url))
const fixture = mkdtempSync(join(tmpdir(), "doops-rpc-route-test-"))
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
  const parsed = TypeScript.createSourceFile(
    filename,
    javascript,
    TypeScript.ScriptTarget.ES2022,
    true,
    TypeScript.ScriptKind.JS,
  )
  for (const statement of parsed.statements.filter((statement) =>
    (TypeScript.isImportDeclaration(statement) || TypeScript.isExportDeclaration(statement)) &&
    statement.moduleSpecifier,
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
    const replacement = sourceModule(resolved.resolvedFileName)
    javascript = javascript.slice(0, specifier.getStart(parsed)) +
      JSON.stringify(replacement) +
      javascript.slice(specifier.end)
  }
  writeFileSync(output, javascript)
  return url
}

const { POST } = await import(sourceModule(join(root, "app/api/rpc/route.ts")))

function deferred() {
  let resolvePromise
  const promise = new Promise((resolve) => { resolvePromise = resolve })
  return { promise, resolve: resolvePromise }
}

function send(socket, message) {
  if (socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ jsonrpc: "2.0", ...message }))
}

async function localGateway(t, {
  onInitialize = (connection, message) => send(connection.socket, { id: message.id, result: { capabilities: {} } }),
  onCall = () => {},
  onCancel = (connection, message) => send(connection.socket, { id: message.id, result: { canceled: true } }),
} = {}) {
  const server = new WebSocketServer({ host: "127.0.0.1", port: 0, path: "/v1/rpc" })
  const connectionReady = deferred()
  const connections = []
  const calls = []
  const cancels = []

  server.on("connection", (socket, request) => {
    const connection = {
      socket,
      request,
      initialized: deferred(),
      call: deferred(),
      cancel: deferred(),
    }
    connections.push(connection)
    connectionReady.resolve(connection)
    socket.on("message", (raw) => {
      const message = JSON.parse(raw.toString())
      if (message.method === "initialize") {
        connection.initialized.resolve(message)
        onInitialize(connection, message)
      } else if (message.method === "tools/call") {
        calls.push(message)
        connection.call.resolve(message)
        onCall(connection, message)
      } else if (message.method === "tools/cancel") {
        cancels.push(message)
        connection.cancel.resolve(message)
        onCancel(connection, message)
      }
    })
  })

  await once(server, "listening")
  const address = server.address()
  assert.ok(address && typeof address === "object")
  const gateway = `http://127.0.0.1:${address.port}`

  t.after(async () => {
    for (const socket of server.clients) socket.terminate()
    await new Promise((resolve, reject) => {
      server.close((error) => {
        if (error && error.code !== "ERR_SERVER_NOT_RUNNING") reject(error)
        else resolve()
      })
    })
  })

  return {
    gateway,
    calls,
    cancels,
    connection: connectionReady.promise,
    connections,
  }
}

function sessionCookie(gateway) {
  return `doops_session_gateway=${encodeURIComponent(gateway)}; doops_session_token=synthetic-token`
}

function rpcRequest(gateway, body) {
  return new Request("http://console.test/api/rpc", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Cookie: sessionCookie(gateway),
    },
    body: JSON.stringify(body),
  })
}

function callBody(requestId, sessionId = "rpc-session") {
  return {
    cluster: "test",
    instance: "node",
    tool: "doops_agent_prompt",
    arguments: { session_id: sessionId, instruction: "synthetic operation" },
    requestId,
  }
}

async function collectEvents(response) {
  assert.ok(response.body)
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  const events = []
  let buffer = ""
  try {
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      let index
      while ((index = buffer.indexOf("\n")) >= 0) {
        const line = buffer.slice(0, index).trim()
        buffer = buffer.slice(index + 1)
        if (line) events.push(JSON.parse(line))
      }
    }
    buffer += decoder.decode()
    if (buffer.trim()) events.push(JSON.parse(buffer.trim()))
    return events
  } finally {
    reader.releaseLock()
  }
}

function sleep(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds))
}

async function cancelRequest(gateway, requestId, sessionId = "rpc-session") {
  return POST(rpcRequest(gateway, {
    cluster: "test",
    instance: "node",
    arguments: { session_id: sessionId },
    cancelRequestId: requestId,
  }))
}

test("cancellation ACK leaves the original RPC stream open until terminal evidence", { timeout: 5_000 }, async (t) => {
  const gateway = await localGateway(t)
  const requestId = "cancel-waits"
  const response = await POST(rpcRequest(gateway.gateway, callBody(requestId)))
  assert.equal(response.status, 200)
  let settled = false
  const eventsPromise = collectEvents(response).finally(() => { settled = true })
  const connection = await gateway.connection
  await connection.initialized.promise
  await connection.call.promise

  const cancellation = await cancelRequest(gateway.gateway, requestId)
  assert.equal(cancellation.status, 200)
  assert.deepEqual(await cancellation.json(), {
    request_id: requestId,
    cancel_requested: true,
  })
  await connection.cancel.promise
  assert.equal(settled, false, "cancel ACK must not close the original RPC stream")
  await sleep(25)
  assert.equal(settled, false, "the original stream must wait for terminal evidence")

  send(connection.socket, { id: 2, error: { code: -32007, message: "operation canceled" } })
  const events = await eventsPromise
  assert.equal(events.some((event) => event.type === "error" && event.code === -32007), true)
  assert.equal(events.some((event) => event.code === -32008), false)
  assert.equal(events.at(-1)?.type, "done")
})

test("ACK-only cancellation becomes unknown when the upstream disconnects", { timeout: 5_000 }, async (t) => {
  const gateway = await localGateway(t, {
    onCancel: (connection, message) => {
      send(connection.socket, { id: message.id, result: { canceled: true } })
      setTimeout(() => connection.socket.terminate(), 10)
    },
  })
  const requestId = "cancel-unknown"
  const response = await POST(rpcRequest(gateway.gateway, callBody(requestId)))
  const eventsPromise = collectEvents(response)
  const connection = await gateway.connection
  await connection.call.promise
  const cancellation = await cancelRequest(gateway.gateway, requestId)
  assert.equal(cancellation.status, 200)
  await connection.cancel.promise

  const events = await eventsPromise
  assert.deepEqual(events.filter((event) => event.type === "error").map((event) => event.code), [-32008])
  assert.equal(events.some((event) => event.type === "result"), false)
  assert.equal(events.at(-1)?.type, "done")
})

test("cross-wired notifications and cancellation requests are rejected", { timeout: 5_000 }, async (t) => {
  const gateway = await localGateway(t, {
    onCall: (connection, message) => {
      send(connection.socket, {
        method: "notifications/message",
        params: { sessionID: "other-session", request_id: message.id, data: "wrong session" },
      })
      send(connection.socket, {
        method: "notifications/message",
        params: { sessionID: "rpc-session", request_id: 999, data: "wrong request" },
      })
      send(connection.socket, {
        method: "notifications/message",
        params: { sessionID: "rpc-session", request_id: message.id, data: "accepted" },
      })
      connection.complete = () => send(connection.socket, {
        id: message.id,
        result: { isError: false, content: [{ text: "complete" }] },
      })
    },
  })
  const requestId = "cross-wire"
  const response = await POST(rpcRequest(gateway.gateway, callBody(requestId)))
  const eventsPromise = collectEvents(response)
  const connection = await gateway.connection
  await connection.call.promise

  const wrongCancellation = await cancelRequest(gateway.gateway, requestId, "other-session")
  assert.equal(wrongCancellation.status, 409)
  assert.equal(gateway.cancels.length, 0)
  connection.complete()

  const events = await eventsPromise
  assert.deepEqual(events.filter((event) => event.type === "output").map((event) => event.data), ["accepted"])
  assert.equal(events.some((event) => event.data === "wrong session" || event.data === "wrong request"), false)
  assert.equal(events.some((event) => event.type === "result"), true)
})

test("cancelling before dispatch prevents a late initialize response from dispatching the tool", { timeout: 5_000 }, async (t) => {
  const gateway = await localGateway(t, { onInitialize: () => {} })
  const requestId = "cancel-before-dispatch"
  const response = await POST(rpcRequest(gateway.gateway, callBody(requestId)))
  const eventsPromise = collectEvents(response)
  const connection = await gateway.connection
  await connection.initialized.promise

  const cancellation = await cancelRequest(gateway.gateway, requestId)
  assert.equal(cancellation.status, 200)
  send(connection.socket, { id: 1, result: { capabilities: {} } })
  await sleep(50)

  assert.equal(gateway.calls.length, 0)
  const events = await eventsPromise
  assert.deepEqual(events.filter((event) => event.type === "error").map((event) => event.code), [-32007])
})
