import assert from "node:assert/strict"
import { randomUUID } from "node:crypto"
import { EventEmitter, once } from "node:events"
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { join, resolve } from "node:path"
import test, { after } from "node:test"
import { fileURLToPath, pathToFileURL } from "node:url"
import sdkPackage from "@agent-cloud/agent-ui/package.json" with { type: "json" }
import { AgentSessionReductionError } from "@agent-cloud/agent-ui/server"
import { createAgentCommandEnvelope } from "@agent-cloud/agent-ui/runtime"
import { AgentEventSchema, CommandReceiptState, SessionSnapshotSchema, SessionStatus } from "@agent-cloud/agent-ui/protocol"
import { create } from "@bufbuild/protobuf"
import TypeScript from "typescript"
import { WebSocketServer } from "ws"

const root = fileURLToPath(new URL("../../", import.meta.url))
assert.equal(sdkPackage.version, "0.1.4")
const fixture = mkdtempSync(join(root, ".agent-session-test-"))
const compiled = new Map()
after(() => rmSync(fixture, { recursive: true, force: true }))

// Keep fixtures under the checkout so untouched package imports resolve normally.
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
  const imports = parsed.statements.filter((statement) =>
    (TypeScript.isImportDeclaration(statement) || TypeScript.isExportDeclaration(statement)) && statement.moduleSpecifier)
  for (const statement of imports.reverse()) {
    const specifier = statement.moduleSpecifier
    const name = specifier.text
    if (!name.startsWith(".")) continue
    const resolved = TypeScript.resolveModuleName(name, filename, {
      moduleResolution: TypeScript.ModuleResolutionKind.Bundler, module: TypeScript.ModuleKind.ESNext,
    }, TypeScript.sys).resolvedModule
    assert.ok(resolved && resolved.resolvedFileName.startsWith(root) && resolved.resolvedFileName.endsWith(".ts"),
      `Missing DoOps TypeScript source: ${name} from ${filename}`)
    const replacement = sourceModule(resolved.resolvedFileName)
    javascript = javascript.slice(0, specifier.getStart(parsed)) + JSON.stringify(replacement) + javascript.slice(specifier.end)
  }
  writeFileSync(output, javascript)
  return url
}

const { authorizedAgentSession, executeAgentCommand } = await import(sourceModule(join(root, "lib/server/agent-session.ts")))
const { storeAgentAttachment } = await import(sourceModule(join(root, "lib/server/agent-attachments.ts")))
const { AgentGatewayError } = await import(sourceModule(join(root, "lib/server/agent-gateway-rpc.ts")))

const prompt = { case: "sendPrompt", value: { text: "synthetic prompt", attachments: [] } }
const apply = { case: "extension", value: {
  namespace: "doops.operations", semanticType: "apply", schemaVersion: "1",
  payload: { mediaType: "application/json", data: new TextEncoder().encode(JSON.stringify({ confirmed: true, input: {
    services: "synthetic", release: "synthetic", namespace: "synthetic", chart: "https://example.test/chart.tgz",
    values: "", repository: "registry.example.test/synthetic", selector: "digest", version: `sha256:${"a".repeat(64)}`,
    commit: "b".repeat(40), ci: "https://example.test/ci", reason: "functional test", acceptance: "synthetic acceptance", credentials: "",
  } })) },
} }

async function command(hosted, payload) {
  const snapshot = hosted.source.getSnapshot()
  return createAgentCommandEnvelope({
    sessionId: snapshot.sessionId, streamEpoch: snapshot.streamEpoch, commandId: randomUUID(),
    issuedAt: new Date().toISOString(), command: payload,
  })
}

async function fakeRPC(t) {
  const server = new WebSocketServer({ host: "127.0.0.1", port: 0, path: "/v1/rpc" })
  t.after(async () => {
    for (const socket of server.clients) socket.terminate()
    await new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve()))
  })
  await once(server, "listening")
  const operations = [], requests = [], dispatched = new EventEmitter()
  let read = 0
  const send = (socket, message) => socket.send(JSON.stringify({ jsonrpc: "2.0", ...message }))
  server.on("connection", (socket, request) => {
    assert.equal(request.headers.authorization, "Bearer synthetic-token")
    assert.equal(new URL(request.url, "http://localhost").searchParams.get("cluster"), "test")
    assert.equal(new URL(request.url, "http://localhost").searchParams.get("instance"), "node")
    let operation
    socket.on("message", (raw) => {
      const message = JSON.parse(raw.toString())
      if (message.method === "initialize") {
        send(socket, { id: message.id, result: {} })
      } else if (message.method === "tools/call") {
        requests.push(message.params)
        assert.equal(message.params.name, "doops_agent_prompt")
        const args = message.params.arguments
        if (args.mode === "metadata") {
          send(socket, { id: message.id, result: { content: [{ type: "text", text: JSON.stringify({
            status: "connected", model: "synthetic-model", event_schema: "doops.sh/acp-events/v1", event_formats: ["acp"],
          }) }] } })
          return
        }
        assert.equal(args.mode, undefined, "No history or audit reads")
        assert.equal(args.event_format, "acp")
        if (args.attachments?.length) {
          assert.equal(args.attachments.length, 1)
          assert.equal(args.attachments[0].filename, "note.txt")
          assert.equal(args.attachments[0].media_type, "text/plain")
          assert.equal(args.attachments[0].base64_data, Buffer.from("uploaded document").toString("base64"))
        }
        let acknowledge
        operation = {
          args, socket, turnId: randomUUID(), ack: new Promise((resolve) => { acknowledge = resolve }),
          acknowledge,
          update(update) { send(socket, { method: "notifications/message", params: {
            request_id: message.id, sessionID: args.session_id, kind: "acp_update", data: JSON.stringify(update),
          } }) },
          error(code, text) { send(socket, { id: message.id, error: { code, message: text } }) },
          finish(canceled = false) {
            this.update({ sessionUpdate: "agent_message", turnId: this.turnId, content: { type: "text", text: "synthetic response" } })
            this.update({ sessionUpdate: "turn_finished", turnId: this.turnId,
              status: canceled ? "cancelled" : "completed", stopReason: canceled ? "cancelled" : "end_turn" })
            if (canceled) this.error(-32007, "operation canceled")
            else send(socket, { id: message.id, result: { content: [{ type: "text", text: "Operation complete." }] } })
          },
        }
        operations.push(operation)
        dispatched.emit("operation")
      } else {
        assert.equal(message.method, "tools/cancel")
        assert.equal(message.params.id, 2)
        assert.equal(message.id, 3)
        assert.ok(operation)
        send(socket, { id: message.id, result: { canceled: true } })
        operation.acknowledge()
      }
    })
  })
  return {
    operations, requests,
    context: { gateway: `http://127.0.0.1:${server.address().port}`, token: "synthetic-token", cluster: "test", instance: "node", sessionId: randomUUID() },
    async nextOperation() {
      if (operations.length === read) await once(dispatched, "operation")
      return operations[read++]
    },
  }
}

async function authorize(t, context) {
  const hosted = await authorizedAgentSession(context, new AbortController().signal)
  const epoch = hosted.source.getSnapshot().streamEpoch
  const emit = hosted.source.emit
  t.mock.method(hosted.source, "emit", function (events, streamEpoch) {
    assert.equal(streamEpoch, epoch, "Every main emission must carry the owning epoch")
    return emit.call(this, events, streamEpoch)
  })
  return hosted
}

async function assertBlocked(hosted, context, rpc) {
  const dispatched = rpc.operations.length
  for (const payload of [prompt, apply]) {
    await assert.rejects(executeAgentCommand(hosted, context, await command(hosted, payload)),
      (error) => error instanceof AgentGatewayError && error.status === 409)
    assert.equal(rpc.operations.length, dispatched, "A blocked prompt/apply must not be dispatched")
  }
}

async function finishNext(hosted, context, rpc, payload = prompt) {
  const owned = await command(hosted, payload)
  assert.equal((await executeAgentCommand(hosted, context, owned)).state, CommandReceiptState.ACCEPTED)
  const active = hosted.active
  const operation = await rpc.nextOperation()
  assert.equal(operation.args.session_id, context.sessionId)
  assert.equal(operation.args.operation, payload.case === "extension" ? "apply" : "ask")
  operation.finish()
  await active.done
  assert.equal(hosted.active, undefined)
  assert.equal(hosted.source.getSnapshot().commandReceipts.find((receipt) => receipt.commandId === owned.commandId).state, CommandReceiptState.SUCCEEDED)
}

test("cancel ACK keeps same-SID prompt/apply blocked until original RPC confirms cancellation", { timeout: 5_000 }, async (t) => {
  const rpc = await fakeRPC(t), hosted = await authorize(t, rpc.context)
  const owned = await command(hosted, prompt)
  await executeAgentCommand(hosted, rpc.context, owned)
  const active = hosted.active, operation = await rpc.nextOperation()
  let settled = false
  const interruption = executeAgentCommand(hosted, rpc.context, await command(hosted, { case: "interrupt", value: { turnId: owned.commandId } }))
    .then((receipt) => { settled = true; return receipt })
  await operation.ack
  await assertBlocked(hosted, rpc.context, rpc)
  assert.equal(settled, false, "Cancel ACK cannot settle interrupt")
  assert.equal(hosted.active, active)
  operation.finish(true)
  assert.equal((await interruption).state, CommandReceiptState.SUCCEEDED)
  assert.equal(hosted.source.getSnapshot().commandReceipts.find((receipt) => receipt.commandId === owned.commandId).state, CommandReceiptState.CANCELLED)
  assert.equal(hosted.active, undefined)
  await finishNext(hosted, rpc.context, rpc)
  await finishNext(hosted, rpc.context, rpc, apply)
  assert.equal(rpc.operations.length, 3)
})

for (const ending of ["disconnect", "execution_outcome_unknown", "execution_outcome_unknown: cancellation not confirmed", "timeout"]) {
  test(`ACK-only ${ending} retains same-SID lock but permits a new session`, { timeout: 20_000 }, async (t) => {
    const rpc = await fakeRPC(t), hosted = await authorize(t, rpc.context)
    const owned = await command(hosted, prompt)
    await executeAgentCommand(hosted, rpc.context, owned)
    const active = hosted.active, operation = await rpc.nextOperation()
    const interruption = executeAgentCommand(hosted, rpc.context, await command(hosted, { case: "interrupt", value: { turnId: owned.commandId } }))
    await operation.ack
    await assertBlocked(hosted, rpc.context, rpc)
    if (ending === "disconnect") operation.socket.terminate()
    else if (ending !== "timeout") operation.error(-32603, ending)
    else operation.update({ sessionUpdate: "agent_message", turnId: operation.turnId, content: { type: "text", text: "ACK is not terminal" } })
    assert.equal((await interruption).state, CommandReceiptState.FAILED)
    assert.equal(hosted.active, active)
    assert.equal(active.uncertain, true)
    const snapshot = hosted.source.getSnapshot()
    assert.equal(snapshot.commandReceipts.find((receipt) => receipt.commandId === owned.commandId).state, CommandReceiptState.FAILED)
    assert.deepEqual(snapshot.capabilities.commandSchemas, [])
    assert.deepEqual(snapshot.capabilities.extensions, [])
    assert.equal(await authorizedAgentSession(rpc.context, new AbortController().signal), hosted)
    await assertBlocked(hosted, rpc.context, rpc)
    const otherContext = { ...rpc.context, sessionId: randomUUID() }
    const other = await authorize(t, otherContext)
    await finishNext(other, otherContext, rpc)
    assert.equal(hosted.active, active)
    assert.equal(rpc.operations.length, 2)
  })
}

test("ordinary original-RPC terminal errors release the session", { timeout: 5_000 }, async (t) => {
  const rpc = await fakeRPC(t), hosted = await authorize(t, rpc.context)
  const owned = await command(hosted, prompt)
  await executeAgentCommand(hosted, rpc.context, owned)
  const active = hosted.active, operation = await rpc.nextOperation()
  operation.error(-32603, "doagent_execution_failed")
  await active.done
  assert.equal(active.uncertain, false)
  assert.equal(hosted.active, undefined)
  assert.equal(hosted.source.getSnapshot().commandReceipts.find((receipt) => receipt.commandId === owned.commandId).state, CommandReceiptState.FAILED)
  await finishNext(hosted, rpc.context, rpc)
})

test("conversation attachments are forwarded once and remain in the user timeline", { timeout: 5_000 }, async (t) => {
  const rpc = await fakeRPC(t)
  const hosted = await authorize(t, rpc.context)
  const reference = await storeAgentAttachment(
    rpc.context,
    { name: "note.txt", mediaType: "text/plain", bytes: new TextEncoder().encode("uploaded document") },
  )
  const payload = {
    case: "sendPrompt",
    value: {
      text: "请阅读这个文档",
      attachments: [{
        contentId: reference.id,
        identity: { namespace: "agentcloud.session-file", semanticKey: "attachment", schemaVersion: "1" },
        content: { case: "file", value: {
          artifactId: reference.id, representationId: "source", revision: 1n,
          mediaType: reference.mediaType, role: "input", filename: reference.name,
        } },
      }],
    },
  }
  await finishNext(hosted, rpc.context, rpc, payload)
  const user = hosted.source.getSnapshot().history.find((item) => item.content?.content.case === "message" && item.content.content.value.role === 1)
  assert.ok(user)
  assert.equal(user.content.content.value.content.some((block) => block.content.case === "file" && block.content.value.artifactId === reference.id), true)
})

test("released SDK artifact rejects missing/stale epochs before and inside the publication queue", { timeout: 5_000 }, async (t) => {
  const rpc = await fakeRPC(t)
  const hosted = await authorizedAgentSession(rpc.context, new AbortController().signal)
  const previous = hosted.source.getSnapshot()
  const next = create(SessionSnapshotSchema, { sessionId: previous.sessionId, streamEpoch: randomUUID(), status: SessionStatus.IDLE })
  const events = [{ itemId: "status", event: create(AgentEventSchema, {
    event: { case: "sessionStatusChanged", value: { status: SessionStatus.COMPLETED } },
  }).event }]
  const stale = (error) => error instanceof AgentSessionReductionError && error.code === "session_source_epoch_stale"
  const reset = hosted.source.resetEpoch(next)
  const queued = assert.rejects(hosted.source.emit(events, previous.streamEpoch), stale)
  await reset
  await queued
  await assert.rejects(hosted.source.emit(events, previous.streamEpoch), stale)
  await assert.rejects(hosted.source.emit(events), stale)
  const untouched = hosted.source.getSnapshot()
  assert.equal(untouched.status, SessionStatus.IDLE)
  assert.equal(untouched.revision, 0n)
  assert.equal(untouched.latestSequence, 0n)
  assert.deepEqual(untouched.history, [])
  await hosted.source.emit(events, next.streamEpoch)
  assert.equal(hosted.source.getSnapshot().status, SessionStatus.COMPLETED)
  assert.equal(hosted.source.getSnapshot().revision, 1n)
})
