import assert from "node:assert/strict"
import { mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs"
import test, { after } from "node:test"
import { fileURLToPath, pathToFileURL } from "node:url"
import { join, resolve } from "node:path"
import TypeScript from "typescript"
import { create, toJson } from "@bufbuild/protobuf"
import { createAgentCommandEnvelope } from "@agent-cloud/agent-ui/runtime"
import { SessionSnapshotSchema, SessionDeltaBatchSchema, SessionCursorSchema, CommandReceiptSchema, CommandReceiptState } from "@agent-cloud/agent-ui/protocol"

const root = fileURLToPath(new URL("../", import.meta.url))
const fixture = mkdtempSync(join(root, ".agent-session-transport-test-"))
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
    (TypeScript.isImportDeclaration(statement) || TypeScript.isExportDeclaration(statement)) && statement.moduleSpecifier).reverse()) {
    const specifier = statement.moduleSpecifier
    const name = specifier.text
    if (!name.startsWith(".") && !name.startsWith("@/")) continue
    const source = name.startsWith("@/") ? join(root, name.slice(2)) : name
    const resolved = TypeScript.resolveModuleName(source, filename, {
      moduleResolution: TypeScript.ModuleResolutionKind.Bundler,
      module: TypeScript.ModuleKind.ESNext,
    }, TypeScript.sys).resolvedModule
    assert.ok(resolved && resolved.resolvedFileName.startsWith(root) && resolved.resolvedFileName.endsWith(".ts"))
    const replacement = sourceModule(resolved.resolvedFileName)
    javascript = javascript.slice(0, specifier.getStart(parsed)) + JSON.stringify(replacement) + javascript.slice(specifier.end)
  }
  writeFileSync(output, javascript)
  return url
}

const { createDoopsAgentSessionTransport } = await import(sourceModule(join(root, "lib/agent-session-transport.ts")))
const { SESSION_GENERATION_HEADER } = await import(sourceModule(join(root, "lib/auth-session.ts")))

test("native agent prompt keeps text and retry identity stable without injected bindings", async (t) => {
  const transport = createDoopsAgentSessionTransport({
    session: { gateway: "https://gateway.example.test", sessionGeneration: "generation-one" },
    target: { cluster: "test", instance: "node" },
    sessionId: "session-one",
  })
  const issuedAt = new Date().toISOString()
  const commandInput = {
    sessionId: "session-one",
    streamEpoch: "epoch-one",
    commandId: "command-one",
    issuedAt,
    expectedRevision: 0n,
    command: { case: "sendPrompt", value: { text: "deploy synthetic", attachments: [] } },
  }
  const first = await createAgentCommandEnvelope(commandInput)
  const requests = []
  t.mock.method(globalThis, "fetch", async (_url, init) => {
    const body = JSON.parse(init.body)
    assert.equal(init.headers[SESSION_GENERATION_HEADER], "generation-one")
    requests.push(body.command)
    const receipt = create(CommandReceiptSchema, {
      sessionId: body.command.sessionId,
      commandId: body.command.commandId,
      payloadDigest: body.command.payloadDigest,
      state: CommandReceiptState.ACCEPTED,
      receivedAt: issuedAt,
      updatedAt: issuedAt,
    })
    return new Response(JSON.stringify(toJson(CommandReceiptSchema, receipt)), {
      headers: { "Content-Type": "application/json" },
    })
  })

  await transport.execute(first, new AbortController().signal)
  await transport.execute(first, new AbortController().signal)
  const retryEnvelope = await createAgentCommandEnvelope(commandInput)
  await transport.execute(retryEnvelope, new AbortController().signal)

  assert.equal(requests.length, 3)
  assert.deepEqual(requests[0], requests[1])
  assert.deepEqual(requests[1], requests[2])
  assert.match(JSON.stringify(requests[0]), /deploy synthetic/)
  assert.doesNotMatch(JSON.stringify(requests[0]), /数据库引用|代码仓库引用/)
})

test("a target without a model stops retrying after the limit and shows the Chinese hint", { timeout: 10_000 }, async (t) => {
  const { createDoopsAgentUiRuntime } = await import(sourceModule(join(root, "lib/agent-ui-runtime.ts")))
  const requests = []
  t.mock.method(globalThis, "fetch", async (url) => {
    requests.push(String(url))
    return new Response(JSON.stringify({ error: "Gateway 还没有可下发到该节点的模型" }), {
      status: 503, headers: { "Content-Type": "application/json" },
    })
  })
  const runtime = createDoopsAgentUiRuntime({
    session: { gateway: "https://gateway.example.test", sessionGeneration: "generation-one" },
    target: { cluster: "test", instance: "node", key: "test/node" },
    sessionId: "session-no-model",
  })
  await assert.rejects(runtime.open("session-no-model"))
  const snapshotRequests = requests.filter((url) => url.startsWith("/api/agent-sessions?"))
  // maxConsecutiveFailures=2: the first attempt plus two retries, then the connection fails closed.
  assert.equal(snapshotRequests.length, 3)
  // The runtime publishes connection changes on its next frame; wait past it and past any retry delay.
  await new Promise((resolve) => setTimeout(resolve, 1_500))
  const snapshot = runtime.getSnapshot("session-no-model")
  assert.equal(snapshot.connection, "disconnected")
  assert.equal(snapshot.error, "该目标还没有绑定 DoAgent 模型。平台管理员可在「凭据」里为该目标绑定并激活 modelProvider 凭据")
  assert.equal(requests.length, snapshotRequests.length, "no further requests after the retry limit")
  runtime.close("session-no-model")
})


test("snapshot reads and event reconnects carry the owning browser generation", async (t) => {
  const sessionId = "session-generation"
  const transport = createDoopsAgentSessionTransport({
    session: { gateway: "https://gateway.example.test", sessionGeneration: "generation-one" },
    target: { cluster: "test", instance: "node", key: "test/node" }, sessionId,
  })
  const snapshot = create(SessionSnapshotSchema, { sessionId, streamEpoch: "epoch-one" })
  const batch = create(SessionDeltaBatchSchema, { sessionId, streamEpoch: "epoch-one", baseRevision: 0n, revision: 1n, firstSequence: 1n, lastSequence: 1n })
  const calls = []
  t.mock.method(globalThis, "fetch", async (url, init) => {
    assert.equal(init.headers[SESSION_GENERATION_HEADER], "generation-one")
    calls.push(String(url))
    return String(url).includes("view=snapshot") ? Response.json(toJson(SessionSnapshotSchema, snapshot))
      : new Response(JSON.stringify(toJson(SessionDeltaBatchSchema, batch)) + "\n")
  })
  assert.equal((await transport.getSnapshot(new AbortController().signal)).sessionId, sessionId)
  const cursor = create(SessionCursorSchema, { sessionId, streamEpoch: "epoch-one" })
  const deltas = []
  for await (const delta of transport.streamDeltas(cursor, new AbortController().signal)) deltas.push(delta)
  assert.equal(deltas.length, 1)
  assert.equal(deltas[0].sessionId, sessionId)
  assert.equal(calls.length, 2)
})

test("Agent attachment uploads carry the same browser generation as its session", async (t) => {
  const { createDoopsAgentUiRuntime } = await import(sourceModule(join(root, "lib/agent-ui-runtime.ts")))
  const runtime = createDoopsAgentUiRuntime({
    session: { gateway: "https://gateway.example.test", sessionGeneration: "upload-generation" },
    target: { cluster: "test", instance: "node", key: "test/node" }, sessionId: "session-upload",
  })
  t.mock.method(globalThis, "fetch", async (url, init) => {
    assert.equal(url, "/api/agent-attachments")
    assert.equal(init.headers[SESSION_GENERATION_HEADER], "upload-generation")
    assert.equal(init.body.get("session_id"), "session-upload")
    assert.equal(init.body.get("file").name, "note.txt")
    return Response.json({ id: "att_synthetic", name: "note.txt", mediaType: "text/plain", bytes: 4 })
  })
  const reference = await runtime.uploadAttachment("session-upload", new File(["note"], "note.txt", { type: "text/plain" }))
  assert.equal(reference.id, "att_synthetic")
  runtime.close("session-upload")
})
