import assert from "node:assert/strict"
import { mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs"
import test, { after } from "node:test"
import { fileURLToPath, pathToFileURL } from "node:url"
import { join, resolve } from "node:path"
import TypeScript from "typescript"
import { create, toJson } from "@bufbuild/protobuf"
import { createAgentCommandEnvelope } from "@agent-cloud/agent-ui/runtime"
import { CommandReceiptSchema, CommandReceiptState } from "@agent-cloud/agent-ui/protocol"

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

test("conversation references keep command digest and retry identity stable", async (t) => {
  const references = { current: { databaseRefs: ["db-one"], repositoryRefs: ["repo-one@main"] } }
  const transport = createDoopsAgentSessionTransport({
    session: { gateway: "https://gateway.example.test" },
    target: { cluster: "test", instance: "node" },
    sessionId: "session-one",
    references,
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
  references.current = { databaseRefs: ["db-two"], repositoryRefs: ["repo-two@main"] }
  await transport.execute(first, new AbortController().signal)
  const retryEnvelope = await createAgentCommandEnvelope(commandInput)
  await transport.execute(retryEnvelope, new AbortController().signal)

  assert.equal(requests.length, 3)
  assert.deepEqual(requests[0], requests[1])
  assert.deepEqual(requests[1], requests[2])
  assert.match(JSON.stringify(requests[0]), /db-one/)
  assert.doesNotMatch(JSON.stringify(requests[0]), /db-two/)
})
