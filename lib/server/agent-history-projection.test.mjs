import assert from "node:assert/strict"
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { join, resolve } from "node:path"
import test, { after } from "node:test"
import { fileURLToPath, pathToFileURL } from "node:url"
import { create } from "@bufbuild/protobuf"
import { AgentWorkbenchSessionSource } from "@agent-cloud/agent-ui/server"
import {
  AgentEventSchema,
  MessageRole,
  SessionSnapshotSchema,
  SessionStatus,
  TimelineItemStatus,
  ToolPhase,
} from "@agent-cloud/agent-ui/protocol"
import TypeScript from "typescript"

const root = fileURLToPath(new URL("../../", import.meta.url))
const fixture = mkdtempSync(join(root, ".agent-history-proj-test-"))
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

const {
  AGENT_HISTORY_SCHEMA,
  parseAgentSessionsList,
  projectAgentHistoryToSnapshotItems,
} = await import(sourceModule(join(root, "lib/server/agent-history-projection.ts")))

test("projectAgentHistoryToSnapshotItems maps user, reasoning, assistant, tools, attachments, and truncated notice into a valid SessionSnapshot", async () => {
  const payload = JSON.stringify({
    source: "doagent_history",
    history_schema: AGENT_HISTORY_SCHEMA,
    sessionId: "console-hist1",
    truncated: true,
    total_messages: 6,
    messages: [
      { role: "system", content: "internal system prompt must never render" },
      {
        role: "user",
        created_at: "2026-11-26T10:00:00Z",
        parts: [
          { type: "text", text: "请检查 nginx 配置\n\n---\nThe following target facts\nsecret-metadata" },
          { type: "attachment", filename: "nginx.conf", media_type: "text/plain" },
        ],
      },
      {
        role: "assistant",
        created_at: "2026-11-26T10:00:05Z",
        reasoning_content: "先查看配置文件和测试语法",
        content: "我先检查 nginx 语法。",
        tool_calls: [
          { id: "call-1", type: "function", function: { name: "bash", arguments: "{\"cmd\":\"nginx -t\"}" } },
          { id: "call-2", type: "function", function: { name: "bash", arguments: "{\"cmd\":\"cat /missing\"}" } },
        ],
      },
      { role: "tool", tool_call_id: "call-1", name: "bash", content: "syntax is ok", created_at: "2026-11-26T10:00:06Z" },
      { role: "tool", tool_call_id: "call-2", name: "bash", content: "No such file", is_error: true, created_at: "2026-11-26T10:00:07Z" },
      { role: "assistant", created_at: "2026-11-26T10:00:10Z", content: "nginx 语法正确，但缺少一个文件。" },
    ],
  })

  const streamEpoch = "epoch-hist-1"
  const history = projectAgentHistoryToSnapshotItems(payload, {
    sessionId: "console-hist1",
    streamEpoch,
    fallbackTimestamp: "2026-11-26T10:00:00Z",
  })

  // Expect: truncated notice, user, reasoning, assistant preamble, tool call-1, tool call-2, final assistant
  assert.equal(history.length, 7)
  assert.equal(history[0].content.content.case, "system")
  assert.match(history[0].content.content.value.content[0].content.value.text, /体积上限/)

  assert.equal(history[1].content.content.case, "message")
  assert.equal(history[1].content.content.value.role, MessageRole.USER)
  assert.equal(history[1].content.content.value.content[0].content.value.text, "请检查 nginx 配置")
  assert.equal(history[1].content.content.value.content[1].content.value.text, "[附件: nginx.conf]")

  assert.equal(history[2].content.content.case, "reasoning")
  assert.equal(history[3].content.content.case, "message")
  assert.equal(history[3].content.content.value.role, MessageRole.ASSISTANT)

  assert.equal(history[4].content.content.case, "toolExecution")
  assert.equal(history[4].content.content.value.phase, ToolPhase.COMPLETED)
  assert.equal(history[4].content.content.value.identity.semanticKey, "shell.execute")
  assert.equal(JSON.parse(new TextDecoder().decode(history[4].content.content.value.input.data)).command, "nginx -t")
  assert.equal(history[4].content.content.value.results[0].blocks[0].content.value.text, "syntax is ok")

  assert.equal(history[5].content.content.case, "toolExecution")
  assert.equal(history[5].content.content.value.phase, ToolPhase.FAILED)
  assert.equal(history[5].content.content.value.identity.semanticKey, "shell.execute")
  assert.equal(JSON.parse(new TextDecoder().decode(history[5].content.content.value.input.data)).command, "cat /missing")
  assert.equal(history[5].content.content.value.failure.message, "No such file")

  assert.equal(history[6].content.content.case, "message")
  assert.equal(history[6].content.content.value.content[0].content.value.text, "nginx 语法正确，但缺少一个文件。")

  // Verify the projected history satisfies real AgentWorkbenchSessionSource validation and can continue emitting.
  const source = new AgentWorkbenchSessionSource({
    snapshot: create(SessionSnapshotSchema, {
      sessionId: "console-hist1",
      streamEpoch,
      status: SessionStatus.IDLE,
      revision: 1n,
      latestSequence: BigInt(history.length),
      history,
    }),
  })
  assert.equal(source.getSnapshot().history.length, 7)
  const batch = await source.emit([{
    itemId: "user:follow-up",
    turnId: "turn-2",
    event: create(AgentEventSchema, {
      event: {
        case: "timelineItemAppended",
        value: {
          content: {
            content: {
              case: "message",
              value: {
                role: MessageRole.USER,
                status: TimelineItemStatus.COMPLETED,
                content: [{ contentId: "text", content: { case: "text", value: { text: "继续排查" } } }],
              },
            },
          },
        },
      },
    }).event,
  }], streamEpoch)
  assert.equal(batch.revision, 2n)
  assert.equal(batch.firstSequence, 8n)
  assert.equal(source.getSnapshot().history.length, 8)
})

test("parseAgentSessionsList validates schema, deduplicates, and rejects unsafe session IDs", () => {
  const sessions = parseAgentSessionsList(JSON.stringify({
    source: "agent_sessions",
    history_schema: AGENT_HISTORY_SCHEMA,
    total: 3,
    sessions: [
      { session_id: "console-a1", title: "排查 CPU", message_count: 4, turn_count: 2, updated_at: "2026-11-26T12:00:00Z" },
      { session_id: "console-a1", title: "duplicate", message_count: 1, turn_count: 1, updated_at: "2026-11-26T11:00:00Z" },
      { session_id: "../escape", title: "bad", message_count: 1, turn_count: 1, updated_at: "2026-11-26T11:00:00Z" },
      { session_id: "deploy-check", title: "检查发布", last_message_preview: "全部就绪", message_count: 2, turn_count: 1, updated_at: "2026-11-26T10:00:00Z" },
    ],
  }))
  assert.deepEqual(sessions.map((s) => s.session_id), ["console-a1", "deploy-check"])
  assert.equal(sessions[1].last_message_preview, "全部就绪")

  assert.throws(() => parseAgentSessionsList("{}"), /协议版本/)
  assert.throws(() => projectAgentHistoryToSnapshotItems("{\"messages\":[]}", {
    sessionId: "s",
    streamEpoch: "e",
    fallbackTimestamp: "2026-11-26T10:00:00Z",
  }), /协议版本/)
})
